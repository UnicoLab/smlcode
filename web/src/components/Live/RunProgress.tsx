import { useMemo } from 'react';
import { ArrowUpRight, Check, Circle, Radio, WifiOff } from 'lucide-react';
import type { ConnectionState, Task } from '@/types';
import { ticketState } from './floor/floorModel';

interface Props {
  tasks: Task[];
  running: boolean;
  connection: ConnectionState;
  onTasks: () => void;
  onReconnect?: () => void;
}

/** Board completion is evidence of work, not a time estimate or a QA verdict. */
export default function RunProgress({ tasks, running, connection, onTasks, onReconnect }: Props) {
  const counts = useMemo(() => {
    const next = { done: 0, active: 0, attention: 0, queued: 0 };
    for (const task of tasks) {
      switch (ticketState(task)) {
        case 'done': next.done++; break;
        case 'working':
        case 'review': next.active++; break;
        case 'blocked':
        case 'failed': next.attention++; break;
        default: next.queued++;
      }
    }
    return next;
  }, [tasks]);
  if (!running && tasks.length === 0) return null;
  const delayed = connection !== 'live';
  const summary = tasks.length === 0
    ? 'Preparing the work'
    : counts.done === tasks.length && running
      ? 'Tasks complete · finishing checks'
      : `${counts.done} of ${tasks.length} tasks complete`;

  return (
    <section aria-label="Delivery progress" className="shrink-0 border-b border-gray-200 bg-white/80 px-3 py-2.5 dark:border-gray-800 dark:bg-gray-950/80 sm:px-4">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1.5 text-[11px]">
        <button type="button" onClick={onTasks} className="focus-ring group inline-flex min-w-0 items-center gap-2 rounded text-left font-semibold text-gray-800 dark:text-gray-100" title="Open the task board">
          {counts.done > 0 && counts.done === tasks.length
            ? <Check size={13} className="text-emerald-500" aria-hidden="true" />
            : <Circle size={11} className="text-brand-500" aria-hidden="true" />}
          <span className="tabular-nums">{summary}</span>
          <ArrowUpRight size={12} className="text-gray-400 transition-transform group-hover:-translate-y-0.5 group-hover:translate-x-0.5" aria-hidden="true" />
        </button>
        <div className="flex flex-wrap items-center gap-3 text-[10px] text-gray-500 dark:text-gray-400">
          {counts.active > 0 && <span className="inline-flex items-center gap-1.5"><span className="h-1.5 w-1.5 rounded-full bg-brand-500" aria-hidden="true" />{counts.active} active / review</span>}
          {counts.attention > 0 && <span className="font-medium text-amber-700 dark:text-amber-400">{counts.attention} blocked / failed</span>}
          {counts.queued > 0 && <span>{counts.queued} queued</span>}
          {running && <span className={delayed ? 'inline-flex items-center gap-1 text-amber-700 dark:text-amber-400' : 'inline-flex items-center gap-1 text-emerald-700 dark:text-emerald-400'}>
            {delayed ? <WifiOff size={11} aria-hidden="true" /> : <Radio size={11} aria-hidden="true" />}
            {delayed ? 'Updates delayed' : 'Live updates'}
          </span>}
        </div>
      </div>
      {tasks.length > 0 && (
        <div role="progressbar" aria-label="Completed tasks" aria-valuemin={0} aria-valuemax={tasks.length} aria-valuenow={counts.done}
          aria-valuetext={`${counts.done} of ${tasks.length} tasks complete; ${counts.attention} blocked or failed. Completion does not replace final verification.`}
          className="mt-2 flex h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
          <span aria-hidden="true" className="delivery-progress-segment h-full bg-emerald-500" style={{ width: `${counts.done / tasks.length * 100}%` }} />
          <span aria-hidden="true" className="delivery-progress-segment h-full bg-brand-400/70" style={{ width: `${counts.active / tasks.length * 100}%` }} />
          <span aria-hidden="true" className="delivery-progress-segment h-full bg-amber-500" style={{ width: `${counts.attention / tasks.length * 100}%` }} />
        </div>
      )}
      {running && delayed && (
        <div role="status" className="mt-2 flex flex-wrap items-center gap-x-2 text-[11px] text-amber-700 dark:text-amber-400">
          <span>Reconnecting to Studio. The run may still be working; this view shows the last known state.</span>
          {onReconnect && <button type="button" onClick={onReconnect} className="focus-ring rounded font-semibold underline underline-offset-2">Reconnect now</button>}
        </div>
      )}
    </section>
  );
}
