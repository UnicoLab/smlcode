import { useState, useEffect, useRef, useContext, useMemo } from 'react';
import type { ReactNode } from 'react';
import {
  Play,
  Square,
  ChevronDown,
  ChevronUp,
  AlertTriangle,
  Bot,
  Loader2,
  Circle,
  Activity,
  Target,
  Zap,
  Layers,
  Cpu,
} from 'lucide-react';
import { AppContext } from '@/App';
import { startRun, stopRun, getAgents, getPipeline, getComposition, previewComposition, getInterruptedRuns, resumeRun } from '@/api/client';
import type { RunEvent, AgentSpec, PipelineView, DynamicComposition, InterruptedRun } from '@/types';
import EventLog from './EventLog';
import LiveTaskPanel from './LiveTaskPanel';
import LiveFileInspector from './LiveFileInspector';
import LiveFeedback from './LiveFeedback';
import TokenStream from './TokenStream';
import { useToast } from '@/components/ui/Toast';
import { FOCUS_PROMPT_EVENT } from '@/hooks/useKeyboard';
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

// ── Phase state enum ──
type PhaseState = 'pending' | 'active' | 'completed';

export default function LiveView() {
  const ctx = useContext(AppContext);
  const toast = useToast();

  // The event stream is owned by App (see hooks/useLiveStream) so it survives
  // navigation and stays connected while the user is on another page. This view
  // is a pure consumer — it holds no EventSource and no event state of its own,
  // which is what made the log collapse to a single entry before.
  const events = useMemo(() => ctx?.liveEvents ?? [], [ctx?.liveEvents]);
  const running = ctx?.liveRunning ?? false;
  const setRunning = ctx?.setLiveRunning ?? (() => {});
  const resetEvents = ctx?.resetLiveEvents ?? (() => {});
  const result = ctx?.liveResult || null;
  const setResult = ctx?.setLiveResult || (() => {});
  const [query, setQuery] = useState('');
  const [sidebarTab, setSidebarTab] = useState<'tasks' | 'result' | 'files'>('tasks');
  const [agents, setAgents] = useState<AgentSpec[]>([]);
  const [specialist, setSpecialist] = useState('');
  const [pipelineView, setPipelineView] = useState<PipelineView | null>(null);
  const [persistedComposition, setPersistedComposition] = useState<DynamicComposition | null>(null);
  const [persistedCompositionError, setPersistedCompositionError] = useState('');
  const [compositionPreview, setCompositionPreview] = useState<DynamicComposition | null>(null);
  const [compositionPreviewFit, setCompositionPreviewFit] = useState<string[]>([]);
  const [interrupted, setInterrupted] = useState<InterruptedRun[]>([]);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [logExpanded, setLogExpanded] = useState(true);
  const logEnd = useRef<HTMLDivElement>(null);
  const promptRef = useRef<HTMLInputElement>(null);

  // Scroll to bottom on new events
  useEffect(() => {
    logEnd.current?.scrollIntoView({ behavior: 'smooth' });
  }, [events]);

  // `/` focuses the prompt from anywhere in the app.
  useEffect(() => {
    const focus = () => promptRef.current?.focus();
    window.addEventListener(FOCUS_PROMPT_EVENT, focus);
    return () => window.removeEventListener(FOCUS_PROMPT_EVENT, focus);
  }, []);

  // Refresh the resumable-run list whenever a run finishes.
  useEffect(() => {
    if (running) return;
    getInterruptedRuns()
      .then(setInterrupted)
      .catch(() => {
        /* the connection badge already reports API trouble */
      });
  }, [running]);

  // A fresh run clears the composition panels; the log itself is reset by the
  // stream hook when the server emits `run_start`.
  const lastRunStart = events.length > 0 ? events[events.length - 1] : null;
  useEffect(() => {
    if (lastRunStart?.kind === 'run_start') {
      setPersistedComposition(null);
      setPersistedCompositionError('');
    }
  }, [lastRunStart]);

  // Load agents for specialist picker
  useEffect(() => {
    getAgents().then(setAgents).catch(() => {});
  }, []);

  // Load pipeline config
  useEffect(() => {
    getPipeline().then(setPipelineView).catch(() => {});
    getComposition()
      .then((r) => {
        setPersistedComposition(r.composition || null);
        setPersistedCompositionError(r.composition_error || '');
      })
      .catch((e) => {
        setPersistedComposition(null);
        setPersistedCompositionError(e instanceof Error ? e.message : 'Unable to load saved composition');
      });
  }, []);

  useEffect(() => {
    const q = query.trim();
    if (running || specialist || !ctx?.config?.dynamic_pipeline || q.length < 3) {
      setCompositionPreview(null);
      setCompositionPreviewFit([]);
      setPreviewLoading(false);
      return;
    }
    let cancelled = false;
    setPreviewLoading(true);
    const timer = window.setTimeout(() => {
      previewComposition(q)
        .then((r) => {
          if (!cancelled) {
            setCompositionPreview(r.composition || null);
            setCompositionPreviewFit(r.slm_fit || []);
          }
        })
        .catch(() => {
          if (!cancelled) {
            setCompositionPreview(null);
            setCompositionPreviewFit([]);
          }
        })
        .finally(() => {
          if (!cancelled) setPreviewLoading(false);
        });
    }, 350);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [query, running, specialist, ctx?.config?.dynamic_pipeline]);

  const handleRun = async () => {
    const q = query.trim();
    if (!q || running) return;
    resetEvents();
    setResult(null);
    setPersistedComposition(null);
    setPersistedCompositionError('');
    setCompositionPreview(null);
    setCompositionPreviewFit([]);
    setRunning(true);
    try {
      await startRun({
        query: q,
        mode: specialist ? 'specialist' : undefined,
        specialist: specialist || undefined,
        skills: ctx?.config?.pinned_skills,
      });
    } catch (e) {
      toast.reportError(e, 'Could not start the run');
      setRunning(false);
    }
  };

  const handleResume = async (id?: string) => {
    if (running) return;
    resetEvents();
    setResult(null);
    setPersistedComposition(null);
    setPersistedCompositionError('');
    setCompositionPreview(null);
    setCompositionPreviewFit([]);
    setRunning(true);
    try {
      await resumeRun(id);
      setInterrupted([]);
    } catch (e) {
      toast.reportError(e, 'Could not resume the run');
      setRunning(false);
      getInterruptedRuns().then(setInterrupted).catch(() => {});
    }
  };

  const handleStop = async () => {
    try {
      await stopRun();
    } catch (e) {
      toast.reportError(e, 'Could not stop the run');
    }
    setRunning(false);
  };

  // ── Memoized computations ──

  const dynamicComposition = useMemo<DynamicComposition | null>(() => {
    for (let i = events.length - 1; i >= 0; i--) {
      const ev = events[i];
      if (ev.kind === 'composition' && ev.data) return ev.data;
    }
    return running ? null : persistedComposition;
  }, [events, persistedComposition, running]);

  const shownComposition = dynamicComposition || (!running ? compositionPreview : null);
  const shownCompositionMode = dynamicComposition ? 'runtime' : compositionPreview ? 'preview' : '';
  const shownCompositionFit = shownComposition?.slm_fit || (shownCompositionMode === 'preview' ? compositionPreviewFit : []);
  const shownCompositionPhases = useMemo(() => shownComposition?.phases || [], [shownComposition]);
  const activeExecute = dynamicComposition?.execute || pipelineView?.config?.execute;

  const dynamicPhaseOrder = useMemo(() => {
    if (!shownCompositionPhases.length) return null;
    return shownCompositionPhases
      .filter((p) => p.enabled && p.when !== 'never')
      .map((p) => p.id)
      .filter(Boolean);
  }, [shownCompositionPhases]);

  // Resolve pipeline groups (use config if available, otherwise built-in defaults)
  const groups = useMemo<PhaseGroup[]>(() => {
    const keep = dynamicPhaseOrder ? new Set(dynamicPhaseOrder) : null;
    if (pipelineView?.config?.groups?.length) {
      return pipelineView.config.groups
        .map((g) => ({
          id: g.id,
          label: g.label,
          phases: keep ? g.steps.filter((p) => keep.has(p)) : g.steps,
          color: PIPELINE_GROUPS.find((pg) => pg.id === g.id)?.color || 'gray',
        }))
        .filter((g) => g.phases.length > 0);
    }
    if (keep) return PIPELINE_GROUPS.map((g) => ({ ...g, phases: g.phases.filter((p) => keep.has(p)) })).filter((g) => g.phases.length > 0);
    return PIPELINE_GROUPS;
  }, [pipelineView, dynamicPhaseOrder]);

  // All phases in order
  const allPhases = useMemo(() => dynamicPhaseOrder || groups.flatMap((g) => g.phases), [groups, dynamicPhaseOrder]);
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

  const selectedAgentSpec = useMemo(() => {
    if (!specialist) return null;
    return agents.find((a) => a.id === specialist) || null;
  }, [agents, specialist]);

  const recentAgents = useMemo(() => {
    const seen = new Set<string>();
    const list: Array<{ id: string; title: string; model?: string }> = [];
    for (let i = events.length - 1; i >= 0 && list.length < 5; i--) {
      const id = events[i].agent;
      if (!id || seen.has(id)) continue;
      seen.add(id);
      const spec = agents.find((a) => a.id === id);
      list.push({ id, title: spec?.title || id, model: spec?.effective_model || spec?.model });
    }
    return list;
  }, [agents, events]);

  const lastEvent = events.length > 0 ? events[events.length - 1] : null;
  const currentPhaseIndex = activePhase ? allPhases.indexOf(activePhase) : -1;
  const nextPhase = currentPhaseIndex >= 0 ? allPhases[currentPhaseIndex + 1] : allPhases[0];

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

  const progressPct = totalPhases > 0 ? Math.round((stats.phasesCompleted / totalPhases) * 100) : 0;

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden bg-gray-50/70 dark:bg-gray-950">
      {/* ── Command Center ── */}
      <div className="shrink-0 border-b border-gray-200 bg-white px-4 py-4 shadow-sm dark:border-gray-800 dark:bg-gray-950">
        <div className="mx-auto flex max-w-[1600px] flex-col gap-4">
          <div className="flex flex-col gap-3 xl:flex-row xl:items-start xl:justify-between">
            <div className="min-w-0 flex-1">
              <div className="mb-2 flex flex-wrap items-center gap-2">
                <span className={clsx(
                  'inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs font-semibold',
                  running
                    ? 'border-brand-300 bg-brand-50 text-brand-700 dark:border-brand-800 dark:bg-brand-950/40 dark:text-brand-300'
                    : 'border-gray-200 bg-gray-50 text-gray-600 dark:border-gray-800 dark:bg-gray-900 dark:text-gray-300',
                )}>
                  {running ? <Loader2 size={13} className="animate-spin" /> : <Circle size={12} />}
                  {running ? 'Running' : 'Ready'}
                </span>
                {ctx?.config?.dynamic_pipeline !== undefined && (
                  <span className={ctx.config.dynamic_pipeline ? 'badge-brand text-[10px]' : 'badge-neutral text-[10px]'}>
                    {ctx.config.dynamic_pipeline ? 'dynamic' : 'static'}
                  </span>
                )}
                {ctx?.config?.active_pack && <span className="badge-neutral text-[10px]">pack {ctx.config.active_pack}</span>}
                {ctx?.config?.active_pipeline && <span className="badge-neutral text-[10px]">pipeline {ctx.config.active_pipeline}</span>}
                {ctx?.config?.active_stack && <span className="badge-neutral text-[10px]">stack {ctx.config.active_stack}</span>}
                {ctx?.config?.model && (
                  <span
                    className="min-w-0 truncate rounded-md border border-gray-200 bg-gray-50 px-2 py-1 text-[10px] font-medium text-gray-500 dark:border-gray-800 dark:bg-gray-900 dark:text-gray-400"
                    title={ctx.config.model}
                  >
                    {ctx.config.model}
                  </span>
                )}
              </div>

              <div className="flex flex-col gap-2 lg:flex-row">
                <input
                  ref={promptRef}
                  type="text"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && handleRun()}
                  placeholder="Ask the harness to plan, code, test, or inspect a change  ( / to focus )"
                  aria-label="Run prompt"
                  className="input focus-ring h-12 flex-1 text-sm shadow-sm"
                  disabled={running}
                />
                <div className="flex gap-2">
                  {agents.length > 0 && (
                    <select
                      value={specialist}
                      onChange={(e) => setSpecialist(e.target.value)}
                      className="input h-12 min-w-0 flex-1 text-sm lg:w-56"
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
                    <button onClick={handleStop} className="btn-danger h-12 shrink-0 px-4 gap-2">
                      <Square size={16} fill="currentColor" />
                      Stop
                    </button>
                  ) : (
                    <button
                      onClick={handleRun}
                      disabled={!query.trim()}
                      className="btn-primary h-12 shrink-0 px-5 gap-2"
                    >
                      <Play size={16} fill="currentColor" />
                      Run
                    </button>
                  )}
                </div>
              </div>

              {selectedAgentSpec && (
                <div className="mt-2 rounded-lg border border-violet-200 bg-violet-50/70 px-3 py-2 dark:border-violet-900/70 dark:bg-violet-950/25">
                  <div className="flex flex-wrap items-start gap-2">
                    <span className="badge-brand shrink-0 text-[10px]">specialist</span>
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="text-xs font-bold text-violet-800 line-clamp-2 dark:text-violet-200" title={selectedAgentSpec.title || selectedAgentSpec.id}>
                          {selectedAgentSpec.title || selectedAgentSpec.id}
                        </span>
                        <span className="font-mono text-[10px] text-violet-500" title={selectedAgentSpec.id}>{selectedAgentSpec.id}</span>
                      </div>
                      {selectedAgentSpec.description && (
                        <p className="mt-1 text-xs text-violet-700 line-clamp-2 dark:text-violet-300" title={selectedAgentSpec.description}>
                          {selectedAgentSpec.description}
                        </p>
                      )}
                    </div>
                    <span
                      className="inline-flex max-w-full items-center gap-1 rounded-md bg-white/80 px-2 py-1 text-[10px] text-violet-700 dark:bg-gray-900/70 dark:text-violet-300"
                      title={`${selectedAgentSpec.effective_provider || selectedAgentSpec.provider || 'stack'}/${selectedAgentSpec.effective_model || selectedAgentSpec.model || 'inherit'}`}
                    >
                      <Cpu size={11} className="shrink-0" />
                      <span className="min-w-0 break-all">
                        {selectedAgentSpec.effective_model || selectedAgentSpec.model || 'inherit'}
                      </span>
                    </span>
                  </div>
                </div>
              )}
            </div>

            <div className="grid grid-cols-2 gap-2 sm:grid-cols-4 xl:w-[31rem]">
              <LiveMetric label="Phases" value={`${stats.phasesCompleted}/${stats.totalPhases}`} icon={<Target size={15} />} tone="sky" />
              <LiveMetric label="Tasks" value={String(stats.tasksSeen ?? 0)} icon={<Zap size={15} />} tone="amber" />
              <LiveMetric label="Events" value={String(stats.eventsCount)} icon={<Activity size={15} />} tone="emerald" />
              <LiveMetric label="Agent" value={activeAgentId ? (activeAgentSpec?.title || activeAgentId) : 'idle'} icon={<Bot size={15} />} tone="violet" />
            </div>
          </div>

          <div className="grid gap-3 xl:grid-cols-[1fr_24rem]">
            <div className="rounded-lg border border-gray-200 bg-gray-50/80 p-3 dark:border-gray-800 dark:bg-gray-900/60">
              <div className="mb-2 flex items-center justify-between gap-3">
                <div className="flex min-w-0 items-center gap-2">
                  <span className="text-xs font-semibold text-gray-700 dark:text-gray-200">
                    Pipeline Progress
                  </span>
                  {activePhase && <span className="badge-neutral text-[10px]">phase {activePhase}</span>}
                  {activeExecute && (
                    <span
                      className="hidden min-w-0 truncate text-[10px] text-gray-400 sm:inline"
                      title={`worker ${activeExecute.default_role || 'worker'}; reviewer ${activeExecute.reviewer || 'reviewer'}`}
                    >
                      worker {activeExecute.default_role || 'worker'} · reviewer {activeExecute.reviewer || 'reviewer'}
                    </span>
                  )}
                </div>
                <span className="font-mono text-xs font-semibold text-gray-500 dark:text-gray-400">{progressPct}%</span>
              </div>
              <div className="h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-800">
                <div
                  className="h-full rounded-full bg-brand-500 transition-all duration-700"
                  style={{ width: `${progressPct}%` }}
                />
              </div>
              <div className="mt-3 grid grid-cols-2 gap-1.5 sm:grid-cols-4 lg:grid-cols-8 xl:grid-cols-10">
                {allPhases.map((phase) => {
                  const state = phaseStateMap[phase] || 'pending';
                  return (
                    <span
                      key={phase}
                      className={clsx(
                        'flex min-w-0 items-center gap-1 rounded-md border px-2 py-1 text-[10px] font-semibold',
                        state === 'active' && 'border-brand-300 bg-brand-50 text-brand-700 dark:border-brand-800 dark:bg-brand-950/40 dark:text-brand-300',
                        state === 'completed' && 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/30 dark:text-emerald-300',
                        state === 'pending' && 'border-gray-200 bg-white text-gray-400 dark:border-gray-800 dark:bg-gray-950',
                      )}
                      title={`${phase}: ${state}`}
                    >
                      <span className={clsx('h-1.5 w-1.5 shrink-0 rounded-full', PHASE_DOT_COLORS[phase] || 'bg-gray-400')} />
                      <span className="min-w-0 truncate">{phase}</span>
                    </span>
                  );
                })}
              </div>
            </div>

            <div className="rounded-lg border border-gray-200 bg-white p-3 dark:border-gray-800 dark:bg-gray-900">
              <LiveFeedback />
            </div>
          </div>

          <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_minmax(18rem,28rem)]">
            <div className="rounded-lg border border-gray-200 bg-white p-3 dark:border-gray-800 dark:bg-gray-900">
              <div className="mb-2 flex flex-wrap items-center gap-2">
                <span className="text-xs font-bold text-gray-800 dark:text-gray-100">Current Stage</span>
                {activePhase ? (
                  <span className="badge-brand text-[10px]">{activePhase}</span>
                ) : (
                  <span className="badge-neutral text-[10px]">idle</span>
                )}
                {nextPhase && <span className="badge-neutral text-[10px]">next {nextPhase}</span>}
              </div>
              <div className="grid gap-2 sm:grid-cols-3">
                <StageFact label="Last event" value={lastEvent?.kind || 'none'} detail={lastEvent?.message || 'No events in this run yet'} />
                <StageFact label="Phase" value={activePhase || 'waiting'} detail={currentPhaseIndex >= 0 ? `${currentPhaseIndex + 1} of ${allPhases.length}` : `${allPhases.length} configured`} />
                <StageFact label="Loop" value={activeExecute?.default_role || 'worker'} detail={`review ${activeExecute?.reviewer || 'reviewer'} / fix ${activeExecute?.corrector || 'corrector'}`} />
              </div>
            </div>

            <div className="rounded-lg border border-gray-200 bg-white p-3 dark:border-gray-800 dark:bg-gray-900">
              <div className="mb-2 flex items-center justify-between gap-2">
                <span className="text-xs font-bold text-gray-800 dark:text-gray-100">Agent Activity</span>
                <span className="text-[10px] text-gray-400">{recentAgents.length ? `${recentAgents.length} recent` : 'idle'}</span>
              </div>
              {recentAgents.length ? (
                <div className="space-y-1.5">
                  {recentAgents.map((agent) => (
                    <div key={agent.id} className="flex min-w-0 items-center gap-2 rounded-md bg-gray-50 px-2 py-1.5 dark:bg-gray-800/60" title={`${agent.title}${agent.model ? ` / ${agent.model}` : ''}`}>
                      <Bot size={13} className="shrink-0 text-violet-500" />
                      <span className="min-w-0 flex-1 text-xs font-semibold text-gray-700 line-clamp-2 dark:text-gray-200">{agent.title}</span>
                      {agent.model && <span className="hidden max-w-28 truncate font-mono text-[10px] text-gray-400 sm:block">{agent.model}</span>}
                    </div>
                  ))}
                </div>
              ) : (
                <div className="rounded-md border border-dashed border-gray-200 px-3 py-4 text-center text-xs text-gray-400 dark:border-gray-800">
                  No agent has run in this session yet.
                </div>
              )}
            </div>
          </div>

          {!running && interrupted.length > 0 && (
            <div className="flex flex-col gap-2 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-100 sm:flex-row sm:items-center sm:justify-between">
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-semibold">Interrupted run</span>
                  <span className="rounded bg-white/70 px-1.5 py-0.5 font-mono text-[11px] dark:bg-black/20">{interrupted[0].id}</span>
                  {interrupted[0].react_resume && <span className="badge-neutral text-[10px]">ReAct</span>}
                  {interrupted[0].phase && <span className="text-xs opacity-75">{interrupted[0].phase}</span>}
                </div>
                <p className="text-xs opacity-80 line-clamp-2" title={interrupted[0].query}>
                  {interrupted[0].done}/{interrupted[0].tasks} done, {interrupted[0].blocked} blocked · {interrupted[0].query}
                </p>
              </div>
              <button
                onClick={() => handleResume(interrupted[0].id)}
                className="btn-primary h-9 shrink-0 gap-2 text-xs"
              >
                <Play size={14} fill="currentColor" />
                Resume
              </button>
            </div>
          )}
        </div>
      </div>

      {previewLoading && !shownComposition && !running && (
        <div className="flex shrink-0 items-center gap-2 border-b border-gray-200 bg-teal-50/30 px-4 py-2 text-xs text-teal-700 dark:border-gray-800 dark:bg-teal-950/10 dark:text-teal-300">
          <Loader2 size={13} className="animate-spin" />
          Previewing dynamic pipeline
        </div>
      )}

      {!running && persistedCompositionError && (
        <div className="flex shrink-0 items-start gap-2 border-b border-amber-200 bg-amber-50 px-4 py-2 text-xs text-amber-900 dark:border-amber-900/70 dark:bg-amber-950/30 dark:text-amber-100">
          <AlertTriangle size={14} className="mt-0.5 shrink-0" />
          <span className="min-w-0 break-words">Saved dynamic composition could not be read: {persistedCompositionError}</span>
        </div>
      )}

      {shownComposition && (
        <div className="shrink-0 border-b border-gray-200 bg-teal-50/45 px-4 py-3 dark:border-gray-800 dark:bg-teal-950/20">
          <div className="flex items-start gap-3">
            <div className="w-8 h-8 rounded-lg bg-teal-100 dark:bg-teal-900/50 flex items-center justify-center shrink-0">
              <Layers size={16} className="text-teal-700 dark:text-teal-300" />
            </div>
            <div className="min-w-0 flex-1 space-y-2">
              <div className="flex items-center gap-2 flex-wrap">
                <span className="text-sm font-bold text-teal-800 dark:text-teal-200">
                  {shownCompositionMode === 'preview' ? 'Preview Composition' : 'Dynamic Composition'}
                </span>
                {shownCompositionMode === 'preview' && <span className="badge-neutral text-[10px]">deterministic</span>}
                <span className="max-w-4xl text-xs text-gray-600 line-clamp-2 dark:text-gray-300" title={shownComposition.summary}>
                  {shownComposition.summary}
                </span>
              </div>

              {shownComposition.handoff && shownComposition.handoff.length > 0 && (
                <div className="flex items-start gap-2 text-[11px] text-gray-700 dark:text-gray-300">
                  <span className="shrink-0 font-semibold text-teal-700 dark:text-teal-300">Handoff</span>
                  <div className="flex flex-wrap gap-1.5 min-w-0">
                    {shownComposition.handoff.slice(0, 6).map((h, i) => (
                      <span key={`${h}-${i}`} className="px-2 py-1 rounded-md bg-white/80 dark:bg-gray-900/60 border border-teal-100 dark:border-teal-800/70 break-words">
                        {h}
                      </span>
                    ))}
                  </div>
                </div>
              )}

              {shownCompositionFit.length > 0 && (
                <div className="flex items-start gap-2 text-[11px] text-amber-900 dark:text-amber-100">
                  <span className="shrink-0 font-semibold text-amber-700 dark:text-amber-300">SLM Fit</span>
                  <div className="flex flex-wrap gap-1.5 min-w-0">
                    {shownCompositionFit.slice(0, 4).map((hint, i) => (
                      <span key={`${hint}-${i}`} className="px-2 py-1 rounded-md bg-amber-50 dark:bg-amber-950/35 border border-amber-200 dark:border-amber-800/70 break-words">
                        {hint}
                      </span>
                    ))}
                  </div>
                </div>
              )}

              <div className="flex flex-wrap gap-1.5">
                {shownCompositionPhases
                  .filter((p) => p.enabled && p.when !== 'never')
                  .map((p, i) => (
                    <span
                      key={`${p.id}-${i}`}
                      className="inline-flex max-w-full items-center gap-1.5 rounded-md border border-gray-200 bg-white/80 px-2 py-1 text-[11px] dark:border-gray-700 dark:bg-gray-900/60"
                      title={`${p.id}${p.agent ? ` @${p.agent}` : ''}${p.when ? ` (${p.when})` : ''}`}
                    >
                      <span className={clsx('w-1.5 h-1.5 rounded-full', PHASE_DOT_COLORS[p.id] || 'bg-teal-500')} />
                      <span className="font-semibold text-gray-700 dark:text-gray-200">{p.id}</span>
                      {p.agent && <span className="text-gray-400">@{p.agent}</span>}
                    </span>
                  ))}
              </div>

              <div className="flex items-center gap-2 flex-wrap text-[11px]">
                <span className="font-semibold text-teal-700 dark:text-teal-300">Loop</span>
                <span className="badge-neutral">worker: {shownComposition.execute?.default_role || 'worker'}</span>
                <span className="badge-neutral">reviewer: {shownComposition.execute?.reviewer || 'reviewer'}</span>
                <span className="badge-neutral">corrector: {shownComposition.execute?.corrector || 'corrector'}</span>
                {shownComposition.execute?.max_waves ? (
                  <span className="badge-neutral">waves: {shownComposition.execute.max_waves}</span>
                ) : null}
              </div>

              {shownComposition.team && shownComposition.team.length > 0 && (
                <div className="flex flex-wrap gap-1.5">
                  {shownComposition.team.map((member) => {
                    const spec = agents.find((a) => a.id === member.role);
                    return (
                      <span
                        key={member.role}
                        className="inline-flex max-w-full items-center gap-1.5 rounded-md bg-teal-100/80 px-2 py-1 text-[11px] text-teal-900 dark:bg-teal-900/40 dark:text-teal-100"
                        title={`${spec?.title || member.role}${member.skills?.length ? `; skills: ${member.skills.join(', ')}` : ''}`}
                      >
                        <Bot size={12} />
                        <span className="font-semibold line-clamp-2">{spec?.title || member.role}</span>
                        {member.skills && member.skills.length > 0 && (
                          <span className="min-w-0 text-teal-700 line-clamp-2 dark:text-teal-300">{member.skills.join(', ')}</span>
                        )}
                      </span>
                    );
                  })}
                </div>
              )}

              {shownComposition.slots && shownComposition.slots.length > 0 && (
                <div className="flex items-start gap-2 text-[11px] text-gray-700 dark:text-gray-300">
                  <span className="shrink-0 font-semibold text-teal-700 dark:text-teal-300">Slots</span>
                  <div className="flex flex-wrap gap-1.5 min-w-0">
                    {shownComposition.slots.slice(0, 8).map((slot) => (
                      <span key={slot.id} className="px-2 py-1 rounded-md bg-white/80 dark:bg-gray-900/60 border border-teal-100 dark:border-teal-800/70">
                        <span className="font-semibold">{slot.id}</span>
                        {slot.agent ? ` @${slot.agent}` : ''}
                        <span className="text-gray-400">
                          {' '}
                          {slot.before ? `before ${slot.before}` : slot.after ? `after ${slot.after}` : slot.replace ? `replace ${slot.replace}` : 'slot'}
                        </span>
                      </span>
                    ))}
                  </div>
                </div>
              )}
            </div>
          </div>
        </div>
      )}

      {/* ═══════════════════════════════════════════════════════════════ */}
      {/* Active Agent Panel */}
      {/* ═══════════════════════════════════════════════════════════════ */}
      {running && activeAgentId && (
        <div className="shrink-0 border-b border-gray-200 bg-violet-50/40 px-4 py-2.5 dark:border-gray-800 dark:bg-violet-950/20">
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
                <span className="text-sm font-bold text-violet-700 line-clamp-2 dark:text-violet-300" title={activeAgentSpec?.title || activeAgentId}>
                  {activeAgentSpec?.title || activeAgentId}
                </span>
                <span className="badge text-[10px] bg-violet-100 dark:bg-violet-900/40 text-violet-600 dark:text-violet-400">
                  Active
                </span>
              </div>
              {activeAgentSpec?.description && (
                <p className="mt-0.5 text-[11px] text-gray-500 line-clamp-2 dark:text-gray-400" title={activeAgentSpec.description}>
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
                  <span className="min-w-0 line-clamp-2" title={ev.message}>{ev.message}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* ═══════════════════════════════════════════════════════════════ */}
      {/* ── Content area: event log + result sidebar ── */}
      {/* ═══════════════════════════════════════════════════════════════ */}
      <div className="flex min-h-0 flex-1 flex-col overflow-hidden lg:flex-row">
        {/* Event log */}
        <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
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
            <div className="min-h-0 flex-1 overflow-auto p-4">
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
                <div className="space-y-3">
                  {/* Live token deltas render above the structural log. */}
                  <TokenStream text={ctx?.tokenStream ?? ''} running={running} />
                  <EventLog events={events} />
                </div>
              )}
              <div ref={logEnd} />
            </div>
          )}
        </div>

        {/* Right sidebar: Tasks + Results tabs */}
        <div className="flex min-h-0 w-full shrink-0 flex-col border-t border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-950 lg:w-[27rem] lg:border-l lg:border-t-0">
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
          <div className="min-h-0 flex-1 overflow-hidden">
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
    </div>
  );
}

function LiveMetric({
  label,
  value,
  icon,
  tone,
}: {
  label: string;
  value: string;
  icon: ReactNode;
  tone: 'sky' | 'amber' | 'emerald' | 'violet';
}) {
  const toneClass: Record<typeof tone, { box: string; icon: string }> = {
    sky: {
      box: 'border-sky-200 bg-sky-50 text-sky-700 dark:border-sky-900 dark:bg-sky-950/35 dark:text-sky-300',
      icon: 'bg-sky-100 text-sky-600 dark:bg-sky-900/50 dark:text-sky-300',
    },
    amber: {
      box: 'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-200',
      icon: 'bg-amber-100 text-amber-600 dark:bg-amber-900/50 dark:text-amber-300',
    },
    emerald: {
      box: 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/35 dark:text-emerald-300',
      icon: 'bg-emerald-100 text-emerald-600 dark:bg-emerald-900/50 dark:text-emerald-300',
    },
    violet: {
      box: 'border-violet-200 bg-violet-50 text-violet-700 dark:border-violet-900 dark:bg-violet-950/35 dark:text-violet-300',
      icon: 'bg-violet-100 text-violet-600 dark:bg-violet-900/50 dark:text-violet-300',
    },
  };
  const cls = toneClass[tone];
  return (
    <div className={clsx('flex min-h-[3.5rem] min-w-0 items-center gap-2 rounded-lg border px-3 py-2', cls.box)} title={`${label}: ${value}`}>
      <span className={clsx('flex h-7 w-7 shrink-0 items-center justify-center rounded-md', cls.icon)}>
        {icon}
      </span>
      <span className="min-w-0 flex-1">
        <span className="block text-[10px] font-semibold uppercase leading-none opacity-70">{label}</span>
        <span className="mt-1 block break-words text-sm font-bold leading-tight line-clamp-2">
          {value}
        </span>
      </span>
    </div>
  );
}

function StageFact({ label, value, detail }: { label: string; value: string; detail: string }) {
  return (
    <div className="min-w-0 rounded-md bg-gray-50 px-3 py-2 dark:bg-gray-800/60" title={`${label}: ${value}${detail ? ` - ${detail}` : ''}`}>
      <div className="text-[10px] font-semibold uppercase text-gray-400">{label}</div>
      <div className="mt-1 text-sm font-bold text-gray-800 line-clamp-2 dark:text-gray-100">{value}</div>
      {detail && <div className="mt-0.5 text-[11px] text-gray-500 line-clamp-2 dark:text-gray-400">{detail}</div>}
    </div>
  );
}
