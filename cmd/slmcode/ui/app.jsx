const { useState, useEffect, useCallback, useMemo, useRef } = React;

async function api(path, opts = {}) {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json", ...(opts.headers || {}) },
    ...opts,
  });
  if (!res.ok) throw new Error((await res.text()) || res.statusText);
  return res.json();
}

const COLUMNS = [
  { id: "to_scope", label: "To scope" },
  { id: "scoped", label: "Scoped" },
  { id: "ready_to_dev", label: "Ready" },
  { id: "in_progress", label: "In progress" },
  { id: "in_review", label: "In review" },
  { id: "done", label: "Done" },
  { id: "blocked", label: "Blocked" },
];

const ROLES = ["worker", "deep", "explorer", "docs", "architect", "reviewer", "corrector", "tester", "context", "coordinator"];
const PIPE = ["init", "skills", "context", "explore", "docs", "architect", "plan", "split", "coord", "execute", "learn", "test", "memory", "done"];

const DOC_TABS = [
  { id: "CONTEXT.md", label: "Context" },
  { id: "PLAN.md", label: "Plan" },
  { id: "MEMORY.md", label: "Memory" },
  { id: "PROJECT.md", label: "Project" },
  { id: "SKILLS.md", label: "Skills" },
  { id: "QUERY.md", label: "Query" },
  { id: "SCRATCH.md", label: "Scratch" },
  { id: "TASKS.md", label: "Tasks" },
  { id: "settings", label: "⚙" },
];

function checklistProgress(t) {
  const list = t.checklist || [];
  return [list.filter((x) => x.done).length, list.length];
}

function App() {
  const [health, setHealth] = useState(null);
  const [config, setConfig] = useState(null);
  const [models, setModels] = useState([]);
  const [agents, setAgents] = useState([]);
  const [query, setQuery] = useState("");
  const [events, setEvents] = useState([]);
  const [phase, setPhase] = useState("idle");
  const [running, setRunning] = useState(false);
  const [tab, setTab] = useState("CONTEXT.md");
  const [doc, setDoc] = useState("");
  const [board, setBoard] = useState({ tasks: [], plan: {} });
  const [skills, setSkills] = useState([]);
  const [nav, setNav] = useState("board");
  const [selected, setSelected] = useState(null);
  const [draft, setDraft] = useState({ title: "", description: "", column: "to_scope", role: "worker", checklist: "" });
  const [err, setErr] = useState("");
  const [toast, setToast] = useState("");
  const [dragging, setDragging] = useState("");
  const [dragOver, setDragOver] = useState("");
  const [liveAgent, setLiveAgent] = useState(null);
  const [runMode, setRunMode] = useState("full");
  const [runSpecialist, setRunSpecialist] = useState("worker");
  const [pinSkills, setPinSkills] = useState([]);
  const [skillDraft, setSkillDraft] = useState({ name: "", description: "", agents: "worker", body: "" });
  const [skillEdit, setSkillEdit] = useState(null);
  const selectedRef = useRef(null);
  selectedRef.current = selected;

  const showToast = (msg) => {
    setToast(msg);
    setTimeout(() => setToast(""), 2800);
  };

  const refreshBoard = useCallback(async () => {
    const b = await api("/api/board");
    setBoard(b);
    const cur = selectedRef.current;
    if (cur) {
      const t = (b.tasks || []).find((x) => x.id === cur.id);
      if (t) setSelected(t);
    }
  }, []);

  const refresh = useCallback(async () => {
    try {
      const [h, c, sk, latest, mods, ag] = await Promise.all([
        api("/api/health"),
        api("/api/config"),
        api("/api/skills"),
        api("/api/runs/latest"),
        api("/api/models").catch(() => ({ models: [] })),
        api("/api/agents").catch(() => []),
      ]);
      setHealth(h);
      setConfig(c);
      setSkills(Array.isArray(sk) ? sk : []);
      setModels(Array.isArray(mods.models) ? mods.models : []);
      setAgents(Array.isArray(ag) ? ag : []);
      if (c?.mode) setRunMode(c.mode);
      if (c?.specialist) setRunSpecialist(c.specialist);
      if (Array.isArray(c?.pinned_skills)) setPinSkills(c.pinned_skills);
      setRunning(!!(h.running || latest.running));
      if (Array.isArray(latest.events)) setEvents(latest.events);
      if (latest.events?.length) {
        const last = latest.events[latest.events.length - 1];
        if (last?.phase) setPhase(last.phase);
      }
      await refreshBoard();
      setErr("");
    } catch (e) {
      setErr(String(e.message || e));
    }
  }, [refreshBoard]);

  useEffect(() => {
    refresh();
    const id = setInterval(() => {
      refreshBoard();
      // Keep live docs fresh while pipeline evolves CONTEXT/MEMORY
      if (tab !== "settings") {
        api("/api/docs/" + tab)
          .then((d) => setDoc(d.content || ""))
          .catch(() => {});
      }
    }, 2000);
    return () => clearInterval(id);
  }, [refresh, refreshBoard, tab]);

  useEffect(() => {
    const es = new EventSource("/api/events");
    es.onmessage = (msg) => {
      try {
        const e = JSON.parse(msg.data);
        setEvents((prev) => [...prev.slice(-400), e]);
        setPhase(e.phase || "idle");
        if (e.agent || e.kind === "agent_start" || e.kind === "agent_end" || e.kind === "output") {
          setLiveAgent({
            agent: e.agent || e.phase,
            kind: e.kind,
            task: e.task_id,
            scope: e.scope,
            message: e.message,
            output: e.output,
            time: e.time,
          });
        }
        // Don't mark running=true on every SSE replay (would flash READY→RUNNING after finished runs)
        if (e.phase === "done" || e.phase === "error") {
          setRunning(false);
          showToast(e.phase === "done" ? "Run finished" : (e.message || "Run error"));
          refresh();
        } else if (e.kind === "agent_start" || e.phase === "init" || e.phase === "execute") {
          setRunning(true);
        }
        refreshBoard();
      } catch (_) {}
    };
    es.onerror = () => {
      // keep UI usable if SSE drops; poll will refresh running state
    };
    return () => es.close();
  }, [refresh, refreshBoard]);

  useEffect(() => {
    if (tab === "settings") return;
    api("/api/docs/" + tab)
      .then((d) => setDoc(d.content || ""))
      .catch((e) => setErr(String(e.message || e)));
  }, [tab]);

  async function startRun() {
    setErr("");
    setEvents([]);
    setRunning(true);
    setPhase("init");
    setNav("run"); // jump to Live so progress is obvious
    try {
      await api("/api/runs", {
        method: "POST",
        body: JSON.stringify({
          query,
          mode: runMode,
          specialist: runMode === "specialist" ? runSpecialist : "",
          skills: pinSkills,
        }),
      });
      showToast(runMode === "specialist"
        ? `Running @${runSpecialist} — watch Live`
        : "Running full engine — watch Live");
    } catch (e) {
      setRunning(false);
      setErr(String(e.message || e));
    }
  }

  async function createSkill() {
    if (!skillDraft.name.trim()) return;
    const name = skillDraft.name.trim();
    await api("/api/skills", {
      method: "POST",
      body: JSON.stringify({
        name,
        description: skillDraft.description || "Custom project skill",
        agents: skillDraft.agents ? skillDraft.agents.split(",").map((x) => x.trim()).filter(Boolean) : [],
        body: skillDraft.body || undefined,
        user_invocable: true,
      }),
    });
    setSkillDraft({ name: "", description: "", agents: "worker", body: "" });
    await refresh();
    showToast("Skill created — use @skill:" + name.toLowerCase().replace(/\s+/g, "-"));
  }

  async function saveSkillEdit() {
    if (!skillEdit?.name) return;
    await api("/api/skills/" + encodeURIComponent(skillEdit.name), {
      method: "PUT",
      body: JSON.stringify(skillEdit),
    });
    setSkillEdit(null);
    await refresh();
    showToast("Skill saved");
  }

  async function patchTask(id, patch) {
    const t = await api("/api/tasks/" + id, { method: "PATCH", body: JSON.stringify(patch) });
    setSelected(t);
    await refreshBoard();
    showToast(id + " updated");
  }

  async function addTask() {
    if (!draft.title.trim()) return;
    const checklist = draft.checklist
      ? draft.checklist.split("\n").filter(Boolean).map((text, i) => ({ id: "c" + (i + 1), text, done: false }))
      : [];
    await api("/api/tasks", {
      method: "POST",
      body: JSON.stringify({
        title: draft.title,
        description: draft.description,
        column: draft.column,
        role: draft.role,
        checklist,
      }),
    });
    setDraft({ title: "", description: "", column: "to_scope", role: "worker", checklist: "" });
    await refreshBoard();
    showToast("Task added");
  }

  async function saveDoc() {
    await api("/api/docs/" + tab, { method: "PUT", body: JSON.stringify({ content: doc }) });
    showToast(tab + " saved — next packs will use it");
  }

  async function saveConfig(patch) {
    const next = await api("/api/config", { method: "PUT", body: JSON.stringify({ ...config, ...patch }) });
    setConfig(next);
    showToast("Config saved");
  }

  const byColumn = useMemo(() => {
    const m = {};
    COLUMNS.forEach((c) => (m[c.id] = []));
    (board.tasks || []).forEach((t) => {
      const col = t.column || "to_scope";
      (m[col] || (m[col] = [])).push(t);
    });
    return m;
  }, [board]);

  const counts = useMemo(() => {
    const c = { total: 0, ready: 0, doing: 0, done: 0, blocked: 0 };
    (board.tasks || []).forEach((t) => {
      c.total++;
      if (t.column === "ready_to_dev") c.ready++;
      if (t.column === "in_progress" || t.column === "in_review") c.doing++;
      if (t.column === "done") c.done++;
      if (t.column === "blocked") c.blocked++;
    });
    return c;
  }, [board]);

  const phaseIdx = PIPE.indexOf(phase);

  const NAV = [
    { id: "board", label: "Board", tip: "Tasks" },
    { id: "run", label: "Live", tip: "Agent stream" },
    { id: "agents", label: "Agents", tip: "Specialists" },
    { id: "skills", label: "Skills", tip: "Knowledge" },
  ];

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand" title="SLMCode Studio">SLM<span>Code</span></div>
        <div className="query-wrap">
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Tell SLMCode what to do…  (@skill:name works)  Enter to run"
            aria-label="Task for SLMCode"
            onKeyDown={(e) => e.key === "Enter" && !running && query.trim() && startRun()}
          />
          {!running ? (
            <button className="primary" disabled={!query.trim()} onClick={startRun} title="Start the pipeline">Run</button>
          ) : (
            <button className="danger" onClick={async () => {
              try {
                await api("/api/runs/stop", { method: "POST", body: "{}" });
                setRunning(false);
                showToast("Stopped");
              } catch (e) {
                setErr(String(e.message || e));
              }
            }}>Stop</button>
          )}
        </div>
        <div className="engine-pick" title="Full engine or one specialist">
          <select value={runMode} onChange={(e) => setRunMode(e.target.value)} aria-label="Engine mode">
            <option value="full">full engine</option>
            <option value="specialist">specialist</option>
          </select>
          {runMode === "specialist" && (
            <select value={runSpecialist} onChange={(e) => setRunSpecialist(e.target.value)} aria-label="Specialist">
              {(agents.length ? agents.map((a) => a.id) : ROLES).map((id) => (
                <option key={id} value={id}>@{id}</option>
              ))}
            </select>
          )}
        </div>
        <div className="meta" title={config?.model}>
          {config ? `${config.provider} · ${String(config.model || "").split("/").pop()}` : "…"}
        </div>
      </header>

      <div className="pipeline" title="Pipeline progress">
        {PIPE.map((p, i) => (
          <React.Fragment key={p}>
            {i > 0 && <span className="pipe-sep">›</span>}
            <span className={"pipe-step" + (phase === p ? " active" : "") + (phaseIdx > i ? " done" : "")}>{p}</span>
          </React.Fragment>
        ))}
      </div>

      {!running && counts.total === 0 && !query && (
        <div className="howto">
          <strong>Quick start:</strong>
          <span>1. Type a task above → <em>Run</em></span>
          <span>2. Watch <em>Live</em> while agents work</span>
          <span>3. Drag cards on the <em>Board</em> anytime</span>
        </div>
      )}

      <div className="main">
        <aside className="left">
          <h3>Views</h3>
          {NAV.map((item) => (
            <button
              key={item.id}
              className={"nav-item" + (nav === item.id ? " active" : "")}
              onClick={() => setNav(item.id)}
              title={item.tip}
            >
              {item.label}
              {item.id === "run" && running ? <span className="nav-badge">live</span> : null}
              {item.id === "board" && counts.ready > 0 ? <span className="nav-badge dim">{counts.ready}</span> : null}
            </button>
          ))}

          <div className="stat-grid">
            <div className="stat"><b>{counts.total}</b>tasks</div>
            <div className="stat"><b>{counts.ready}</b>ready</div>
            <div className="stat"><b>{counts.doing}</b>active</div>
            <div className="stat"><b>{counts.done}</b>done</div>
          </div>

          <p className="sidebar-hint">
            Drag cards to change status. Edit <strong>Context</strong> on the right — agents read it next wave.
          </p>
        </aside>

        <section className="center">
          {err && <p className="err-banner">{err}</p>}

          {nav === "run" && (
            <>
              <h2>Live</h2>
              <p className="lead">
                {running
                  ? "Agents are working — this updates in real time."
                  : "Press Run above. Progress, agent names, and file scope appear here."}
              </p>
              {liveAgent && (
                <div className="live-agent">
                  <div className="row" style={{ justifyContent: "space-between" }}>
                    <strong>@{liveAgent.agent || "…"}</strong>
                    <span className="phase">{liveAgent.kind || "phase"}</span>
                  </div>
                  <div style={{ color: "var(--muted)", marginTop: 4 }}>{liveAgent.message}</div>
                  {liveAgent.task && <div className="id">{liveAgent.task}</div>}
                  {liveAgent.scope && <div style={{ marginTop: 4 }}><span style={{ color: "var(--accent)" }}>scope</span> {liveAgent.scope}</div>}
                  {liveAgent.output && <pre className="output-box" style={{ marginTop: 8, maxHeight: 180 }}>{liveAgent.output}</pre>}
                </div>
              )}
              <ul className="event-list">
                {events.slice().reverse().map((e, i) => (
                  <li key={i}>
                    <span className="phase">{e.kind || e.phase}</span>
                    {e.agent ? <strong>@{e.agent} </strong> : null}
                    {e.task_id ? <span className="id">{e.task_id} </span> : null}
                    {e.message}
                    {e.scope ? <div style={{ color: "var(--muted)", fontSize: "0.75rem" }}>scope: {e.scope}</div> : null}
                    {e.output ? <pre className="mini-out">{e.output.slice(0, 500)}</pre> : null}
                  </li>
                ))}
                {!events.length && (
                  <li className="empty-state" style={{ listStyle: "none" }}>
                    <strong>Waiting for a run</strong>
                    <p>Type a task up top and press <em>Run</em>.</p>
                  </li>
                )}
              </ul>
            </>
          )}

          {nav === "agents" && (
            <>
              <h2>Agents</h2>
              <p className="lead">Specialists the orchestrator can call. Workers with ⚙ can edit files.</p>
              <div className="split-panels">
                {agents.map((a) => (
                  <div key={a.id} className="panel-box">
                    <strong>@{a.id}</strong>
                    <div style={{ color: "var(--muted)", marginTop: 4 }}>{a.role}</div>
                    <div style={{ marginTop: 6, fontFamily: "var(--font-mono)", fontSize: "0.72rem" }}>
                      {a.tools ? "can edit files" : "reasoning only"}
                    </div>
                  </div>
                ))}
              </div>
            </>
          )}

          {nav === "skills" && (
            <>
              <h2>Skills</h2>
              <p className="lead">
                Claude Code–style <code>SKILL.md</code> packs. Pin for the next run, or reference with <code>@skill:name</code>.
              </p>
              <div className="panel-box" style={{ marginBottom: 12 }}>
                <h3>Pin for next run</h3>
                <div className="row" style={{ flexWrap: "wrap", gap: 6 }}>
                  {skills.map((s) => {
                    const on = pinSkills.includes(s.name);
                    return (
                      <button
                        key={s.name}
                        className={"sm" + (on ? "" : " ghost")}
                        onClick={() => setPinSkills((prev) => on ? prev.filter((x) => x !== s.name) : [...prev, s.name])}
                        title={s.description}
                      >
                        {on ? "✓ " : ""}{s.name}
                      </button>
                    );
                  })}
                </div>
                {pinSkills.length > 0 && (
                  <p className="lead" style={{ marginBottom: 0 }}>Pinned: {pinSkills.map((n) => "@skill:" + n).join(" ")}</p>
                )}
              </div>
              <div className="split-panels">
                <div className="panel-box">
                  <h3>All skills ({skills.length})</h3>
                  <ul className="event-list">
                    {skills.map((s) => (
                      <li key={s.name}>
                        <strong>{s.name}</strong> — {s.description}
                        <div style={{ fontFamily: "var(--font-mono)", fontSize: "0.7rem", color: "var(--muted)" }}>
                          agents: {(s.agents && s.agents.length) ? s.agents.join(", ") : "*"}
                          {s.triggers?.length ? " · triggers: " + s.triggers.join(", ") : ""}
                        </div>
                        <div className="row" style={{ marginTop: 4 }}>
                          <button className="sm ghost" onClick={async () => {
                            const full = await api("/api/skills/" + encodeURIComponent(s.name));
                            setSkillEdit(full);
                          }}>Edit</button>
                          <button className="sm ghost" onClick={() => {
                            setQuery((q) => (q ? q + " " : "") + "@skill:" + s.name);
                            showToast("Inserted @" + "skill:" + s.name);
                          }}>@ref</button>
                        </div>
                      </li>
                    ))}
                  </ul>
                </div>
                <div className="panel-box">
                  {skillEdit ? (
                    <>
                      <h3>Edit {skillEdit.name}</h3>
                      <label>Description<input value={skillEdit.description || ""} onChange={(e) => setSkillEdit({ ...skillEdit, description: e.target.value })} /></label>
                      <label>Agents (csv)<input value={(skillEdit.agents || []).join(", ")} onChange={(e) => setSkillEdit({ ...skillEdit, agents: e.target.value.split(",").map((x) => x.trim()).filter(Boolean) })} /></label>
                      <label>Body<textarea className="doc-editor" style={{ minHeight: 180 }} value={skillEdit.body || ""} onChange={(e) => setSkillEdit({ ...skillEdit, body: e.target.value })} /></label>
                      <div className="row">
                        <button className="sm" onClick={saveSkillEdit}>Save project copy</button>
                        <button className="sm ghost" onClick={() => setSkillEdit(null)}>Cancel</button>
                      </div>
                    </>
                  ) : (
                    <>
                      <h3>New project skill</h3>
                      <label>Name<input placeholder="my-skill" value={skillDraft.name} onChange={(e) => setSkillDraft({ ...skillDraft, name: e.target.value })} /></label>
                      <label>Description<input value={skillDraft.description} onChange={(e) => setSkillDraft({ ...skillDraft, description: e.target.value })} /></label>
                      <label>Agents (csv)<input value={skillDraft.agents} onChange={(e) => setSkillDraft({ ...skillDraft, agents: e.target.value })} placeholder="worker, reviewer or empty=all" /></label>
                      <label>Body (optional)<textarea className="doc-editor" style={{ minHeight: 120 }} value={skillDraft.body} onChange={(e) => setSkillDraft({ ...skillDraft, body: e.target.value })} /></label>
                      <button className="sm" onClick={createSkill}>Create</button>
                    </>
                  )}
                </div>
              </div>
            </>
          )}

          {nav === "board" && (
            <>
              <div className="row" style={{ justifyContent: "space-between", marginBottom: 8 }}>
                <h2 style={{ margin: 0 }}>Board</h2>
                <div className="row">
                  <span className="lead" style={{ margin: 0 }}>Drag to move · edit while agents run</span>
                  <button className="sm ghost" onClick={refreshBoard}>Refresh</button>
                </div>
              </div>
              {board.plan?.summary && <p className="lead" style={{ marginTop: 0 }}>{board.plan.summary}</p>}

              {counts.total === 0 && (
                <div className="empty-state">
                  <strong>No tasks yet</strong>
                  <p>Press <em>Run</em> — context, plan, and tasks are filled by agents (not seeded).</p>
                </div>
              )}

              <div className="kanban wide">
                {COLUMNS.map((c) => (
                  <div
                    className={"col" + (dragOver === c.id ? " drop-target" : "")}
                    key={c.id}
                    onDragOver={(e) => { e.preventDefault(); setDragOver(c.id); }}
                    onDragLeave={() => setDragOver((x) => (x === c.id ? "" : x))}
                    onDrop={(e) => {
                      e.preventDefault();
                      setDragOver("");
                      const id = e.dataTransfer.getData("text/task-id") || dragging;
                      if (id) {
                        patchTask(id, { column: c.id });
                        showToast(id + " → " + c.label);
                      }
                      setDragging("");
                    }}
                  >
                    <h4>{c.label}<span className="n">{(byColumn[c.id] || []).length}</span></h4>
                    {(byColumn[c.id] || []).map((t) => {
                      const [done, total] = checklistProgress(t);
                      return (
                        <div
                          key={t.id}
                          draggable
                          className={"card " + c.id + (selected?.id === t.id ? " selected" : "") + (dragging === t.id ? " dragging" : "")}
                          onClick={() => setSelected(t)}
                          onDragStart={(e) => {
                            setDragging(t.id);
                            e.dataTransfer.setData("text/task-id", t.id);
                            e.dataTransfer.effectAllowed = "move";
                          }}
                          onDragEnd={() => { setDragging(""); setDragOver(""); }}
                        >
                          <div className="id">{t.id} · @{t.role}</div>
                          <strong>{t.title}</strong>
                          {total > 0 && (
                            <div style={{ color: "var(--muted)", marginTop: 3, fontSize: "0.72rem" }}>
                              ✓ {done}/{total}
                            </div>
                          )}
                        </div>
                      );
                    })}
                  </div>
                ))}
              </div>

              <div className="split-panels">
                <div className="panel-box add-task">
                  <h3>Add task</h3>
                  <input placeholder="Short title" value={draft.title} onChange={(e) => setDraft({ ...draft, title: e.target.value })} onKeyDown={(e) => e.key === "Enter" && addTask()} />
                  <textarea rows={2} placeholder="What should the agent do? (optional)" value={draft.description} onChange={(e) => setDraft({ ...draft, description: e.target.value })} />
                  <textarea rows={2} placeholder={"Checklist (optional)\none item per line"} value={draft.checklist} onChange={(e) => setDraft({ ...draft, checklist: e.target.value })} />
                  <div className="row">
                    <select value={draft.column} onChange={(e) => setDraft({ ...draft, column: e.target.value })}>
                      {COLUMNS.map((c) => <option key={c.id} value={c.id}>{c.label}</option>)}
                    </select>
                    <select value={draft.role} onChange={(e) => setDraft({ ...draft, role: e.target.value })}>
                      {ROLES.map((r) => <option key={r} value={r}>@{r}</option>)}
                    </select>
                    <button className="primary" onClick={addTask} disabled={!draft.title.trim()}>Add</button>
                  </div>
                </div>

                <div className="panel-box task-detail">
                  <h3>Selected task</h3>
                  {!selected ? (
                    <p className="lead" style={{ margin: 0 }}>
                      Click a card to edit it. Use <strong>Ready</strong> to queue work for the next wave.
                    </p>
                  ) : (
                    <>
                      <div className="row" style={{ marginBottom: 6 }}>
                        <strong>{selected.id}</strong>
                        <button className="sm primary" onClick={() => patchTask(selected.id, { column: "ready_to_dev" })}>Ready</button>
                        <button className="sm ghost" onClick={() => api("/api/tasks/" + selected.id, { method: "DELETE" }).then(() => { setSelected(null); refreshBoard(); showToast("Deleted"); })}>Delete</button>
                      </div>
                      <input value={selected.title || ""} onChange={(e) => setSelected({ ...selected, title: e.target.value })} onBlur={() => patchTask(selected.id, { title: selected.title })} />
                      <textarea rows={3} value={selected.description || ""} onChange={(e) => setSelected({ ...selected, description: e.target.value })} onBlur={() => patchTask(selected.id, { description: selected.description })} />
                      <textarea rows={2} placeholder="Notes for next wave / specialist" value={selected.notes || ""} onChange={(e) => setSelected({ ...selected, notes: e.target.value })} onBlur={() => patchTask(selected.id, { notes: selected.notes })} />
                      <div className="row">
                        <select value={selected.column || "to_scope"} onChange={(e) => patchTask(selected.id, { column: e.target.value })}>
                          {COLUMNS.map((c) => <option key={c.id} value={c.id}>{c.label}</option>)}
                        </select>
                        <select value={selected.role || "worker"} onChange={(e) => patchTask(selected.id, { role: e.target.value })}>
                          {ROLES.map((r) => <option key={r} value={r}>@{r}</option>)}
                        </select>
                      </div>
                      <h3 style={{ marginTop: 10 }}>Checklist</h3>
                      {(selected.checklist || []).map((c) => (
                        <label key={c.id} className="check-row">
                          <input
                            type="checkbox"
                            checked={!!c.done}
                            onChange={() => {
                              const checklist = (selected.checklist || []).map((x) => x.id === c.id ? { ...x, done: !x.done } : x);
                              setSelected({ ...selected, checklist });
                              patchTask(selected.id, { checklist });
                            }}
                          />
                          {c.text}
                        </label>
                      ))}
                      <button
                        className="sm ghost"
                        onClick={() => {
                          const text = prompt("Checklist item");
                          if (!text) return;
                          const checklist = [...(selected.checklist || []), { id: "c" + Date.now(), text, done: false }];
                          setSelected({ ...selected, checklist });
                          patchTask(selected.id, { checklist });
                        }}
                      >+ item</button>
                      {(selected.output || selected.review || selected.error) && (
                        <>
                          <h3 style={{ marginTop: 10 }}>Agent output / review</h3>
                          <div className="output-box">
                            {selected.error && ("ERROR: " + selected.error + "\n\n")}
                            {selected.review && ("REVIEW:\n" + selected.review + "\n\n")}
                            {selected.output && selected.output.slice(0, 4000)}
                          </div>
                        </>
                      )}
                    </>
                  )}
                </div>
              </div>
            </>
          )}
        </section>

        <aside className="right">
          <div className="tabs">
            {DOC_TABS.map((t) => (
              <button key={t.id} className={tab === t.id ? "active" : ""} onClick={() => setTab(t.id)}>{t.label}</button>
            ))}
          </div>
          <div className="panel-body">
            {tab === "settings" ? (
              !config ? (
                <p style={{ color: "var(--muted)" }}>Loading settings…</p>
              ) : (
              <div className="settings">
                <h3>Model & quality</h3>
                <label>Provider
                  <select value={config.provider} onChange={(e) => saveConfig({ provider: e.target.value })}>
                    <option value="omlx">omlx</option>
                    <option value="ollama">ollama</option>
                    <option value="openai">openai</option>
                  </select>
                </label>
                <label>Model
                  <select value={config.model || ""} onChange={(e) => saveConfig({ model: e.target.value })}>
                    {[config.model, ...models.filter((m) => m && m !== config.model)].filter(Boolean).map((m) => (
                      <option key={m} value={m}>{m}</option>
                    ))}
                  </select>
                </label>
                <label>Endpoint<input defaultValue={config.endpoint} onBlur={(e) => saveConfig({ endpoint: e.target.value })} /></label>
                <label>Backend
                  <select value={config.backend} onChange={(e) => saveConfig({ backend: e.target.value })}>
                    <option value="slmcode">slmcode specialists</option>
                    <option value="claude-code">claude-code CLI</option>
                  </select>
                </label>
                <label>Engine mode
                  <select
                    value={config.mode || "full"}
                    onChange={(e) => {
                      const mode = e.target.value;
                      setRunMode(mode);
                      saveConfig({ mode, specialist: mode === "specialist" ? (config.specialist || runSpecialist || "worker") : "" });
                    }}
                  >
                    <option value="full">full pipeline</option>
                    <option value="specialist">single specialist</option>
                  </select>
                </label>
                {(config.mode === "specialist" || runMode === "specialist") && (
                  <label>Default specialist
                    <select
                      value={config.specialist || runSpecialist}
                      onChange={(e) => {
                        setRunSpecialist(e.target.value);
                        saveConfig({ mode: "specialist", specialist: e.target.value });
                      }}
                    >
                      {(agents.length ? agents.map((a) => a.id) : ROLES).map((id) => (
                        <option key={id} value={id}>{id}</option>
                      ))}
                    </select>
                  </label>
                )}
                <label>Think passes (quality)
                  <input type="number" min={1} max={5} defaultValue={config.think_passes} onBlur={(e) => saveConfig({ think_passes: Number(e.target.value) })} />
                </label>
                <label>Max parallel<input type="number" min={1} max={8} defaultValue={config.max_parallel} onBlur={(e) => saveConfig({ max_parallel: Number(e.target.value) })} /></label>
                <label>Review retries<input type="number" min={0} max={5} defaultValue={config.max_retries} onBlur={(e) => saveConfig({ max_retries: Number(e.target.value) })} /></label>
                <label>Context budget KB<input type="number" min={4} max={64} defaultValue={config.max_context_kb} onBlur={(e) => saveConfig({ max_context_kb: Number(e.target.value) })} /></label>
                <label>Permission
                  <select
                    value={config.permission || (config.dry_run ? "dry-run" : "auto")}
                    onChange={(e) => saveConfig({ permission: e.target.value, dry_run: e.target.value === "dry-run" })}
                  >
                    <option value="auto">auto (write immediately)</option>
                    <option value="dry-run">dry-run (no code writes)</option>
                    <option value="review">review (pending patches)</option>
                  </select>
                </label>
              </div>
              )
            ) : (
              <>
                <div className="row" style={{ justifyContent: "space-between", marginBottom: 6 }}>
                  <h3 style={{ margin: 0 }}>{tab}</h3>
                  <button className="sm" onClick={saveDoc}>Save</button>
                </div>
                <p className="lead" style={{ fontSize: "0.78rem", marginTop: 0 }}>
                  Shared memory — save anytime; next agent wave picks it up.
                </p>
                <textarea className="doc-editor" value={doc} onChange={(e) => setDoc(e.target.value)} spellCheck={false} />
              </>
            )}
          </div>
        </aside>
      </div>

      <footer className="bottom">
        <span>
          <span className={"pulse" + (running ? " live" : "")} />
          {health?.root ? health.root.replace(/^\/Users\/[^/]+/, "~") : "…"}
        </span>
        <span>{running ? `● ${phase}` : "ready"}{counts.blocked ? ` · ${counts.blocked} blocked` : ""}</span>
      </footer>

      {toast && <div className="toast">{toast}</div>}
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")).render(<App />);
