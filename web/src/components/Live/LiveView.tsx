import { useState, useEffect, useRef, useCallback, useContext, useMemo } from 'react';
import {
  Play,
  Square,
  ChevronDown,
  ChevronUp,
  Bot,
  CheckCircle2,
  Loader2,
  Circle,
  Activity,
  Target,
  Zap,
  Layers,
} from 'lucide-react';
import { AppContext } from '@/App';
import { startRun, stopRun, getLatestRun, getAgents, getPipeline } from '@/api/client';
import type { RunEvent, LatestRunResponse, AgentSpec, PipelineView } from '@/types';
import EventLog from './EventLog';
import LiveTaskPanel from './LiveTaskPanel';
import LiveFileInspector from './LiveFileInspector';
import HITLPopup from './HITLPopup';
import clsx from 'clsx';

// ── Pipeline group definitions ──
interface PhaseGroup {
  id: string;
  label: string;
  phases: string[];
  color: string;
}

const PIPELINE_GROUPS: PhaseGroup[] = [
  { id: 'prepare', label: 'Prepare', phases: ['init', 'skills', 'context', 'explore', 'docs'], color: 'sky' },
  { id: 'design', label: 'Design', phases: ['architect', 'clarify', 'plan', 'split'], color: 'violet' },
  { id: 'build', label: 'Build', phases: ['coord', 'execute', 'learn'], color: 'amber' },
  { id: 'verify', label: 'Verify', phases: ['polish', 'test'], color: 'emerald' },
  { id: 'finish', label: 'Finish', phases: ['memory', 'done'], color: 'gray' },
];

// ── Phase → dot color mapping ──
const PHASE_DOT_COLORS: Record<string, string> = {
  // blue shades
  init: 'bg-blue-400 dark:bg-blue-500',
  skills: 'bg-blue-500 dark:bg-blue-400',
  context: 'bg-cyan-500 dark:bg-cyan-400',
  explore: 'bg-sky-500 dark:bg-sky-400',
  docs: 'bg-indigo-500 dark:bg-indigo-400',
  // purple shades
  architect: 'bg-violet-500 dark:bg-violet-400',
  clarify: 'bg-purple-500 dark:bg-purple-400',
  plan: 'bg-fuchsia-500 dark:bg-fuchsia-400',
  split: 'bg-pink-500 dark:bg-pink-400',
  // orange/amber shades
  coord: 'bg-orange-500 dark:bg-orange-400',
  execute: 'bg-amber-500 dark:bg-amber-400',
  learn: 'bg-yellow-600 dark:bg-yellow-400',
  // green shades
  polish: 'bg-lime-500 dark:bg-lime-400',
  test: 'bg-emerald-500 dark:bg-emerald-400',
  // teal/green shades
  memory: 'bg-teal-500 dark:bg-teal-400',
  done: 'bg-green-500 dark:bg-green-400',
};

const GROUP_BORDER_COLORS: Record<string, string> = {
  sky: 'border-sky-400 dark:border-sky-500',
  violet: 'border-violet-400 dark:border-violet-500',
  amber: 'border-amber-400 dark:border-amber-500',
  emerald: 'border-emerald-400 dark:border-emerald-500',
  gray: 'border-gray-400 dark:border-gray-500',
};

const GROUP_BG_COLORS: Record<string, string> = {
  sky: 'bg-sky-50 dark:bg-sky-950/30',
  violet: 'bg-violet-50 dark:bg-violet-950/30',
  amber: 'bg-amber-50 dark:bg-amber-950/30',
  emerald: 'bg-emerald-50 dark:bg-emerald-950/30',
  gray: 'bg-gray-50 dark:bg-gray-800/50',
};

const GROUP_TEXT_COLORS: Record<string, string> = {
  sky: 'text-sky-600 dark:text-sky-400',
  violet: 'text-violet-600 dark:text-violet-400',
  amber: 'text-amber-600 dark:text-amber-400',
  emerald: 'text-emerald-600 dark:text-emerald-400',
  gray: 'text-gray-500 dark:text-gray-400',
};

// ── Phase state enum ──
type PhaseState = 'pending' | 'active' | 'completed';

export default function LiveView() {
  const ctx = useContext(AppContext);
  // Use sessionStorage-backed state so it survives page navigation
  const [localEvents, setLocalEvents] = useState<RunEvent[]>(() => {
    try { return JSON.parse(sessionStorage.getItem('slmcode:events') || '[]'); } catch { return []; }
  });
  const [localRunning, setLocalRunning] = useState(() => sessionStorage.getItem('slmcode:running') === 'true');
  const events = ctx?.liveEvents?.length ? ctx.liveEvents : localEvents;
  const setEvents = (v: RunEvent[] | ((p: RunEvent[]) => RunEvent[])) => {
    const next = typeof v === 'function' ? v(localEvents) : v;
    setLocalEvents(next);
    ctx?.setLiveEvents?.(next);
    try { sessionStorage.setItem('slmcode:events', JSON.stringify(next.slice(-200))); } catch {}
  };
  const running = ctx?.liveRunning || localRunning;
  const setRunning = (v: boolean) => {
    setLocalRunning(v);
    ctx?.setLiveRunning?.(v);
    sessionStorage.setItem('slmcode:running', String(v));
  };
  const result = ctx?.liveResult || null;
  const setResult = ctx?.setLiveResult || (() => {});
  const [query, setQuery] = useState('');
  const [sidebarTab, setSidebarTab] = useState<'tasks' | 'result' | 'files'>('tasks');
  const [agents, setAgents] = useState<AgentSpec[]>([]);
  const [specialist, setSpecialist] = useState('');
  const [pipelineView, setPipelineView] = useState<PipelineView | null>(null);
  const [logExpanded, setLogExpanded] = useState(true);
  const eventSource = useRef<EventSource | null>(null);
  const logEnd = useRef<HTMLDivElement>(null);

  // Scroll to bottom on new events
  useEffect(() => {
    logEnd.current?.scrollIntoView({ behavior: 'smooth' });
  }, [events]);

  // Connect SSE
  const connectSSE = useCallback(() => {
    if (eventSource.current) {
      eventSource.current.close();
    }
    const es = new EventSource('/api/events');
    es.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data);
        if (data.phase === 'done' || data.phase === 'error') {
          setRunning(false);
          getLatestRun().then(setResult).catch(() => {});
        }
        if (data.kind === 'run_start') {
          setEvents([]);
        }
        setEvents((prev) => [...prev.slice(-500), data]);
      } catch { /* ignore */ }
    };
    es.onerror = () => {
      es.close();
      // Reconnect after 2s
      setTimeout(() => {
        if (eventSource.current === es) connectSSE();
      }, 2000);
    };
    eventSource.current = es;
  }, []);

  useEffect(() => {
    connectSSE();
    // Sync running state from latest run on remount
    getLatestRun().then((r) => {
      setResult(r);
      if (!r.running && r.result) setRunning(false);
    }).catch(() => {});
    return () => {
      eventSource.current?.close();
    };
  }, [connectSSE]);

  // Load agents for specialist picker
  useEffect(() => {
    getAgents().then(setAgents).catch(() => {});
  }, []);

  // Load pipeline config
  useEffect(() => {
    getPipeline().then(setPipelineView).catch(() => {});
  }, []);

  const handleRun = async () => {
    const q = query.trim();
    if (!q || running) return;
    setEvents([]);
    setResult(null);
    setRunning(true);
    try {
      await startRun({
        query: q,
        mode: specialist ? 'specialist' : undefined,
        specialist: specialist || undefined,
        skills: ctx?.config?.pinned_skills,
      });
    } catch (e) {
      console.error('Run failed:', e);
      setRunning(false);
    }
  };

  const handleStop = async () => {
    try {
      await stopRun();
    } catch { /* ignore */ }
    setRunning(false);
  };

  // ── Memoized computations ──

  // Resolve pipeline groups (use config if available, otherwise built-in defaults)
  const groups = useMemo<PhaseGroup[]>(() => {
    if (pipelineView?.config?.groups?.length) {
      return pipelineView.config.groups.map((g) => ({
        id: g.id,
        label: g.label,
        phases: g.steps,
        color: PIPELINE_GROUPS.find((pg) => pg.id === g.id)?.color || 'gray',
      }));
    }
    return PIPELINE_GROUPS;
  }, [pipelineView]);

  // All phases in order
  const allPhases = useMemo(() => groups.flatMap((g) => g.phases), [groups]);
  const totalPhases = allPhases.length;

  // Unique phases that have appeared in events
  const seenPhases = useMemo(() => {
    const set = new Set<string>();
    for (const e of events) {
      if (e.phase) set.add(e.phase);
    }
    return set;
  }, [events]);

  // Active phase = most recent event's phase
  const activePhase = useMemo(() => {
    for (let i = events.length - 1; i >= 0; i--) {
      if (events[i].phase) return events[i].phase;
    }
    return null;
  }, [events]);

  // Compute phase state map: pending | active | completed
  const phaseStateMap = useMemo<Record<string, PhaseState>>(() => {
    const map: Record<string, PhaseState> = {};
    let activeFound = false;
    for (const phase of allPhases) {
      if (!activeFound && phase === activePhase) {
        map[phase] = 'active';
        activeFound = true;
      } else if (seenPhases.has(phase)) {
        map[phase] = 'completed';
      } else {
        map[phase] = 'pending';
      }
    }
    // If active phase is not in the pipeline order, still mark as active
    if (activePhase && !allPhases.includes(activePhase)) {
      map[activePhase] = 'active';
    }
    return map;
  }, [allPhases, activePhase, seenPhases]);

  // Active agent = most recent event with agent field set
  const activeAgentId = useMemo(() => {
    for (let i = events.length - 1; i >= 0; i--) {
      if (events[i].agent) return events[i].agent;
    }
    return null;
  }, [events]);

  const activeAgentSpec = useMemo(() => {
    if (!activeAgentId) return null;
    return agents.find((a) => a.id === activeAgentId) || null;
  }, [activeAgentId, agents]);

  // Recent agent events (last 3 with agent field)
  const recentAgentEvents = useMemo(() => {
    const withAgent = events.filter((e) => e.agent);
    return withAgent.slice(-3).reverse();
  }, [events]);

  // Stats
  const stats = useMemo(() => {
    const completed = Object.values(phaseStateMap).filter((s) => s === 'completed').length;

    // Tasks: count unique task_ids from ALL events (more reliable than kind-matching)
    const taskIds = new Set<string>();
    for (const e of events) {
      if (e.task_id) taskIds.add(e.task_id);
    }
    const tasksSeen = taskIds.size;

    return {
      phasesCompleted: completed,
      totalPhases,
      activeAgent: activeAgentId,
      tasksSeen,
      eventsCount: events.length,
    };
  }, [phaseStateMap, totalPhases, activeAgentId, events]);

  // Determine if there's any pipeline activity
  const hasActivity = events.length > 0;

  return (
    <div className="h-full flex flex-col">
      {/* ── Run bar ── */}
      <div className="p-4 border-b border-gray-200 dark:border-gray-800 glass-alt">
        <div className="flex items-center gap-3 max-w-3xl">
          <input
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && handleRun()}
            placeholder="What would you like to build?"
            className="input flex-1 h-10"
            disabled={running}
          />
          {/* Specialist picker */}
          {agents.length > 0 && (
            <select
              value={specialist}
              onChange={(e) => setSpecialist(e.target.value)}
              className="input w-40 h-10"
              disabled={running}
            >
              <option value="">Any agent</option>
              {agents.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.title || a.id}
                  {a.effective_model ? ` · ${a.effective_model}` : ''}
                </option>
              ))}
            </select>
          )}
          {running ? (
            <button onClick={handleStop} className="btn-danger h-10 px-4 gap-2">
              <Square size={16} fill="currentColor" />
              Stop
            </button>
          ) : (
            <button
              onClick={handleRun}
              disabled={!query.trim()}
              className="btn-primary h-10 px-6 gap-2"
            >
              <Play size={16} fill="currentColor" />
              Run
            </button>
          )}
        </div>
      </div>

      {/* ── Active pipeline + config indicator ── */}
      {(ctx?.config?.active_pack || ctx?.config?.active_pipeline || ctx?.config?.active_stack || running) && (
        <div className="px-4 py-1.5 glass-alt border-b border-gray-100 dark:border-gray-700 flex items-center gap-3 flex-wrap">
          <span className="text-[10px] text-gray-400 font-medium">Active:</span>
          {ctx?.config?.active_pack && (
            <span className="badge-brand text-[10px]">📦 {ctx.config.active_pack}</span>
          )}
          {ctx?.config?.active_pipeline && (
            <span className="badge-neutral text-[10px]">⚙️ {ctx.config.active_pipeline}</span>
          )}
          {ctx?.config?.active_stack && (
            <span className="badge-neutral text-[10px]">⚡ {ctx.config.active_stack}</span>
          )}
          {running && pipelineView?.config?.execute && (
            <>
              <span className="text-[10px] text-gray-300">|</span>
              <span className="text-[10px] text-gray-400">worker:</span>
              <span className="badge-neutral text-[10px]">{pipelineView.config.execute.default_role || 'worker'}</span>
              <span className="text-[10px] text-gray-400">review:</span>
              <span className="badge-neutral text-[10px]">{pipelineView.config.execute.reviewer || 'reviewer'}</span>
              {(pipelineView.config.execute.max_waves ?? 0) > 0 && (
                <span className="text-[10px] text-gray-400">waves: {pipelineView.config.execute.max_waves}</span>
              )}
            </>
          )}
          {running && ctx?.config && (ctx.config as any).qa_gate_command && (
            <>
              <span className="text-[10px] text-gray-300">|</span>
              <span className="text-[10px] text-gray-400">qa:</span>
              <span className="badge-neutral text-[10px] font-mono">{(ctx.config as any).qa_gate_command}</span>
            </>
          )}
        </div>
      )}

      {/* ═══════════════════════════════════════════════════════════════ */}
      {/* Pipeline Progress — full-width animated strip */}
      {/* ═══════════════════════════════════════════════════════════════ */}
      <div className="px-4 py-2.5 border-b border-gray-200 dark:border-gray-800 bg-gradient-to-r from-gray-50 via-white to-gray-50 dark:from-gray-900 dark:via-gray-850 dark:to-gray-900">
        <div className="flex items-center gap-1.5 flex-wrap">
          {groups.map((group, gi) => {
            const done = group.phases.filter((p) => phaseStateMap[p] === 'completed').length;
            const active = group.phases.some((p) => phaseStateMap[p] === 'active');
            const allDone = done === group.phases.length;
            const pct = group.phases.length > 0 ? Math.round((done / group.phases.length) * 100) : 0;

            return (
              <div key={group.id} className="flex items-center gap-1">
                {gi > 0 && <ChevronDown size={10} className="text-gray-300 dark:text-gray-600 -rotate-90 shrink-0" />}
                <div className={clsx(
                  'relative flex items-center gap-2 px-3 py-2 rounded-xl border-2 transition-all duration-500 min-w-[120px]',
                  active ? clsx(GROUP_BG_COLORS[group.color], 'shadow-md scale-105 border-current') :
                  allDone ? 'bg-emerald-50/50 dark:bg-emerald-950/20 border-emerald-300 dark:border-emerald-700' :
                  'bg-white dark:bg-gray-900 border-gray-200 dark:border-gray-700 opacity-75'
                )}>
                  <div className="flex flex-col gap-0.5">
                    <span className={clsx('text-[10px] font-bold uppercase tracking-wider leading-none', allDone ? 'text-emerald-600' : active ? 'text-current opacity-80' : 'text-gray-400')}>{group.label}</span>
                    <span className="text-[9px] text-gray-400 font-mono tabular-nums leading-none">{done}/{group.phases.length}</span>
                  </div>
                  {/* Progress bar */}
                  <div className="flex-1 h-1.5 bg-gray-200 dark:bg-gray-700 rounded-full overflow-hidden min-w-[40px]">
                    <div className={clsx('h-full rounded-full transition-all duration-700', allDone ? 'bg-emerald-500' : active ? 'bg-current opacity-70' : 'bg-gray-300')} style={{ width: `${pct}%` }} />
                  </div>
                  {/* Phase dots */}
                  <div className="flex items-center gap-0.5 shrink-0">
                    {group.phases.map((p) => {
                      const s = phaseStateMap[p] || 'pending';
                      return s === 'completed' ? <CheckCircle2 key={p} size={12} className="text-emerald-500" /> :
                        s === 'active' ? <div key={p} className="relative"><div className={clsx('w-2.5 h-2.5 rounded-full animate-pulse', PHASE_DOT_COLORS[p] || 'bg-blue-500')} /></div> :
                        <div key={p} className="w-2 h-2 rounded-full bg-gray-300 dark:bg-gray-600" />;
                    })}
                  </div>
                  {active && <div className="absolute -top-1 -right-1 w-2 h-2 rounded-full bg-brand-500 animate-ping" />}
                </div>
              </div>
            );
          })}
        </div>
      </div>

      {/* ═══════════════════════════════════════════════════════════════ */}
      {/* Live Stats Bar — prominent cards */}
      {/* ═══════════════════════════════════════════════════════════════ */}
      <div className="px-4 py-2 border-b border-gray-200 dark:border-gray-800 bg-gradient-to-r from-gray-50 via-white to-gray-50 dark:from-gray-900 dark:via-gray-850 dark:to-gray-900">
        <div className="flex items-center gap-3">
          {/* Phases */}
          <div className="flex items-center gap-2 px-4 py-2 rounded-xl bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 shadow-sm hover:shadow-md transition-shadow">
            <div className="w-8 h-8 rounded-lg bg-sky-100 dark:bg-sky-900/30 flex items-center justify-center"><Target size={16} className="text-sky-600" /></div>
            <div>
              <div className="text-[10px] text-gray-400 font-medium leading-none">Phases</div>
              <div className="text-base font-bold text-gray-800 dark:text-white tabular-nums leading-tight">{stats.phasesCompleted}<span className="text-sm text-gray-400 font-normal">/{stats.totalPhases}</span></div>
            </div>
          </div>
          {/* Agent */}
          <div className="flex items-center gap-2 px-4 py-2 rounded-xl bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 shadow-sm hover:shadow-md transition-shadow">
            <div className={clsx('w-8 h-8 rounded-lg flex items-center justify-center', activeAgentId ? 'bg-violet-100 dark:bg-violet-900/30' : 'bg-gray-100 dark:bg-gray-800')}>
              <Bot size={16} className={activeAgentId ? 'text-violet-600' : 'text-gray-400'} />
            </div>
            <div>
              <div className="text-[10px] text-gray-400 font-medium leading-none">Agent</div>
              <div className="text-sm font-bold text-gray-800 dark:text-white leading-tight truncate max-w-[120px]">
                {activeAgentId ? (activeAgentSpec?.title || activeAgentId) : <span className="text-gray-400 font-normal">idle</span>}
              </div>
            </div>
          </div>
          {/* Tasks */}
          <div className="flex items-center gap-2 px-4 py-2 rounded-xl bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 shadow-sm hover:shadow-md transition-shadow">
            <div className="w-8 h-8 rounded-lg bg-amber-100 dark:bg-amber-900/30 flex items-center justify-center"><Zap size={16} className="text-amber-600" /></div>
            <div>
              <div className="text-[10px] text-gray-400 font-medium leading-none">Tasks</div>
              <div className={clsx('text-base font-bold tabular-nums leading-tight', stats.tasksSeen > 0 ? 'text-amber-600' : 'text-gray-400')}>{stats.tasksSeen}</div>
            </div>
          </div>
          {/* Events */}
          <div className="flex items-center gap-2 px-4 py-2 rounded-xl bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 shadow-sm hover:shadow-md transition-shadow">
            <div className="w-8 h-8 rounded-lg bg-emerald-100 dark:bg-emerald-900/30 flex items-center justify-center"><Activity size={16} className="text-emerald-600" /></div>
            <div>
              <div className="text-[10px] text-gray-400 font-medium leading-none">Events</div>
              <div className="text-base font-bold text-gray-800 dark:text-white tabular-nums leading-tight">{stats.eventsCount}</div>
            </div>
          </div>
          {/* Running */}
          {running && (
            <div className="flex items-center gap-2 px-4 py-2 rounded-xl bg-brand-50 dark:bg-brand-950/30 border-2 border-brand-300 dark:border-brand-700 shadow-sm animate-pulse">
              <Loader2 size={18} className="text-brand-500 animate-spin" />
              <span className="text-sm font-bold text-brand-700 dark:text-brand-300">Running</span>
            </div>
          )}
          {/* Progress bar for overall pipeline */}
          <div className="flex-1 h-2 bg-gray-200 dark:bg-gray-700 rounded-full overflow-hidden min-w-[60px]">
            <div className="h-full bg-gradient-to-r from-brand-400 to-brand-600 rounded-full transition-all duration-1000" style={{ width: `${totalPhases > 0 ? Math.round((stats.phasesCompleted / totalPhases) * 100) : 0}%` }} />
          </div>
        </div>
      </div>

      {/* ═══════════════════════════════════════════════════════════════ */}
      {/* Active Agent Panel */}
      {/* ═══════════════════════════════════════════════════════════════ */}
      {running && activeAgentId && (
        <div className="px-4 py-2.5 border-b border-gray-200 dark:border-gray-800 bg-violet-50/40 dark:bg-violet-950/20">
          <div className="flex items-center gap-3">
            {/* Pulsing agent icon */}
            <div className="relative shrink-0">
              <div className="w-8 h-8 rounded-lg bg-violet-100 dark:bg-violet-900/50 flex items-center justify-center">
                <Bot size={16} className="text-violet-600 dark:text-violet-400 animate-pulse" />
              </div>
            </div>

            {/* Agent info */}
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-2">
                <span className="text-sm font-bold text-violet-700 dark:text-violet-300">
                  {activeAgentSpec?.title || activeAgentId}
                </span>
                <span className="badge text-[10px] bg-violet-100 dark:bg-violet-900/40 text-violet-600 dark:text-violet-400">
                  Active
                </span>
              </div>
              {activeAgentSpec?.description && (
                <p className="text-[11px] text-gray-500 dark:text-gray-400 mt-0.5 truncate">
                  {activeAgentSpec.description}
                </p>
              )}
            </div>
          </div>

          {/* Recent agent events */}
          {recentAgentEvents.length > 0 && (
            <div className="mt-2 ml-11 space-y-0.5">
              {recentAgentEvents.map((ev, i) => (
                <div
                  key={`${ev.time}-${i}`}
                  className="flex items-center gap-2 text-[11px] text-gray-600 dark:text-gray-400"
                >
                  <span className="w-1.5 h-1.5 rounded-full bg-violet-400 shrink-0" />
                  <span className="text-violet-600 dark:text-violet-400 font-medium">
                    {ev.kind}
                  </span>
                  <span className="truncate">{ev.message}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* ═══════════════════════════════════════════════════════════════ */}
      {/* ── Content area: event log + result sidebar ── */}
      {/* ═══════════════════════════════════════════════════════════════ */}
      <div className="flex-1 flex overflow-hidden">
        {/* Event log */}
        <div className="flex-1 flex flex-col overflow-hidden">
          {/* Collapsible header */}
          <button
            onClick={() => setLogExpanded((v) => !v)}
            className="flex items-center gap-2 px-4 py-2 text-[11px] font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wider hover:bg-gray-50 dark:hover:bg-gray-800/50 transition-colors shrink-0"
          >
            {logExpanded ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
            Event Log
            {events.length > 0 && (
              <span className="text-[10px] text-gray-400 font-normal normal-case ml-1">
                ({events.length} events)
              </span>
            )}
          </button>

          {/* Log body */}
          {logExpanded && (
            <div className="flex-1 overflow-auto p-4">
              {events.length === 0 && !result ? (
                <div className="flex flex-col items-center justify-center h-full text-center space-y-4">
                  <div className="w-16 h-16 rounded-2xl bg-brand-100 dark:bg-brand-900/30 flex items-center justify-center">
                    <Play size={28} className="text-brand-500" />
                  </div>
                  <div>
                    <h2 className="text-lg font-semibold text-gray-700 dark:text-gray-300">
                      SLMCode Studio
                    </h2>
                    <p className="text-sm text-gray-400 mt-1 max-w-sm">
                      Enter a query above to start the agent pipeline. Watch live
                      progress via SSE streaming.
                    </p>
                  </div>
                </div>
              ) : (
                <EventLog events={events} />
              )}
              <div ref={logEnd} />
            </div>
          )}
        </div>

        {/* Right sidebar: Tasks + Results tabs */}
        <div className="w-96 border-l border-gray-200 dark:border-gray-800 flex flex-col shrink-0 animate-slide-left">
          {/* Tab bar */}
          <div className="flex border-b border-gray-200 dark:border-gray-700 shrink-0">
            <button
              onClick={() => setSidebarTab('tasks')}
              className={clsx(
                'flex-1 py-2.5 text-xs font-semibold transition-colors border-b-2',
                sidebarTab === 'tasks'
                  ? 'border-brand-500 text-brand-600 dark:text-brand-400'
                  : 'border-transparent text-gray-400 hover:text-gray-600',
              )}
            >
              📋 Tasks
            </button>
            <button
              onClick={() => setSidebarTab('result')}
              className={clsx(
                'flex-1 py-2.5 text-xs font-semibold transition-colors border-b-2',
                sidebarTab === 'result'
                  ? 'border-brand-500 text-brand-600 dark:text-brand-400'
                  : 'border-transparent text-gray-400 hover:text-gray-600',
              )}
            >
              📊 Result{result?.result ? ' ✓' : ''}
            </button>
            <button
              onClick={() => setSidebarTab('files')}
              className={clsx(
                'flex-1 py-2.5 text-xs font-semibold transition-colors border-b-2',
                sidebarTab === 'files'
                  ? 'border-brand-500 text-brand-600 dark:text-brand-400'
                  : 'border-transparent text-gray-400 hover:text-gray-600',
              )}
            >
              📁 Files
            </button>
          </div>

          {/* Tab content */}
          <div className="flex-1 overflow-auto">
            {sidebarTab === 'tasks' && <LiveTaskPanel />}
            {sidebarTab === 'result' && (
              <div className="h-full overflow-auto p-4">
                {result && result.result ? (
                  <div className="space-y-3">
                    <div className="flex items-center justify-between mb-3">
                      <h3 className="text-sm font-bold">Result</h3>
                      <span className={clsx('badge text-[10px]', result.result.success ? 'badge-success' : 'badge-error')}>
                        {result.result.success ? 'Success' : 'Failed'}
                      </span>
                    </div>
                    {result.result.summary && (
                      <div><div className="label">Summary</div><p className="text-sm text-gray-600 dark:text-gray-400">{result.result.summary}</p></div>
                    )}
                    <div className="grid grid-cols-2 gap-2">
                      <div className="card p-2"><div className="text-[10px] text-gray-400">Failed tasks</div><div className={clsx('text-lg font-bold', result.result.failed_tasks > 0 && 'text-red-500')}>{result.result.failed_tasks}</div></div>
                      <div className="card p-2"><div className="text-[10px] text-gray-400">Duration</div><div className="text-sm font-mono font-medium">{result.result.duration > 1e9 ? (result.result.duration / 1e9).toFixed(1) + 's' : (result.result.duration / 1e6).toFixed(0) + 'ms'}</div></div>
                    </div>
                    <div className="card p-2"><div className="text-[10px] text-gray-400">Events</div><div className="text-lg font-bold">{result.events?.length || 0}</div></div>
                  </div>
                ) : (
                  <div className="flex flex-col items-center justify-center h-full text-center text-gray-400 gap-3">
                    <Target size={32} className="opacity-50" />
                    <p className="text-sm">No run results yet.</p>
                    <p className="text-xs">Results appear here when a pipeline run completes.</p>
                  </div>
                )}
              </div>
            )}
            {sidebarTab === 'files' && (
              <LiveFileInspector events={events} running={running} />
            )}
          </div>
        </div>
      </div>
      <HITLPopup running={running} />
    </div>
  );
}
