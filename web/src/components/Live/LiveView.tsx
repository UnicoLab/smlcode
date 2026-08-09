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
  const [events, setEvents] = useState<RunEvent[]>([]);
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<LatestRunResponse | null>(null);
  const [query, setQuery] = useState('');
  const [sidebarTab, setSidebarTab] = useState<'tasks' | 'result'>('tasks');
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
    getLatestRun().then(setResult).catch(() => {});
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

    // Tasks running: count unique task_ids that have started but not finished
    const taskStarts = new Set<string>();
    const taskEnds = new Set<string>();
    for (const e of events) {
      if (!e.task_id) continue;
      if (e.kind === 'task_start') taskStarts.add(e.task_id);
      if (e.kind === 'task_done' || e.kind === 'task_fail') taskEnds.add(e.task_id);
    }
    const tasksRunning = [...taskStarts].filter((id) => !taskEnds.has(id)).length;

    return {
      phasesCompleted: completed,
      totalPhases,
      activeAgent: activeAgentId,
      tasksRunning,
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

      {/* ── Active config indicator ── */}
      {(ctx?.config?.active_pack || ctx?.config?.active_stack || ctx?.config?.active_pipeline) && (
        <div className="px-4 py-1.5 glass-alt border-b border-gray-100 dark:border-gray-700 flex items-center gap-3">
          <span className="text-[10px] text-gray-400 font-medium">Active:</span>
          {ctx.config.active_pack && (
            <span className="badge-brand text-[10px]">📦 {ctx.config.active_pack}</span>
          )}
          {ctx.config.active_pipeline && (
            <span className="badge-neutral text-[10px]">{ctx.config.active_pipeline}</span>
          )}
          {ctx.config.active_stack && (
            <span className="badge-neutral text-[10px]">⚡ {ctx.config.active_stack}</span>
          )}
        </div>
      )}

      {/* ═══════════════════════════════════════════════════════════════ */}
      {/* ── NEW: Pipeline Progress Strip ── */}
      {/* ═══════════════════════════════════════════════════════════════ */}
      {hasActivity && (
        <div className="px-4 py-3 border-b border-gray-200 dark:border-gray-800 bg-white/50 dark:bg-gray-900/50">
          <div className="flex items-center gap-2 mb-2">
            <Layers size={14} className="text-gray-400" />
            <span className="text-[10px] font-semibold uppercase tracking-wider text-gray-400">
              Pipeline Progress
            </span>
          </div>
          <div className="flex items-center gap-2 flex-wrap">
            {groups.map((group, gi) => {
              const groupPhaseStates = group.phases.map((p) => phaseStateMap[p] || 'pending');
              const hasActive = groupPhaseStates.includes('active');
              const allDone = groupPhaseStates.every((s) => s === 'completed');
              const allPending = groupPhaseStates.every((s) => s === 'pending');

              return (
                <div key={group.id} className="flex items-center gap-2">
                  {/* Separator arrow between groups */}
                  {gi > 0 && (
                    <ChevronDown
                      size={12}
                      className="text-gray-300 dark:text-gray-600 rotate-[-90deg] shrink-0"
                    />
                  )}

                  {/* Group card */}
                  <div
                    className={clsx(
                      'flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg border transition-all duration-300',
                      GROUP_BG_COLORS[group.color],
                      hasActive
                        ? clsx(GROUP_BORDER_COLORS[group.color], 'shadow-sm')
                        : allDone
                          ? 'border-emerald-300 dark:border-emerald-700'
                          : 'border-gray-200 dark:border-gray-700',
                    )}
                  >
                    {/* Group label */}
                    <span
                      className={clsx(
                        'text-[10px] font-semibold uppercase tracking-wider mr-1',
                        allDone
                          ? 'text-emerald-600 dark:text-emerald-400'
                          : hasActive
                            ? GROUP_TEXT_COLORS[group.color]
                            : 'text-gray-400 dark:text-gray-500',
                      )}
                    >
                      {group.label}
                    </span>

                    {/* Phase dots */}
                    {group.phases.map((phase) => {
                      const state = phaseStateMap[phase] || 'pending';
                      const isActive = state === 'active';
                      const isCompleted = state === 'completed';
                      const dotColor = PHASE_DOT_COLORS[phase] || 'bg-gray-400';

                      return (
                        <div
                          key={phase}
                          className="relative flex items-center justify-center"
                          title={`${phase}${isActive ? ' (active)' : isCompleted ? ' (completed)' : ''}`}
                        >
                          {isCompleted ? (
                            <CheckCircle2
                              size={14}
                              className="text-emerald-500 dark:text-emerald-400"
                            />
                          ) : isActive ? (
                            <div className="relative">
                              <Circle
                                size={14}
                                className={clsx(dotColor, 'animate-pulse')}
                                fill="currentColor"
                              />
                              {/* Outer pulse ring */}
                              <span
                                className={clsx(
                                  'absolute inset-0 rounded-full animate-ping opacity-40',
                                  dotColor.replace('bg-', 'bg-'),
                                )}
                              />
                            </div>
                          ) : (
                            <Circle
                              size={10}
                              className="text-gray-300 dark:text-gray-600"
                              fill="currentColor"
                            />
                          )}
                        </div>
                      );
                    })}
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}

      {/* ═══════════════════════════════════════════════════════════════ */}
      {/* ── NEW: Stats Dashboard Row ── */}
      {/* ═══════════════════════════════════════════════════════════════ */}
      {hasActivity && (
        <div className="px-4 py-2.5 border-b border-gray-200 dark:border-gray-800 glass-alt">
          <div className="flex items-center gap-4 flex-wrap">
            {/* Phases completed */}
            <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700">
              <Target size={14} className="text-sky-500" />
              <div>
                <span className="text-[10px] text-gray-400 font-medium">Phases</span>
                <span className="ml-1.5 text-sm font-bold text-gray-800 dark:text-gray-200 tabular-nums">
                  {stats.phasesCompleted}
                  <span className="text-gray-400 font-normal">/{stats.totalPhases}</span>
                </span>
              </div>
            </div>

            {/* Active agent */}
            <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700">
              <Bot size={14} className="text-violet-500" />
              <div>
                <span className="text-[10px] text-gray-400 font-medium">Agent</span>
                <span className="ml-1.5 text-sm font-bold text-gray-800 dark:text-gray-200">
                  {activeAgentId ? (
                    <span className="text-violet-600 dark:text-violet-400">
                      {activeAgentSpec?.title || activeAgentId}
                    </span>
                  ) : (
                    <span className="text-gray-400">—</span>
                  )}
                </span>
              </div>
            </div>

            {/* Tasks running */}
            <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700">
              <Zap size={14} className="text-amber-500" />
              <div>
                <span className="text-[10px] text-gray-400 font-medium">Tasks</span>
                <span className="ml-1.5 text-sm font-bold text-gray-800 dark:text-gray-200 tabular-nums">
                  {stats.tasksRunning > 0 ? (
                    <span className="text-amber-600 dark:text-amber-400">{stats.tasksRunning} running</span>
                  ) : (
                    <span className="text-gray-400">—</span>
                  )}
                </span>
              </div>
            </div>

            {/* Events count */}
            <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700">
              <Activity size={14} className="text-emerald-500" />
              <div>
                <span className="text-[10px] text-gray-400 font-medium">Events</span>
                <span className="ml-1.5 text-sm font-bold text-gray-800 dark:text-gray-200 tabular-nums">
                  {stats.eventsCount}
                </span>
              </div>
            </div>

            {/* Running indicator */}
            {running && (
              <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-brand-50 dark:bg-brand-950/30 border border-brand-200 dark:border-brand-800">
                <Loader2 size={14} className="text-brand-500 animate-spin" />
                <span className="text-[11px] font-semibold text-brand-600 dark:text-brand-400">
                  Running…
                </span>
              </div>
            )}
          </div>
        </div>
      )}

      {/* ═══════════════════════════════════════════════════════════════ */}
      {/* ── NEW: Active Agent Panel ── */}
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
          </div>

          {/* Tab content */}
          <div className="flex-1 overflow-hidden">
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
          </div>
        </div>
      </div>
    </div>
  );
}
