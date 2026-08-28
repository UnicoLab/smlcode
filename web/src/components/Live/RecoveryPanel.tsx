import { useMemo } from 'react';
import { CheckCircle2, Wrench, AlertTriangle, ArrowRight, ShieldCheck } from 'lucide-react';
import clsx from 'clsx';
import { buildRecovery, recoveryTally, type RecoveryEpisode } from './recovery';
import type { RunEvent } from '@/types';

// ── What the harness fixed by itself ─────────────────────────────────────
//
// The harness recovers from most of what goes wrong, and none of it was
// visible. The failures are red and loud — "tester found 1 failure", "T2
// reassigned after its retries were spent" — and the recovery is four plain
// lines somewhere in a log of fifty. A user watching that sees a run going
// wrong with no evidence anything is handling it, which is the worst possible
// reading of a system that is, in fact, fixing itself.
//
// One row per defect: what was found, who has it now, whether it closed.

export default function RecoveryPanel({ events }: { events: RunEvent[] }) {
  const episodes = useMemo(() => buildRecovery(events), [events]);
  const tally = useMemo(() => recoveryTally(episodes), [episodes]);

  if (episodes.length === 0) {
    return (
      <div className="p-4 text-center">
        <ShieldCheck size={26} className="mx-auto mb-2 text-gray-300 dark:text-gray-600" aria-hidden="true" />
        <p className="text-xs font-semibold text-gray-600 dark:text-gray-300">Nothing has needed fixing</p>
        <p className="mx-auto mt-1 max-w-xs text-[11px] text-gray-500 dark:text-gray-400">
          When a gate rejects the work, what was found and who picked it up shows up here.
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-2 p-2">
      <p className="px-1 text-[11px] text-gray-500 dark:text-gray-400">
        {tally.resolved > 0 && (
          <span className="font-semibold text-emerald-600 dark:text-emerald-400">
            {tally.resolved} fixed automatically
          </span>
        )}
        {tally.resolved > 0 && (tally.healing > 0 || tally.needsYou > 0) && ' · '}
        {tally.healing > 0 && <span>{tally.healing} being worked on</span>}
        {tally.healing > 0 && tally.needsYou > 0 && ' · '}
        {tally.needsYou > 0 && (
          <span className="font-semibold text-amber-600 dark:text-amber-400">{tally.needsYou} needs you</span>
        )}
      </p>
      <ul className="space-y-2">
        {episodes.map((ep) => (
          <li key={ep.id}>
            <Episode episode={ep} />
          </li>
        ))}
      </ul>
    </div>
  );
}

const TONE = {
  resolved: {
    ring: 'border-emerald-300 dark:border-emerald-800',
    text: 'text-emerald-700 dark:text-emerald-300',
    label: 'Fixed automatically',
    Icon: CheckCircle2,
  },
  healing: {
    ring: 'border-brand-300 dark:border-brand-800',
    text: 'text-brand-600 dark:text-brand-300',
    label: 'Being fixed',
    Icon: Wrench,
  },
  'needs-you': {
    ring: 'border-amber-300 dark:border-amber-800',
    text: 'text-amber-700 dark:text-amber-300',
    label: 'Needs you',
    Icon: AlertTriangle,
  },
} as const;

function Episode({ episode }: { episode: RecoveryEpisode }) {
  const tone = TONE[episode.state];
  return (
    <div className={clsx('rounded-lg border p-2.5', tone.ring)}>
      <div className="flex items-center gap-1.5">
        <tone.Icon size={13} className={clsx('shrink-0', tone.text)} aria-hidden="true" />
        <span className={clsx('text-[11px] font-bold', tone.text)}>{tone.label}</span>
        {episode.attempts > 1 && (
          <span className="badge-neutral text-[10px]">
            {episode.attempts} attempts
          </span>
        )}
      </div>

      <p className="mt-1 break-words text-xs text-gray-700 dark:text-gray-200">{episode.found}</p>
      {episode.failures.length > 0 && (
        <ul className="mt-1 space-y-0.5">
          {episode.failures.map((f) => (
            <li key={f} className="break-words font-mono text-[10px] text-gray-500 dark:text-gray-400">
              {f}
            </li>
          ))}
        </ul>
      )}

      {episode.handedTo && (
        <p className="mt-1.5 flex flex-wrap items-center gap-1 text-[11px] text-gray-600 dark:text-gray-300">
          <ArrowRight size={11} className="shrink-0 text-gray-400" aria-hidden="true" />
          reassigned to
          <code className="font-mono font-semibold text-gray-800 dark:text-gray-100">{episode.handedTo}</code>
        </p>
      )}

      <ol className="mt-1.5 space-y-0.5">
        {episode.steps.map((s, i) => (
          <li
            key={`${s.action}-${i}`}
            className="flex items-baseline gap-1.5 text-[10px] text-gray-500 dark:text-gray-400"
          >
            <span aria-hidden="true" className="text-gray-300 dark:text-gray-600">
              {i + 1}.
            </span>
            <span className="min-w-0 break-words">{s.label}</span>
          </li>
        ))}
      </ol>
    </div>
  );
}
