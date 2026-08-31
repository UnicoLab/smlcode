import { Users, ShieldCheck, AlertTriangle, ArrowRight } from 'lucide-react';
import clsx from 'clsx';
import { teamColor, UNASSIGNED } from './teamColor';
import type { SquadsView, Task } from '@/types';

// ── Who is building what, above the board ────────────────────────────────
//
// The board showed columns and cards and said nothing about teams, so on a
// two-team run the two halves were interleaved in every column with no way to
// tell them apart — and the three questions a watcher actually has went
// unanswered:
//
//   who owns this card          → a coloured team badge, same colour everywhere
//   who answers when it fails   → the team's project manager, named
//   why is that team not moving → a cross-team stall is a CONTRACT dependency,
//                                 not a defect, and reads completely differently
//
// Clicking a team filters the board to its lane. That is the difference between
// "seven cards" and "the backend's four, and they are all blocked".

export interface TeamStripProps {
  view: SquadsView | null;
  tasks: Task[];
  /** The team currently filtered to; '' is everything. */
  filter: string;
  onFilter: (teamID: string) => void;
}

export default function TeamStrip({ view, tasks, filter, onFilter }: TeamStripProps) {
  const squads = view?.ok ? (view.squads ?? []) : [];
  if (squads.length === 0) return null;

  // A task on no team is not a defect — it is the seam, or a file nobody owns.
  // It gets its own filter because "everything else" is a real thing to look at.
  const unassigned = tasks.filter((t) => !t.squad).length;
  const stalls = view?.stalls ?? [];
  const integration = view?.integration;

  return (
    <section
      aria-label="Teams on this run"
      className="mb-4 rounded-xl border border-gray-200 bg-white/60 p-3 dark:border-gray-800 dark:bg-gray-900/40"
    >
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <Users size={13} className="shrink-0 text-brand-500" aria-hidden="true" />
        <h2 className="text-xs font-bold uppercase tracking-wider text-gray-600 dark:text-gray-300">
          Teams
        </h2>
        {view?.summary && (
          <span className="min-w-0 flex-1 truncate text-[11px] text-gray-500 dark:text-gray-400">
            {view.summary}
          </span>
        )}
        {filter !== '' && (
          <button
            type="button"
            onClick={() => onFilter('')}
            className="btn-ghost focus-ring ml-auto h-7 px-2 text-[11px]"
          >
            Show all lanes
          </button>
        )}
      </div>

      <div
        className="grid gap-2"
        style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(min(17rem, 100%), 1fr))' }}
      >
        {squads.map((s) => {
          const color = teamColor(s.id);
          const active = filter === s.id;
          const pct = s.total > 0 ? Math.round((s.done / s.total) * 100) : 0;
          const waiting = stalls.filter((st) => st.squad === s.id);
          const gate = (view?.gates ?? []).find((g) => g.team === s.id);
          return (
            <button
              key={s.id}
              type="button"
              onClick={() => onFilter(active ? '' : s.id)}
              aria-pressed={active}
              className={clsx(
                'focus-ring rounded-lg border p-2 text-left transition-colors',
                active
                  ? 'border-brand-400 bg-brand-50/60 dark:border-brand-600 dark:bg-brand-950/30'
                  : 'border-gray-200 hover:bg-gray-50 dark:border-gray-800 dark:hover:bg-gray-800/40',
              )}
            >
              <div className="mb-1 flex items-center gap-1.5">
                <span className={clsx('rounded px-1.5 py-0.5 font-mono text-[10px] font-semibold', color.badge)}>
                  {s.id}
                </span>
                <span className="min-w-0 flex-1 truncate text-[11px] font-semibold text-gray-800 dark:text-gray-100">
                  {s.name || s.id}
                </span>
                <span className="shrink-0 font-mono text-[10px] text-gray-500">
                  {s.done}/{s.total}
                </span>
              </div>

              <div className="mb-1.5 h-1 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-800">
                <div className={clsx('h-full rounded-full', color.accent)} style={{ width: `${pct}%` }} />
              </div>

              <dl className="space-y-0.5 text-[10px]">
                {/* Who answers when this team's work is rejected. The single
                    most useful thing to know about a team that is going red,
                    and it was nowhere on this page. */}
                <div className="flex gap-1.5">
                  <dt className="shrink-0 text-gray-400 dark:text-gray-500">manager</dt>
                  <dd className="min-w-0 flex-1 truncate font-mono text-gray-700 dark:text-gray-300">
                    {s.manager || <span className="font-sans text-gray-400">run default</span>}
                  </dd>
                </div>
                <div className="flex gap-1.5">
                  <dt className="shrink-0 text-gray-400 dark:text-gray-500">proves it</dt>
                  <dd className="min-w-0 flex-1 truncate font-mono text-gray-700 dark:text-gray-300">
                    {s.acceptance || (
                      <span className="font-sans text-amber-700 dark:text-amber-400">
                        nothing — breaks surface at integration
                      </span>
                    )}
                  </dd>
                </div>
              </dl>

              <div className="mt-1 flex flex-wrap items-center gap-1">
                {s.blocked > 0 && (
                  <span className="rounded bg-red-100 px-1 py-0.5 text-[10px] font-semibold text-red-700 dark:bg-red-950/60 dark:text-red-300">
                    {s.blocked} blocked
                  </span>
                )}
                {s.in_flight > 0 && (
                  <span className="rounded bg-amber-100 px-1 py-0.5 text-[10px] text-amber-800 dark:bg-amber-950/60 dark:text-amber-200">
                    {s.in_flight} running
                  </span>
                )}
                {/* PROVED green, not assumed green. `complete` is a statement
                    about the board — every task in the lane reached done — and
                    a half can finish its tasks without building. The gate is
                    the half actually passing its own command. */}
                {gate?.ran && gate.ok && (
                  <span
                    title={`Passed its own acceptance: ${gate.command}`}
                    className="rounded bg-emerald-100 px-1 py-0.5 text-[10px] font-semibold text-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-300"
                  >
                    proved green
                  </span>
                )}
                {gate?.ran && !gate.ok && (
                  <span
                    title={gate.summary}
                    className="rounded bg-red-100 px-1 py-0.5 text-[10px] font-semibold text-red-700 dark:bg-red-950/60 dark:text-red-300"
                  >
                    half is red
                  </span>
                )}
                {gate && !gate.ran && (
                  <span
                    title={gate.summary || 'the acceptance command could not start on this machine'}
                    className="rounded bg-gray-100 px-1 py-0.5 text-[10px] text-gray-600 dark:bg-gray-800 dark:text-gray-300"
                  >
                    unverified
                  </span>
                )}
                {!gate && s.complete && (
                  <span
                    title="Every task in this lane is done. Its acceptance command has not run yet."
                    className="rounded bg-emerald-50 px-1 py-0.5 text-[10px] text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300"
                  >
                    tasks done
                  </span>
                )}
                {/* A team blocked on another team's interface is NOT a defect in
                    its own work, and a red badge would say it was. */}
                {waiting.map((st) => (
                  <span
                    key={st.interface}
                    title={`Waiting for ${st.provider} to deliver ${st.interface} — a contract dependency, not a defect`}
                    className="inline-flex items-center gap-0.5 rounded bg-sky-100 px-1 py-0.5 text-[10px] text-sky-800 dark:bg-sky-950/60 dark:text-sky-200"
                  >
                    waiting <ArrowRight size={9} aria-hidden="true" /> {st.provider}
                  </span>
                ))}
              </div>
            </button>
          );
        })}

        {unassigned > 0 && (
          <button
            type="button"
            onClick={() => onFilter(filter === UNASSIGNED_FILTER ? '' : UNASSIGNED_FILTER)}
            aria-pressed={filter === UNASSIGNED_FILTER}
            title="Tasks on no team: the seam between two halves, or files nobody owns"
            className={clsx(
              'focus-ring rounded-lg border border-dashed p-2 text-left transition-colors',
              filter === UNASSIGNED_FILTER
                ? 'border-brand-400 bg-brand-50/60 dark:border-brand-600 dark:bg-brand-950/30'
                : 'border-gray-300 hover:bg-gray-50 dark:border-gray-700 dark:hover:bg-gray-800/40',
            )}
          >
            <div className="mb-1 flex items-center gap-1.5">
              <span className={clsx('rounded px-1.5 py-0.5 font-mono text-[10px] font-semibold', UNASSIGNED.badge)}>
                no team
              </span>
              <span className="shrink-0 font-mono text-[10px] text-gray-500">{unassigned}</span>
            </div>
            <p className="text-[10px] leading-tight text-gray-500 dark:text-gray-400">
              The seam between two halves, or files no team owns. Handing one to a team is how a
              frontend task acquires permission to rewrite the API.
            </p>
          </button>
        )}
      </div>

      {integration && (
        <p className="mt-2 flex flex-wrap items-center gap-1.5 text-[11px] text-gray-500 dark:text-gray-400">
          <ShieldCheck size={12} className="shrink-0 text-brand-500" aria-hidden="true" />
          <span className="font-semibold">Integration:</span>
          {integration.acceptance ? (
            <code className="font-mono">{integration.acceptance}</code>
          ) : (
            <span className="inline-flex items-center gap-1 text-amber-700 dark:text-amber-400">
              <AlertTriangle size={11} aria-hidden="true" />
              no command — every team can be green with the assembled app still broken
            </span>
          )}
          {integration.reason && <span className="text-gray-400">· {integration.reason}</span>}
        </p>
      )}
    </section>
  );
}

/** The filter value for "tasks on no team". Not a team id, so it cannot collide. */
export const UNASSIGNED_FILTER = ' unassigned';
