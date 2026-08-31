import { useEffect, useMemo, useState } from 'react';
import { Loader2, Cpu, ListTodo, Users, Clock, Coins } from 'lucide-react';
import clsx from 'clsx';
import { teamColor } from '@/components/Board/teamColor';
import type { RunEvent, SquadsView } from '@/types';

// ── What is happening RIGHT NOW ──────────────────────────────────────────
//
// The event log answers "what happened". The phase rail answers "where in the
// run are we". Neither answers the question a person actually asks while
// watching a local model work for eleven minutes: *what is it doing right now,
// and is it stuck?*
//
// A log line scrolls away the moment the next one arrives, and on a 30B the
// next one can be four minutes later — so during the part of the run where the
// user most needs reassurance, the screen is a wall of finished lines and a
// blinking cursor. That reads as a hang. It is the single biggest reason a run
// that is working feels like a run that is failing.
//
// This is one line that does not scroll: the agent, its task, its team, the
// model, and a clock that is still moving. The clock is the point — a number
// that ticks is the difference between "thinking" and "hung", and no amount of
// log output provides it.

export interface NowBarProps {
  events: RunEvent[];
  running: boolean;
  /** The org chart, so the active task's team can be named and coloured. */
  squads?: SquadsView | null;
}

/** What the stream says is in flight, derived from the tail of the log. */
interface Now {
  phase: string;
  agent: string;
  taskID: string;
  message: string;
  model: string;
  /** When this activity started, for the elapsed clock. */
  since: number;
  /** Cumulative tokens and cost for the run so far. */
  tokens: number;
  cost: number;
}

/**
 * readNow walks the log backwards for the most recent thing that STARTED.
 *
 * Backwards, and stopping at the first agent line, because the log's own order
 * is the only ordering there is — a wave runs several agents and the last one
 * announced is the one the user is waiting on. Totals are summed forward, since
 * every event may carry usage.
 */
function readNow(events: RunEvent[]): Now | null {
  if (events.length === 0) return null;

  let tokens = 0;
  let cost = 0;
  for (const e of events) {
    tokens += e.tokens ?? 0;
    cost += e.cost_usd ?? 0;
  }

  for (let i = events.length - 1; i >= 0; i--) {
    const e = events[i];
    if (!e.agent) continue;
    return {
      phase: e.phase || '',
      agent: e.agent,
      taskID: e.task_id ?? '',
      message: e.message ?? '',
      model: e.model ?? '',
      since: Date.parse(e.time) || Date.now(),
      tokens,
      cost,
    };
  }

  const last = events[events.length - 1];
  return {
    phase: last.phase || '',
    agent: '',
    taskID: last.task_id ?? '',
    message: last.message ?? '',
    model: last.model ?? '',
    since: Date.parse(last.time) || Date.now(),
    tokens,
    cost,
  };
}

export default function NowBar({ events, running, squads }: NowBarProps) {
  const now = useMemo(() => readNow(events), [events]);

  // A clock that only re-renders when an event arrives is a clock that stops
  // exactly when it matters — during the four-minute gap this bar exists for.
  const [tick, setTick] = useState(() => Date.now());
  useEffect(() => {
    if (!running) return undefined;
    const id = window.setInterval(() => setTick(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [running]);

  if (!running || !now) return null;

  const seconds = Math.max(0, Math.round((tick - now.since) / 1000));
  const team = squads?.ok
    ? (squads.squads ?? []).find((s) => s.id === teamOfTask(squads, now.taskID))
    : undefined;
  const color = teamColor(team?.id);

  return (
    <div
      role="status"
      aria-live="polite"
      className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-gray-200 bg-brand-50/60 px-3 py-1.5 text-[11px] dark:border-gray-800 dark:bg-brand-950/20 sm:px-4"
    >
      <Loader2 size={13} className="shrink-0 animate-spin text-brand-500" aria-hidden="true" />

      {now.agent && (
        <span className="inline-flex shrink-0 items-center gap-1 font-semibold text-brand-700 dark:text-brand-300">
          @{now.agent}
        </span>
      )}

      {now.taskID && (
        <span className="inline-flex shrink-0 items-center gap-1 font-mono text-gray-600 dark:text-gray-300">
          <ListTodo size={11} aria-hidden="true" />
          {now.taskID}
        </span>
      )}

      {team && (
        <span
          className={clsx('inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-0.5 font-mono font-semibold', color.badge)}
          title={`Team ${team.id}${team.manager ? ` · manager ${team.manager}` : ''}`}
        >
          <Users size={10} aria-hidden="true" />
          {team.id}
        </span>
      )}

      {/* The message is the only part allowed to be long, so it is the only
          part allowed to truncate. */}
      <span className="min-w-0 flex-1 truncate text-gray-600 dark:text-gray-400" title={now.message}>
        {now.message || `${now.phase} in progress`}
      </span>

      {/* Everything from here is the "is it stuck" evidence. */}
      <span
        className={clsx(
          'inline-flex shrink-0 items-center gap-1 font-mono tabular-nums',
          // Past two minutes on one step, say so in a colour rather than
          // leaving the reader to do the arithmetic. Local 30B calls really do
          // take this long, and a user who knows that is a user who waits.
          seconds >= 120 ? 'text-amber-600 dark:text-amber-400' : 'text-gray-500',
        )}
        title="Time on this step. A local 30B routinely takes minutes per call."
      >
        <Clock size={11} aria-hidden="true" />
        {formatDuration(seconds)}
      </span>

      {now.model && (
        <span
          className="hidden shrink-0 items-center gap-1 font-mono text-gray-400 sm:inline-flex"
          title={`Model: ${now.model}`}
        >
          <Cpu size={11} aria-hidden="true" />
          <span className="max-w-[14rem] truncate">{now.model}</span>
        </span>
      )}

      {now.tokens > 0 && (
        <span
          className="hidden shrink-0 items-center gap-1 font-mono tabular-nums text-gray-400 md:inline-flex"
          title={`${now.tokens.toLocaleString()} tokens this run${now.cost > 0 ? ` · $${now.cost.toFixed(4)}` : ''}`}
        >
          <Coins size={11} aria-hidden="true" />
          {compactTokens(now.tokens)}
        </span>
      )}
    </div>
  );
}

/**
 * teamOfTask is best-effort: the squad view carries per-team counts, not a task
 * index, so this only resolves a team when the LOG named one. A wrong team
 * badge would be worse than none, so an unknown task shows nothing.
 */
function teamOfTask(squads: SquadsView | null | undefined, taskID: string): string {
  if (!squads?.ok || !taskID) return '';
  return squads.task_teams?.[taskID] ?? '';
}

function formatDuration(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return `${m}m${s.toString().padStart(2, '0')}s`;
}

function compactTokens(n: number): string {
  if (n < 1000) return `${n}`;
  if (n < 1_000_000) return `${(n / 1000).toFixed(1)}k`;
  return `${(n / 1_000_000).toFixed(2)}M`;
}
