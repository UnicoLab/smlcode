const { useState, useEffect, useCallback, useMemo, useRef } = React;

async function api(path, opts = {}) {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json", ...(opts.headers || {}) },
    ...opts,
  });
  if (!res.ok) throw new Error((await res.text()) || res.statusText);
  const ct = res.headers.get("content-type") || "";
  if (!ct.includes("application/json")) {
    const text = await res.text();
    throw new Error("expected JSON from " + path + ", got: " + text.slice(0, 80));
  }
  return res.json();
}

function normalizeBoard(b) {
  const tasks = Array.isArray(b?.tasks) ? b.tasks : [];
  return { ...(b || {}), tasks, plan: b?.plan || {} };
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

function agentRoleClass(name) {
  const n = String(name || "").toLowerCase().replace(/^@/, "");
  if (n.includes("review")) return "role-reviewer";
  if (n.includes("correct")) return "role-corrector";
  if (n.includes("explor") || n.includes("docs")) return "role-explorer";
  if (n.includes("test") || n.includes("qa")) return "role-tester";
  if (n.includes("plan") || n.includes("arch") || n.includes("split")) return "role-planner";
  if (n.includes("coord")) return "role-coordinator";
  return "role-worker";
}

function AgentAvatar({ agent, size }) {
  const name = String(agent || "?").replace(/^@/, "");
  const initials = name.slice(0, 2).toUpperCase() || "??";
  return (
    <span
      className={"agent-avatar " + agentRoleClass(name)}
      style={size ? { width: size, height: size, fontSize: Math.max(9, size * 0.36) } : undefined}
      title={"@" + name}
    >
      {initials}
    </span>
  );
}

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

const PROVIDER_PRESETS = [
  { id: "omlx", label: "omlx (local)" },
  { id: "ollama", label: "ollama" },
  { id: "openai", label: "openai" },
  { id: "lmstudio", label: "lmstudio" },
  { id: "openrouter", label: "openrouter" },
  { id: "vllm", label: "vllm" },
  { id: "litellm", label: "litellm" },
  { id: "together", label: "together" },
  { id: "groq", label: "groq" },
  { id: "deepseek", label: "deepseek" },
  { id: "custom", label: "custom (OpenAI-compat)" },
];

/** Lightweight markdown → HTML for Studio docs (no CDN). */
function renderMarkdown(src) {
  const esc = (s) => String(s)
    .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  const inline = (s) => {
    let t = esc(s);
    t = t.replace(/`([^`]+)`/g, "<code>$1</code>");
    t = t.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
    t = t.replace(/\*([^*]+)\*/g, "<em>$1</em>");
    t = t.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noreferrer">$1</a>');
    return t;
  };
  const lines = String(src || "").replace(/\r\n/g, "\n").split("\n");
  const out = [];
  let i = 0;
  let inCode = false;
  let codeBuf = [];
  let listBuf = [];
  const flushList = () => {
    if (!listBuf.length) return;
    out.push("<ul>" + listBuf.map((x) => "<li>" + inline(x) + "</li>").join("") + "</ul>");
    listBuf = [];
  };
  while (i < lines.length) {
    const line = lines[i];
    if (line.trim().startsWith("```")) {
      if (inCode) {
        out.push("<pre><code>" + esc(codeBuf.join("\n")) + "</code></pre>");
        codeBuf = [];
        inCode = false;
      } else {
        flushList();
        inCode = true;
      }
      i++;
      continue;
    }
    if (inCode) {
      codeBuf.push(line);
      i++;
      continue;
    }
    if (/^\s*[-*]\s+/.test(line)) {
      listBuf.push(line.replace(/^\s*[-*]\s+/, ""));
      i++;
      continue;
    }
    flushList();
    if (/^###\s+/.test(line)) out.push("<h3>" + inline(line.replace(/^###\s+/, "")) + "</h3>");
    else if (/^##\s+/.test(line)) out.push("<h2>" + inline(line.replace(/^##\s+/, "")) + "</h2>");
    else if (/^#\s+/.test(line)) out.push("<h1>" + inline(line.replace(/^#\s+/, "")) + "</h1>");
    else if (/^>\s?/.test(line)) out.push("<blockquote>" + inline(line.replace(/^>\s?/, "")) + "</blockquote>");
    else if (/^---+$/.test(line.trim())) out.push("<hr/>");
    else if (line.trim() === "") out.push("");
    else out.push("<p>" + inline(line) + "</p>");
    i++;
  }
  flushList();
  if (inCode) out.push("<pre><code>" + esc(codeBuf.join("\n")) + "</code></pre>");
  return out.join("\n");
}

function DepGraph({ tasks }) {
  const list = tasks || [];
  if (!list.length) return null;
  const idIndex = {};
  list.forEach((t, i) => { idIndex[t.id] = i; });
  const cols = Math.min(4, Math.max(2, Math.ceil(Math.sqrt(list.length))));
  const nodeW = 120;
  const nodeH = 52;
  const gapX = 36;
  const gapY = 28;
  const positions = list.map((_, i) => {
    const col = i % cols;
    const row = Math.floor(i / cols);
    return { x: 16 + col * (nodeW + gapX), y: 16 + row * (nodeH + gapY) };
  });
  const width = 16 + cols * (nodeW + gapX);
  const rows = Math.ceil(list.length / cols);
  const height = 16 + rows * (nodeH + gapY);
  const edges = [];
  list.forEach((t, i) => {
    (t.depends_on || []).forEach((dep) => {
      const j = idIndex[dep];
      if (j == null) return;
      const a = positions[j];
      const b = positions[i];
      edges.push({
        x1: a.x + nodeW,
        y1: a.y + nodeH / 2,
        x2: b.x,
        y2: b.y + nodeH / 2,
        key: dep + "->" + t.id,
      });
    });
  });
  return (
    <div className="dep-graph-flow" title="Task dependency graph">
      <div className="dep-graph-label">Dependencies</div>
      <svg className="dep-svg" width="100%" viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="xMinYMin meet">
        <defs>
          <marker id="depArrow" markerWidth="8" markerHeight="8" refX="6" refY="3" orient="auto">
            <path d="M0,0 L6,3 L0,6 Z" fill="var(--accent)" />
          </marker>
        </defs>
        {edges.map((e) => (
          <line key={e.key} x1={e.x1} y1={e.y1} x2={e.x2} y2={e.y2}
            stroke="var(--accent)" strokeOpacity="0.55" strokeWidth="1.5" markerEnd="url(#depArrow)" />
        ))}
        {list.map((t, i) => {
          const p = positions[i];
          return (
            <g key={t.id} className={"dep-flow-node " + (t.column || "")}>
              <rect x={p.x} y={p.y} width={nodeW} height={nodeH} rx="8"
                className={"dep-flow-rect " + (t.column || "")} />
              <text x={p.x + 10} y={p.y + 20} className="dep-flow-id">{t.id}</text>
              <text x={p.x + 10} y={p.y + 38} className="dep-flow-role">@{t.role || "worker"}</text>
            </g>
          );
        })}
      </svg>
    </div>
  );
}

function MarkdownDocEditor({ value, onChange, onSave, title }) {
  const [mode, setMode] = useState("split"); // edit | preview | split
  return (
    <>
      <div className="row" style={{ justifyContent: "space-between", marginBottom: 6 }}>
        <h3 style={{ margin: 0 }}>{title}</h3>
        <div className="row">
          {["edit", "split", "preview"].map((m) => (
            <button key={m} className={"sm" + (mode === m ? "" : " ghost")} onClick={() => setMode(m)}>{m}</button>
          ))}
          <button className="sm" onClick={onSave}>Save</button>
        </div>
      </div>
      <p className="lead" style={{ fontSize: "0.78rem", marginTop: 0 }}>
        Shared memory — save anytime; next agent wave picks it up.
      </p>
      <div className={"md-shell mode-" + mode}>
        {mode !== "preview" && (
          <textarea className="doc-editor" value={value} onChange={(e) => onChange(e.target.value)} spellCheck={false} />
        )}
        {mode !== "edit" && (
          <div className="md-preview" dangerouslySetInnerHTML={{ __html: renderMarkdown(value) }} />
        )}
      </div>
    </>
  );
}

function readStoredTheme() {
  try {
    const t = localStorage.getItem("slmcode-theme");
    if (t === "light" || t === "dark") return t;
  } catch (_) {}
  if (typeof window !== "undefined" && window.matchMedia) {
    return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }
  return "light";
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
  const emptyAgentDraft = () => ({
    id: "", title: "", description: "", system_prompt: "",
    skills: [], model: "", provider: "", endpoint: "", tools: true,
    max_iter: 10, temperature: 0.2, max_tokens: 2048,
  });
  const [agentDraft, setAgentDraft] = useState(emptyAgentDraft);
  const [agentEdit, setAgentEdit] = useState(null);
  const [activeTask, setActiveTask] = useState(null);
  const [taskHistory, setTaskHistory] = useState([]);
  const [autoScroll, setAutoScroll] = useState(true);
  const [streamPaused, setStreamPaused] = useState(false);
  const [fileRef, setFileRef] = useState("");
  const [theme, setTheme] = useState(readStoredTheme);
  const [injectNote, setInjectNote] = useState("");
  const [archives, setArchives] = useState([]);
  const [archiveView, setArchiveView] = useState(null);
  const [queries, setQueries] = useState([]);
  const [queryView, setQueryView] = useState(null);
  const [queryDocTab, setQueryDocTab] = useState("summary");
  const [apiConnected, setApiConnected] = useState(false);
  const [sseConnected, setSseConnected] = useState(false);
  const [apiKeyDraft, setApiKeyDraft] = useState("");
  const liveEndRef = useRef(null);
  const streamPausedRef = useRef(false);
  const autoScrollRef = useRef(true);
  const selectedRef = useRef(null);
  const esRef = useRef(null);
  selectedRef.current = selected;
  streamPausedRef.current = streamPaused;
  autoScrollRef.current = autoScroll;

  useEffect(() => {
    document.documentElement.setAttribute("data-theme", theme);
    try { localStorage.setItem("slmcode-theme", theme); } catch (_) {}
  }, [theme]);

  const toggleTheme = () => setTheme((t) => (t === "dark" ? "light" : "dark"));

  const showToast = (msg) => {
    setToast(msg);
    setTimeout(() => setToast(""), 2800);
  };

  const refreshBoard = useCallback(async () => {
    const b = normalizeBoard(await api("/api/board"));
    setBoard(b);
    const cur = selectedRef.current;
    if (cur) {
      const t = (b.tasks || []).find((x) => x.id === cur.id);
      if (t) setSelected(t);
    }
  }, []);

  const refreshArchives = useCallback(async () => {
    try {
      const list = await api("/api/archives");
      setArchives(Array.isArray(list) ? list : []);
    } catch (_) {
      setArchives([]);
    }
  }, []);

  const refreshQueries = useCallback(async () => {
    try {
      const list = await api("/api/queries");
      setQueries(Array.isArray(list) ? list : []);
    } catch (_) {
      setQueries([]);
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
      setApiConnected(!!h?.ok);
      setConfig(c);
      setSkills(Array.isArray(sk) ? sk : []);
      setModels(Array.isArray(mods.models) ? mods.models : []);
      setAgents(Array.isArray(ag) ? ag : []);
      if (c?.mode) setRunMode(c.mode);
      if (c?.specialist) setRunSpecialist(c.specialist);
      if (Array.isArray(c?.pinned_skills)) setPinSkills(c.pinned_skills);
      setRunning(!!(h.running || latest.running));
      if (Array.isArray(latest.events)) {
        setEvents(latest.events.filter((e) => e && e.kind !== "connected"));
      }
      if (latest.events?.length) {
        const last = latest.events[latest.events.length - 1];
        if (last?.phase && last.phase !== "idle") setPhase(last.phase);
      }
      await refreshBoard();
      await refreshArchives();
      await refreshQueries();
      setErr("");
    } catch (e) {
      setApiConnected(false);
      setErr("API unreachable: " + String(e.message || e));
    }
  }, [refreshBoard, refreshArchives, refreshQueries]);

  async function injectContext() {
    const note = injectNote.trim();
    if (!note) return;
    try {
      const cur = await api("/api/docs/SCRATCH.md");
      const stamp = new Date().toISOString();
      const next = (cur.content || "") + "\n\n## Injected context (" + stamp + ")\n\n" + note + "\n";
      await api("/api/docs/SCRATCH.md", { method: "PUT", body: JSON.stringify({ content: next }) });
      setInjectNote("");
      showToast("Context injected into SCRATCH.md");
      if (tab === "SCRATCH.md") setDoc(next);
    } catch (e) {
      setErr(String(e.message || e));
    }
  }

  async function openArchive(name) {
    try {
      const a = await api("/api/archives/" + encodeURIComponent(name));
      setArchiveView(a);
      setNav("archives");
    } catch (e) {
      setErr(String(e.message || e));
    }
  }

  async function openQuery(id) {
    try {
      const q = await api("/api/queries/" + encodeURIComponent(id));
      setQueryView(q);
      setQueryDocTab("summary");
      setNav("queries");
    } catch (e) {
      setErr(String(e.message || e));
    }
  }

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
    let closed = false;
    let retry = 0;
    let timer = null;

    const attach = () => {
      if (closed) return;
      if (esRef.current) {
        try { esRef.current.close(); } catch (_) {}
      }
      const es = new EventSource("/api/events");
      esRef.current = es;
      es.addEventListener("connected", (msg) => {
        setSseConnected(true);
        setApiConnected(true);
        retry = 0;
        try {
          const e = JSON.parse(msg.data);
          if (e?.message) showToast(e.message);
        } catch (_) {}
      });
      es.onmessage = (msg) => {
        setSseConnected(true);
        setApiConnected(true);
        retry = 0;
        try {
          const e = JSON.parse(msg.data);
          if (!e || e.kind === "connected") return;
          setEvents((prev) => {
            if (streamPausedRef.current) return prev;
            return [...prev.slice(-400), e];
          });
          if (e.phase) setPhase(e.phase);
          if (e.agent || e.kind === "agent_start" || e.kind === "agent_end" || e.kind === "output" || e.kind === "file_change") {
            setLiveAgent({
              agent: e.agent || e.phase,
              kind: e.kind,
              task: e.task_id,
              scope: e.scope,
              message: e.message,
              output: e.output,
              time: e.time,
            });
            if (e.task_id) setActiveTask(e.task_id);
            setTaskHistory((prev) => {
              const row = {
                id: e.task_id || e.phase,
                agent: e.agent || e.phase,
                kind: e.kind,
                message: e.message,
                scope: e.scope,
                time: e.time || new Date().toISOString(),
              };
              return [...prev.slice(-80), row];
            });
          }
          if (e.phase === "done" || e.phase === "error" || e.kind === "run_end" || e.kind === "run_stop") {
            setRunning(false);
            showToast(e.phase === "error" ? (e.message || "Run error") : (e.message || "Run finished"));
            refresh();
          } else if (e.kind === "run_start" || e.kind === "agent_start" || e.phase === "init" || e.phase === "execute") {
            setRunning(true);
          }
          refreshBoard();
        } catch (_) {}
      };
      es.onerror = () => {
        setSseConnected(false);
        try { es.close(); } catch (_) {}
        if (closed) return;
        const wait = Math.min(8000, 500 * Math.pow(2, retry++));
        timer = setTimeout(attach, wait);
      };
    };
    attach();
    return () => {
      closed = true;
      if (timer) clearTimeout(timer);
      if (esRef.current) {
        try { esRef.current.close(); } catch (_) {}
      }
    };
  }, [refresh, refreshBoard]);

  useEffect(() => {
    if (tab === "settings") return;
    api("/api/docs/" + tab)
      .then((d) => setDoc(d.content || ""))
      .catch((e) => setErr(String(e.message || e)));
  }, [tab]);

  useEffect(() => {
    if (!autoScrollRef.current || streamPausedRef.current) return;
    if (liveEndRef.current) {
      liveEndRef.current.scrollIntoView({ behavior: "smooth", block: "end" });
    }
  }, [events]);

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

  async function createAgent() {
    if (!agentDraft.id.trim()) return;
    try {
      await api("/api/agents", {
        method: "POST",
        body: JSON.stringify({
          ...agentDraft,
          id: agentDraft.id.trim().toLowerCase(),
          skills: Array.isArray(agentDraft.skills) ? agentDraft.skills : [],
          max_iter: Number(agentDraft.max_iter) || 10,
          temperature: Number(agentDraft.temperature) || 0.2,
          max_tokens: Number(agentDraft.max_tokens) || 2048,
        }),
      });
      setAgentDraft(emptyAgentDraft());
      await refresh();
      showToast("Custom agent @" + agentDraft.id.trim().toLowerCase() + " created");
    } catch (e) {
      setErr(String(e.message || e));
    }
  }

  async function saveAgentEdit() {
    if (!agentEdit?.id) return;
    try {
      await api("/api/agents/" + encodeURIComponent(agentEdit.id), {
        method: "PUT",
        body: JSON.stringify({
          ...agentEdit,
          skills: Array.isArray(agentEdit.skills) ? agentEdit.skills : [],
          max_iter: Number(agentEdit.max_iter) || 10,
          temperature: Number(agentEdit.temperature) || 0.2,
          max_tokens: Number(agentEdit.max_tokens) || 2048,
        }),
      });
      setAgentEdit(null);
      await refresh();
      showToast("Agent @" + agentEdit.id + " saved");
    } catch (e) {
      setErr(String(e.message || e));
    }
  }

  async function deleteAgent(id) {
    const a = agents.find((x) => x.id === id);
    const label = a?.override ? "Reset override for @" + id + "?" : "Delete custom agent @" + id + "?";
    if (!id || !confirm(label)) return;
    try {
      await api("/api/agents/" + encodeURIComponent(id), { method: "DELETE" });
      if (agentEdit?.id === id) setAgentEdit(null);
      await refresh();
      showToast((a?.override ? "Reset @" : "Deleted @") + id);
    } catch (e) {
      setErr(String(e.message || e));
    }
  }

  function toggleAgentSkill(target, setTarget, name) {
    const cur = Array.isArray(target.skills) ? target.skills : [];
    const next = cur.includes(name) ? cur.filter((x) => x !== name) : [...cur, name];
    setTarget({ ...target, skills: next });
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
    // Send only patch keys — never round-trip Public() config (api_key "***", etc.).
    const body = { ...patch };
    Object.keys(body).forEach((k) => {
      if (body[k] === undefined || body[k] === null) delete body[k];
    });
    const next = await api("/api/config", { method: "PUT", body: JSON.stringify(body) });
    setConfig(next);
    setApiConnected(true);
    if (patch.provider || patch.endpoint || patch.model || patch.api_key) {
      try {
        const mods = await api("/api/models");
        setModels(Array.isArray(mods.models) ? mods.models : []);
      } catch (_) { /* offline provider is fine */ }
    }
    showToast("Config saved — " + (next.provider || "?") + " / " + (next.model || "?"));
    await refresh();
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
    { id: "queries", label: "Queries", tip: "Per-turn plan/tasks" },
    { id: "archives", label: "Archives", tip: "Past runs" },
    { id: "agents", label: "Agents", tip: "Specialists" },
    { id: "skills", label: "Skills", tip: "Knowledge" },
  ];
  const boardPct = counts.total ? Math.round((counts.done / counts.total) * 100) : 0;

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand" title="SLMCode Studio">SLM<span>Code</span></div>
        <div className="query-wrap">
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Tell SLMCode what to do…  (@skill:name  @file:path/to.go)  Enter to run"
            aria-label="Task for SLMCode"
            onKeyDown={(e) => e.key === "Enter" && !running && query.trim() && startRun()}
          />
          <input
            className="file-ref-input"
            value={fileRef}
            onChange={(e) => setFileRef(e.target.value)}
            placeholder="@file…"
            title="Add a file/folder reference to the query"
            onKeyDown={(e) => {
              if (e.key === "Enter" && fileRef.trim()) {
                const ref = fileRef.trim().startsWith("@") ? fileRef.trim() : "@file:" + fileRef.trim();
                setQuery((q) => (q ? q + " " : "") + ref);
                setFileRef("");
                showToast("Added " + ref);
              }
            }}
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
        <button
          type="button"
          className="theme-toggle ghost sm"
          onClick={toggleTheme}
          title={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
          aria-label="Toggle color theme"
        >
          {theme === "dark" ? "Light" : "Dark"}
        </button>
      </header>

      <div className="pipeline" title="Pipeline progress">
        {PIPE.map((p, i) => (
          <React.Fragment key={p}>
            {i > 0 && <span className="pipe-sep">›</span>}
            <span className={"pipe-step" + (phase === p ? " active" : "") + (phaseIdx > i ? " done" : "")}>{p}</span>
          </React.Fragment>
        ))}
      </div>

      <div className={"conn-strip" + (apiConnected && sseConnected ? " ok" : apiConnected ? " warn" : " bad")}>
        <span className={"pulse" + (apiConnected ? " live" : "")} />
        <strong>{apiConnected ? "API connected" : "API offline"}</strong>
        <span>· SSE {sseConnected ? "live" : "reconnecting…"}</span>
        <span>· {health?.provider || "…"} / {String(health?.model || "").split("/").pop() || "…"}</span>
        <span className="conn-root" title={health?.root || ""}>{health?.root ? health.root.replace(/^\/Users\/[^/]+/, "~") : ""}</span>
        {!apiConnected && (
          <button className="sm ghost" onClick={() => refresh()}>Retry</button>
        )}
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

              <div className="agent-status-dashboard">
                {liveAgent && (
                  <div className="agent-card">
                    <div className="agent-card-header">
                      <div className="agent-card-title">
                        <AgentAvatar agent={liveAgent.agent} />
                        <div>
                          <strong>@{liveAgent.agent || "unknown"}</strong>
                          <div className="agent-card-role">{liveAgent.kind || "phase"}</div>
                        </div>
                      </div>
                      <div className="agent-card-role">
                        {liveAgent.kind === "agent_start" ? (
                          <span className="status-indicator running" />
                        ) : liveAgent.kind === "agent_end" ? (
                          <span className="status-indicator succeeded" />
                        ) : (
                          <span className="status-indicator idle" />
                        )}
                      </div>
                    </div>
                    <div className="agent-card-body">
                      <div><strong>Status</strong> — {liveAgent.message}</div>
                      <div className="meta-row">
                        {liveAgent.task ? <span><strong>Task</strong> {liveAgent.task}</span> : null}
                        {liveAgent.scope ? <span><strong>Scope</strong> {liveAgent.scope}</span> : null}
                      </div>
                      {liveAgent.output ? (
                        <pre className="output-box" style={{ maxHeight: 120 }}>{liveAgent.output}</pre>
                      ) : null}
                    </div>
                  </div>
                )}
              </div>

              <div className="inject-box">
                <strong>Enrich context</strong>
                <div className="lead" style={{ margin: "0.2rem 0 0" }}>
                  Append notes to SCRATCH.md — workers pick them up on the next step.
                </div>
                <textarea
                  value={injectNote}
                  onChange={(e) => setInjectNote(e.target.value)}
                  placeholder="Add constraints, paths, or corrections…"
                />
                <button className="sm secondary" disabled={!injectNote.trim()} onClick={injectContext}>
                  Inject context
                </button>
              </div>

              <div className="event-history">
                <div className="row" style={{ justifyContent: "space-between", alignItems: "center" }}>
                  <h3 style={{ margin: 0 }}>Live stream</h3>
                  <div className="row">
                    <button className={"sm" + (autoScroll ? "" : " ghost")} onClick={() => setAutoScroll((v) => !v)} title="Auto-scroll to latest">
                      {autoScroll ? "↓ Live scroll" : "Scroll paused"}
                    </button>
                    <button className={"sm" + (streamPaused ? "" : " ghost")} onClick={() => setStreamPaused((v) => !v)} title="Pause appending events">
                      {streamPaused ? "Resume" : "Pause"}
                    </button>
                    <button className="sm ghost" onClick={() => { setEvents([]); setTaskHistory([]); }}>Clear</button>
                  </div>
                </div>
                {taskHistory.length > 0 && (
                  <div className="observability-strip">
                    {taskHistory.slice(-8).map((h, i) => (
                      <div key={i} className={"obs-chip " + (h.kind || "")} title={h.message}>
                        <AgentAvatar agent={h.agent} size={18} />
                        <span className="obs-agent">@{h.agent || "?"}</span>
                        <span className="obs-task">{h.id || "—"}</span>
                        <span className="obs-kind">{h.kind || "phase"}</span>
                      </div>
                    ))}
                  </div>
                )}
                {(board.tasks || []).length > 0 && <DepGraph tasks={board.tasks} />}
                <ul className="event-list live-stream">
                  {events.map((e, i) => (
                    <li key={i} className={"event-item kind-" + (e.kind || e.phase || "phase")}>
                      <div className="event-avatar">
                        <AgentAvatar agent={e.agent || e.phase} size={28} />
                      </div>
                      <div className="event-body">
                        <div className="event-header">
                          <span className="phase">{e.kind || e.phase}</span>
                          {e.agent ? <strong className="agent-name">@{e.agent}</strong> : null}
                          {e.task_id ? <span className="id">{e.task_id}</span> : null}
                        </div>
                        <div className="event-content">
                          <div className="event-message">{e.message}</div>
                          {e.scope ? (
                            <div style={{ color: "var(--muted)", fontSize: "0.75rem", marginTop: "0.2rem" }}>
                              {e.kind === "file_change" ? "file: " : "scope: "}{e.scope}
                            </div>
                          ) : null}
                          {e.kind === "file_change" && e.output ? (
                            <pre className="file-patch-card">{String(e.output).slice(0, 600)}{String(e.output).length > 600 ? "…" : ""}</pre>
                          ) : e.output ? (
                            <pre className="mini-out" style={{ marginTop: "0.3rem" }}>{String(e.output).slice(0, 400)}{String(e.output).length > 400 ? "…" : ""}</pre>
                          ) : null}
                        </div>
                      </div>
                    </li>
                  ))}
                  <li ref={liveEndRef} style={{ listStyle: "none", height: 1, margin: 0, padding: 0 }} />
                  {!events.length && (
                    <li className="empty-state" style={{ listStyle: "none" }}>
                      <strong>Waiting for a run</strong>
                      <p>Type a task up top and press <em>Run</em>. Live scroll follows the latest agent action.</p>
                    </li>
                  )}
                </ul>
              </div>
            </>
          )}

          {nav === "queries" && (
            <>
              <h2>Queries</h2>
              <p className="lead">
                Each user turn gets a dedicated plan, tasks, and <code>summary.md</code> under{" "}
                <code>.slmcode/queries/&lt;id&gt;/</code>. Live board is always the current query only.
              </p>
              <div className="split-panels">
                <div className="panel-box">
                  <div className="row" style={{ justifyContent: "space-between" }}>
                    <h3 style={{ margin: 0 }}>Turns ({queries.length})</h3>
                    <button className="sm ghost" onClick={refreshQueries}>Refresh</button>
                  </div>
                  {!queries.length ? (
                    <div className="empty-state" style={{ marginTop: 10 }}>
                      <strong>No query turns yet</strong>
                      <p>Run a task — each interaction is stored as its own plan/tasks/summary.</p>
                    </div>
                  ) : (
                    <ul className="archive-list query-list" style={{ marginTop: 10 }}>
                      {queries.map((q) => (
                        <li
                          key={q.id}
                          className={queryView?.id === q.id ? "active" : ""}
                          onClick={() => openQuery(q.id)}
                        >
                          <div className="row" style={{ justifyContent: "space-between", gap: 8 }}>
                            <div className="name">{q.id}</div>
                            <span className={"query-badge" + (q.success ? " ok" : " bad")}>
                              {q.success ? "ok" : "open"}
                            </span>
                          </div>
                          <div className="query-ask">{q.query || "(empty query)"}</div>
                          <div className="when">{q.updated_at || ""}{q.summary ? " · " + q.summary : ""}</div>
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
                <div className="panel-box">
                  {queryView ? (
                    <>
                      <h3 style={{ marginTop: 0 }}>{queryView.id}</h3>
                      <p className="lead" style={{ marginTop: 4 }}>{queryView.query}</p>
                      <div className="row query-doc-tabs" style={{ gap: 6, marginBottom: 10, flexWrap: "wrap" }}>
                        {[
                          { id: "summary", label: "Summary" },
                          { id: "plan", label: "Plan" },
                          { id: "tasks", label: "Tasks" },
                          { id: "board", label: "Board" },
                        ].map((t) => (
                          <button
                            key={t.id}
                            className={"sm" + (queryDocTab === t.id ? "" : " ghost")}
                            onClick={() => setQueryDocTab(t.id)}
                          >
                            {t.label}
                          </button>
                        ))}
                      </div>
                      {queryDocTab === "board" ? (
                        <div className="query-board-meta">
                          <div className="lead">
                            {(queryView.board?.tasks || []).length} tasks · plan:{" "}
                            {queryView.board?.plan?.summary || "—"}
                          </div>
                          <ul className="archive-list" style={{ marginTop: 8 }}>
                            {(queryView.board?.tasks || []).map((t) => (
                              <li key={t.id} style={{ cursor: "default" }}>
                                <div className="name">{t.id} · {t.column} · {t.role}</div>
                                <div className="query-ask">{t.title}</div>
                                {t.files?.length ? (
                                  <div className="when">files: {t.files.join(", ")}</div>
                                ) : null}
                              </li>
                            ))}
                          </ul>
                        </div>
                      ) : (
                        <div
                          className="md-preview"
                          dangerouslySetInnerHTML={{
                            __html: renderMarkdown(
                              queryDocTab === "plan"
                                ? (queryView.plan_md || "")
                                : queryDocTab === "tasks"
                                  ? (queryView.tasks_md || "")
                                  : (queryView.summary_md || queryView.summary || "")
                            ),
                          }}
                        />
                      )}
                    </>
                  ) : (
                    <div className="empty-state">
                      <strong>Select a query turn</strong>
                      <p>Inspect that turn’s plan, tasks, and summary without mutating the live board.</p>
                    </div>
                  )}
                </div>
              </div>
            </>
          )}

          {nav === "archives" && (
            <>
              <h2>Archives</h2>
              <p className="lead">Completed runs are saved as history threads under <code>.slmcode/archives/</code>.</p>
              <div className="split-panels">
                <div className="panel-box">
                  <div className="row" style={{ justifyContent: "space-between" }}>
                    <h3 style={{ margin: 0 }}>Past runs ({archives.length})</h3>
                    <button className="sm ghost" onClick={refreshArchives}>Refresh</button>
                  </div>
                  {!archives.length ? (
                    <div className="empty-state" style={{ marginTop: 10 }}>
                      <strong>No archives yet</strong>
                      <p>Finish a Run — each completed query is archived automatically.</p>
                    </div>
                  ) : (
                    <ul className="archive-list" style={{ marginTop: 10 }}>
                      {archives.map((a) => (
                        <li
                          key={a.name}
                          className={archiveView?.name === a.name ? "active" : ""}
                          onClick={() => openArchive(a.name)}
                        >
                          <div className="name">{a.name}</div>
                          <div className="when">{a.modified || a.size ? ((a.modified || "") + (a.size ? " · " + a.size + " B" : "")) : ""}</div>
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
                <div className="panel-box">
                  {archiveView ? (
                    <>
                      <h3>{archiveView.name}</h3>
                      <div className="md-preview" dangerouslySetInnerHTML={{ __html: renderMarkdown(archiveView.content || "") }} />
                    </>
                  ) : (
                    <div className="empty-state">
                      <strong>Select an archive</strong>
                      <p>Open a finished run to review query, plan, and memory snapshot.</p>
                    </div>
                  )}
                </div>
              </div>
            </>
          )}

          {nav === "agents" && (
            <>
              <h2>Agents</h2>
              <p className="lead">
                Built-in specialists plus your custom agents. Custom agents persist under{" "}
                <code>.slmcode/agents/</code> (and <code>~/.slmcode/agents/</code>).
              </p>
              <div className="split-panels agents-crud">
                <div className="panel-box">
                  <h3>Roster ({agents.length})</h3>
                  <ul className="event-list agent-roster">
                    {agents.map((a) => (
                      <li key={a.id} className={a.custom ? "agent-custom" : "agent-builtin"}>
                        <div className="row" style={{ justifyContent: "space-between", gap: 8 }}>
                          <strong>@{a.id}</strong>
                          <span className={"badge " + (a.custom ? "ok" : "muted")}>
                            {a.custom ? "custom" : "built-in"}
                          </span>
                        </div>
                        <div style={{ color: "var(--muted)", marginTop: 4 }}>{a.title || a.role}</div>
                        {a.description ? <p className="lead" style={{ marginTop: 4 }}>{a.description}</p> : null}
                        <div style={{ marginTop: 6, fontFamily: "var(--font-mono)", fontSize: "0.72rem", color: "var(--muted)" }}>
                          {a.tools ? "⚙ can edit files" : "reasoning only"}
                          {a.provider || a.model ? ` · ${a.provider || "default"}/${a.model || "default"}` : ""}
                          {a.endpoint ? ` · ${a.endpoint}` : ""}
                          {a.override ? " · overridden" : ""}
                          {a.skills?.length ? ` · skills: ${a.skills.join(", ")}` : ""}
                        </div>
                        <div className="row" style={{ marginTop: 8 }}>
                          <button className="sm ghost" onClick={() => setAgentEdit({
                            id: a.id,
                            title: a.title || a.role || "",
                            description: a.description || "",
                            system_prompt: a.system_prompt || "",
                            skills: a.skills || [],
                            model: a.model || "",
                            provider: a.provider || "",
                            endpoint: a.endpoint || "",
                            tools: !!a.tools,
                            max_iter: a.max_iter || 10,
                            temperature: a.temperature ?? 0.2,
                            max_tokens: a.max_tokens || 2048,
                            builtin: !!a.builtin,
                            override: !!a.override,
                          })}>{a.custom ? "Edit" : (a.override ? "Edit override" : "Customize")}</button>
                          {(a.custom || a.override) && (
                            <button className="sm ghost danger" onClick={() => deleteAgent(a.id)}>
                              {a.override ? "Reset" : "Delete"}
                            </button>
                          )}
                          <button className="sm ghost" onClick={() => {
                            setRunMode("specialist");
                            setRunSpecialist(a.id);
                            showToast("Selected @" + a.id + " for specialist run");
                          }}>Use</button>
                        </div>
                      </li>
                    ))}
                  </ul>
                </div>
                <div className="panel-box">
                  {agentEdit ? (
                    <>
                      <h3>{agentEdit.builtin ? "Customize" : "Edit"} @{agentEdit.id}</h3>
                      {agentEdit.builtin ? <p className="lead">Saves a project override under <code>.slmcode/agents/{agentEdit.id}.yaml</code> — provider/model/settings apply at runtime.</p> : null}
                      <label>Title<input value={agentEdit.title || ""} onChange={(e) => setAgentEdit({ ...agentEdit, title: e.target.value })} /></label>
                      <label>Description<input value={agentEdit.description || ""} onChange={(e) => setAgentEdit({ ...agentEdit, description: e.target.value })} /></label>
                      <label>Provider<input value={agentEdit.provider || ""} onChange={(e) => setAgentEdit({ ...agentEdit, provider: e.target.value })} placeholder="empty = active provider" /></label>
                      <label>Model<input value={agentEdit.model || ""} onChange={(e) => setAgentEdit({ ...agentEdit, model: e.target.value })} placeholder="empty = active model" /></label>
                      <label>Endpoint<input value={agentEdit.endpoint || ""} onChange={(e) => setAgentEdit({ ...agentEdit, endpoint: e.target.value })} placeholder="empty = provider default" /></label>
                      <div className="row" style={{ gap: 12, flexWrap: "wrap" }}>
                        <label>Max iter<input type="number" value={agentEdit.max_iter} onChange={(e) => setAgentEdit({ ...agentEdit, max_iter: e.target.value })} /></label>
                        <label>Temp<input type="number" step="0.05" value={agentEdit.temperature} onChange={(e) => setAgentEdit({ ...agentEdit, temperature: e.target.value })} /></label>
                        <label>Max tokens<input type="number" value={agentEdit.max_tokens} onChange={(e) => setAgentEdit({ ...agentEdit, max_tokens: e.target.value })} /></label>
                        <label className="check"><input type="checkbox" checked={!!agentEdit.tools} onChange={(e) => setAgentEdit({ ...agentEdit, tools: e.target.checked })} /> Coding tools</label>
                      </div>
                      <div style={{ margin: "8px 0" }}>
                        <div className="lead" style={{ marginBottom: 6 }}>Skills</div>
                        <div className="row" style={{ flexWrap: "wrap", gap: 6 }}>
                          {skills.map((s) => {
                            const on = (agentEdit.skills || []).includes(s.name);
                            return (
                              <button key={s.name} type="button" className={"sm" + (on ? "" : " ghost")}
                                onClick={() => toggleAgentSkill(agentEdit, setAgentEdit, s.name)}>{on ? "✓ " : ""}{s.name}</button>
                            );
                          })}
                        </div>
                      </div>
                      <label>System prompt<textarea className="doc-editor" style={{ minHeight: 160 }} value={agentEdit.system_prompt || ""} onChange={(e) => setAgentEdit({ ...agentEdit, system_prompt: e.target.value })} /></label>
                      <div className="row">
                        <button className="sm" onClick={saveAgentEdit}>Save</button>
                        <button className="sm ghost" onClick={() => setAgentEdit(null)}>Cancel</button>
                      </div>
                    </>
                  ) : (
                    <>
                      <h3>New custom agent</h3>
                      <label>ID<input placeholder="night-auditor" value={agentDraft.id} onChange={(e) => setAgentDraft({ ...agentDraft, id: e.target.value })} /></label>
                      <label>Title<input value={agentDraft.title} onChange={(e) => setAgentDraft({ ...agentDraft, title: e.target.value })} placeholder="Night Auditor" /></label>
                      <label>Description<input value={agentDraft.description} onChange={(e) => setAgentDraft({ ...agentDraft, description: e.target.value })} /></label>
                      <label>Provider<input value={agentDraft.provider} onChange={(e) => setAgentDraft({ ...agentDraft, provider: e.target.value })} placeholder="ollama / omlx / openai / …" /></label>
                      <label>Model<input value={agentDraft.model} onChange={(e) => setAgentDraft({ ...agentDraft, model: e.target.value })} placeholder="model id" /></label>
                      <label>Endpoint<input value={agentDraft.endpoint} onChange={(e) => setAgentDraft({ ...agentDraft, endpoint: e.target.value })} placeholder="http://127.0.0.1:11434 or …/v1" /></label>
                      <div className="row" style={{ gap: 12, flexWrap: "wrap" }}>
                        <label>Max iter<input type="number" value={agentDraft.max_iter} onChange={(e) => setAgentDraft({ ...agentDraft, max_iter: e.target.value })} /></label>
                        <label>Temp<input type="number" step="0.05" value={agentDraft.temperature} onChange={(e) => setAgentDraft({ ...agentDraft, temperature: e.target.value })} /></label>
                        <label>Max tokens<input type="number" value={agentDraft.max_tokens} onChange={(e) => setAgentDraft({ ...agentDraft, max_tokens: e.target.value })} /></label>
                        <label className="check"><input type="checkbox" checked={!!agentDraft.tools} onChange={(e) => setAgentDraft({ ...agentDraft, tools: e.target.checked })} /> Coding tools</label>
                      </div>
                      <div style={{ margin: "8px 0" }}>
                        <div className="lead" style={{ marginBottom: 6 }}>Skills</div>
                        <div className="row" style={{ flexWrap: "wrap", gap: 6 }}>
                          {skills.map((s) => {
                            const on = (agentDraft.skills || []).includes(s.name);
                            return (
                              <button key={s.name} type="button" className={"sm" + (on ? "" : " ghost")}
                                onClick={() => toggleAgentSkill(agentDraft, setAgentDraft, s.name)}>{on ? "✓ " : ""}{s.name}</button>
                            );
                          })}
                        </div>
                      </div>
                      <label>System prompt<textarea className="doc-editor" style={{ minHeight: 140 }} value={agentDraft.system_prompt} onChange={(e) => setAgentDraft({ ...agentDraft, system_prompt: e.target.value })} placeholder="You are a specialist that…" /></label>
                      <button className="sm" onClick={createAgent}>Create agent</button>
                    </>
                  )}
                </div>
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
              {counts.total > 0 && (
                <div className="board-progress">
                  <div className="progress-bar"><span style={{ width: boardPct + "%" }} /></div>
                  <span className="pct">{counts.done}/{counts.total} · {boardPct}%</span>
                </div>
              )}

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
                          <div className="card-header">
                            <div className="id">{t.id}</div>
                            <div className="role-badge">@{t.role}</div>
                          </div>
                          <strong>{t.title}</strong>
                          <div className="card-footer">
                            {t.depends_on && t.depends_on.length > 0 && (
                              <div className="dependencies">
                                <span style={{ color: "var(--warn)", fontSize: "0.7rem" }}>deps: {t.depends_on.join(", ")}</span>
                              </div>
                            )}
                            {total > 0 && (
                              <div style={{ color: "var(--muted)", marginTop: "0.2rem", fontSize: "0.7rem" }}>
                                ✓ {done}/{total} completed
                              </div>
                            )}
                            {t.status && t.status !== "pending" && (
                              <div style={{
                                color: t.status === "done" ? "var(--success)" :
                                       t.status === "failed" ? "var(--bad)" :
                                       t.status === "in_progress" ? "var(--accent)" : "var(--warn)",
                                fontSize: "0.7rem",
                                marginTop: "0.2rem"
                              }}>
                                Status: {t.status}
                              </div>
                            )}
                          </div>
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
                <h3>Model & provider</h3>
                <p className="lead" style={{ fontSize: "0.75rem", marginTop: 0 }}>
                  Any OpenAI-compatible endpoint works — oMLX, Ollama, LM Studio, cloud OpenAI, OpenRouter, vLLM, …
                </p>
                <label>Provider
                  <select
                    value={PROVIDER_PRESETS.some((p) => p.id === config.provider) ? config.provider : "custom"}
                    onChange={(e) => {
                      const v = e.target.value;
                      if (v === "custom") {
                        const name = prompt("Provider name (OpenAI-compatible gateway id)", config.provider || "custom");
                        if (name) saveConfig({ provider: name });
                        return;
                      }
                      saveConfig({ provider: v });
                    }}
                  >
                    {PROVIDER_PRESETS.map((p) => (
                      <option key={p.id} value={p.id}>{p.label}</option>
                    ))}
                  </select>
                </label>
                {!PROVIDER_PRESETS.some((p) => p.id === config.provider) && (
                  <label>Custom provider id
                    <input defaultValue={config.provider} onBlur={(e) => e.target.value && saveConfig({ provider: e.target.value })} />
                  </label>
                )}
                <label>Model
                  <input
                    list="model-list"
                    defaultValue={config.model || ""}
                    key={"model-" + (config.model || "")}
                    onBlur={(e) => e.target.value && saveConfig({ model: e.target.value })}
                    placeholder="model id served by your provider"
                  />
                  <datalist id="model-list">
                    {[config.model, ...models.filter((m) => m && m !== config.model)].filter(Boolean).map((m) => (
                      <option key={m} value={m} />
                    ))}
                  </datalist>
                </label>
                <label>Endpoint
                  <input
                    defaultValue={config.endpoint}
                    key={"ep-" + (config.endpoint || "")}
                    onBlur={(e) => saveConfig({ endpoint: e.target.value })}
                    placeholder="http://127.0.0.1:8000/v1"
                  />
                </label>
                <label>API key (optional — leave blank to keep current / env / oMLX store)
                  <input
                    type="password"
                    value={apiKeyDraft}
                    onChange={(e) => setApiKeyDraft(e.target.value)}
                    onBlur={() => {
                      if (apiKeyDraft.trim()) {
                        saveConfig({ api_key: apiKeyDraft.trim() });
                        setApiKeyDraft("");
                      }
                    }}
                    placeholder={config.api_key === "***" ? "•••• saved" : "not set"}
                    autoComplete="off"
                  />
                </label>
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
                <label>Think passes (planning depth)
                  <input type="number" min={1} max={5} defaultValue={config.think_passes} onBlur={(e) => saveConfig({ think_passes: Number(e.target.value) })} />
                </label>
                <label>Max parallel<input type="number" min={1} max={8} defaultValue={config.max_parallel} onBlur={(e) => saveConfig({ max_parallel: Number(e.target.value) })} /></label>
                <label>Review retries<input type="number" min={0} max={5} defaultValue={config.max_retries} onBlur={(e) => saveConfig({ max_retries: Number(e.target.value) })} /></label>
                <label>Context budget KB<input type="number" min={4} max={64} defaultValue={config.max_context_kb} onBlur={(e) => saveConfig({ max_context_kb: Number(e.target.value) })} /></label>
                <h3 style={{ marginTop: 14 }}>QA gate</h3>
                <label className="row" style={{ gap: 8, alignItems: "center" }}>
                  <input
                    type="checkbox"
                    checked={!!config.qa_gate}
                    onChange={(e) => saveConfig({ qa_gate: e.target.checked })}
                  />
                  Iterate until tests pass
                </label>
                <label>QA command
                  <input
                    defaultValue={config.qa_gate_command || ""}
                    key={"qacmd-" + (config.qa_gate_command || "")}
                    placeholder="auto (go test ./… -short, npm test, …)"
                    onBlur={(e) => saveConfig({ qa_gate_command: e.target.value })}
                  />
                </label>
                <label>QA max rounds
                  <input type="number" min={1} max={8} defaultValue={config.qa_gate_max_rounds || 3}
                    onBlur={(e) => saveConfig({ qa_gate_max_rounds: Number(e.target.value) })} />
                </label>
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
              <MarkdownDocEditor title={tab} value={doc} onChange={setDoc} onSave={saveDoc} />
            )}
          </div>
        </aside>
      </div>

      <footer className="bottom">
        <span>
          <span className={"pulse" + (running || sseConnected ? " live" : "")} />
          {apiConnected ? "api ok" : "api down"} · {sseConnected ? "sse ok" : "sse down"} ·{" "}
          {health?.root ? health.root.replace(/^\/Users\/[^/]+/, "~") : "…"}
        </span>
        <span className="footer-live">
          {running ? `● ${phase}` : "ready"}
          {liveAgent?.agent ? ` · @${liveAgent.agent}` : ""}
          {activeTask ? ` · ${activeTask}` : ""}
          {counts.doing ? ` · ${counts.doing} active` : ""}
          {counts.done ? ` · ${counts.done} done` : ""}
          {counts.blocked ? ` · ${counts.blocked} blocked` : ""}
          {typeof health?.events === "number" ? ` · ${health.events} events` : ""}
        </span>
        <span className="footer-actions">
          {running && (
            <button className="sm danger" onClick={async () => {
              try {
                await api("/api/runs/stop", { method: "POST", body: "{}" });
                setRunning(false);
                showToast("Stopped");
              } catch (e) {
                setErr(String(e.message || e));
              }
            }}>Stop run</button>
          )}
        </span>
      </footer>

      {toast && <div className="toast">{toast}</div>}
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")).render(<App />);
