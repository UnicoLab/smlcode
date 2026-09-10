import { useState, useEffect, useRef, useContext, useMemo } from 'react';
import {
  AlertTriangle,
  Bot,
  Circle,
  Loader2,
  PanelRightClose,
  PanelRightOpen,
  Play,
  Square,
} from 'lucide-react';
import { AppContext } from '@/App';
import {
  startRun,
  stopRun,
  getAgents,
  getPipeline,
  getComposition,
  previewComposition,
  getTeams,
  getInterruptedRuns,
  resumeRun,
  getSquads,
  getTasks,
} from '@/api/client';
import type {
  AgentSpec,
  PipelineView,
  DynamicComposition,
  InterruptedRun,
  SquadsView,
  Task,
  TeamSpec,
} from '@/types';
import TeamPicker from './TeamPicker';
import NowBar from './NowBar';
import PhaseRail from './PhaseRail';
import type { PhaseState, RailGroup } from './PhaseRail';
import RunSetup from './RunSetup';
import ActivityRail from './ActivityRail';
import type { RailView } from './ActivityRail';
import TeamFloor from './floor/TeamFloor';
import { buildFloor } from './floor/floorModel';
import ResizeHandle from '@/components/ui/ResizeHandle';
import { useToast } from '@/components/ui/Toast';
import { FOCUS_PROMPT_EVENT } from '@/hooks/useKeyboard';
import { usePersistentState, useMediaQuery } from '@/hooks/useUiState';
import clsx from 'clsx';

/**
 * The live run console.
 *
 * Four fixed zones, in priority order:
 *
 *   1. Command bar   — what you type and the button you press. Never scrolls.
 *   2. Phase journey — where the run is, as one track it walks. See PhaseRail.
 *   3. Run setup     — how the run is configured, behind a disclosure that is
 *                      open while idle and closed while running. See RunSetup.
 *   4. Floor + rail  — the rest of the screen. The FLOOR is the teams at their
 *                      tables: who is on which team, who manages them, which
 *                      ticket each holds, who is working right now, what flows
 *                      between teams and where one waits on another (see
 *                      floor/). The RAIL is one column for everything the floor
 *                      does not draw — the log by default, with tasks, fixes,
 *                      files and the result as filters on it (see ActivityRail).
 *
 * The log used to be the centre of this page and five tabs sat beside it. A
 * run is people doing things, and a person watching one for eleven minutes
 * wants to SEE that; the lines are still there, one column to the right.
 */

const PIPELINE_GROUPS: RailGroup[] = [
  { id: 'prepare', label: 'Prepare', phases: ['init', 'skills', 'context', 'explore', 'docs'] },
  { id: 'design', label: 'Design', phases: ['architect', 'clarify', 'plan', 'split'] },
  { id: 'build', label: 'Build', phases: ['coord', 'execute', 'learn'] },
  { id: 'verify', label: 'Verify', phases: ['polish', 'test'] },
  { id: 'finish', label: 'Finish', phases: ['memory', 'done'] },
];

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
  // Persisted: a layout the user arranged has to survive a reload. The old
  // 'live.rail.tab' key held tab names this column no longer has.
  const [railView, setRailView] = usePersistentState<RailView>('live.rail.view', 'log');
  const [railWidth, setRailWidth] = usePersistentState('live.rail.width', RAIL_DEFAULT_PX);
  // The org chart and the board, so the floor can draw the teams, their
  // tickets and which team the running task belongs to. Polled with the run
  // rather than once: a run that assembles teams does so several phases in,
  // long after this component mounted, and the board grows with every wave.
  const [squads, setSquads] = useState<SquadsView | null>(null);
  const [tasks, setTasks] = useState<Task[]>([]);
  // True once the org chart and the board have been asked for at least once:
  // the floor stays empty until then, so the first thing drawn is the real
  // floor and not a crew table that the teams then replace.
  const [boardReady, setBoardReady] = useState(false);
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
  // Teams for THIS run. Empty means the library decides from the request and
  // the workspace; a pick pins them for the run only (the server restores the
  // saved pins when it ends). The library itself is fetched once per visit
  // and again when a run stops, since a run can create a manager or a team.
  const [teamLibrary, setTeamLibrary] = useState<TeamSpec[]>([]);
  const [configPinnedTeams, setConfigPinnedTeams] = useState<string[]>([]);
  const [runTeams, setRunTeams] = useState<string[]>([]);
  const [pipelineView, setPipelineView] = useState<PipelineView | null>(null);
  const [persistedComposition, setPersistedComposition] = useState<DynamicComposition | null>(null);
  const [persistedCompositionError, setPersistedCompositionError] = useState('');
  const [compositionPreview, setCompositionPreview] = useState<DynamicComposition | null>(null);
  const [compositionPreviewFit, setCompositionPreviewFit] = useState<string[]>([]);
  const [interrupted, setInterrupted] = useState<InterruptedRun[]>([]);
  const [previewLoading, setPreviewLoading] = useState(false);
  const promptRef = useRef<HTMLInputElement>(null);

  // `/` focuses the prompt from anywhere in the app.
  useEffect(() => {
    const focus = () => promptRef.current?.focus();
    window.addEventListener(FOCUS_PROMPT_EVENT, focus);
    return () => window.removeEventListener(FOCUS_PROMPT_EVENT, focus);
  }, []);

  // The org chart, refreshed while a run is going and once when it stops.
  //
  // Polled slowly on purpose: it is written once, at charter, and everything
  // downstream reads it unchanged. A fast poll would spend a request every
  // second re-fetching a file that has not moved.
  useEffect(() => {
    let alive = true;
    // Both halves land together: the floor decides between "teams" and
    // "one crew" from the org chart, so tickets arriving a beat before the
    // chart would draw every ticket on one crew table for a frame and then
    // redraw it as teams. One settled pair, one paint.
    const load = () => {
      Promise.allSettled([getSquads(), getTasks()]).then(([chart, board]) => {
        if (!alive) return;
        // No org chart is the normal state, not an error; an empty board is a fact.
        if (chart.status === 'fulfilled') setSquads(chart.value);
        if (board.status === 'fulfilled') setTasks(board.value?.tasks ?? []);
        setBoardReady(true);
      });
    };
    load();
    if (!running) return () => { alive = false; };
    const id = window.setInterval(load, 5000);
    return () => {
      alive = false;
      window.clearInterval(id);
    };
  }, [running]);

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
    if (running) return;
    getTeams()
      .then((lib) => {
        setTeamLibrary(lib.teams ?? []);
        setConfigPinnedTeams(lib.pinned ?? []);
      })
      .catch(() => {
        /* no library is fine — the picker simply does not render */
      });
  }, [running]);

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
      previewComposition(q, runTeams)
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
  }, [query, running, specialist, runTeams, ctx?.config?.dynamic_pipeline]);

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
        teams: runTeams.length > 0 ? runTeams : undefined,
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

  // The floor: everything the stage draws, derived once per change.
  const floor = useMemo(
    () =>
      boardReady
        ? buildFloor({ squads, tasks, events, composition: shownComposition, running })
        : buildFloor({ squads: null, tasks: [], events: [], composition: null, running: false }),
    [boardReady, squads, tasks, events, shownComposition, running],
  );

  // Picking a ticket on the floor opens it in the rail's task list.
  const focusTicket = () => {
    setRailView('tasks');
    setRailOpen(true);
  };

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
            <TeamPicker
              teams={teamLibrary}
              configPinned={configPinnedTeams}
              value={runTeams}
              disabled={running || !!specialist}
              onChange={setRunTeams}
            />
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

      {/* ── 4. Floor + rail ────────────────────────────────────────── */}
      <div className="flex min-h-0 flex-1 overflow-hidden">
        <main className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
          <div className="relative min-h-0 flex-1">
            <TeamFloor floor={floor} running={running} events={events} onTicket={focusTicket} />
          </div>
          {/* What is happening RIGHT NOW, pinned under the floor as a ticker.
              On a local 30B the next log line can be four minutes out, and a
              clock that keeps moving is the difference between "thinking" and
              "hung". */}
          <NowBar events={events} running={running} squads={squads} />
        </main>

        {/* The rail is a column at ≥1024px and a full-height OVERLAY below it.
            An overlay rather than a stacked block: on a phone the floor and the
            log both want the whole screen, and splitting it gives neither
            enough to be readable. */}
        {railOpen && (
          <>
            <button
              type="button"
              aria-label="Close side panel"
              onClick={() => setRailOpen(false)}
              className="fixed inset-0 z-30 bg-black/30 lg:hidden"
            />
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
              style={isWide ? { width: railWidth, maxWidth: '60%', minWidth: RAIL_MIN_PX } : undefined}
              className={clsx(
                'z-40 flex min-h-0 flex-col border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-950',
                'fixed inset-y-0 right-0 w-[min(28rem,92vw)] border-l shadow-2xl',
                'lg:static lg:shadow-none',
              )}
            >
              <ActivityRail
                events={events}
                running={running}
                result={result}
                tokenStream={ctx?.tokenStream ?? ''}
                view={railView}
                onView={setRailView}
                overlay={!isWide}
                onClose={() => setRailOpen(false)}
              />
            </aside>
          </>
        )}
      </div>
    </div>
  );
}
