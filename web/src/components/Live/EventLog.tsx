import { useMemo, useRef, useEffect } from 'react';
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

export default function EventLog({ events }: EventLogProps) {
  const grouped = useMemo(() => {
    const groups: { phase: string; events: RunEvent[] }[] = [];
    let current: RunEvent[] = [];

    for (const e of events) {
      if (e.kind === 'run_start' && current.length > 0) {
        if (current[0]) groups.push({ phase: current[0].phase, events: current });
        current = [];
      }
      current.push(e);
    }
    if (current.length > 0 && current[0]) {
      groups.push({ phase: current[0].phase, events: current });
    }
    return groups;
  }, [events]);

  if (events.length === 0) {
    return (
      <div className="flex items-center justify-center h-32 text-xs text-gray-400">
        Waiting for events…
      </div>
    );
  }

  return (
    <div className="space-y-0.5 font-mono text-xs">
      {events.map((event, i) => (
        <div
          key={`${event.time}-${i}`}
          className={clsx(
            'flex items-start gap-3 px-3 py-2 rounded-lg transition-colors hover:bg-gray-50 dark:hover:bg-gray-800/50',
            event.phase === 'error' && 'bg-red-50/50 dark:bg-red-900/10',
          )}
        >
          {/* Icon */}
          <span className="shrink-0 mt-px text-sm" title={event.kind}>
            {KIND_ICONS[event.kind] || '·'}
          </span>

          {/* Timestamp */}
          <span className="text-gray-400 dark:text-gray-600 shrink-0 w-20 tabular-nums">
            {formatTime(event.time)}
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
      ))}
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
