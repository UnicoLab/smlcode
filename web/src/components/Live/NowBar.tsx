import { useEffect, useMemo, useState } from 'react';
import { Loader2, Cpu, Users, Clock, Coins, Hourglass } from 'lucide-react';
import clsx from 'clsx';
import { teamColor } from '@/components/Board/teamColor';
import EntityLink from '@/components/shared/EntityLink';
import type { RunEvent, SquadsView } from '@/types';
import { nowFor, readActivity, type FloorNow } from './floor/floorModel';

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
//
// Who is "in flight" is the floor's reading of the log (floorModel's working
// set): an agent between its agent_start and agent_end, or the newest voice.
// It used to be "the last line that named an agent", which kept the clock
// counting on a worker whose agent_end had already arrived — a finished step
// timed as if it were still going. When nobody is working, the bar says so and
// the clock stops.

export interface NowBarProps {
  events: RunEvent[];
  running: boolean;
  /** The org chart, so the active task's team can be named and coloured. */
  squads?: SquadsView | null;
  /**
   * Who is working, from the floor model. LiveView passes the floor's own so
   * the log is read once; left undefined, the bar reads the log itself.
   */
  now?: FloorNow | null;
  /** Cumulative tokens and cost for the run, from the stream's accumulator. */
  totals?: { tokens: number; cost: number };
}

export default function NowBar({ events, running, squads, now: nowProp, totals }: NowBarProps) {
  // The fallback path (no floor to borrow from) reads the log the way the
  // floor does, so both agree on who is working.
  const ownNow = useMemo<FloorNow | null>(() => {
    if (nowProp !== undefined) return null;
    if (events.length === 0) return null;
    const activity = readActivity(events);
    return nowFor(activity, squads?.ok ? (squads.task_teams ?? {}) : {}, Date.now());
  }, [events, nowProp, squads]);
  const now = nowProp !== undefined ? nowProp : ownNow;

  const ownTotals = useMemo(() => {
    if (totals) return null;
    let tokens = 0;
    let cost = 0;
    for (const e of events) {
      tokens += e.tokens ?? 0;
      cost += e.cost_usd ?? 0;
    }
    return { tokens, cost };
  }, [events, totals]);
  const sum = totals ?? ownTotals ?? { tokens: 0, cost: 0 };

  const last = events.length > 0 ? events[events.length - 1] : null;

  // A clock that only re-renders when an event arrives is a clock that stops
  // exactly when it matters — during the four-minute gap this bar exists for.
  // It runs only while someone is working: an idle bar has nothing to time.
  const ticking = running && !!now;
  const [tick, setTick] = useState(() => Date.now());
  useEffect(() => {
    if (!ticking) return undefined;
    setTick(Date.now());
    const id = window.setInterval(() => setTick(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [ticking]);

  if (!running || !last) return null;

  const seconds = now ? Math.max(0, Math.round((tick - now.since) / 1000)) : 0;
  const teamID = now ? now.team || teamOfTask(squads, now.task) : '';
  const team = squads?.ok && teamID ? (squads.squads ?? []).find((s) => s.id === teamID) : undefined;
  const color = teamColor(team?.id);
  const phase = now ? '' : last.phase || '';

  return (
    <div
      role="status"
      aria-live="polite"
      className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-gray-200 bg-brand-50/60 px-3 py-1.5 text-[11px] dark:border-gray-800 dark:bg-brand-950/20 sm:px-4"
      data-testid="now-bar"
      data-working={now ? 'true' : 'false'}
    >
      {now ? (
        <Loader2 size={13} className="shrink-0 animate-spin text-brand-500" aria-hidden="true" />
      ) : (
        <Hourglass size={13} className="shrink-0 text-gray-400" aria-hidden="true" />
      )}

      {now?.agent && (
        <EntityLink kind="agent" id={now.agent} label={`@${now.agent}`} params={{ team: teamID || undefined }} title={`Agent ${now.agent} — open on the floor`} />
      )}

      {now?.task && (
        <EntityLink kind="task" id={now.task} params={{ team: teamID || undefined }} title={`Task ${now.task} — open on the floor`} />
      )}

      {team && (
        <EntityLink
          kind="team"
          id={team.id}
          className={clsx('!border-transparent', color.badge)}
          title={`Team ${team.id}${team.manager ? ` · manager ${team.manager}` : ''}`}
          label={
            <span className="inline-flex items-center gap-1">
              <Users size={10} aria-hidden="true" />
              {team.id}
            </span>
          }
          bare
        />
      )}

      {/* The message is the only part allowed to be long, so it is the only
          part allowed to truncate. */}
      <span className="min-w-0 flex-1 truncate text-gray-600 dark:text-gray-400" title={now ? now.message : last.message}>
        {now ? now.message || 'in progress' : `${phase ? `${phase}: ` : ''}nobody is working — waiting for the next step`}
      </span>

      {/* Everything from here is the "is it stuck" evidence. */}
      {now && (
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
      )}

      {now?.model && (
        <span
          className="hidden shrink-0 items-center gap-1 font-mono text-gray-400 sm:inline-flex"
          title={`Model: ${now.model}`}
        >
          <Cpu size={11} aria-hidden="true" />
          <span className="max-w-[14rem] truncate">{now.model}</span>
        </span>
      )}

      {sum.tokens > 0 && (
        <span
          className="hidden shrink-0 items-center gap-1 font-mono tabular-nums text-gray-400 md:inline-flex"
          title={`${sum.tokens.toLocaleString()} tokens this run${sum.cost > 0 ? ` · $${sum.cost.toFixed(4)}` : ''}`}
        >
          <Coins size={11} aria-hidden="true" />
          {compactTokens(sum.tokens)}
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
