import { useCallback, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  ArrowLeftCircle,
  Check,
  CheckCircle2,
  Circle,
  History,
  Loader2,
  MessageSquareText,
  RotateCcw,
  ShieldCheck,
  Users,
  XCircle,
} from 'lucide-react';
import clsx from 'clsx';
import { ApiError, patchTask, retryTask } from '@/api/client';
import type { Task, TaskCriterion } from '@/types';
import { useToast } from '@/components/ui/Toast';
import { steerTask } from '@/components/ui/events';

// ── The story of a stuck task, and what to do about it ──
//
// A blocked card used to render `task.error` — the last line — and stop. The
// attempt log is the actual story ("attempt 1 failed because the test file
// was not found; attempt 2 failed because …"), the review verdict is what the
// gate said, and the criteria are what it was judged against. Shown together,
// with the four things a person can do next, on the board card AND in the
// live rail so the fix is one click from wherever the failure was noticed.

/** Blocked or failed by status or by column. */
export function isStuck(task: Task): boolean {
  return (
    task.status === 'blocked' ||
    task.status === 'failed' ||
    task.column === 'blocked' ||
    task.column === 'failed'
  );
}

// ── Attempts timeline ──

/** "attempt 2 failed because X" → { n: 2, reason: "X" }; anything else keeps the whole line. */
export function parseAttempt(line: string, index: number): { n: number; reason: string } {
  const m = /^attempt\s+(\d+)\s+failed(?:\s+because\s*:?\s*|\s*[:—-]\s*)?(.*)$/i.exec(line.trim());
  if (m) return { n: Number.parseInt(m[1], 10), reason: m[2] || 'no reason recorded' };
  return { n: index + 1, reason: line };
}

export function AttemptsTimeline({ task, compact = false }: { task: Task; compact?: boolean }) {
  const log = task.attempt_log ?? [];
  if (log.length === 0) return null;
  return (
    <div data-testid="attempts-timeline">
      <div className={clsx('mb-1 flex items-center gap-1 font-semibold uppercase text-gray-400', compact ? 'text-[9px]' : 'text-[10px]')}>
        <History size={compact ? 9 : 10} aria-hidden="true" />
        Attempts
        <span className="font-mono normal-case">
          · {log.length} failed{task.gate_retries ? ` · ${task.gate_retries} gate ${task.gate_retries === 1 ? 'retry' : 'retries'}` : ''}
        </span>
      </div>
      <ol className="relative ml-1.5 space-y-1.5 border-l border-red-200 pl-3 dark:border-red-900/60">
        {log.map((line, i) => {
          const a = parseAttempt(line, i);
          return (
            <li key={`${i}-${line.slice(0, 24)}`} className={clsx('relative', compact ? 'text-[10px]' : 'text-xs')}>
              <span
                aria-hidden="true"
                className="absolute -left-[0.95rem] top-1 h-2 w-2 rounded-full border border-red-400 bg-white dark:bg-gray-900"
              />
              <span className="font-mono text-[10px] text-red-600 dark:text-red-400">#{a.n}</span>{' '}
              <span className="break-words text-gray-700 dark:text-gray-300">{a.reason}</span>
            </li>
          );
        })}
      </ol>
    </div>
  );
}

// ── Review verdict ──

export function ReviewVerdict({ review, compact = false }: { review?: string; compact?: boolean }) {
  if (!review) return null;
  return (
    <div>
      <div className={clsx('mb-1 flex items-center gap-1 font-semibold uppercase text-gray-400', compact ? 'text-[9px]' : 'text-[10px]')}>
        <ShieldCheck size={compact ? 9 : 10} aria-hidden="true" />
        Review verdict
      </div>
      <div
        className={clsx(
          'whitespace-pre-wrap break-words rounded-md border border-amber-200 bg-amber-50 px-2 py-1.5 text-amber-900',
          'dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-100',
          compact ? 'max-h-24 overflow-y-auto text-[10px]' : 'text-xs',
        )}
      >
        {review}
      </div>
    </div>
  );
}

// ── Criteria checklist ──

export function CriteriaChecklist({ criteria, compact = false }: { criteria?: TaskCriterion[]; compact?: boolean }) {
  if (!criteria || criteria.length === 0) return null;
  return (
    <div>
      <div className={clsx('mb-1 flex items-center gap-1 font-semibold uppercase text-gray-400', compact ? 'text-[9px]' : 'text-[10px]')}>
        <Check size={compact ? 9 : 10} aria-hidden="true" />
        Criteria
      </div>
      <ul className="space-y-1">
        {criteria.map((c, i) => {
          const state = c.met === true ? 'met' : c.met === false ? 'unmet' : 'open';
          return (
            <li
              key={c.id || i}
              className={clsx('flex items-start gap-1.5 text-gray-700 dark:text-gray-300', compact ? 'text-[10px]' : 'text-xs')}
            >
              {state === 'met' && <CheckCircle2 size={12} className="mt-0.5 shrink-0 text-emerald-500" aria-hidden="true" />}
              {state === 'unmet' && <XCircle size={12} className="mt-0.5 shrink-0 text-red-500" aria-hidden="true" />}
              {state === 'open' && <Circle size={12} className="mt-0.5 shrink-0 text-gray-300 dark:text-gray-600" aria-hidden="true" />}
              <span className="sr-only">{state === 'met' ? 'met:' : state === 'unmet' ? 'not met:' : 'not yet checked:'}</span>
              <span className="min-w-0 break-words">
                {c.id && <span className="mr-1 font-mono text-[10px] text-gray-400">{c.id}</span>}
                {c.text}
                {c.priority && c.priority !== 'must' && (
                  <span className="ml-1 rounded bg-gray-100 px-1 text-[9px] uppercase text-gray-500 dark:bg-gray-800">
                    {c.priority}
                  </span>
                )}
              </span>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

// ── Actions ──

/**
 * useTaskRecovery wires the four next steps and reports each outcome through
 * the toast. Retry degrades gracefully when the server does not have the
 * endpoint yet: a 404/405 from POST /tasks/{id}/retry falls back to moving the
 * task to Ready, which the orchestrator has always honoured.
 */
export function useTaskRecovery(onUpdate?: () => void) {
  const toast = useToast();
  const [busy, setBusy] = useState<'ready' | 'retry' | null>(null);

  const sendBackToReady = useCallback(
    async (task: Task) => {
      setBusy('ready');
      try {
        await patchTask(task.id, { column: 'ready_to_dev', status: 'ready', error: '' });
        toast.success(`${task.id} is back in Ready`, 'The next wave picks it up.');
        onUpdate?.();
      } catch (err) {
        toast.reportError(err, `Could not send ${task.id} back to Ready`);
      } finally {
        setBusy(null);
      }
    },
    [onUpdate, toast],
  );

  const retry = useCallback(
    async (task: Task) => {
      setBusy('retry');
      try {
        await retryTask(task.id);
        toast.success(`Retrying ${task.id}`, 'Its attempt log is kept, so the next try knows what failed.');
        onUpdate?.();
      } catch (err) {
        if (err instanceof ApiError && (err.status === 404 || err.status === 405)) {
          // The endpoint is newer than this server; fall back to the move.
          try {
            await patchTask(task.id, { column: 'ready_to_dev', status: 'ready' });
            toast.success(`${task.id} queued for another attempt`);
            onUpdate?.();
          } catch (inner) {
            toast.reportError(inner, `Could not retry ${task.id}`);
          }
        } else if (err instanceof ApiError && err.isConflict) {
          toast.push({ tone: 'warning', title: 'Nothing to retry', detail: 'No board is loaded — start a run first.' });
        } else {
          toast.reportError(err, `Could not retry ${task.id}`);
        }
      } finally {
        setBusy(null);
      }
    },
    [onUpdate, toast],
  );

  return { busy, sendBackToReady, retry };
}

interface TaskActionRowProps {
  task: Task;
  onUpdate?: () => void;
  /** Surfaces the team control of the edit form; hidden when absent. */
  onReassign?: () => void;
  compact?: boolean;
  /** Which actions to show. Defaults to all four. */
  actions?: Array<'ready' | 'retry' | 'reassign' | 'steer'>;
}

export function TaskActionRow({
  task,
  onUpdate,
  onReassign,
  compact = false,
  actions = ['ready', 'retry', 'reassign', 'steer'],
}: TaskActionRowProps) {
  const navigate = useNavigate();
  const { busy, sendBackToReady, retry } = useTaskRecovery(onUpdate);
  const btn = clsx(
    'focus-ring inline-flex items-center gap-1 rounded-md border border-gray-200 bg-white font-medium text-gray-700',
    'hover:border-brand-300 hover:text-brand-700 disabled:opacity-50 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-200 dark:hover:border-brand-600',
    compact ? 'h-6 px-1.5 text-[10px]' : 'h-7 px-2 text-[11px]',
  );
  const icon = compact ? 10 : 12;

  const handleSteer = () => {
    // LiveFeedback listens on the Live page. Anywhere else, go there first;
    // steerTask() parks the prefill for the composer to pick up on mount.
    if (!steerTask(task.id)) navigate('/');
  };

  return (
    <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label={`Next steps for ${task.id}`}>
      {actions.includes('ready') && (
        <button type="button" className={btn} onClick={() => sendBackToReady(task)} disabled={busy !== null} title="Move the task back to Ready so the next wave picks it up">
          {busy === 'ready' ? <Loader2 size={icon} className="animate-spin" aria-hidden="true" /> : <ArrowLeftCircle size={icon} aria-hidden="true" />}
          Send back to Ready
        </button>
      )}
      {actions.includes('retry') && (
        <button type="button" className={btn} onClick={() => retry(task)} disabled={busy !== null} title="Run the task again, keeping its attempt log">
          {busy === 'retry' ? <Loader2 size={icon} className="animate-spin" aria-hidden="true" /> : <RotateCcw size={icon} aria-hidden="true" />}
          Retry
        </button>
      )}
      {actions.includes('reassign') && onReassign && (
        <button type="button" className={btn} onClick={onReassign} title="Give the task to a different team">
          <Users size={icon} aria-hidden="true" />
          Reassign team
        </button>
      )}
      {actions.includes('steer') && (
        <button type="button" className={btn} onClick={handleSteer} title="Tell the agents how to approach this task">
          <MessageSquareText size={icon} aria-hidden="true" />
          Steer
        </button>
      )}
    </div>
  );
}
