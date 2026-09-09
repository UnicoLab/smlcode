import { useMemo, useState } from 'react';
import {
  Activity,
  RefreshCw,
  UserCog,
  ArrowRightLeft,
  ShieldCheck,
  AlertTriangle,
  CheckCircle2,
  XCircle,
  Route,
  Waves,
  Link2,
  Pencil,
  BarChart3,
  Loader2,
} from 'lucide-react';
import clsx from 'clsx';
import { teamColor } from '@/components/Board/teamColor';
import type { TeamActivity as TeamActivityData, TeamActivityEntry, TeamActivityKind } from '@/types';

// ── What the managers did, and how the teams worked together ─────────────
//
// A run with two teams produces a log of a thousand lines in which the thirty
// that matter to a person asking "how did the teams collaborate" — the manager
// deciding who takes a rejected delivery, a task moved to another agent, a
// team waiting on the other's interface, a half proved or not — are buried
// between tool calls. This is those thirty, classified and filterable by team
// and by kind, with one row per manager on top saying what they did in
// numbers: decisions, tasks moved, stalls on their team, and whether their
// half came out green.

export interface TeamActivityProps {
  activity: TeamActivityData | null;
  loading: boolean;
  running: boolean;
  onRefresh: () => void;
}

const KIND_META: Record<TeamActivityKind, { label: string; icon: React.ReactNode; tone: string }> = {
  selection: { label: 'selection', icon: <ShieldCheck size={11} aria-hidden="true" />, tone: 'text-gray-500' },
  contract: { label: 'contract', icon: <Link2 size={11} aria-hidden="true" />, tone: 'text-indigo-600 dark:text-indigo-400' },
  routing: { label: 'routing', icon: <Route size={11} aria-hidden="true" />, tone: 'text-gray-500' },
  triage: { label: 'manager decision', icon: <UserCog size={11} aria-hidden="true" />, tone: 'text-brand-600 dark:text-brand-400' },
  reassign: { label: 'reassigned', icon: <ArrowRightLeft size={11} aria-hidden="true" />, tone: 'text-brand-600 dark:text-brand-400' },
  stall: { label: 'waiting on a team', icon: <AlertTriangle size={11} aria-hidden="true" />, tone: 'text-amber-600 dark:text-amber-400' },
  wave: { label: 'wave', icon: <Waves size={11} aria-hidden="true" />, tone: 'text-gray-500' },
  gate: { label: 'team gate', icon: <CheckCircle2 size={11} aria-hidden="true" />, tone: 'text-emerald-600 dark:text-emerald-400' },
  integration: { label: 'integration', icon: <Link2 size={11} aria-hidden="true" />, tone: 'text-indigo-600 dark:text-indigo-400' },
  progress: { label: 'progress', icon: <BarChart3 size={11} aria-hidden="true" />, tone: 'text-gray-500' },
  edit: { label: 'edited by hand', icon: <Pencil size={11} aria-hidden="true" />, tone: 'text-gray-500' },
};

const KIND_ORDER: TeamActivityKind[] = [
  'triage',
  'reassign',
  'stall',
  'gate',
  'integration',
  'contract',
  'selection',
  'routing',
  'wave',
  'progress',
  'edit',
];

export default function TeamActivity({ activity, loading, running, onRefresh }: TeamActivityProps) {
  const [teamFilter, setTeamFilter] = useState('');
  const [kindFilter, setKindFilter] = useState<TeamActivityKind | ''>('');

  const entries = useMemo(() => activity?.entries ?? [], [activity]);
  const teams = activity?.teams ?? [];
  const counts = activity?.counts ?? {};
  const managers = activity?.managers ?? [];

  const shown = useMemo(
    () =>
      entries.filter(
        (e) => (!teamFilter || e.team === teamFilter) && (!kindFilter || e.kind === kindFilter),
      ),
    [entries, teamFilter, kindFilter],
  );

  const decisions = (counts.triage ?? 0) + (counts.reassign ?? 0);

  return (
    <section className="space-y-3" aria-labelledby="team-activity-heading">
      <header className="flex flex-wrap items-center gap-2">
        <Activity size={14} className="shrink-0 text-brand-500" aria-hidden="true" />
        <h2 id="team-activity-heading" className="text-sm font-bold text-gray-900 dark:text-gray-100">
          How the teams worked
        </h2>
        {running && (
          <span className="badge-brand inline-flex items-center gap-1 text-[10px]">
            <Loader2 size={10} className="animate-spin" aria-hidden="true" /> live
          </span>
        )}
        {entries.length > 0 && (
          <span className="text-[11px] text-gray-500 dark:text-gray-400">
            {entries.length} events · {decisions} manager decision{decisions === 1 ? '' : 's'}
          </span>
        )}
        <button
          type="button"
          onClick={onRefresh}
          disabled={loading}
          className="btn-ghost focus-ring ml-auto h-7 gap-1 px-2 text-[11px]"
        >
          <RefreshCw size={11} className={clsx(loading && 'animate-spin')} aria-hidden="true" />
          Refresh
        </button>
      </header>
      <p className="text-[11px] text-gray-500 dark:text-gray-400">
        The team-relevant slice of the run: which teams were chosen and why, the contract they froze,
        every decision a project manager made about a rejected delivery, tasks moved between agents,
        a team waiting on another&rsquo;s interface, and whether each half proved itself alone.
      </p>

      {managers.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[32rem] text-left text-[11px]">
            <thead>
              <tr className="text-[10px] uppercase tracking-wider text-gray-400 dark:text-gray-500">
                <th className="py-1 pr-3 font-semibold">team</th>
                <th className="py-1 pr-3 font-semibold">project manager</th>
                <th className="py-1 pr-3 font-semibold">decisions</th>
                <th className="py-1 pr-3 font-semibold">tasks moved</th>
                <th className="py-1 pr-3 font-semibold">waited on others</th>
                <th className="py-1 pr-3 font-semibold">its half</th>
              </tr>
            </thead>
            <tbody>
              {managers.map((m) => {
                const color = teamColor(m.team);
                return (
                  <tr key={m.team} className="border-t border-gray-100 dark:border-gray-800">
                    <td className="py-1.5 pr-3">
                      <button
                        type="button"
                        onClick={() => setTeamFilter((cur) => (cur === m.team ? '' : m.team))}
                        aria-pressed={teamFilter === m.team}
                        className={clsx('focus-ring rounded px-1.5 py-0.5 font-mono font-semibold', color.badge)}
                      >
                        {m.team}
                      </button>
                    </td>
                    <td className="py-1.5 pr-3 font-mono text-gray-700 dark:text-gray-200">
                      {m.manager}
                      {m.default && <span className="ml-1 font-sans text-gray-400">(run default)</span>}
                    </td>
                    <td className="py-1.5 pr-3 tabular-nums">{m.decisions}</td>
                    <td className="py-1.5 pr-3 tabular-nums">{m.moved}</td>
                    <td className="py-1.5 pr-3 tabular-nums">{m.stalls}</td>
                    <td className="py-1.5 pr-3">
                      <Gate gate={m.gate} />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {entries.length === 0 ? (
        <p className="rounded-md border border-dashed border-gray-200 px-3 py-5 text-center text-[11px] text-gray-400 dark:border-gray-800">
          {loading
            ? 'Loading…'
            : running
              ? 'Nothing team-related yet — the teams are chosen at the charter phase, a few phases in.'
              : 'No team activity on record. Send a request to two or more teams and the managers’ decisions, handoffs and gates show up here as they happen.'}
        </p>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-1">
            <button
              type="button"
              onClick={() => setKindFilter('')}
              aria-pressed={kindFilter === ''}
              className={clsx('focus-ring rounded px-1.5 py-0.5 text-[10px]', kindFilter === '' ? 'bg-brand-500 text-white' : 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300')}
            >
              all ({entries.length})
            </button>
            {KIND_ORDER.filter((k) => (counts[k] ?? 0) > 0).map((k) => (
              <button
                key={k}
                type="button"
                onClick={() => setKindFilter((cur) => (cur === k ? '' : k))}
                aria-pressed={kindFilter === k}
                className={clsx(
                  'focus-ring inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px]',
                  kindFilter === k ? 'bg-brand-500 text-white' : 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300',
                )}
              >
                {KIND_META[k].icon}
                {KIND_META[k].label} ({counts[k]})
              </button>
            ))}
            {teams.length > 0 && (
              <>
                <span className="ml-2 text-[10px] text-gray-400">team:</span>
                {teams.map((id) => (
                  <button
                    key={id}
                    type="button"
                    onClick={() => setTeamFilter((cur) => (cur === id ? '' : id))}
                    aria-pressed={teamFilter === id}
                    className={clsx(
                      'focus-ring rounded px-1.5 py-0.5 font-mono text-[10px]',
                      teamFilter === id ? 'ring-2 ring-brand-500' : '',
                      teamColor(id).badge,
                    )}
                  >
                    {id}
                  </button>
                ))}
              </>
            )}
          </div>

          <ol className="divide-y divide-gray-100 rounded-lg border border-gray-200 dark:divide-gray-800 dark:border-gray-800" aria-label="Team activity timeline">
            {shown.length === 0 && (
              <li className="px-3 py-3 text-center text-[11px] text-gray-400">Nothing matches that filter.</li>
            )}
            {shown.map((e, i) => (
              <Entry key={`${e.time}-${i}`} entry={e} />
            ))}
          </ol>
        </>
      )}
    </section>
  );
}

function Entry({ entry }: { entry: TeamActivityEntry }) {
  const meta = KIND_META[entry.kind] ?? KIND_META.selection;
  const warn = entry.level === 'warning' || entry.level === 'error' || entry.level === 'problem';
  const decision = entry.kind === 'triage' || entry.kind === 'reassign';
  return (
    <li className={clsx('flex items-start gap-2 px-3 py-1.5 text-[11px]', decision && 'bg-brand-50/40 dark:bg-brand-950/20')}>
      <span className="w-14 shrink-0 pt-0.5 font-mono text-[10px] text-gray-400" title={entry.time}>
        {clock(entry.time)}
      </span>
      <span className={clsx('inline-flex w-32 shrink-0 items-center gap-1 pt-0.5 text-[10px] font-semibold', meta.tone)}>
        {meta.icon}
        {meta.label}
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex flex-wrap items-center gap-1">
          {entry.team && (
            <span className={clsx('rounded px-1 py-0.5 font-mono text-[10px]', teamColor(entry.team).badge)}>{entry.team}</span>
          )}
          {entry.agent && (
            <span className="badge-neutral font-mono text-[10px]" title={decision ? 'the manager, or the agent the task moved to' : 'agent'}>
              {entry.agent}
            </span>
          )}
          {entry.task_id && <span className="font-mono text-[10px] text-gray-500">{entry.task_id}</span>}
        </span>
        <span className={clsx('block break-words', warn ? 'text-amber-800 dark:text-amber-200' : 'text-gray-700 dark:text-gray-200')}>
          {entry.message}
        </span>
      </span>
    </li>
  );
}

function Gate({ gate }: { gate?: string }) {
  switch (gate) {
    case 'green':
      return (
        <span className="inline-flex items-center gap-1 text-emerald-600 dark:text-emerald-400">
          <CheckCircle2 size={11} aria-hidden="true" /> green
        </span>
      );
    case 'red':
      return (
        <span className="inline-flex items-center gap-1 text-red-600 dark:text-red-400">
          <XCircle size={11} aria-hidden="true" /> red
        </span>
      );
    case 'unverified':
      return (
        <span className="inline-flex items-center gap-1 text-gray-500" title="The acceptance command could not run — a fact about the machine, not the code">
          <AlertTriangle size={11} aria-hidden="true" /> unverified
        </span>
      );
    default:
      return <span className="text-gray-400">not yet proved</span>;
  }
}

function clock(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}
