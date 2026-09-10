import { useCallback, useContext, useEffect, useState } from 'react';
import { Link, useInRouterContext } from 'react-router-dom';
import clsx from 'clsx';
import { CheckCircle2, FileDiff, Loader2, OctagonAlert, Play, RotateCw, Wrench } from 'lucide-react';
import type { InterruptedRun, LatestRunResponse } from '@/types';
import { AppContext } from '@/App';
import { getInterruptedRuns, getQueries, resumeRun, startRun } from '@/api/client';
import { useToast } from '@/components/ui/Toast';
import { RUN_PROMPT_EVENT, emit, type RunPromptDetail } from '@/components/ui/events';

// ── The screen people read last ──────────────────────────────────────────
//
// It used to say only success/failed, a summary line and three counters. A run
// that hit two defects and fixed both looked identical to one where nothing
// happened — after a stream full of loud red failures. Either the user
// concludes the failures were swallowed, or that the panel is not to be
// trusted, and the repair line is where the run's own resilience becomes
// visible instead.
//
// And it was a dead end. Every run ends with one of four next moves — resume
// what was interrupted, deal with what is blocked, review what is queued, or
// go again — so they are buttons here, not things to remember.

interface ResultPanelProps {
  result: LatestRunResponse | null;
  /**
   * Everything below is optional. The Live view owns the resumable-run list
   * and the prompt; when it passes them the panel uses them, and when it does
   * not the panel fetches the list itself and runs the prompt through the
   * shared RUN_PROMPT_EVENT (falling back to POST /api/runs directly).
   */
  interrupted?: InterruptedRun[];
  onResume?: (id?: string) => void;
  /** The prompt of the run that just finished. Defaults to the newest archive. */
  query?: string;
  onRunAgain?: (query: string) => void;
  /** Review-queue size. Defaults to health.pending from the app context. */
  pending?: number;
  running?: boolean;
}

export default function ResultPanel({
  result,
  interrupted: interruptedProp,
  onResume,
  query: queryProp,
  onRunAgain,
  pending: pendingProp,
  running: runningProp,
}: ResultPanelProps) {
  const ctx = useContext(AppContext);
  const toast = useToast();
  const inRouter = useInRouterContext();
  const running = runningProp ?? ctx?.liveRunning ?? false;
  const pending = pendingProp ?? ctx?.health?.pending ?? 0;
  const hasResult = Boolean(result?.result);

  const [interruptedOwn, setInterruptedOwn] = useState<InterruptedRun[]>([]);
  const [queryOwn, setQueryOwn] = useState('');
  const [busy, setBusy] = useState<'resume' | 'again' | null>(null);
  const interrupted = interruptedProp ?? interruptedOwn;
  const query = queryProp ?? queryOwn;

  // Own the data only when nobody passed it, and only once there is a result
  // to act on. Both are one request each and are re-read when a run ends.
  useEffect(() => {
    if (!hasResult || running) return;
    if (interruptedProp === undefined) {
      getInterruptedRuns().then(setInterruptedOwn).catch(() => setInterruptedOwn([]));
    }
    if (queryProp === undefined) {
      getQueries()
        .then((list) => setQueryOwn(list[0]?.query ?? ''))
        .catch(() => setQueryOwn(''));
    }
  }, [hasResult, running, interruptedProp, queryProp]);

  const beginRun = useCallback(() => {
    ctx?.resetLiveEvents();
    ctx?.setLiveResult(null);
    ctx?.setLiveRunning(true);
  }, [ctx]);

  const handleResume = useCallback(async () => {
    const id = interrupted[0]?.id;
    if (onResume) {
      onResume(id);
      return;
    }
    setBusy('resume');
    try {
      beginRun();
      await resumeRun(id);
      setInterruptedOwn([]);
    } catch (err) {
      ctx?.setLiveRunning(false);
      toast.reportError(err, 'Could not resume the run');
    } finally {
      setBusy(null);
    }
  }, [interrupted, onResume, beginRun, ctx, toast]);

  const handleRunAgain = useCallback(async () => {
    if (!query) return;
    if (onRunAgain) {
      onRunAgain(query);
      return;
    }
    // The Live view, when mounted, takes the prompt and starts it with the
    // teams and specialist the user picked. Otherwise start it plainly.
    if (emit<RunPromptDetail>(RUN_PROMPT_EVENT, { query })) return;
    setBusy('again');
    try {
      beginRun();
      await startRun({ query, skills: ctx?.config?.pinned_skills });
    } catch (err) {
      ctx?.setLiveRunning(false);
      toast.reportError(err, 'Could not start the run');
    } finally {
      setBusy(null);
    }
  }, [query, onRunAgain, beginRun, ctx, toast]);

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
  const actionClass =
    'focus-ring inline-flex h-8 items-center gap-1.5 rounded-md border border-gray-200 bg-white px-2.5 text-xs font-medium text-gray-700 hover:border-brand-300 hover:text-brand-700 disabled:opacity-50 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-200 dark:hover:border-brand-600';
  const blockedHref = '/board?column=blocked';
  const reviewHref = '/review';

  return (
    <div className="h-full space-y-3 overflow-auto p-4">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-bold text-gray-800 dark:text-gray-100">Result</h3>
        <span className={clsx('badge text-[10px]', r.success ? 'badge-success' : 'badge-error')}>
          {r.success ? 'Success' : 'Failed'}
        </span>
      </div>
      {r.summary && <p className="text-sm text-gray-600 dark:text-gray-300">{r.summary}</p>}
      {r.repairs && r.repairs.found > 0 && (
        <p
          className={clsx(
            'flex items-start gap-1.5 rounded-md border px-2 py-1.5 text-xs',
            r.repairs.needs_human > 0
              ? 'border-amber-300 text-amber-700 dark:border-amber-800 dark:text-amber-300'
              : 'border-emerald-300 text-emerald-700 dark:border-emerald-800 dark:text-emerald-300',
          )}
        >
          <Wrench size={13} className="mt-px shrink-0" aria-hidden="true" />
          <span>
            {r.repairs.resolved === r.repairs.found
              ? `Fixed ${r.repairs.found === 1 ? 'the 1 defect' : `all ${r.repairs.found} defects`} without you`
              : `${r.repairs.resolved} of ${r.repairs.found} defects fixed · ${r.repairs.found - r.repairs.resolved} still open`}
            {r.repairs.restaffed > 0 && ` · ${r.repairs.restaffed} reassigned by the project manager`}
          </span>
        </p>
      )}
      <div className="grid grid-cols-3 gap-2">
        <Stat label="Failed" value={String(r.failed_tasks)} bad={r.failed_tasks > 0} />
        <Stat label="Duration" value={seconds} />
        <Stat label="Events" value={String(result.events?.length ?? 0)} />
      </div>

      {/* ── What next ── */}
      <div>
        <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-wide text-gray-400">What next</div>
        <div className="flex flex-wrap gap-1.5" role="group" aria-label="Next steps">
          {interrupted.length > 0 && (
            <button
              type="button"
              className={clsx(actionClass, 'border-brand-300 text-brand-700 dark:border-brand-700 dark:text-brand-300')}
              onClick={handleResume}
              disabled={running || busy !== null}
              title={`Resume ${interrupted[0].id}: ${interrupted[0].done}/${interrupted[0].tasks} done, ${interrupted[0].blocked} blocked`}
            >
              {busy === 'resume' ? <Loader2 size={13} className="animate-spin" aria-hidden="true" /> : <Play size={13} fill="currentColor" aria-hidden="true" />}
              Resume interrupted run
            </button>
          )}
          {r.failed_tasks > 0 &&
            (inRouter ? (
              <Link to={blockedHref} className={actionClass}>
                <OctagonAlert size={13} aria-hidden="true" />
                Open blocked on Board
              </Link>
            ) : (
              <a href={blockedHref} className={actionClass}>
                <OctagonAlert size={13} aria-hidden="true" />
                Open blocked on Board
              </a>
            ))}
          {pending > 0 &&
            (inRouter ? (
              <Link to={reviewHref} className={actionClass}>
                <FileDiff size={13} aria-hidden="true" />
                Review {pending} pending
              </Link>
            ) : (
              <a href={reviewHref} className={actionClass}>
                <FileDiff size={13} aria-hidden="true" />
                Review {pending} pending
              </a>
            ))}
          {query && (
            <button
              type="button"
              className={actionClass}
              onClick={handleRunAgain}
              disabled={running || busy !== null}
              title={query}
            >
              {busy === 'again' ? <Loader2 size={13} className="animate-spin" aria-hidden="true" /> : <RotateCw size={13} aria-hidden="true" />}
              Run again with this prompt
            </button>
          )}
        </div>
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
