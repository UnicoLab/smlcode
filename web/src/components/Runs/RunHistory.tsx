import { useContext, useEffect, useMemo, useState } from 'react';
import {
  Activity,
  AlertTriangle,
  Bot,
  CheckCircle2,
  Clock,
  FileText,
  GitBranch,
  ListChecks,
  Play,
  RefreshCw,
  Search,
  XCircle,
} from 'lucide-react';
import { getQueries, getQuery, getQueryEvents, resumeRun } from '@/api/client';
import type { DynamicComposition, QuerySession, QueryView, RunEvent, RunEventSummary, Task } from '@/types';
import { AppContext } from '@/App';
import EventLog from '@/components/Live/EventLog';
import clsx from 'clsx';

const COLUMN_LABELS: Record<string, string> = {
  to_scope: 'To Scope',
  scoped: 'Scoped',
  ready_to_dev: 'Ready',
  in_progress: 'In Progress',
  in_review: 'In Review',
  blocked: 'Blocked',
  done: 'Done',
};

export default function RunHistory() {
  const ctx = useContext(AppContext);
  const [runs, setRuns] = useState<QuerySession[]>([]);
  const [selectedID, setSelectedID] = useState('');
  const [selected, setSelected] = useState<QueryView | null>(null);
  const [events, setEvents] = useState<RunEvent[]>([]);
  const [eventSummary, setEventSummary] = useState<RunEventSummary | null>(null);
  const [loadingList, setLoadingList] = useState(true);
  const [loadingRun, setLoadingRun] = useState(false);
  const [filter, setFilter] = useState('');
  const [error, setError] = useState('');
  const [resuming, setResuming] = useState('');

  const loadRuns = async () => {
    setLoadingList(true);
    setError('');
    try {
      const list = await getQueries();
      setRuns(list);
      setSelectedID((prev) => prev || list[0]?.id || '');
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load runs');
    } finally {
      setLoadingList(false);
    }
  };

  useEffect(() => {
    loadRuns();
  }, []);

  useEffect(() => {
    if (!selectedID) {
      setSelected(null);
      setEvents([]);
      setEventSummary(null);
      return;
    }
    let cancelled = false;
    setLoadingRun(true);
    setError('');
    setEvents([]);
    setEventSummary(null);
    Promise.all([getQuery(selectedID), getQueryEvents(selectedID, 1500)])
      .then(([q, ev]) => {
        if (cancelled) return;
        setSelected(q);
        setEvents(ev.events || []);
        setEventSummary(ev.summary || null);
      })
      .catch((e) => {
        if (!cancelled) setError(e instanceof Error ? e.message : 'Failed to load run');
      })
      .finally(() => {
        if (!cancelled) setLoadingRun(false);
      });
    return () => {
      cancelled = true;
    };
  }, [selectedID]);

  const filteredRuns = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    if (!needle) return runs;
    return runs.filter((r) =>
      [r.id, r.query, r.summary].some((v) => (v || '').toLowerCase().includes(needle)),
    );
  }, [runs, filter]);

  const boardStats = useMemo(() => summarizeTasks(selected?.board?.tasks || []), [selected]);
  const composition = selected?.composition || latestCompositionEvent(events);
  const compositionError = selected?.composition_error || '';

  const handleResume = async (id: string) => {
    if (!id || ctx?.liveRunning) return;
    setResuming(id);
    setError('');
    try {
      await resumeRun(id);
      ctx?.setLiveEvents([]);
      ctx?.setLiveResult(null);
      ctx?.setLiveRunning(true);
      await loadRuns();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to resume run');
    } finally {
      setResuming('');
    }
  };

  return (
    <div className="h-full flex overflow-hidden">
      <aside className="w-80 shrink-0 border-r border-gray-200 dark:border-gray-800 bg-surface-alt flex flex-col">
        <div className="p-4 border-b border-gray-200 dark:border-gray-800 space-y-3">
          <div className="flex items-center justify-between gap-3">
            <div>
              <h1 className="text-lg font-bold">Runs</h1>
              <p className="text-xs text-gray-500 dark:text-gray-400">{runs.length} archived sessions</p>
            </div>
            <button className="btn-ghost p-2" onClick={loadRuns} title="Refresh runs" disabled={loadingList}>
              <RefreshCw size={16} className={clsx(loadingList && 'animate-spin')} />
            </button>
          </div>
          <label className="relative block">
            <Search size={14} className="absolute left-3 top-2.5 text-gray-400" />
            <input
              className="input pl-9"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="Filter runs"
            />
          </label>
        </div>

        <div className="flex-1 overflow-auto p-2 space-y-1">
          {filteredRuns.map((run) => (
            <button
              key={run.id}
              onClick={() => setSelectedID(run.id)}
              className={clsx(
                'w-full text-left p-3 rounded-lg border transition-colors',
                selectedID === run.id
                  ? 'border-brand-300 bg-brand-50 dark:border-brand-800 dark:bg-brand-950/30'
                  : 'border-transparent hover:bg-gray-100 dark:hover:bg-gray-800',
              )}
            >
              <div className="flex items-center gap-2">
                {run.success ? (
                  <CheckCircle2 size={14} className="text-emerald-500 shrink-0" />
                ) : (
                  <XCircle size={14} className="text-red-500 shrink-0" />
                )}
                <span className="font-mono text-[10px] text-gray-400 truncate">{run.id}</span>
                {run.interrupted && <span className="badge-neutral ml-auto text-[10px]">Interrupted</span>}
              </div>
              <div className="mt-1 text-sm font-medium text-gray-900 dark:text-gray-100 line-clamp-2">
                {run.query || '(empty query)'}
              </div>
              <div className="mt-1 flex items-center gap-2 text-[10px] text-gray-500">
                <Clock size={11} />
                <span>{formatDate(run.updated_at)}</span>
              </div>
            </button>
          ))}
          {!loadingList && filteredRuns.length === 0 && (
            <div className="p-6 text-center text-xs text-gray-400">No archived runs match this filter.</div>
          )}
        </div>
      </aside>

      <main className="flex-1 overflow-auto">
        {error && (
          <div className="m-4 flex items-center gap-2 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300">
            <AlertTriangle size={16} />
            {error}
          </div>
        )}

        {!selected && !loadingRun ? (
          <div className="h-full flex items-center justify-center text-sm text-gray-400">
            Select an archived run.
          </div>
        ) : (
          <div className="p-5 space-y-5">
            <RunHeader
              run={selected}
              loading={loadingRun}
              stats={boardStats}
              resuming={resuming === selected?.id}
              running={!!ctx?.liveRunning}
              onResume={handleResume}
            />
            {compositionError && <CompositionWarning message={compositionError} />}
            {composition && <CompositionPanel composition={composition} />}
            <BoardSnapshot tasks={selected?.board?.tasks || []} stats={boardStats} />
            <SummaryPanel run={selected} />
            <section className="card p-4">
              <div className="flex items-center gap-2 mb-3">
                <Activity size={16} className="text-brand-500" />
                <h2 className="text-sm font-bold">Event Log</h2>
                <span className="badge-neutral ml-auto">{events.length}</span>
              </div>
              <EventLog events={events} summary={eventSummary} />
            </section>
          </div>
        )}
      </main>
    </div>
  );
}

function RunHeader({
  run,
  loading,
  stats,
  resuming,
  running,
  onResume,
}: {
  run: QueryView | null;
  loading: boolean;
  stats: TaskStats;
  resuming: boolean;
  running: boolean;
  onResume: (id: string) => void;
}) {
  return (
    <section className="card p-4">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 text-xs text-gray-500">
            <GitBranch size={14} />
            <span className="font-mono truncate">{run?.id || 'loading'}</span>
            {loading && <RefreshCw size={13} className="animate-spin" />}
          </div>
          <h1 className="mt-2 text-xl font-bold text-gray-950 dark:text-gray-50 break-words">
            {run?.query || 'Loading run'}
          </h1>
          {run?.summary && <p className="mt-2 text-sm text-gray-500 dark:text-gray-400">{run.summary}</p>}
        </div>
        <div className="flex items-center gap-2">
          {run?.interrupted && (
            <button
              onClick={() => onResume(run.id)}
              disabled={running || resuming}
              className="btn-primary gap-1.5 text-xs"
            >
              <Play size={13} fill="currentColor" />
              {resuming ? 'Resuming...' : 'Resume'}
            </button>
          )}
          <span className={run?.success ? 'badge-success' : 'badge-error'}>
            {run?.success ? 'Success' : 'Needs Review'}
          </span>
          <span className="badge-neutral">{stats.total} tasks</span>
        </div>
      </div>
    </section>
  );
}

function CompositionWarning({ message }: { message: string }) {
  return (
    <section className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900 dark:border-amber-900/70 dark:bg-amber-950/30 dark:text-amber-100">
      <div className="flex items-start gap-2">
        <AlertTriangle size={16} className="mt-0.5 shrink-0" />
        <div className="min-w-0">
          <div className="font-semibold">Saved composition unavailable</div>
          <div className="mt-1 break-words text-xs opacity-85">{message}</div>
        </div>
      </div>
    </section>
  );
}

function CompositionPanel({ composition }: { composition: DynamicComposition }) {
  const enabledPhases = (composition.phases || []).filter((p) => p.enabled && p.when !== 'never');
  const fitHints = composition.slm_fit || [];
  return (
    <section className="card p-4 space-y-4">
      <div className="flex items-center gap-2">
        <Bot size={16} className="text-teal-500" />
        <h2 className="text-sm font-bold">Dynamic Composition</h2>
        <span className="badge-brand ml-auto">{enabledPhases.length} phases</span>
      </div>
      {composition.summary && <p className="text-sm text-gray-600 dark:text-gray-300">{composition.summary}</p>}
      {!!fitHints.length && (
        <div>
          <div className="label">SLM Fit</div>
          <div className="flex flex-wrap gap-2">
            {fitHints.map((hint, i) => (
              <span key={`${hint}-${i}`} className="rounded-md border border-amber-200 bg-amber-50 px-2 py-1 text-xs text-amber-900 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-100">
                {hint}
              </span>
            ))}
          </div>
        </div>
      )}
      {!!composition.handoff?.length && (
        <div>
          <div className="label">Handoff</div>
          <div className="grid gap-2 sm:grid-cols-2">
            {composition.handoff.map((h, i) => (
              <div key={`${h}-${i}`} className="rounded-lg border border-gray-200 dark:border-gray-800 px-3 py-2 text-xs text-gray-600 dark:text-gray-300">
                {h}
              </div>
            ))}
          </div>
        </div>
      )}
      <div>
        <div className="label">Pipeline</div>
        <div className="flex flex-wrap gap-2">
          {enabledPhases.map((p) => (
            <span key={p.id} className="inline-flex items-center gap-1.5 rounded-md border border-gray-200 dark:border-gray-800 px-2 py-1 text-xs">
              <span className="font-semibold">{p.id}</span>
              <span className="text-gray-400">@{p.agent || 'default'}</span>
            </span>
          ))}
        </div>
      </div>
      <div className="grid gap-4 lg:grid-cols-[1fr_1fr]">
        <div>
          <div className="label">Execute Loop</div>
          <div className="rounded-lg bg-gray-50 dark:bg-gray-800/50 p-3 text-xs text-gray-600 dark:text-gray-300 space-y-1">
            <div>worker: <span className="font-mono">{composition.execute?.default_role || 'worker'}</span></div>
            <div>reviewer: <span className="font-mono">{composition.execute?.reviewer || 'reviewer'}</span></div>
            <div>corrector: <span className="font-mono">{composition.execute?.corrector || 'corrector'}</span></div>
            <div>waves: <span className="font-mono">{composition.execute?.max_waves || 1}</span></div>
          </div>
        </div>
        <div>
          <div className="label">Team Skills</div>
          <div className="space-y-2">
            {(composition.team || []).map((member) => (
              <div key={member.role} className="rounded-lg bg-gray-50 dark:bg-gray-800/50 p-3">
                <div className="font-mono text-xs text-gray-900 dark:text-gray-100">{member.role}</div>
                <div className="mt-1 flex flex-wrap gap-1">
                  {(member.skills || []).map((skill) => <span key={skill} className="badge-neutral text-[10px]">{skill}</span>)}
                  {!member.skills?.length && <span className="text-[10px] text-gray-400">default skills</span>}
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>
      {!!composition.slots?.length && (
        <div>
          <div className="label">Slots</div>
          <div className="grid gap-2 sm:grid-cols-2">
            {composition.slots.map((slot) => (
              <div key={slot.id} className="rounded-lg border border-gray-200 dark:border-gray-800 px-3 py-2 text-xs text-gray-600 dark:text-gray-300">
                <div className="font-mono text-gray-900 dark:text-gray-100">
                  {slot.id}{slot.agent ? ` @${slot.agent}` : ''}
                </div>
                <div className="mt-1 text-gray-400 dark:text-gray-500">
                  {slot.before ? `before ${slot.before}` : slot.after ? `after ${slot.after}` : slot.replace ? `replace ${slot.replace}` : 'slot'}
                  {slot.persist_to ? ` · ${slot.persist_to}` : ''}
                  {slot.fail_mode ? ` · ${slot.fail_mode}` : ''}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </section>
  );
}

function BoardSnapshot({ tasks, stats }: { tasks: Task[]; stats: TaskStats }) {
  return (
    <section className="card p-4">
      <div className="flex items-center gap-2 mb-3">
        <ListChecks size={16} className="text-violet-500" />
        <h2 className="text-sm font-bold">Board Snapshot</h2>
        <span className="badge-neutral ml-auto">{stats.done}/{stats.total} done</span>
      </div>
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-7">
        {Object.entries(stats.byColumn).map(([col, count]) => (
          <div key={col} className="rounded-lg border border-gray-200 dark:border-gray-800 p-3">
            <div className="text-[10px] uppercase tracking-wider text-gray-400">{COLUMN_LABELS[col] || col}</div>
            <div className="mt-1 text-lg font-bold">{count}</div>
          </div>
        ))}
      </div>
      <div className="mt-4 grid gap-2 lg:grid-cols-2">
        {tasks.slice(0, 12).map((task) => (
          <div key={task.id} className="rounded-lg border border-gray-200 dark:border-gray-800 p-3">
            <div className="flex items-center gap-2">
              <span className="font-mono text-[10px] text-gray-400">{task.id}</span>
              <span className="badge-neutral text-[10px]">{task.column || task.status || 'task'}</span>
              {task.role && <span className="ml-auto font-mono text-[10px] text-brand-500">{task.role}</span>}
            </div>
            <div className="mt-1 text-sm font-medium">{task.title}</div>
            {!!task.files?.length && (
              <div className="mt-2 flex flex-wrap gap-1">
                {task.files.slice(0, 4).map((f) => <span key={f} className="badge-neutral text-[10px]">{f}</span>)}
              </div>
            )}
          </div>
        ))}
      </div>
    </section>
  );
}

function SummaryPanel({ run }: { run: QueryView | null }) {
  const tabs = [
    ['summary_md', 'Summary'],
    ['plan_md', 'Plan'],
    ['tasks_md', 'Tasks'],
  ] as const;
  const [active, setActive] = useState<(typeof tabs)[number][0]>('summary_md');
  const content = run?.[active] || '';
  return (
    <section className="card p-4">
      <div className="flex items-center gap-2 mb-3">
        <FileText size={16} className="text-sky-500" />
        <h2 className="text-sm font-bold">Artifacts</h2>
        <div className="ml-auto flex items-center gap-1 rounded-lg bg-gray-100 p-1 dark:bg-gray-800">
          {tabs.map(([id, label]) => (
            <button
              key={id}
              onClick={() => setActive(id)}
              className={clsx(
                'rounded-md px-2 py-1 text-xs font-medium',
                active === id ? 'bg-white text-gray-900 shadow-sm dark:bg-gray-700 dark:text-gray-100' : 'text-gray-500',
              )}
            >
              {label}
            </button>
          ))}
        </div>
      </div>
      <pre className="max-h-96 overflow-auto whitespace-pre-wrap rounded-lg bg-gray-50 p-3 text-xs text-gray-700 dark:bg-gray-950 dark:text-gray-300">
        {content || 'No artifact saved for this run.'}
      </pre>
    </section>
  );
}

interface TaskStats {
  total: number;
  done: number;
  byColumn: Record<string, number>;
}

function summarizeTasks(tasks: Task[]): TaskStats {
  const byColumn: Record<string, number> = {
    to_scope: 0,
    scoped: 0,
    ready_to_dev: 0,
    in_progress: 0,
    in_review: 0,
    blocked: 0,
    done: 0,
  };
  for (const task of tasks) {
    const col = task.column || task.status || 'to_scope';
    byColumn[col] = (byColumn[col] || 0) + 1;
  }
  return { total: tasks.length, done: byColumn.done || 0, byColumn };
}

function latestCompositionEvent(events: RunEvent[]): DynamicComposition | null {
  for (let i = events.length - 1; i >= 0; i--) {
    const ev = events[i];
    if (ev.kind === 'composition' && ev.data) return ev.data;
  }
  return null;
}

function formatDate(value: string): string {
  if (!value) return 'unknown';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toLocaleString();
}
