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

const ROLES = ["worker", "deep", "explorer", "docs", "architect", "reviewer", "corrector", "tester", "placeholder", "context", "coordinator"];
const DEFAULT_PIPE = ["init", "skills", "context", "explore", "docs", "architect", "clarify", "plan", "split", "coord", "execute", "learn", "polish", "test", "memory", "done"];

/** Fallback metadata when /api/pipeline is unavailable. */
const DEFAULT_PIPE_META = {
  init: { label: "Init", tip: "Boot workspace + session", group: "prepare" },
  skills: { label: "Skills", tip: "Load skills & knowledge packs", group: "prepare" },
  context: { label: "Context", tip: "Refresh CONTEXT / project memory", group: "prepare" },
  explore: { label: "Explore", tip: "Discover relevant files", group: "prepare" },
  docs: { label: "Docs", tip: "Read docs & conventions", group: "prepare" },
  architect: { label: "Architect", tip: "Shape approach & components", group: "design" },
  clarify: { label: "Clarify", tip: "Lock PRD / ask decisions", group: "design" },
  plan: { label: "Plan", tip: "Write the execution plan", group: "design" },
  split: { label: "Split", tip: "Break into atomic tasks", group: "design" },
  coord: { label: "Coord", tip: "Coordinate board & focus", group: "build" },
  execute: { label: "Execute", tip: "Workers implement + review", group: "build" },
  learn: { label: "Learn", tip: "Capture lessons mid-run", group: "build" },
  polish: { label: "Polish", tip: "Fill placeholders / flag precise gaps", group: "verify" },
  test: { label: "Test", tip: "Tester + QA gate verification", group: "verify" },
  memory: { label: "Memory", tip: "Distill long-term memory", group: "finish" },
  done: { label: "Done", tip: "Run complete", group: "finish" },
  idle: { label: "Idle", tip: "Waiting for a run", group: "prepare" },
  error: { label: "Error", tip: "Run failed", group: "finish" },
};

const DEFAULT_PIPE_GROUPS = [
  { id: "prepare", label: "Prepare", steps: ["init", "skills", "context", "explore", "docs"] },
  { id: "design", label: "Design", steps: ["architect", "clarify", "plan", "split"] },
  { id: "build", label: "Build", steps: ["coord", "execute", "learn"] },
  { id: "verify", label: "Verify", steps: ["polish", "test"] },
  { id: "finish", label: "Finish", steps: ["memory", "done"] },
];

function pipeFromConfig(cfg) {
  const order = (cfg?.order && cfg.order.length) ? cfg.order : DEFAULT_PIPE;
  const phases = cfg?.phases || {};
  const meta = { ...DEFAULT_PIPE_META };
  Object.keys(phases).forEach((id) => {
    const p = phases[id] || {};
    meta[id] = {
      label: p.label || meta[id]?.label || id,
      tip: p.tip || meta[id]?.tip || id,
      group: p.group || meta[id]?.group || "prepare",
      agent: p.agent || "",
      when: p.when || "always",
    };
  });
  meta.idle = DEFAULT_PIPE_META.idle;
  meta.error = DEFAULT_PIPE_META.error;
  const groups = (cfg?.groups && cfg.groups.length)
    ? cfg.groups.map((g) => ({ id: g.id, label: g.label, steps: g.steps || [] }))
    : DEFAULT_PIPE_GROUPS;
  return { order, meta, groups, execute: cfg?.execute || {}, slots: cfg?.slots || [] };
}

function pipeIndex(phase, order) {
  const pipe = order && order.length ? order : DEFAULT_PIPE;
  const i = pipe.indexOf(phase);
  return i < 0 ? -1 : i;
}

const LOOP_DONE_ACTIONS = new Set(["resolved", "aborted", "flag_only"]);
const LOOP_LABELS = {
  tester_reject: "Tester rejected",
  rewrite: "Rewriting plan",
  replan: "Re-scoping",
  corrective_wave: "Corrective wave",
  reverify: "Re-verifying",
  continue_pending: "Awaiting decision",
  continue_wave: "Continue wave",
  placeholder_gaps: "Placeholder gaps",
  resolved: "Loop cleared",
  aborted: "Stopped",
  flag_only: "Gaps flagged",
};

function parseLoopEvent(e) {
  if (!e) return null;
  let data = {};
  if (e.output) {
    try { data = JSON.parse(e.output) || {}; } catch (_) { data = {}; }
  }
  return {
    action: data.action || e.scope || "loop",
    reason: data.reason || e.message || "",
    wave: data.wave || 0,
    failures: Array.isArray(data.failures) ? data.failures : [],
    from: data.from || "",
    to: data.to || e.phase || "",
    awaiting: !!data.awaiting || String(e.scope || "").includes("awaiting"),
    message: e.message || data.reason || "",
    time: e.time,
  };
}

function PipelineHeader({
  phase, running, liveAgent, counts, intervention, turnMeter,
  loopState, continueAsk, escalateAsk, onContinue,
  pipeOrder, pipeMeta, pipeGroups, slots,
}) {
  const order = pipeOrder && pipeOrder.length ? pipeOrder : DEFAULT_PIPE;
  const metaMap = pipeMeta || DEFAULT_PIPE_META;
  const groups = pipeGroups && pipeGroups.length ? pipeGroups : DEFAULT_PIPE_GROUPS;
  const idx = pipeIndex(phase, order);
  const total = order.length;
  const looping = !!(loopState && !LOOP_DONE_ACTIONS.has(loopState.action));
  const awaiting = !!(continueAsk || escalateAsk || (loopState && loopState.awaiting));
  const rewindIdx = looping ? pipeIndex(loopState.to || phase, order) : -1;
  const effectiveIdx = looping && rewindIdx >= 0 ? Math.min(idx, rewindIdx) : idx;
  const doneCount = phase === "done" && !looping ? total : Math.max(0, effectiveIdx);
  const pct = !running && phase === "idle"
    ? 0
    : phase === "done" && !looping
      ? 100
      : Math.max(4, Math.round(((doneCount + (running || looping ? 0.45 : 0)) / total) * 100));
  const meta = metaMap[phase] || metaMap.idle || DEFAULT_PIPE_META.idle;
  const activeGroup = looping
    ? (metaMap[loopState.to || phase]?.group || meta.group || "verify")
    : (meta.group || "prepare");
  const boardPct = counts.total ? Math.round((counts.done / counts.total) * 100) : 0;
  const slotCount = (slots || []).filter((s) => s && s.enabled !== false && s.when !== "never").length;
  const statusLabel = phase === "error"
    ? "Failed"
    : awaiting
      ? "Awaiting you"
      : looping
        ? (LOOP_LABELS[loopState.action] || "Looping")
        : phase === "done"
          ? "Complete"
          : running
            ? "In progress"
            : phase && phase !== "idle"
              ? "Paused / last"
              : "Ready";
  const headerClass =
    "pipeline-header" +
    (running ? " is-running" : "") +
    (phase === "done" && !looping ? " is-done" : "") +
    (phase === "error" ? " is-error" : "") +
    (looping ? " is-looping" : "") +
    (awaiting ? " is-awaiting" : "");

  return (
    <div className={headerClass}>
      <div className="pipeline-header-top">
        <div className="pipeline-status-block">
          <div className="pipeline-status-row">
            <span className={
              "pipeline-status-dot" +
              (awaiting ? " await" : looping ? " loop" : running ? " live" : phase === "done" ? " ok" : phase === "error" ? " bad" : "")
            } />
            <strong className="pipeline-status-label">{statusLabel}</strong>
            <span className="pipeline-phase-chip" title={meta.tip}>
              {meta.label}
              <span className="pipeline-phase-id">{phase && phase !== "idle" ? phase : "idle"}</span>
            </span>
            {looping || loopState?.wave ? (
              <span className="pipeline-loop-chip" title={loopState?.reason || "pipeline loop"}>
                wave {loopState.wave || "?"}
                {loopState.from && loopState.to ? (
                  <span className="pipeline-phase-id">{loopState.from}→{loopState.to}</span>
                ) : null}
              </span>
            ) : null}
          </div>
          <div className="pipeline-status-sub">
            {looping && loopState?.reason ? (
              <span className="pipeline-loop-reason">{String(loopState.reason).slice(0, 120)}</span>
            ) : running && liveAgent?.agent ? (
              <span>
                <AgentAvatar agent={liveAgent.agent} size={16} />
                @{liveAgent.agent}
                {liveAgent.task ? <span className="pipeline-task-id"> · {liveAgent.task}</span> : null}
                {liveAgent.message ? <span className="pipeline-msg"> — {String(liveAgent.message).slice(0, 72)}</span> : null}
              </span>
            ) : (
              <span>{meta.tip}</span>
            )}
          </div>
        </div>
        <div className="pipeline-metrics">
          <div className="pipeline-metric" title="Pipeline step progress">
            <b>{phase === "done" && !looping ? total : Math.max(0, effectiveIdx + (effectiveIdx >= 0 ? 1 : 0))}/{total}</b>
            <span>steps</span>
          </div>
          <div className="pipeline-metric" title="Board task completion">
            <b>{counts.done}/{counts.total || 0}</b>
            <span>tasks</span>
          </div>
          {loopState?.wave ? (
            <div className={"pipeline-metric" + (looping ? " loop" : "")} title="Corrective loop wave">
              <b>W{loopState.wave}</b>
              <span>wave</span>
            </div>
          ) : null}
          {slotCount > 0 ? (
            <div className="pipeline-metric" title="Custom pipeline slots">
              <b>{slotCount}</b>
              <span>slots</span>
            </div>
          ) : null}
          {turnMeter ? (
            <div className="pipeline-metric" title="Turn budget">
              <b>{turnMeter}</b>
              <span>turns</span>
            </div>
          ) : null}
          {intervention ? (
            <div className="pipeline-metric warn" title={intervention.message}>
              <b>!</b>
              <span>{intervention.code || "harness"}</span>
            </div>
          ) : null}
        </div>
      </div>

      {looping || awaiting ? (
        <div className={"pipeline-loop-banner" + (awaiting ? " awaiting" : "")} role="status">
          <div className="pipeline-loop-banner-main">
            <strong>{LOOP_LABELS[loopState?.action] || (awaiting ? "Decision needed" : "Loop")}</strong>
            <span>{loopState?.reason || continueAsk?.reason || "Tester not satisfied — restarting scoped work"}</span>
            {(loopState?.failures || []).length > 0 ? (
              <ul className="pipeline-loop-fails">
                {loopState.failures.slice(0, 4).map((f) => <li key={f}>{f}</li>)}
              </ul>
            ) : (continueAsk?.gaps || []).length > 0 ? (
              <ul className="pipeline-loop-fails">
                {continueAsk.gaps.slice(0, 4).map((f) => <li key={f}>{f}</li>)}
              </ul>
            ) : null}
          </div>
          {awaiting && onContinue ? (
            <div className="pipeline-loop-actions">
              <button type="button" className="ghost danger" onClick={() => onContinue("stop")}>Abort</button>
              <button type="button" className="ghost" onClick={() => onContinue("flag_only")}>Flag gaps</button>
              <button type="button" onClick={() => onContinue("continue")}>Continue loop</button>
            </div>
          ) : null}
        </div>
      ) : null}

      <div className="pipeline-track" aria-hidden="true">
        <div className="pipeline-track-fill" style={{ width: pct + "%" }} />
      </div>

      <div className="pipeline-groups" role="list" aria-label="Pipeline stages">
        {groups.map((g) => {
          const gIdxs = g.steps.map((s) => pipeIndex(s, order)).filter((i) => i >= 0);
          if (!gIdxs.length) return null;
          const gStart = Math.min(...gIdxs);
          const gEnd = Math.max(...gIdxs);
          const gDone = !looping && (idx > gEnd || phase === "done");
          const gActive = (running || looping) && activeGroup === g.id;
          const gPartial = !gDone && effectiveIdx >= gStart;
          const gLoop = looping && gStart <= (rewindIdx >= 0 ? rewindIdx : idx) && gEnd >= (rewindIdx >= 0 ? rewindIdx : idx);
          const groupSlots = (slots || []).filter((s) =>
            g.steps.includes(s.before) || g.steps.includes(s.after) || g.steps.includes(s.replace)
          );
          return (
            <div
              key={g.id}
              className={
                "pipeline-group" +
                (gActive ? " active" : "") +
                (gDone ? " done" : "") +
                (gPartial && !gActive ? " partial" : "") +
                (gLoop ? " looping" : "")
              }
              role="listitem"
            >
              <div className="pipeline-group-label">
                {g.label}
                {groupSlots.length ? <span className="pipeline-slot-badge">+{groupSlots.length}</span> : null}
              </div>
              <div className="pipeline-steps">
                {g.steps.map((p) => {
                  const i = pipeIndex(p, order);
                  const m = metaMap[p] || { label: p, tip: p };
                  const target = looping ? (loopState.to || phase) : phase;
                  const isActive = phase === p || (looping && p === target);
                  const isDone = (!looping && (phase === "done" || idx > i)) ||
                    (looping && rewindIdx >= 0 && i < rewindIdx);
                  const isRejected = looping && (p === "test" || p === "polish") &&
                    ["tester_reject", "rewrite", "corrective_wave", "continue_pending", "placeholder_gaps"].includes(loopState.action);
                  const tip = m.agent ? `${m.tip || p} · @${m.agent}` : (m.tip || p);
                  return (
                    <span
                      key={p}
                      className={
                        "pipe-step" +
                        (isActive ? " active" : "") +
                        (isDone && !isActive ? " done" : "") +
                        (isRejected ? " rejected" : "") +
                        (looping && isActive ? " rewind" : "")
                      }
                      title={isRejected ? (loopState.reason || tip) : tip}
                    >
                      <span className="pipe-step-mark">
                        {isRejected ? "!" : isDone && !isActive ? "✓" : isActive ? "●" : (i + 1)}
                      </span>
                      <span className="pipe-step-label">{m.label}</span>
                    </span>
                  );
                })}
              </div>
            </div>
          );
        })}
      </div>

      {counts.total > 0 ? (
        <div className="pipeline-board-line" title="Board completion">
          <span>Board</span>
          <div className="progress-bar"><span style={{ width: boardPct + "%" }} /></div>
          <span className="pct">{boardPct}%</span>
        </div>
      ) : null}
    </div>
  );
}

function agentRoleClass(name) {
  const n = String(name || "").toLowerCase().replace(/^@/, "");
  if (n.includes("review")) return "role-reviewer";
  if (n.includes("correct")) return "role-corrector";
  if (n.includes("explor") || n.includes("docs")) return "role-explorer";
  if (n.includes("test") || n.includes("qa")) return "role-tester";
  if (n.includes("placeholder") || n.includes("polish")) return "role-reviewer";
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

function LiveStatusCard({ liveAgent, running, phase, counts }) {
  const pct = counts.total ? Math.round((counts.done / counts.total) * 100) : 0;
  return (
    <div className="live-panel live-status-panel">
      <div className="live-panel-head">
        <h3>Status</h3>
        <span className={"live-run-pill" + (running ? " on" : "")}>
          <span className={"pulse" + (running ? " live" : "")} />
          {running ? "running" : phase && phase !== "idle" ? phase : "idle"}
        </span>
      </div>
      <div className="live-status-metrics">
        <div className="live-metric"><b>{counts.total}</b><span>tasks</span></div>
        <div className="live-metric"><b>{counts.doing}</b><span>active</span></div>
        <div className="live-metric"><b>{counts.done}</b><span>done</span></div>
        <div className="live-metric"><b>{counts.ready}</b><span>ready</span></div>
      </div>
      <div className="board-progress" style={{ marginBottom: 0 }}>
        <div className="progress-bar"><span style={{ width: pct + "%" }} /></div>
        <span className="pct">{pct}%</span>
      </div>
      {liveAgent ? (
        <div className="agent-card live-status-agent">
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
              <pre className="output-box" style={{ maxHeight: 88 }}>{liveAgent.output}</pre>
            ) : null}
          </div>
        </div>
      ) : (
        <p className="live-panel-empty">No active agent yet — start a run to see live status.</p>
      )}
    </div>
  );
}

function LiveEnrichBox({ injectNote, setInjectNote, onInject }) {
  return (
    <div className="live-panel live-enrich-panel">
      <div className="live-panel-head">
        <h3>Enrich context</h3>
      </div>
      <p className="live-panel-hint">
        Append notes to SCRATCH.md — workers pick them up on the next step.
      </p>
      <textarea
        value={injectNote}
        onChange={(e) => setInjectNote(e.target.value)}
        placeholder="Add constraints, paths, or corrections…"
      />
      <button className="sm secondary" disabled={!injectNote.trim()} onClick={onInject}>
        Inject context
      </button>
    </div>
  );
}

function LiveActivityStrip({ taskHistory }) {
  if (!taskHistory.length) {
    return (
      <div className="live-panel live-activity-panel">
        <div className="live-panel-head"><h3>Recent activity</h3></div>
        <p className="live-panel-empty">Agent starts/ends will appear here during a run.</p>
      </div>
    );
  }
  return (
    <div className="live-panel live-activity-panel">
      <div className="live-panel-head"><h3>Recent activity</h3></div>
      <div className="observability-strip">
        {taskHistory.slice(-10).map((h, i) => (
          <div key={i} className={"obs-chip " + (h.kind || "")} title={h.message}>
            <AgentAvatar agent={h.agent} size={18} />
            <span className="obs-agent">@{h.agent || "?"}</span>
            <span className="obs-task">{h.id || "—"}</span>
            <span className="obs-kind">{h.kind || "phase"}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function LiveLogs({
  events, intervention, setIntervention,
  autoScroll, setAutoScroll, streamPaused, setStreamPaused,
  showDebugEvents, setShowDebugEvents, turnMeter, onClear, liveEndRef,
}) {
  return (
    <div className="live-panel live-logs-panel">
      <div className="live-panel-head live-logs-head">
        <div>
          <h3>Live logs</h3>
          <span className="live-logs-count">{events.length} event{events.length === 1 ? "" : "s"}</span>
        </div>
        <div className="row">
          {turnMeter ? <span className="turn-meter" title="Tool/thinking turn budget">⟳ {turnMeter}</span> : null}
          <button className={"sm" + (autoScroll ? "" : " ghost")} onClick={() => setAutoScroll((v) => !v)} title="Auto-scroll to latest">
            {autoScroll ? "↓ Live scroll" : "Scroll paused"}
          </button>
          <button className={"sm" + (streamPaused ? "" : " ghost")} onClick={() => setStreamPaused((v) => !v)} title="Pause appending events">
            {streamPaused ? "Resume" : "Pause"}
          </button>
          <button className={"sm" + (showDebugEvents ? "" : " ghost")} onClick={() => setShowDebugEvents((v) => !v)} title="Show runner debug logs">
            {showDebugEvents ? "Debug on" : "Debug"}
          </button>
          <button className="sm ghost" onClick={onClear}>Clear</button>
        </div>
      </div>
      {intervention ? (
        <div className="intervention-banner" role="status">
          <strong>⚠ Harness</strong>
          <span className="intervention-code">{intervention.code}</span>
          <span>{intervention.message}</span>
          {intervention.detail ? <pre className="mini-out">{String(intervention.detail).slice(0, 280)}</pre> : null}
          <button className="sm ghost" onClick={() => setIntervention(null)}>Dismiss</button>
        </div>
      ) : null}
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
                  <div className="event-scope">
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
  );
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
  const [clarifyAsk, setClarifyAsk] = useState(null);
  const [clarifyAnswers, setClarifyAnswers] = useState({});
  const [planAsk, setPlanAsk] = useState(null);
  const [continueAsk, setContinueAsk] = useState(null);
  const [escalateAsk, setEscalateAsk] = useState(null);
  const [loopState, setLoopState] = useState(null);
  const [pipelineView, setPipelineView] = useState(null);
  const [pipeDraft, setPipeDraft] = useState(null);
  const [slotDraft, setSlotDraft] = useState({
    id: "", agent: "worker", after: "plan", before: "", replace: "",
    title: "", input: "", when: "always", fail_mode: "continue", persist_to: "scratch", multipass: false,
  });
  const [shellAsk, setShellAsk] = useState(null);
  const [autoScroll, setAutoScroll] = useState(true);
  const [streamPaused, setStreamPaused] = useState(false);
  const [showDebugEvents, setShowDebugEvents] = useState(false);
  const showDebugEventsRef = useRef(false);
  const [intervention, setIntervention] = useState(null);
  const [turnMeter, setTurnMeter] = useState("");
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
  showDebugEventsRef.current = showDebugEvents;
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
      const [h, c, sk, latest, mods, ag, pipe] = await Promise.all([
        api("/api/health"),
        api("/api/config"),
        api("/api/skills"),
        api("/api/runs/latest"),
        api("/api/models").catch(() => ({ models: [] })),
        api("/api/agents").catch(() => []),
        api("/api/pipeline").catch(() => null),
      ]);
      setHealth(h);
      setApiConnected(!!h?.ok);
      setConfig(c);
      setSkills(Array.isArray(sk) ? sk : []);
      setModels(Array.isArray(mods.models) ? mods.models : []);
      setAgents(Array.isArray(ag) ? ag : []);
      if (pipe?.config) {
        setPipelineView(pipe);
        setPipeDraft((prev) => prev || JSON.parse(JSON.stringify(pipe.config)));
      }
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
            if (e.kind === "debug" && !showDebugEventsRef.current) return prev;
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
          if (e.kind === "ask" && e.output) {
            try {
              const ask = JSON.parse(e.output);
              if (ask && ask.questions) {
                setClarifyAsk(ask);
                const seed = {};
                (ask.questions || []).forEach((q) => {
                  seed[q.id] = q.recommended || (q.options && q.options.find((o) => o.recommended)?.label) || "";
                });
                setClarifyAnswers(seed);
                showToast("Scope interview — choose options or use recommended");
              } else if (ask && ask.kind === "escalate") {
                setEscalateAsk(ask);
                showToast("⚠ " + (ask.task_id || "Task") + " needs your decision");
              } else if (ask && ask.kind === "continue") {
                setContinueAsk(ask);
                showToast("Retries exhausted — continue another wave?");
              } else if (ask && (ask.task_count != null || ask.tasks)) {
                setPlanAsk(ask);
                showToast("Plan ready — approve to execute");
              } else if (ask && ask.command) {
                setShellAsk(ask);
                showToast("Shell approval required");
              }
            } catch (_) {}
          }
          if (e.kind === "ask_answered") {
            setClarifyAsk(null);
            setPlanAsk(null);
            setContinueAsk(null);
            setEscalateAsk(null);
            setShellAsk(null);
          }
          if (e.kind === "intervention") {
            const banner = {
              code: e.scope || "quality",
              message: e.message || "harness intervention",
              detail: e.output || "",
              taskId: e.task_id || "",
            };
            setIntervention(banner);
            showToast("⚠ " + banner.message);
            if (banner.code === "escalate") {
              // Recover modal if KindAsk raced / was missed.
              api("/api/escalate/pending").then((p) => {
                if (p && p.pending && p.ask) setEscalateAsk(p.ask);
              }).catch(() => {});
            }
          }
          if (e.kind === "loop") {
            const loop = parseLoopEvent(e);
            setLoopState(loop);
            if (loop.awaiting) {
              showToast("Decision needed — continue or abort?");
            } else if (loop.action === "tester_reject" || loop.action === "corrective_wave") {
              showToast("↺ " + (LOOP_LABELS[loop.action] || "Loop") + (loop.wave ? " · wave " + loop.wave : ""));
            } else if (loop.action === "resolved") {
              showToast("Loop cleared — " + (loop.reason || "ok"));
            } else if (loop.action === "aborted" || loop.action === "flag_only") {
              showToast(LOOP_LABELS[loop.action] || loop.action);
            }
            setRunning(true);
          }
          if (e.kind === "turn") {
            setTurnMeter(e.message || e.scope || "");
          }
          if (e.phase === "done" || e.phase === "error" || e.kind === "run_end" || e.kind === "run_stop") {
            setRunning(false);
            setClarifyAsk(null);
            setPlanAsk(null);
            setContinueAsk(null);
            setShellAsk(null);
            setTurnMeter("");
            setLoopState((prev) => (prev && !LOOP_DONE_ACTIONS.has(prev.action)
              ? { ...prev, action: e.phase === "error" ? "aborted" : "resolved", awaiting: false }
              : prev));
            showToast(e.phase === "error" ? (e.message || "Run error") : (e.message || "Run finished"));
            refresh();
          } else if (e.kind === "run_start" || e.kind === "agent_start" || e.kind === "loop" || e.phase === "init" || e.phase === "execute" || e.phase === "clarify" || e.phase === "polish" || e.phase === "plan" || e.phase === "test") {
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
    setLoopState(null);
    setContinueAsk(null);
    setEscalateAsk(null);
    setIntervention(null);
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

  async function openAgent(id) {
    if (!id) return;
    try {
      const full = await api("/api/agents/" + encodeURIComponent(id));
      setAgentEdit({
        id: full.id,
        title: full.title || full.role || "",
        description: full.description || "",
        system_prompt: full.system_prompt || "",
        skills: full.skills || [],
        model: full.model || "",
        provider: full.provider || "",
        endpoint: full.endpoint || "",
        tools: !!full.tools,
        max_iter: full.max_iter ?? 10,
        temperature: full.temperature ?? 0.2,
        max_tokens: full.max_tokens ?? 2048,
        builtin: !!full.builtin,
        override: !!full.override,
      });
    } catch (e) {
      setErr(String(e.message || e));
    }
  }

  async function saveAgentEdit() {
    if (!agentEdit?.id) return;
    try {
      const { builtin, override, ...payload } = agentEdit;
      await api("/api/agents/" + encodeURIComponent(agentEdit.id), {
        method: "PUT",
        body: JSON.stringify({
          ...payload,
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

  async function submitClarify(useRecommended) {
    if (!clarifyAsk) return;
    const answers = (clarifyAsk.questions || []).map((q) => ({
      question_id: q.id,
      selected: useRecommended
        ? [q.recommended || (q.options && q.options.find((o) => o.recommended)?.label) || ""].filter(Boolean)
        : [clarifyAnswers[q.id]].filter(Boolean),
    }));
    await api("/api/clarify/answer", {
      method: "POST",
      body: JSON.stringify({
        answers,
        use_all_recommended: !!useRecommended,
      }),
    });
    setClarifyAsk(null);
    showToast(useRecommended ? "Applied recommended scope" : "Scope answers saved");
  }

  async function submitPlan(decision) {
    if (!planAsk) return;
    await api("/api/plan/approve", {
      method: "POST",
      body: JSON.stringify({ decision }),
    });
    setPlanAsk(null);
    showToast(decision === "approve" ? "Plan approved" : "Replan requested");
  }

  async function submitShell(decision) {
    if (!shellAsk) return;
    await api("/api/shell/approve", {
      method: "POST",
      body: JSON.stringify({ decision }),
    });
    setShellAsk(null);
    showToast(decision === "approve" ? "Shell approved" : "Shell denied");
  }

  async function submitContinue(action) {
    if (!continueAsk && !(loopState && loopState.awaiting)) return;
    await api("/api/continue/answer", {
      method: "POST",
      body: JSON.stringify({ action }),
    });
    setContinueAsk(null);
    setLoopState((prev) => prev ? {
      ...prev,
      awaiting: false,
      action: action === "continue" ? "continue_wave" : (action === "flag_only" ? "flag_only" : "aborted"),
      reason: action === "continue"
        ? "continuing — another corrective wave"
        : action === "flag_only"
          ? "keeping precise gaps for human fill"
          : "aborted — finishing with work flagged",
    } : prev);
    const labels = { continue: "Continuing another wave", stop: "Stopped — gaps flagged", flag_only: "Keeping precise flags" };
    showToast(labels[action] || action);
  }

  async function submitEscalate(action) {
    if (!escalateAsk) return;
    await api("/api/escalate/answer", {
      method: "POST",
      body: JSON.stringify({ action }),
    });
    const taskId = escalateAsk.task_id || "";
    setEscalateAsk(null);
    setIntervention(null);
    setLoopState((prev) => prev ? {
      ...prev,
      awaiting: false,
      action: "escalate_resolved",
      reason: (taskId || "task") + " → " + action,
    } : prev);
    const labels = {
      re_scope: "Left in backlog for re-scope",
      retry: "Retrying task",
      mark_done: "Marked done",
      abort: "Task aborted",
    };
    showToast((taskId ? taskId + ": " : "") + (labels[action] || action));
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

  const NAV = [
    { id: "board", label: "Board", tip: "Tasks" },
    { id: "run", label: "Live", tip: "Agent stream" },
    { id: "pipeline", label: "Pipeline", tip: "Phases & slots" },
    { id: "queries", label: "Queries", tip: "Per-turn plan/tasks" },
    { id: "archives", label: "Archives", tip: "Past runs" },
    { id: "agents", label: "Agents", tip: "Specialists" },
    { id: "skills", label: "Skills", tip: "Knowledge" },
  ];
  const boardPct = counts.total ? Math.round((counts.done / counts.total) * 100) : 0;
  const livePipe = useMemo(() => pipeFromConfig(pipelineView?.config || pipeDraft), [pipelineView, pipeDraft]);
  const roleOptions = useMemo(() => {
    const ids = (agents || []).map((a) => a.id).filter(Boolean);
    return ids.length ? ids : ROLES;
  }, [agents]);

  async function savePipeline() {
    if (!pipeDraft) return;
    try {
      const view = await api("/api/pipeline", {
        method: "PUT",
        body: JSON.stringify({ config: pipeDraft }),
      });
      setPipelineView(view);
      setPipeDraft(JSON.parse(JSON.stringify(view.config)));
      showToast("Pipeline saved · .slmcode/pipeline.yaml");
    } catch (e) {
      setErr(String(e.message || e));
    }
  }

  async function resetPipeline() {
    try {
      const view = await api("/api/pipeline/reset", { method: "POST", body: "{}" });
      setPipelineView(view);
      setPipeDraft(JSON.parse(JSON.stringify(view.config)));
      showToast("Pipeline reset to defaults");
    } catch (e) {
      setErr(String(e.message || e));
    }
  }

  function addPipelineSlot() {
    const id = (slotDraft.id || ("slot-" + Date.now())).toLowerCase().replace(/[^a-z0-9_-]/g, "-");
    if (!id || !slotDraft.agent) return;
    const slot = {
      id,
      agent: slotDraft.agent,
      title: slotDraft.title || id,
      before: slotDraft.before || "",
      after: slotDraft.after || "",
      replace: slotDraft.replace || "",
      when: slotDraft.when || "always",
      input: slotDraft.input || "Run pipeline slot for {{phase}}.\n\nQuery:\n{{query}}\n",
      fail_mode: slotDraft.fail_mode || "continue",
      persist_to: slotDraft.persist_to || "scratch",
      multipass: !!slotDraft.multipass,
    };
    if (!slot.before && !slot.after && !slot.replace) {
      slot.after = "plan";
    }
    setPipeDraft((prev) => {
      const base = prev || pipelineView?.config || { version: 1, phases: {}, order: DEFAULT_PIPE, slots: [], execute: {} };
      const cfg = JSON.parse(JSON.stringify(base));
      cfg.slots = [...(cfg.slots || []).filter((s) => s.id !== id), slot];
      return cfg;
    });
    showToast("Slot @" + slot.agent + " added — Save pipeline");
  }

  function removePipelineSlot(id) {
    setPipeDraft((prev) => {
      if (!prev) return prev;
      return { ...prev, slots: (prev.slots || []).filter((s) => s.id !== id) };
    });
  }

  function setPhaseAgent(phaseId, agent) {
    setPipeDraft((prev) => {
      const base = prev || pipelineView?.config || {};
      const cfg = JSON.parse(JSON.stringify(base));
      cfg.phases = cfg.phases || {};
      cfg.phases[phaseId] = { ...(cfg.phases[phaseId] || {}), agent };
      return cfg;
    });
  }

  function setPhaseWhen(phaseId, when) {
    setPipeDraft((prev) => {
      const base = prev || pipelineView?.config || {};
      const cfg = JSON.parse(JSON.stringify(base));
      cfg.phases = cfg.phases || {};
      cfg.phases[phaseId] = { ...(cfg.phases[phaseId] || {}), when };
      return cfg;
    });
  }

  function setExecuteLoop(field, value) {
    setPipeDraft((prev) => {
      const base = prev || pipelineView?.config || {};
      const cfg = JSON.parse(JSON.stringify(base));
      cfg.execute = { ...(cfg.execute || {}), [field]: value };
      return cfg;
    });
  }

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

      <PipelineHeader
        phase={phase}
        running={running}
        liveAgent={liveAgent}
        counts={counts}
        intervention={intervention}
        turnMeter={turnMeter}
        loopState={loopState}
        continueAsk={continueAsk}
        escalateAsk={escalateAsk}
        onContinue={submitContinue}
        pipeOrder={livePipe.order}
        pipeMeta={livePipe.meta}
        pipeGroups={livePipe.groups}
        slots={livePipe.slots}
      />

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
            <div className="live-page">
              <div className="live-page-header">
                <div>
                  <h2>Live</h2>
                  <p className="lead">
                    {running
                      ? "Agents are working — overview above, logs below."
                      : "Press Run above. Status, context, deps, and a dedicated log stream stay separated so you can scan everything clearly."}
                  </p>
                </div>
              </div>

              <div className="live-overview">
                <div className="live-overview-main">
                  <LiveStatusCard
                    liveAgent={liveAgent}
                    running={running}
                    phase={phase}
                    counts={counts}
                  />
                  <LiveActivityStrip taskHistory={taskHistory} />
                </div>
                <div className="live-overview-side">
                  <LiveEnrichBox
                    injectNote={injectNote}
                    setInjectNote={setInjectNote}
                    onInject={injectContext}
                  />
                  {(board.tasks || []).length > 0 ? (
                    <div className="live-panel live-deps-panel">
                      <DepGraph tasks={board.tasks} />
                    </div>
                  ) : (
                    <div className="live-panel live-deps-panel">
                      <div className="live-panel-head"><h3>Dependencies</h3></div>
                      <p className="live-panel-empty">Task graph appears once the board has tasks.</p>
                    </div>
                  )}
                </div>
              </div>

              <LiveLogs
                events={events}
                intervention={intervention}
                setIntervention={setIntervention}
                autoScroll={autoScroll}
                setAutoScroll={setAutoScroll}
                streamPaused={streamPaused}
                setStreamPaused={setStreamPaused}
                showDebugEvents={showDebugEvents}
                setShowDebugEvents={setShowDebugEvents}
                turnMeter={turnMeter}
                onClear={() => { setEvents([]); setTaskHistory([]); setIntervention(null); setTurnMeter(""); }}
                liveEndRef={liveEndRef}
              />
            </div>
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

          {nav === "pipeline" && (
            <>
              <div className="row" style={{ justifyContent: "space-between", marginBottom: 8, flexWrap: "wrap", gap: 8 }}>
                <div>
                  <h2 style={{ margin: 0 }}>Pipeline</h2>
                  <p className="lead" style={{ margin: "0.25rem 0 0" }}>
                    Config-driven phases, loop agents, and insertable slots — persisted as{" "}
                    <code>.slmcode/pipeline.yaml</code>. Header + engine follow this live.
                  </p>
                </div>
                <div className="row" style={{ gap: 8 }}>
                  <button className="ghost" onClick={resetPipeline}>Reset defaults</button>
                  <button onClick={savePipeline}>Save pipeline</button>
                </div>
              </div>

              <div className="pipeline-editor">
                <section className="panel-box">
                  <h3>Execute loop</h3>
                  <div className="pipeline-exec-grid">
                    <label>Default worker
                      <select value={pipeDraft?.execute?.default_role || "worker"} onChange={(e) => setExecuteLoop("default_role", e.target.value)}>
                        {roleOptions.map((id) => <option key={id} value={id}>@{id}</option>)}
                      </select>
                    </label>
                    <label>Reviewer
                      <select value={pipeDraft?.execute?.reviewer || "reviewer"} onChange={(e) => setExecuteLoop("reviewer", e.target.value)}>
                        {roleOptions.map((id) => <option key={id} value={id}>@{id}</option>)}
                      </select>
                    </label>
                    <label>Corrector
                      <select value={pipeDraft?.execute?.corrector || "corrector"} onChange={(e) => setExecuteLoop("corrector", e.target.value)}>
                        {roleOptions.map((id) => <option key={id} value={id}>@{id}</option>)}
                      </select>
                    </label>
                  </div>
                </section>

                <section className="panel-box">
                  <h3>Phase agents</h3>
                  <div className="pipeline-phase-table">
                    {(pipeDraft?.order || livePipe.order).map((id) => {
                      const p = (pipeDraft?.phases || {})[id] || {};
                      return (
                        <div key={id} className="pipeline-phase-row">
                          <div>
                            <strong>{p.label || livePipe.meta[id]?.label || id}</strong>
                            <span className="pipeline-phase-id">{id}</span>
                          </div>
                          <select value={p.agent || ""} onChange={(e) => setPhaseAgent(id, e.target.value)}>
                            <option value="">(none / harness)</option>
                            {roleOptions.map((aid) => <option key={aid} value={aid}>@{aid}</option>)}
                          </select>
                          <select value={p.when || "always"} onChange={(e) => setPhaseWhen(id, e.target.value)}>
                            <option value="always">always</option>
                            <option value="auto">auto</option>
                            <option value="never">never</option>
                          </select>
                        </div>
                      );
                    })}
                  </div>
                </section>

                <section className="panel-box">
                  <h3>Insert agent slot</h3>
                  <div className="pipeline-slot-form">
                    <label>ID<input value={slotDraft.id} onChange={(e) => setSlotDraft({ ...slotDraft, id: e.target.value })} placeholder="pre-plan-audit" /></label>
                    <label>Agent
                      <select value={slotDraft.agent} onChange={(e) => setSlotDraft({ ...slotDraft, agent: e.target.value })}>
                        {roleOptions.map((id) => <option key={id} value={id}>@{id}</option>)}
                      </select>
                    </label>
                    <label>After
                      <select value={slotDraft.after} onChange={(e) => setSlotDraft({ ...slotDraft, after: e.target.value, before: "", replace: "" })}>
                        <option value="">—</option>
                        {(pipeDraft?.order || livePipe.order).map((id) => <option key={id} value={id}>{id}</option>)}
                      </select>
                    </label>
                    <label>Before
                      <select value={slotDraft.before} onChange={(e) => setSlotDraft({ ...slotDraft, before: e.target.value, after: "", replace: "" })}>
                        <option value="">—</option>
                        {(pipeDraft?.order || livePipe.order).map((id) => <option key={id} value={id}>{id}</option>)}
                      </select>
                    </label>
                    <label>Replace
                      <select value={slotDraft.replace} onChange={(e) => setSlotDraft({ ...slotDraft, replace: e.target.value, after: "", before: "" })}>
                        <option value="">—</option>
                        {(pipeDraft?.order || livePipe.order).map((id) => <option key={id} value={id}>{id}</option>)}
                      </select>
                    </label>
                    <label>When
                      <select value={slotDraft.when} onChange={(e) => setSlotDraft({ ...slotDraft, when: e.target.value })}>
                        <option value="always">always</option>
                        <option value="never">never</option>
                        <option value="query_matches:langgraph">query_matches:langgraph</option>
                      </select>
                    </label>
                    <label className="full">Prompt template
                      <textarea rows={4} value={slotDraft.input} onChange={(e) => setSlotDraft({ ...slotDraft, input: e.target.value })}
                        placeholder={"Audit before plan.\nQuery:\n{{query}}\nExploration:\n{{exploration}}"} />
                    </label>
                    <div className="row full" style={{ gap: 8 }}>
                      <button type="button" onClick={addPipelineSlot}>Add slot</button>
                      <span className="lead" style={{ margin: 0 }}>Placeholders: {"{{query}} {{exploration}} {{plan}} {{phase}}"}</span>
                    </div>
                  </div>
                  <h4 style={{ marginTop: "1rem" }}>Active slots ({(pipeDraft?.slots || []).length})</h4>
                  {(pipeDraft?.slots || []).length === 0 ? (
                    <p className="lead">No custom slots — add one above to inject any agent anywhere.</p>
                  ) : (
                    <ul className="pipeline-slot-list">
                      {(pipeDraft?.slots || []).map((s) => (
                        <li key={s.id}>
                          <strong>{s.id}</strong>
                          <span>@{s.agent}</span>
                          <span className="pipeline-phase-id">
                            {s.replace ? "replace " + s.replace : s.before ? "before " + s.before : "after " + (s.after || "?")}
                          </span>
                          <button className="sm ghost danger" type="button" onClick={() => removePipelineSlot(s.id)}>Remove</button>
                        </li>
                      ))}
                    </ul>
                  )}
                </section>
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
                      <li
                        key={a.id}
                        className={(a.custom ? "agent-custom" : "agent-builtin") + (agentEdit?.id === a.id ? " active" : "")}
                        onClick={() => openAgent(a.id)}
                        style={{ cursor: "pointer" }}
                      >
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
                        <div className="row" style={{ marginTop: 8 }} onClick={(e) => e.stopPropagation()}>
                          <button className="sm ghost" onClick={() => openAgent(a.id)}>
                            {a.custom ? "Edit" : (a.override ? "Edit override" : "Customize")}
                          </button>
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
                      {roleOptions.map((r) => <option key={r} value={r}>@{r}</option>)}
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
                          {roleOptions.map((r) => <option key={r} value={r}>@{r}</option>)}
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
                <h3 style={{ marginTop: 14 }}>Planning / scope</h3>
                <label className="row" style={{ gap: 8, alignItems: "center" }}>
                  <input
                    type="checkbox"
                    checked={!!config.auto_approve}
                    onChange={(e) => saveConfig({ auto_approve: e.target.checked })}
                  />
                  Auto-approve (skip plan/shell/clarify waits)
                </label>
                <label>Clarify mode
                  <select
                    value={config.clarify_mode || "auto"}
                    onChange={(e) => saveConfig({ clarify_mode: e.target.value })}
                  >
                    <option value="auto">auto (recommended defaults)</option>
                    <option value="ask">ask (interview user)</option>
                    <option value="off">off</option>
                  </select>
                </label>
                <label>Plan approve
                  <select
                    value={config.plan_approve || "auto"}
                    onChange={(e) => saveConfig({ plan_approve: e.target.value })}
                  >
                    <option value="auto">auto (continue)</option>
                    <option value="ask">ask (approve before execute)</option>
                    <option value="off">off</option>
                  </select>
                </label>
                <label>Escalate ask
                  <select
                    value={config.escalate_ask || "ask"}
                    onChange={(e) => saveConfig({ escalate_ask: e.target.value })}
                  >
                    <option value="ask">ask (pause for decision)</option>
                    <option value="auto">auto (retry once)</option>
                    <option value="off">off (leave in backlog)</option>
                  </select>
                </label>
                <label>Escalate timeout (sec)
                  <input
                    type="number"
                    min={5}
                    max={600}
                    defaultValue={
                      typeof config.escalate_ask_timeout === "number"
                        ? Math.max(5, Math.round(config.escalate_ask_timeout / 1e9))
                        : 30
                    }
                    onBlur={(e) => saveConfig({ escalate_ask_timeout_sec: Number(e.target.value) })}
                  />
                </label>
                <label>Continue ask
                  <select
                    value={config.continue_ask || "ask"}
                    onChange={(e) => saveConfig({ continue_ask: e.target.value })}
                  >
                    <option value="ask">ask (after QA exhausted)</option>
                    <option value="auto">auto (one more wave)</option>
                    <option value="off">off</option>
                  </select>
                </label>
                <label>Shell permission
                  <select
                    value={config.shell_permission || "allow"}
                    onChange={(e) => saveConfig({ shell_permission: e.target.value })}
                  >
                    <option value="allow">allow</option>
                    <option value="ask">ask (interactive)</option>
                    <option value="deny">deny</option>
                  </select>
                </label>
                <label className="row" style={{ gap: 8, alignItems: "center" }}>
                  <input
                    type="checkbox"
                    checked={config.context_compact !== false}
                    onChange={(e) => saveConfig({ context_compact: e.target.checked })}
                  />
                  Context compact (mid-run CONTEXT.md summarization)
                </label>
                <label className="row" style={{ gap: 8, alignItems: "center" }}>
                  <input
                    type="checkbox"
                    checked={config.wave_snapshots !== false}
                    onChange={(e) => saveConfig({ wave_snapshots: e.target.checked })}
                  />
                  Wave snapshots (rewind)
                </label>
                <label className="row" style={{ gap: 8, alignItems: "center" }}>
                  <input
                    type="checkbox"
                    checked={config.hooks_enabled !== false}
                    onChange={(e) => saveConfig({ hooks_enabled: e.target.checked })}
                  />
                  Hooks (.slmcode/hooks.json)
                </label>
                <label>Clarify timeout (sec)
                  <input type="number" min={15} max={600}
                    defaultValue={(() => {
                      const t = Number(config.clarify_timeout) || 0;
                      if (t > 1e6) return Math.round(t / 1e9); // Go duration ns
                      return t || 120;
                    })()}
                    onBlur={(e) => saveConfig({ clarify_timeout_sec: Number(e.target.value) })} />
                </label>
                <label className="row" style={{ gap: 8, alignItems: "center" }}>
                  <input
                    type="checkbox"
                    checked={config.scope_judge !== false}
                    onChange={(e) => saveConfig({ scope_judge: e.target.checked })}
                  />
                  Scope judge (PRD completeness before execute)
                </label>
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

      {clarifyAsk ? (
        <div className="modal-backdrop" style={{
          position: "fixed", inset: 0, background: "rgba(0,0,0,0.45)", zIndex: 80,
          display: "flex", alignItems: "center", justifyContent: "center", padding: 16,
        }}>
          <div className="card" style={{
            maxWidth: 520, width: "100%", maxHeight: "85vh", overflow: "auto",
            background: "var(--panel)", border: "1px solid var(--border)", borderRadius: 12, padding: 16,
          }}>
            <h2 style={{ marginTop: 0 }}>Scope interview</h2>
            <p className="lead" style={{ marginTop: 0 }}>
              Pick options or use recommended defaults so the plan has a full PRD.
            </p>
            {(clarifyAsk.questions || []).map((q) => (
              <div key={q.id} style={{ marginBottom: 14 }}>
                <strong>{q.header || q.id}</strong>
                <div style={{ color: "var(--muted)", marginBottom: 6 }}>{q.question}</div>
                <select
                  value={clarifyAnswers[q.id] || ""}
                  onChange={(e) => setClarifyAnswers((prev) => ({ ...prev, [q.id]: e.target.value }))}
                  style={{ width: "100%" }}
                >
                  {(q.options || []).map((o) => (
                    <option key={o.label} value={o.label}>
                      {o.label}{o.recommended ? " (recommended)" : ""}{o.description ? ` — ${o.description}` : ""}
                    </option>
                  ))}
                </select>
              </div>
            ))}
            <div className="row" style={{ gap: 8, justifyContent: "flex-end" }}>
              <button className="ghost" onClick={() => submitClarify(true)}>Use all recommended</button>
              <button onClick={() => submitClarify(false)}>Lock PRD & continue</button>
            </div>
          </div>
        </div>
      ) : null}

      {planAsk ? (
        <div className="modal-backdrop" style={{
          position: "fixed", inset: 0, background: "rgba(0,0,0,0.45)", zIndex: 81,
          display: "flex", alignItems: "center", justifyContent: "center", padding: 16,
        }}>
          <div className="card" style={{
            maxWidth: 560, width: "100%", maxHeight: "85vh", overflow: "auto",
            background: "var(--panel)", border: "1px solid var(--border)", borderRadius: 12, padding: 16,
          }}>
            <h2 style={{ marginTop: 0 }}>Approve plan</h2>
            <p className="lead">{planAsk.summary || "Plan ready"}</p>
            <p style={{ color: "var(--muted)" }}>{planAsk.task_count || 0} tasks</p>
            <ul style={{ paddingLeft: 18 }}>
              {(planAsk.tasks || []).map((t) => <li key={t}>{t}</li>)}
            </ul>
            <div className="row" style={{ gap: 8, justifyContent: "flex-end" }}>
              <button className="ghost" onClick={() => submitPlan("replan")}>Replan</button>
              <button onClick={() => submitPlan("approve")}>Approve & execute</button>
            </div>
          </div>
        </div>
      ) : null}

      {escalateAsk ? (
        <div className="modal-backdrop escalate-modal" style={{
          position: "fixed", inset: 0, background: "rgba(0,0,0,0.5)", zIndex: 85,
          display: "flex", alignItems: "center", justifyContent: "center", padding: 16,
        }}>
          <div className="card" style={{
            maxWidth: 640, width: "100%", maxHeight: "85vh", overflow: "auto",
            background: "var(--panel)", border: "1px solid var(--border)", borderRadius: 12, padding: 16,
          }}>
            <h2 style={{ marginTop: 0 }}>⚠ Human review needed</h2>
            <p className="lead">{escalateAsk.summary || "Task escalated after max retries."}</p>
            <p style={{ color: "var(--muted)", fontSize: "0.84rem" }}>
              Pipeline is paused on this task
              {escalateAsk.timeout_sec ? ` · timeout ${escalateAsk.timeout_sec}s → re-scope` : ""}
            </p>
            <p style={{ fontSize: "0.85rem" }}>
              <strong>{escalateAsk.task_id}</strong>
              {escalateAsk.title ? ` — ${escalateAsk.title}` : ""}
              {escalateAsk.role ? ` · @${escalateAsk.role}` : ""}
            </p>
            {(escalateAsk.files || []).length > 0 ? (
              <p style={{ fontSize: "0.8rem" }}><strong>Files:</strong> {escalateAsk.files.join(", ")}</p>
            ) : null}
            {escalateAsk.detail ? (
              <pre style={{
                whiteSpace: "pre-wrap", fontSize: 12, maxHeight: 180, overflow: "auto",
                background: "var(--bg)", padding: 10, borderRadius: 8,
              }}>{escalateAsk.detail}</pre>
            ) : null}
            <div className="row" style={{ gap: 8, justifyContent: "flex-end", marginTop: 12, flexWrap: "wrap" }}>
              <button className="ghost danger" onClick={() => submitEscalate("abort")}>Abort task</button>
              <button className="ghost" onClick={() => submitEscalate("re_scope")}>Re-scope / fix later</button>
              <button className="ghost" onClick={() => submitEscalate("mark_done")}>Mark done</button>
              <button onClick={() => submitEscalate("retry")}>Retry now</button>
            </div>
          </div>
        </div>
      ) : null}

      {continueAsk ? (
        <div className="modal-backdrop" style={{
          position: "fixed", inset: 0, background: "rgba(0,0,0,0.45)", zIndex: 83,
          display: "flex", alignItems: "center", justifyContent: "center", padding: 16,
        }}>
          <div className="card" style={{
            maxWidth: 620, width: "100%", maxHeight: "85vh", overflow: "auto",
            background: "var(--panel)", border: "1px solid var(--border)", borderRadius: 12, padding: 16,
          }}>
            <h2 style={{ marginTop: 0 }}>Continue?</h2>
            <p className="lead">{continueAsk.summary || "Retries/QA exhausted but work remains."}</p>
            <p style={{ color: "var(--muted)", fontSize: "0.84rem" }}>{continueAsk.reason}</p>
            {(continueAsk.escalated || []).length > 0 ? (
              <p style={{ fontSize: "0.8rem" }}><strong>Escalated:</strong> {continueAsk.escalated.join(", ")}</p>
            ) : null}
            {(continueAsk.gaps || []).length > 0 ? (
              <>
                <h3 style={{ fontSize: "0.9rem" }}>Precise gaps</h3>
                <ul style={{ paddingLeft: 18, fontSize: "0.82rem" }}>
                  {continueAsk.gaps.slice(0, 12).map((g) => <li key={g}><code>{g}</code></li>)}
                </ul>
              </>
            ) : null}
            <div className="row" style={{ gap: 8, justifyContent: "flex-end", marginTop: 12 }}>
              <button className="ghost" onClick={() => submitContinue("stop")}>Stop</button>
              <button className="ghost" onClick={() => submitContinue("flag_only")}>Keep precise flags</button>
              <button onClick={() => submitContinue("continue")}>Continue another wave</button>
            </div>
          </div>
        </div>
      ) : null}

      {shellAsk ? (
        <div className="modal-backdrop" style={{
          position: "fixed", inset: 0, background: "rgba(0,0,0,0.45)", zIndex: 82,
          display: "flex", alignItems: "center", justifyContent: "center", padding: 16,
        }}>
          <div className="card" style={{
            maxWidth: 560, width: "100%",
            background: "var(--panel)", border: "1px solid var(--border)", borderRadius: 12, padding: 16,
          }}>
            <h2 style={{ marginTop: 0 }}>Shell approval</h2>
            <pre style={{ whiteSpace: "pre-wrap", fontSize: 13 }}>{shellAsk.command}</pre>
            <div className="row" style={{ gap: 8, justifyContent: "flex-end" }}>
              <button className="ghost danger" onClick={() => submitShell("deny")}>Deny</button>
              <button onClick={() => submitShell("approve")}>Approve</button>
            </div>
          </div>
        </div>
      ) : null}

      <footer className="bottom">
        <span>
          <span className={"pulse" + (running || sseConnected ? " live" : "")} />
          {apiConnected ? "api ok" : "api down"} · {sseConnected ? "sse ok" : "sse down"} ·{" "}
          {health?.root ? health.root.replace(/^\/Users\/[^/]+/, "~") : "…"}
        </span>
        <span className="footer-live">
          {escalateAsk
            ? `⏳ escalate ${escalateAsk.task_id || ""}?`
            : continueAsk || (loopState && loopState.awaiting)
            ? "⏳ continue/abort?"
            : loopState && !LOOP_DONE_ACTIONS.has(loopState.action)
              ? `↺ ${LOOP_LABELS[loopState.action] || "loop"}${loopState.wave ? " W" + loopState.wave : ""}`
              : running ? `● ${phase}` : "ready"}
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
