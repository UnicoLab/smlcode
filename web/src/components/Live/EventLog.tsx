import { useMemo, useState } from 'react';
import type { RunEvent } from '@/types';
import clsx from 'clsx';

interface EventLogProps {
  events: RunEvent[];
}

const PHASE_COLORS: Record<string, string> = {
  init: 'text-sky-500',
  skills: 'text-teal-500',
  context: 'text-cyan-500',
  explore: 'text-blue-500',
  docs: 'text-indigo-500',
  architect: 'text-violet-500',
  clarify: 'text-purple-500',
  plan: 'text-fuchsia-500',
  split: 'text-pink-500',
  coord: 'text-rose-500',
  execute: 'text-amber-500',
  learn: 'text-orange-500',
  polish: 'text-yellow-500',
  test: 'text-lime-500',
  memory: 'text-emerald-500',
  done: 'text-green-500',
  compose: 'text-teal-500',
  error: 'text-red-500',
};

const KIND_ICONS: Record<string, string> = {
  run_start: '🚀',
  run_done: '✅',
  run_error: '❌',
  task_start: '▶️',
  task_done: '✔️',
  task_fail: '❌',
  review: '👁️',
  correct: '🔧',
  coord: '🎯',
  plan: '📋',
  explore: '🔍',
  context: '📝',
  clarify: '❓',
  split: '✂️',
  polish: '✨',
  test: '🧪',
  memory: '🧠',
  agent: '🤖',
  llm: '💭',
  tool: '🔨',
  shell: '💻',
  wave: '🌊',
  gate: '🚧',
  rewind: '⏪',
};

// Severity styling — problems/warnings/errors are visually distinct from routine info.
const LEVEL_STYLES: Record<string, { row: string; badge: string; label: string; icon: string }> = {
  error: { row: 'bg-red-50/70 dark:bg-red-900/15', badge: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300', label: 'ERROR', icon: '❌' },
  problem: { row: 'bg-orange-50/70 dark:bg-orange-900/15', badge: 'bg-orange-100 text-orange-700 dark:bg-orange-900/40 dark:text-orange-300', label: 'PROBLEM', icon: '⚠️' },
  warning: { row: 'bg-amber-50/60 dark:bg-amber-900/10', badge: 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300', label: 'WARN', icon: '⚠️' },
  success: { row: 'bg-green-50/60 dark:bg-green-900/10', badge: 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300', label: 'OK', icon: '✅' },
  info: { row: '', badge: 'bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400', label: 'INFO', icon: '·' },
};

type Filter = 'all' | 'problems';

export default function EventLog({ events }: EventLogProps) {
  const [filter, setFilter] = useState<Filter>('all');

  const counts = useMemo(() => {
    const c = { error: 0, problem: 0, warning: 0, success: 0 };
    for (const e of events) {
      const lvl = e.level || 'info';
      if (lvl in c) c[lvl as keyof typeof c] += 1;
    }
    return c;
  }, [events]);

  const visible = useMemo(() => {
    if (filter === 'all') return events;
    return events.filter((e) => {
      const lvl = e.level || 'info';
      return lvl === 'error' || lvl === 'problem' || lvl === 'warning';
    });
  }, [events, filter]);

  if (events.length === 0) {
    return (
      <div className="flex items-center justify-center h-32 text-xs text-gray-400">
        Waiting for events…
      </div>
    );
  }

  return (
    <div className="space-y-2">
      {/* Summary + filter bar */}
      <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-gray-50 dark:bg-gray-800/40 text-[10px]">
        <button
          onClick={() => setFilter('all')}
          className={clsx(
            'px-2 py-1 rounded-md font-medium transition-colors',
            filter === 'all'
              ? 'bg-gray-200 text-gray-800 dark:bg-gray-700 dark:text-gray-100'
              : 'text-gray-500 hover:text-gray-700 dark:hover:text-gray-300',
          )}
        >
          All ({events.length})
        </button>
        <button
          onClick={() => setFilter('problems')}
          className={clsx(
            'px-2 py-1 rounded-md font-medium transition-colors',
            filter === 'problems'
              ? 'bg-gray-200 text-gray-800 dark:bg-gray-700 dark:text-gray-100'
              : 'text-gray-500 hover:text-gray-700 dark:hover:text-gray-300',
          )}
        >
          Problems ({counts.error + counts.problem + counts.warning})
        </button>
        <span className="ml-auto flex items-center gap-2 text-gray-400 dark:text-gray-500">
          {counts.error > 0 && <span className="text-red-500">❌ {counts.error}</span>}
          {counts.problem > 0 && <span className="text-orange-500">⚠️ {counts.problem}</span>}
          {counts.warning > 0 && <span className="text-amber-500">⚠️ {counts.warning}</span>}
          {counts.success > 0 && <span className="text-green-500">✅ {counts.success}</span>}
        </span>
      </div>

      <div className="space-y-0.5 font-mono text-xs">
        {visible.map((event, i) => {
          const lvl = event.level || 'info';
          const style = LEVEL_STYLES[lvl] || LEVEL_STYLES.info;
          return (
            <div
              key={`${event.time}-${i}`}
              className={clsx(
                'flex items-start gap-3 px-3 py-2 rounded-lg transition-colors hover:bg-gray-50 dark:hover:bg-gray-800/50',
                style.row,
                event.phase === 'error' && 'bg-red-50/50 dark:bg-red-900/10',
              )}
            >
              {/* Severity icon (preferred over kind icon for non-info levels) */}
              <span className="shrink-0 mt-px text-sm" title={lvl}>
                {lvl !== 'info' ? style.icon : KIND_ICONS[event.kind] || '·'}
              </span>

              {/* Timestamp */}
              <span className="text-gray-400 dark:text-gray-600 shrink-0 w-20 tabular-nums">
                {formatTime(event.time)}
              </span>

              {/* Severity badge */}
              <span
                className={clsx(
                  'shrink-0 px-1.5 py-0.5 rounded text-[9px] font-bold leading-none',
                  style.badge,
                )}
              >
                {style.label}
              </span>

              {/* Phase badge */}
              <span
                className={clsx(
                  'shrink-0 w-20 text-right text-[10px] font-semibold uppercase tracking-wider',
                  PHASE_COLORS[event.phase] || 'text-gray-500',
                )}
              >
                {event.phase}
              </span>

              {/* Message */}
              <span className="flex-1 text-gray-700 dark:text-gray-300 break-words">
                {event.agent && (
                  <span className="text-brand-500 font-semibold">[{event.agent}] </span>
                )}
                {event.task_id && (
                  <span className="text-gray-400">#{event.task_id} </span>
                )}
                {event.scope && (
                  <span className="text-violet-500">({event.scope}) </span>
                )}
                {event.message}
              </span>

              {/* Output preview */}
              {event.output && (
                <span className="text-gray-400 truncate max-w-[200px] ml-2">
                  {event.output.slice(0, 80)}
                </span>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function formatTime(iso: string): string {
  try {
    const d = new Date(iso);
    return d.toLocaleTimeString('en-US', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' });
  } catch {
    return '--:--:--';
  }
}
