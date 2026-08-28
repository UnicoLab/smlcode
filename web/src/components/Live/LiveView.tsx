import { useState, useEffect, useRef, useContext, useMemo } from 'react';
import {
  AlertTriangle,
  Bot,
  CheckCircle2,
  Circle,
  FolderTree,
  ListTodo,
  Loader2,
  PanelRightClose,
  PanelRightOpen,
  Play,
  Square,
  Users,
  Wrench,
  XCircle,
} from 'lucide-react';
import { AppContext } from '@/App';
import {
  startRun,
  stopRun,
  getAgents,
  getPipeline,
  getComposition,
  previewComposition,
  getInterruptedRuns,
  resumeRun,
} from '@/api/client';
import type {
  AgentSpec,
  PipelineView,
  DynamicComposition,
  InterruptedRun,
  LatestRunResponse,
} from '@/types';
import EventLog from './EventLog';
import LiveTaskPanel from './LiveTaskPanel';
import LiveFileInspector from './LiveFileInspector';
import LiveFeedback from './LiveFeedback';
import CalibrationBanner from './CalibrationBanner';
import TokenStream from './TokenStream';
import SquadPanel from './SquadPanel';
import RecoveryPanel from './RecoveryPanel';
import { buildRecovery, recoveryTally } from './recovery';
import PhaseRail from './PhaseRail';
import type { PhaseState, RailGroup } from './PhaseRail';
import RunSetup from './RunSetup';
import ResizeHandle from '@/components/ui/ResizeHandle';
import { useToast } from '@/components/ui/Toast';
import { FOCUS_PROMPT_EVENT } from '@/hooks/useKeyboard';
import { usePersistentState, useMediaQuery, useStickToBottom } from '@/hooks/useUiState';
import clsx from 'clsx';

/**
 * The live run console.
 *
 * The layout is four fixed zones, in priority order, and the ordering is the
 * whole design:
 *
 *   1. Command bar   — what you type and the button you press. Always one row
 *                      on desktop; wraps on narrow screens. Never scrolls away.
 *   2. Phase rail    — where the run is, in ~44px. See PhaseRail.
 *   3. Run setup     — how the run is configured, behind a disclosure that is
 *                      open while idle and closed while running. See RunSetup.
 *   4. Stream + rail — everything left over, which is most of the screen.
 *
 * What changed and why: the previous version stacked six always-expanded
 * panels above the log — a metrics grid, a progress card, a stage card, an
 * agent-activity card, a composition panel and an active-agent panel. Each was
 * individually reasonable and together they pushed the event stream, the one
 * thing a page called "Live" exists to show, off a 900px viewport entirely.
 * Every one of them survives here; they just had to stop competing with the
 * stream for the same pixels at the same moment.
 */

const PIPELINE_GROUPS: RailGroup[] = [
  { id: 'prepare', label: 'Prepare', phases: ['init', 'skills', 'context', 'explore', 'docs'] },
  { id: 'design', label: 'Design', phases: ['architect', 'clarify', 'plan', 'split'] },
  { id: 'build', label: 'Build', phases: ['coord', 'execute', 'learn'] },
  { id: 'verify', label: 'Verify', phases: ['polish', 'test'] },
  { id: 'finish', label: 'Finish', phases: ['memory', 'done'] },
];

type RailTab = 'tasks' | 'teams' | 'fixes' | 'files' | 'result';

/** Where the side rail becomes a column instead of an overlay. */
const WIDE_VIEWPORT = '(min-width: 1024px)';

/** Side-rail width bounds, in px. The user's choice is persisted between them. */
const RAIL_DEFAULT_PX = 384;
const RAIL_MIN_PX = 280;
const RAIL_MAX_PX = 720;

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
  // Persisted: a layout the user arranged has to survive a reload.
  const [railTab, setRailTab] = usePersistentState<RailTab>('live.rail.tab', 'tasks');
  // The self-healing tally rides on the tab, so a user who never opens it still
  // sees that something was found and something was done about it.
  const fixes = useMemo(() => recoveryTally(buildRecovery(events)), [events]);
  const fixesBadge =
    fixes.needsYou > 0
      ? String(fixes.needsYou)
      : fixes.healing > 0
        ? String(fixes.healing)
        : fixes.resolved > 0
          ? String(fixes.resolved)
          : undefined;
  const [railWidth, setRailWidth] = usePersistentState('live.rail.width', RAIL_DEFAULT_PX);
  // The rail is a column on a wide viewport and a full-height OVERLAY below it,
  // so its default cannot be the same on both: opening it by default on a phone
  // means the first thing a user sees is the task drawer covering the console
  // they came for. Seeded from the breakpoint, then owned by the user — and
  // reset when the viewport actually CROSSES it, so a window dragged narrow does
  // not strand an overlay nobody asked to open. Not persisted for that reason:
  // a stored `true` restored on a phone is the same trap.
  const isWide = useMediaQuery(WIDE_VIEWPORT);
  const [railOpen, setRailOpen] = useState(isWide);
  useEffect(() => {
    setRailOpen(isWide);
  }, [isWide]);
  const [agents, setAgents] = useState<AgentSpec[]>([]);
  const [specialist, setSpecialist] = useState('');
  const [pipelineView, setPipelineView] = useState<PipelineView | null>(null);
  const [persistedComposition, setPersistedComposition] = useState<DynamicComposition | null>(null);
  const [persistedCompositionError, setPersistedCompositionError] = useState('');
  const [compositionPreview, setCompositionPreview] = useState<DynamicComposition | null>(null);
  const [compositionPreviewFit, setCompositionPreviewFit] = useState<string[]>([]);
  const [interrupted, setInterrupted] = useState<InterruptedRun[]>([]);
  const [previewLoading, setPreviewLoading] = useState(false);
  const promptRef = useRef<HTMLInputElement>(null);
  // Follows the stream only while the user is already AT the bottom. An
  // unconditional scroll yanked the log back down every flush, which made
  // reading anything mid-run impossible.
  const logRef = useStickToBottom<HTMLDivElement>(events, true);

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

  useEffect(() => {
    getAgents().then(setAgents).catch(() => {});
  }, []);

  useEffect(() => {
    getPipeline().then(setPipelineView).catch(() => {});
    getComposition()
      .then((r) => {
        setPersistedComposition(r.composition || null);
        setPersistedCompositionError(r.composition_error || '');
      })
      .catch((e) => {
        setPersistedComposition(null);
        setPersistedCompositionError(
          e instanceof Error ? e.message : 'Unable to load saved composition',
        );
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

  const clearRunPanels = () => {
    setResult(null);
    setPersistedComposition(null);
    setPersistedCompositionError('');
    setCompositionPreview(null);
    setCompositionPreviewFit([]);
  };

  const handleRun = async () => {
    const q = query.trim();
    if (!q || running) return;
    resetEvents();
    clearRunPanels();
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
    clearRunPanels();
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

  // ── Derived run state ──

  const dynamicComposition = useMemo<DynamicComposition | null>(() => {
    for (let i = events.length - 1; i >= 0; i--) {
      const ev = events[i];
      if (ev.kind === 'composition' && ev.data) return ev.data;
    }
    return running ? null : persistedComposition;
  }, [events, persistedComposition, running]);

  const shownComposition = dynamicComposition || (!running ? compositionPreview : null);
  const shownCompositionMode: '' | 'runtime' | 'preview' = dynamicComposition
    ? 'runtime'
    : compositionPreview
      ? 'preview'
      : '';
  const shownCompositionFit =
    shownComposition?.slm_fit || (shownCompositionMode === 'preview' ? compositionPreviewFit : []);
  const shownCompositionPhases = useMemo(() => shownComposition?.phases || [], [shownComposition]);

  const dynamicPhaseOrder = useMemo(() => {
    if (!shownCompositionPhases.length) return null;
    return shownCompositionPhases
      .filter((p) => p.enabled && p.when !== 'never')
      .map((p) => p.id)
      .filter(Boolean);
  }, [shownCompositionPhases]);

  const groups = useMemo<RailGroup[]>(() => {
    const keep = dynamicPhaseOrder ? new Set(dynamicPhaseOrder) : null;
    if (pipelineView?.config?.groups?.length) {
      return pipelineView.config.groups
        .map((g) => ({
          id: g.id,
          label: g.label,
          phases: keep ? g.steps.filter((p) => keep.has(p)) : g.steps,
        }))
        .filter((g) => g.phases.length > 0);
    }
    if (keep) {
      return PIPELINE_GROUPS.map((g) => ({
        ...g,
        phases: g.phases.filter((p) => keep.has(p)),
      })).filter((g) => g.phases.length > 0);
    }
    return PIPELINE_GROUPS;
  }, [pipelineView, dynamicPhaseOrder]);

  const allPhases = useMemo(
    () => dynamicPhaseOrder || groups.flatMap((g) => g.phases),
    [groups, dynamicPhaseOrder],
  );

  const seenPhases = useMemo(() => {
    const set = new Set<string>();
    for (const e of events) {
      if (e.phase) set.add(e.phase);
    }
    return set;
  }, [events]);

  const activePhase = useMemo(() => {
    for (let i = events.length - 1; i >= 0; i--) {
      if (events[i].phase) return events[i].phase;
    }
    return null;
  }, [events]);

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
    if (activePhase && !allPhases.includes(activePhase)) {
      map[activePhase] = 'active';
    }
    return map;
  }, [allPhases, activePhase, seenPhases]);

  const activeAgentId = useMemo(() => {
    for (let i = events.length - 1; i >= 0; i--) {
      if (events[i].agent) return events[i].agent;
    }
    return null;
  }, [events]);

  const activeAgentSpec = useMemo(
    () => (activeAgentId ? agents.find((a) => a.id === activeAgentId) || null : null),
    [activeAgentId, agents],
  );

  const selectedAgentSpec = useMemo(
    () => (specialist ? agents.find((a) => a.id === specialist) || null : null),
    [agents, specialist],
  );

  const taskCount = useMemo(() => {
    const ids = new Set<string>();
    for (const e of events) {
      if (e.task_id) ids.add(e.task_id);
    }
    return ids.size;
  }, [events]);

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden bg-gray-50/70 dark:bg-gray-950">
      {/* ── 1. Command bar ─────────────────────────────────────────── */}
      <div className="shrink-0 border-b border-gray-200 bg-white px-3 py-2.5 dark:border-gray-800 dark:bg-gray-950 sm:px-4">
        <div className="flex flex-col gap-2 lg:flex-row lg:items-center">
          <span
            className={clsx(
              'inline-flex h-9 shrink-0 items-center gap-1.5 rounded-md border px-2.5 text-xs font-semibold',
              running
                ? 'border-brand-300 bg-brand-50 text-brand-700 dark:border-brand-700 dark:bg-brand-950/50 dark:text-brand-300'
                : 'border-gray-200 bg-gray-50 text-gray-500 dark:border-gray-800 dark:bg-gray-900 dark:text-gray-400',
            )}
          >
            {running ? <Loader2 size={13} className="animate-spin" /> : <Circle size={11} />}
            {running ? 'Running' : 'Ready'}
          </span>

          <input
            ref={promptRef}
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && handleRun()}
            placeholder="Plan, code, test, or inspect a change   ( / to focus )"
            aria-label="Run prompt"
            className="input focus-ring h-9 min-w-0 flex-1 text-sm"
            disabled={running}
          />

          <div className="flex shrink-0 items-center gap-2">
            {agents.length > 0 && (
              <select
                value={specialist}
                onChange={(e) => setSpecialist(e.target.value)}
                className="input h-9 min-w-0 flex-1 text-xs sm:w-44 lg:w-52"
                disabled={running}
                aria-label="Agent"
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
              <button onClick={handleStop} className="btn-danger h-9 shrink-0 gap-1.5 px-4 text-sm">
                <Square size={14} fill="currentColor" />
                Stop
              </button>
            ) : (
              <button
                onClick={handleRun}
                disabled={!query.trim()}
                className="btn-primary h-9 shrink-0 gap-1.5 px-5 text-sm"
              >
                <Play size={14} fill="currentColor" />
                Run
              </button>
            )}
            <button
              onClick={() => setRailOpen((v) => !v)}
              className="focus-ring flex h-9 w-9 shrink-0 items-center justify-center rounded-md border border-gray-200 text-gray-500 transition-colors hover:bg-gray-50 hover:text-gray-700 dark:border-gray-700 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-200"
              aria-label={railOpen ? 'Hide side panel' : 'Show side panel'}
              title={railOpen ? 'Hide side panel' : 'Show side panel'}
            >
              {railOpen ? <PanelRightClose size={16} /> : <PanelRightOpen size={16} />}
            </button>
          </div>
        </div>

        {/* Environment facts + the active agent share one quiet strip. On a
            narrow screen the environment tags drop away first: mid-run, which
            agent is talking matters more than which stack is configured. */}
        <div className="mt-2 flex flex-wrap items-center gap-1.5 text-[10px]">
          {running && activeAgentId && (
            <span
              className="inline-flex max-w-full items-center gap-1.5 rounded-md bg-brand-50 px-2 py-1 font-semibold text-brand-700 dark:bg-brand-950/50 dark:text-brand-300"
              title={activeAgentSpec?.description || activeAgentId}
            >
              <Bot size={11} className="shrink-0 animate-pulse" />
              <span className="truncate">{activeAgentSpec?.title || activeAgentId}</span>
            </span>
          )}
          {previewLoading && (
            <span className="inline-flex items-center gap-1.5 text-gray-400">
              <Loader2 size={11} className="animate-spin" />
              previewing pipeline
            </span>
          )}
          <span className="hidden items-center gap-1.5 sm:inline-flex">
            {ctx?.config?.dynamic_pipeline !== undefined && (
              <span className={ctx.config.dynamic_pipeline ? 'badge-brand' : 'badge-neutral'}>
                {ctx.config.dynamic_pipeline ? 'dynamic' : 'static'}
              </span>
            )}
            {ctx?.config?.active_stack && (
              <span className="badge-neutral">{ctx.config.active_stack}</span>
            )}
            {ctx?.config?.model && (
              <span
                className="max-w-[16rem] truncate rounded-md border border-gray-200 bg-gray-50 px-2 py-0.5 font-mono text-gray-500 dark:border-gray-800 dark:bg-gray-900 dark:text-gray-400"
                title={ctx.config.model}
              >
                {ctx.config.model}
              </span>
            )}
          </span>
          <span className="ml-auto shrink-0 font-mono tabular-nums text-gray-400">
            {events.length} events · {taskCount} tasks
          </span>
        </div>

        {selectedAgentSpec && (
          <div
            className="mt-2 flex items-center gap-2 rounded-md border border-brand-200 bg-brand-50/60 px-2.5 py-1.5 text-[11px] dark:border-brand-900/70 dark:bg-brand-950/25"
            title={selectedAgentSpec.description}
          >
            <span className="badge-brand shrink-0 text-[10px]">specialist</span>
            <span className="truncate font-semibold text-brand-800 dark:text-brand-200">
              {selectedAgentSpec.title || selectedAgentSpec.id}
            </span>
            <span className="ml-auto shrink-0 truncate font-mono text-brand-500">
              {selectedAgentSpec.effective_model || selectedAgentSpec.model || 'inherit'}
            </span>
          </div>
        )}
      </div>

      {/* ── Resumable run ──────────────────────────────────────────── */}
      {!running && interrupted.length > 0 && (
        <div className="flex shrink-0 flex-col gap-2 border-b border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-900 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-100 sm:flex-row sm:items-center sm:px-4">
          <AlertTriangle size={14} className="shrink-0" />
          <div className="min-w-0 flex-1">
            <span className="font-semibold">Interrupted run</span>{' '}
            <span className="font-mono opacity-70">{interrupted[0].id}</span>
            <span className="ml-2 opacity-80">
              {interrupted[0].done}/{interrupted[0].tasks} done · {interrupted[0].blocked} blocked
            </span>
            <p className="truncate opacity-70" title={interrupted[0].query}>
              {interrupted[0].query}
            </p>
          </div>
          <button
            onClick={() => handleResume(interrupted[0].id)}
            className="btn-primary h-8 shrink-0 gap-1.5 text-xs"
          >
            <Play size={13} fill="currentColor" />
            Resume
          </button>
        </div>
      )}

      {/* ── 2. Phase rail ──────────────────────────────────────────── */}
      <PhaseRail
        groups={groups}
        phaseState={phaseStateMap}
        activePhase={activePhase ?? null}
        running={running}
      />

      {/* ── 3. Run setup (disclosure) ──────────────────────────────── */}
      <RunSetup
        composition={shownComposition}
        mode={shownCompositionMode}
        fit={shownCompositionFit}
        agents={agents}
        running={running}
        compositionError={running ? '' : persistedCompositionError}
      />

      {/* ── 4. Stream + rail ───────────────────────────────────────── */}
      <div className="flex min-h-0 flex-1 overflow-hidden">
        <main className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
          <div ref={logRef} className="min-h-0 flex-1 overflow-auto px-3 py-3 sm:px-4">
            {events.length === 0 ? (
              // Keyed on EVENTS alone, not on `result`. A finished run leaves a
              // result behind, so the old `!result` clause meant that reopening
              // Studio after any previous run showed a bare "waiting for
              // events" line instead of the empty state — on exactly the visit
              // where a first-time reader most needs to be told what to do.
              // The result itself is one tab away in the rail.
              <EmptyState hasPreviousRun={!!result?.result} />
            ) : (
              <div className="mx-auto max-w-[110ch] space-y-3">
                {/* Calibration runs before the first token of a run, so its
                    progress sits above the stream that replaces it. */}
                <CalibrationBanner events={events} />
                <TokenStream text={ctx?.tokenStream ?? ''} running={running} />
                <EventLog events={events} />
              </div>
            )}
          </div>

          {/* Feedback docks to the bottom of the stream, where a reply belongs —
              next to what you are replying to, not in a header card above it. */}
          <div className="shrink-0 border-t border-gray-200 bg-white px-3 py-2 dark:border-gray-800 dark:bg-gray-950 sm:px-4">
            <LiveFeedback compact />
          </div>
        </main>

        {/* The rail is a column at ≥1024px and a full-height overlay below it.
            An overlay rather than a stacked block: on a phone the stream and the
            task list both want the whole screen, and splitting it gives neither
            enough to be readable. */}
        {railOpen && (
          <>
            <button
              type="button"
              aria-label="Close side panel"
              onClick={() => setRailOpen(false)}
              className="fixed inset-0 z-30 bg-black/30 lg:hidden"
            />
            {/* The divider exists only on a wide viewport: as an overlay the
                rail has no neighbour to steal width from, so a horizontal drag
                would mean nothing. */}
            {isWide && (
              <ResizeHandle
                size={railWidth}
                onResize={setRailWidth}
                min={RAIL_MIN_PX}
                max={RAIL_MAX_PX}
                invert
                label="Resize the side panel"
              />
            )}
            <aside
              // maxWidth is a second clamp, in CSS rather than in the handle: a
              // window narrowed AFTER the width was stored must never leave the
              // stream with no room, even when the stored value is wider than
              // the window itself.
              style={isWide ? { width: railWidth, maxWidth: '60%', minWidth: RAIL_MIN_PX } : undefined}
              className={clsx(
                'z-40 flex min-h-0 flex-col border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-950',
                'fixed inset-y-0 right-0 w-[min(26rem,90vw)] border-l shadow-2xl',
                'lg:static lg:shadow-none',
              )}
            >
              <div className="flex shrink-0 border-b border-gray-200 dark:border-gray-800">
                <RailTabButton
                  active={railTab === 'tasks'}
                  onClick={() => setRailTab('tasks')}
                  icon={<ListTodo size={14} />}
                  label="Tasks"
                />
                {/* Teams sits next to Tasks because with two squads running,
                    "which half is behind" is the question the task list cannot
                    answer — the aggregate count hides one squad finishing while
                    the other sits blocked. The tab renders nothing on a
                    single-stream run, which is most of them. */}
                <RailTabButton
                  active={railTab === 'teams'}
                  onClick={() => setRailTab('teams')}
                  icon={<Users size={14} />}
                  label="Teams"
                />
                {/* What the harness fixed by itself. The failures are red and
                    loud and the recovery is a handful of plain lines in a log
                    of fifty, so a user watching sees a run going wrong with no
                    evidence anything is handling it — the worst possible
                    reading of a system that is fixing itself. */}
                <RailTabButton
                  active={railTab === 'fixes'}
                  onClick={() => setRailTab('fixes')}
                  icon={<Wrench size={14} className={fixes.needsYou > 0 ? 'text-amber-500' : undefined} />}
                  label="Fixes"
                  badge={fixesBadge}
                />
                <RailTabButton
                  active={railTab === 'files'}
                  onClick={() => setRailTab('files')}
                  icon={<FolderTree size={14} />}
                  label="Files"
                />
                <RailTabButton
                  active={railTab === 'result'}
                  onClick={() => setRailTab('result')}
                  icon={
                    result?.result ? (
                      result.result.success ? (
                        <CheckCircle2 size={14} className="text-emerald-500" />
                      ) : (
                        <XCircle size={14} className="text-rose-500" />
                      )
                    ) : (
                      <CheckCircle2 size={14} />
                    )
                  }
                  label="Result"
                />
                <button
                  onClick={() => setRailOpen(false)}
                  className="focus-ring shrink-0 px-3 text-gray-400 hover:text-gray-600 lg:hidden"
                  aria-label="Close side panel"
                >
                  <PanelRightClose size={16} />
                </button>
              </div>

              <div className="min-h-0 flex-1 overflow-hidden">
                {railTab === 'tasks' && <LiveTaskPanel />}
                {railTab === 'teams' && (
                  <div className="h-full overflow-auto p-3">
                    <SquadPanel refreshKey={events.length} />
                  </div>
                )}
                {railTab === 'fixes' && (
                  <div className="h-full overflow-auto">
                    <RecoveryPanel events={events} />
                  </div>
                )}
                {railTab === 'files' && <LiveFileInspector events={events} running={running} />}
                {railTab === 'result' && <ResultPanel result={result} />}
              </div>
            </aside>
          </>
        )}

      </div>
    </div>
  );
}

function RailTabButton({
  active,
  onClick,
  icon,
  label,
  badge,
}: {
  active: boolean;
  onClick: () => void;
  badge?: string;
  icon: React.ReactNode;
  label: string;
}) {
  return (
    <button
      onClick={onClick}
      aria-current={active ? 'page' : undefined}
      className={clsx(
        'focus-ring flex flex-1 items-center justify-center gap-1.5 border-b-2 py-2.5 text-xs font-semibold transition-colors',
        active
          ? 'border-brand-500 text-brand-600 dark:text-brand-400'
          : 'border-transparent text-gray-400 hover:text-gray-600 dark:hover:text-gray-300',
      )}
    >
      {icon}
      {label}
      {badge && (
        <span className="badge-neutral shrink-0 text-[10px] leading-none">{badge}</span>
      )}
    </button>
  );
}

function EmptyState({ hasPreviousRun }: { hasPreviousRun: boolean }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 text-center">
      <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-brand-50 dark:bg-brand-950/40">
        <Play size={24} className="text-brand-500" />
      </div>
      <div className="max-w-sm">
        <h2 className="text-base font-semibold text-gray-700 dark:text-gray-200">
          Nothing running yet
        </h2>
        <p className="mt-1 text-sm text-gray-400">
          Describe a change above and press Run. Phases, agent activity and file
          writes stream here as they happen.
        </p>
        {hasPreviousRun && (
          <p className="mt-2 text-xs text-gray-400">
            The previous run&rsquo;s summary is under <span className="font-semibold">Result</span>.
          </p>
        )}
      </div>
    </div>
  );
}

function ResultPanel({ result }: { result: LatestRunResponse | null }) {
  if (!result?.result) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2 px-6 text-center text-gray-400">
        <CheckCircle2 size={28} className="opacity-40" />
        <p className="text-sm">No result yet</p>
        <p className="text-xs">A summary appears here when the run finishes.</p>
      </div>
    );
  }
  const r = result.result;
  const seconds = r.duration > 1e9 ? `${(r.duration / 1e9).toFixed(1)}s` : `${(r.duration / 1e6).toFixed(0)}ms`;
  return (
    <div className="h-full space-y-3 overflow-auto p-4">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-bold text-gray-800 dark:text-gray-100">Result</h3>
        <span className={clsx('badge text-[10px]', r.success ? 'badge-success' : 'badge-error')}>
          {r.success ? 'Success' : 'Failed'}
        </span>
      </div>
      {r.summary && <p className="text-sm text-gray-600 dark:text-gray-300">{r.summary}</p>}
      <div className="grid grid-cols-3 gap-2">
        <Stat label="Failed" value={String(r.failed_tasks)} bad={r.failed_tasks > 0} />
        <Stat label="Duration" value={seconds} />
        <Stat label="Events" value={String(result.events?.length ?? 0)} />
      </div>
    </div>
  );
}

function Stat({ label, value, bad }: { label: string; value: string; bad?: boolean }) {
  return (
    <div className="rounded-md border border-gray-200 bg-gray-50 px-2 py-1.5 dark:border-gray-800 dark:bg-gray-900">
      <div className="text-[10px] font-semibold uppercase tracking-wide text-gray-400">{label}</div>
      <div
        className={clsx(
          'mt-0.5 font-mono text-sm font-bold tabular-nums',
          bad ? 'text-rose-500' : 'text-gray-800 dark:text-gray-100',
        )}
      >
        {value}
      </div>
    </div>
  );
}
