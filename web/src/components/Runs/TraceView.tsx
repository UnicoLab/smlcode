import { useEffect, useState } from 'react';
import { AlertTriangle, Clock, Coins, Loader2, Wrench } from 'lucide-react';
import clsx from 'clsx';
import { getQueryTrace } from '@/api/client';
import type { RunTrace, TracePhase } from '@/types';

// ── Run trace ──
//
// Replays what actually happened in a recorded turn: how long each phase took,
// which agents and models ran, and where the tokens (and money) went. The
// per-phase wall time is the number that matters most when tuning an SLM.

const PHASE_COLOR: Record<string, string> = {
  init: 'bg-blue-400',
  skills: 'bg-blue-500',
  context: 'bg-cyan-500',
  explore: 'bg-sky-500',
  docs: 'bg-indigo-500',
  architect: 'bg-violet-500',
  clarify: 'bg-purple-500',
  plan: 'bg-fuchsia-500',
  split: 'bg-pink-500',
  coord: 'bg-amber-500',
  execute: 'bg-orange-500',
  learn: 'bg-yellow-500',
  polish: 'bg-lime-500',
  test: 'bg-emerald-500',
  memory: 'bg-teal-500',
  done: 'bg-green-500',
};

export function formatDuration(ms: number): string {
  if (!ms || ms < 0) return '—';
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  const m = Math.floor(ms / 60_000);
  const s = Math.round((ms % 60_000) / 1000);
  return `${m}m ${s}s`;
}

export function formatCost(usd: number): string {
  if (!usd) return '—';
  return usd < 0.01 ? `$${usd.toFixed(4)}` : `$${usd.toFixed(2)}`;
}

interface Props {
  queryId: string;
}

export default function TraceView({ queryId }: Props) {
  const [trace, setTrace] = useState<RunTrace | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    getQueryTrace(queryId)
      .then((t) => {
        if (!cancelled) setTrace(t);
      })
      .catch((err) => {
        if (!cancelled) setError(err instanceof Error ? err.message : 'Could not load the trace');
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [queryId]);

  if (loading) {
    return (
      <div className="flex items-center gap-2 px-4 py-6 text-xs text-gray-400">
        <Loader2 size={14} className="animate-spin" aria-hidden="true" /> Loading trace…
      </div>
    );
  }
  if (error) {
    return (
      <p role="alert" className="px-4 py-4 text-xs text-red-600">
        {error}
      </p>
    );
  }
  if (!trace || trace.phases.length === 0) {
    return (
      <p className="px-4 py-6 text-xs text-gray-400">
        No recorded events for this run. Enable <code className="font-mono">session_event_log</code> to capture a
        replayable timeline.
      </p>
    );
  }

  const longest = Math.max(...trace.phases.map((p) => p.duration_ms), 1);

  return (
    <div className="space-y-4 p-4">
      <dl className="grid grid-cols-2 gap-2 sm:grid-cols-4">
        <Stat icon={<Clock size={13} aria-hidden="true" />} label="Wall time" value={formatDuration(trace.totals.duration_ms)} />
        <Stat icon={<Coins size={13} aria-hidden="true" />} label="Tokens" value={trace.totals.tokens.toLocaleString()} />
        <Stat icon={<Coins size={13} aria-hidden="true" />} label="Cost" value={formatCost(trace.totals.cost_usd)} />
        <Stat
          icon={<AlertTriangle size={13} aria-hidden="true" />}
          label="Problems"
          value={`${trace.totals.errors} err · ${trace.totals.warnings} warn`}
        />
      </dl>

      <table className="w-full text-left text-xs">
        <caption className="sr-only">Per-phase timings and token attribution</caption>
        <thead>
          <tr className="border-b border-gray-200 text-[10px] uppercase tracking-wider text-gray-400 dark:border-gray-800">
            <th scope="col" className="py-1.5 pr-2 font-semibold">Phase</th>
            <th scope="col" className="py-1.5 pr-2 font-semibold">Duration</th>
            <th scope="col" className="hidden py-1.5 pr-2 font-semibold sm:table-cell">Agents</th>
            <th scope="col" className="py-1.5 pr-2 text-right font-semibold">Tokens</th>
            <th scope="col" className="py-1.5 text-right font-semibold">Cost</th>
          </tr>
        </thead>
        <tbody>
          {trace.phases.map((p, i) => (
            <PhaseRow key={`${p.phase}-${i}`} phase={p} longest={longest} />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function PhaseRow({ phase, longest }: { phase: TracePhase; longest: number }) {
  const pct = Math.max(2, Math.round((phase.duration_ms / longest) * 100));
  return (
    <tr className="border-b border-gray-100 align-top dark:border-gray-800/60">
      <th scope="row" className="py-1.5 pr-2 font-mono font-normal">
        <span className="flex items-center gap-1.5">
          <span
            className={clsx('h-2 w-2 shrink-0 rounded-full', PHASE_COLOR[phase.phase] || 'bg-gray-400')}
            aria-hidden="true"
          />
          {phase.phase}
          {(phase.errors ?? 0) > 0 && (
            <AlertTriangle size={11} className="text-red-500" aria-label={`${phase.errors} errors`} />
          )}
          {(phase.tools ?? 0) > 0 && (
            <span className="inline-flex items-center gap-0.5 text-[10px] text-gray-400">
              <Wrench size={9} aria-hidden="true" />
              {phase.tools}
            </span>
          )}
        </span>
      </th>
      <td className="py-1.5 pr-2">
        <span className="block font-mono text-[11px]">{formatDuration(phase.duration_ms)}</span>
        <span className="mt-0.5 block h-1 rounded-full bg-gray-100 dark:bg-gray-800">
          <span
            className={clsx('block h-1 rounded-full', PHASE_COLOR[phase.phase] || 'bg-gray-400')}
            style={{ width: `${pct}%` }}
          />
        </span>
      </td>
      <td className="hidden py-1.5 pr-2 font-mono text-[10px] text-gray-500 sm:table-cell">
        {phase.agents?.join(', ') || '—'}
        {phase.models?.length ? <span className="block opacity-70">{phase.models.join(', ')}</span> : null}
      </td>
      <td className="py-1.5 pr-2 text-right font-mono text-[11px]">
        {phase.tokens ? phase.tokens.toLocaleString() : '—'}
      </td>
      <td className="py-1.5 text-right font-mono text-[11px]">{formatCost(phase.cost_usd ?? 0)}</td>
    </tr>
  );
}

function Stat({ icon, label, value }: { icon: React.ReactNode; label: string; value: string }) {
  return (
    <div className="rounded-lg border border-gray-200 px-3 py-2 dark:border-gray-800">
      <dt className="flex items-center gap-1 text-[10px] uppercase tracking-wider text-gray-400">
        {icon}
        {label}
      </dt>
      <dd className="mt-0.5 font-mono text-sm">{value}</dd>
    </div>
  );
}
