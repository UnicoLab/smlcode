import { useMemo } from 'react';
import type { ReactNode } from 'react';
import { CheckCircle2, FolderTree, ListTodo, ScrollText, Wrench, XCircle, PanelRightClose } from 'lucide-react';
import clsx from 'clsx';
import EventLog from './EventLog';
import LiveTaskPanel from './LiveTaskPanel';
import LiveFileInspector from './LiveFileInspector';
import CalibrationBanner from './CalibrationBanner';
import TokenStream from './TokenStream';
import RecoveryPanel from './RecoveryPanel';
import ResultPanel from './ResultPanel';
import LiveFeedback from './LiveFeedback';
import { buildRecovery, recoveryTally } from './recovery';
import { useStickToBottom } from '@/hooks/useUiState';
import type { LatestRunResponse, RunEvent } from '@/types';

// ── One column for everything the stage does not draw ────────────────────
//
// The Live page used to be a log in the middle and five tabs on the side. The
// floor now takes the middle, and the side is ONE column: the log, first and
// by default, with the task list, the self-healing record, the file changes
// and the result reachable as filters on the same column rather than as a
// second navigation. A run is one stream of things happening; this is the
// place to read it, and the floor is the place to watch it.

export type RailView = 'log' | 'tasks' | 'fixes' | 'files' | 'result';

export interface ActivityRailProps {
  events: RunEvent[];
  running: boolean;
  result: LatestRunResponse | null;
  tokenStream: string;
  view: RailView;
  onView: (v: RailView) => void;
  onClose?: () => void;
  /** True when the rail is an overlay (narrow viewport), so it shows a close button. */
  overlay?: boolean;
}

export default function ActivityRail({ events, running, result, tokenStream, view, onView, onClose, overlay }: ActivityRailProps) {
  const logRef = useStickToBottom<HTMLDivElement>(events, view === 'log');
  const fixes = useMemo(() => recoveryTally(buildRecovery(events)), [events]);
  const fixesBadge =
    fixes.needsYou > 0 ? String(fixes.needsYou) : fixes.healing > 0 ? String(fixes.healing) : fixes.resolved > 0 ? String(fixes.resolved) : undefined;
  const fileCount = useMemo(() => {
    const set = new Set<string>();
    for (const e of events) {
      if (e.kind === 'file_change' && e.scope) set.add(e.scope);
    }
    return set.size;
  }, [events]);
  const taskCount = useMemo(() => {
    const ids = new Set<string>();
    for (const e of events) if (e.task_id) ids.add(e.task_id);
    return ids.size;
  }, [events]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 items-center gap-1 overflow-x-auto border-b border-gray-200 px-2 py-1.5 dark:border-gray-800" role="tablist" aria-label="Activity">
        <Chip active={view === 'log'} onClick={() => onView('log')} icon={<ScrollText size={12} />} label="Log" badge={events.length > 0 ? compact(events.length) : undefined} />
        <Chip active={view === 'tasks'} onClick={() => onView('tasks')} icon={<ListTodo size={12} />} label="Tasks" badge={taskCount > 0 ? String(taskCount) : undefined} />
        <Chip
          active={view === 'fixes'}
          onClick={() => onView('fixes')}
          icon={<Wrench size={12} className={fixes.needsYou > 0 ? 'text-amber-500' : undefined} />}
          label="Fixes"
          badge={fixesBadge}
          tone={fixes.needsYou > 0 ? 'warn' : undefined}
        />
        <Chip active={view === 'files'} onClick={() => onView('files')} icon={<FolderTree size={12} />} label="Files" badge={fileCount > 0 ? String(fileCount) : undefined} />
        <Chip
          active={view === 'result'}
          onClick={() => onView('result')}
          icon={
            result?.result ? (
              result.result.success ? <CheckCircle2 size={12} className="text-emerald-500" /> : <XCircle size={12} className="text-rose-500" />
            ) : (
              <CheckCircle2 size={12} />
            )
          }
          label="Result"
        />
        {overlay && onClose && (
          <button type="button" onClick={onClose} className="focus-ring ml-auto shrink-0 rounded p-1 text-gray-400 hover:text-gray-600" aria-label="Close side panel">
            <PanelRightClose size={15} />
          </button>
        )}
      </div>

      <div className="min-h-0 flex-1 overflow-hidden" role="tabpanel">
        {view === 'log' && (
          <div ref={logRef} className="h-full overflow-auto px-3 py-3">
            {events.length === 0 ? (
              <p className="py-10 text-center text-xs text-gray-400">
                {running ? 'Waiting for the first event…' : 'The run’s log streams here — phases, agents, tool calls and file writes.'}
              </p>
            ) : (
              <div className="space-y-3">
                <CalibrationBanner events={events} />
                <TokenStream text={tokenStream} running={running} />
                <EventLog events={events} />
              </div>
            )}
          </div>
        )}
        {view === 'tasks' && <LiveTaskPanel />}
        {view === 'fixes' && (
          <div className="h-full overflow-auto">
            <RecoveryPanel events={events} />
          </div>
        )}
        {view === 'files' && <LiveFileInspector events={events} running={running} />}
        {view === 'result' && <ResultPanel result={result} />}
      </div>

      {/* Feedback docks to the bottom of the column, next to what you are
          replying to. */}
      <div className="shrink-0 border-t border-gray-200 bg-white px-3 py-2 dark:border-gray-800 dark:bg-gray-950">
        <LiveFeedback compact />
      </div>
    </div>
  );
}

function Chip({ active, onClick, icon, label, badge, tone }: { active: boolean; onClick: () => void; icon: ReactNode; label: string; badge?: string; tone?: 'warn' }) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={clsx(
        'focus-ring inline-flex shrink-0 items-center gap-1 rounded-full border px-2.5 py-1 text-[11px] font-semibold transition-colors',
        active
          ? 'border-brand-500 bg-brand-500 text-white shadow-sm'
          : 'border-gray-200 bg-white text-gray-500 hover:border-gray-300 hover:text-gray-700 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-400 dark:hover:text-gray-200',
      )}
    >
      {icon}
      {label}
      {badge && (
        <span
          className={clsx(
            'rounded-full px-1.5 text-[10px] leading-4',
            active ? 'bg-white/25 text-white' : tone === 'warn' ? 'bg-amber-100 text-amber-800 dark:bg-amber-900/50 dark:text-amber-200' : 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300',
          )}
        >
          {badge}
        </span>
      )}
    </button>
  );
}

function compact(n: number): string {
  return n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n);
}
