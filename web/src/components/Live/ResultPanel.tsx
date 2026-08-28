import clsx from 'clsx';
import { CheckCircle2, Wrench } from 'lucide-react';
import type { LatestRunResponse } from '@/types';

// ── The screen people read last ──────────────────────────────────────────
//
// It used to say only success/failed, a summary line and three counters. A run
// that hit two defects and fixed both looked identical to one where nothing
// happened — after a stream full of loud red failures. Either the user
// concludes the failures were swallowed, or that the panel is not to be
// trusted, and the repair line is where the run's own resilience becomes
// visible instead.

export default function ResultPanel({ result }: { result: LatestRunResponse | null }) {
  if (!result?.result) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2 px-6 text-center text-gray-400">
        <CheckCircle2 size={28} className="opacity-40" />
        <p className="text-sm">No result yet</p>
        <p className="text-xs">A summary appears here when the run finishes.</p>
      </div>
    );
  }
  const r = result.result;
  const seconds = r.duration > 1e9 ? `${(r.duration / 1e9).toFixed(1)}s` : `${(r.duration / 1e6).toFixed(0)}ms`;
  return (
    <div className="h-full space-y-3 overflow-auto p-4">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-bold text-gray-800 dark:text-gray-100">Result</h3>
        <span className={clsx('badge text-[10px]', r.success ? 'badge-success' : 'badge-error')}>
          {r.success ? 'Success' : 'Failed'}
        </span>
      </div>
      {r.summary && <p className="text-sm text-gray-600 dark:text-gray-300">{r.summary}</p>}
      {r.repairs && r.repairs.found > 0 && (
        <p
          className={clsx(
            'flex items-start gap-1.5 rounded-md border px-2 py-1.5 text-xs',
            r.repairs.needs_human > 0
              ? 'border-amber-300 text-amber-700 dark:border-amber-800 dark:text-amber-300'
              : 'border-emerald-300 text-emerald-700 dark:border-emerald-800 dark:text-emerald-300',
          )}
        >
          <Wrench size={13} className="mt-px shrink-0" aria-hidden="true" />
          <span>
            {r.repairs.resolved === r.repairs.found
              ? `Fixed ${r.repairs.found === 1 ? 'the 1 defect' : `all ${r.repairs.found} defects`} without you`
              : `${r.repairs.resolved} of ${r.repairs.found} defects fixed · ${r.repairs.found - r.repairs.resolved} still open`}
            {r.repairs.restaffed > 0 && ` · ${r.repairs.restaffed} reassigned by the project manager`}
          </span>
        </p>
      )}
      <div className="grid grid-cols-3 gap-2">
        <Stat label="Failed" value={String(r.failed_tasks)} bad={r.failed_tasks > 0} />
        <Stat label="Duration" value={seconds} />
        <Stat label="Events" value={String(result.events?.length ?? 0)} />
      </div>
    </div>
  );
}

function Stat({ label, value, bad }: { label: string; value: string; bad?: boolean }) {
  return (
    <div className="rounded-md border border-gray-200 bg-gray-50 px-2 py-1.5 dark:border-gray-800 dark:bg-gray-900">
      <div className="text-[10px] font-semibold uppercase tracking-wide text-gray-400">{label}</div>
      <div
        className={clsx(
          'mt-0.5 font-mono text-sm font-bold tabular-nums',
          bad ? 'text-rose-500' : 'text-gray-800 dark:text-gray-100',
        )}
      >
        {value}
      </div>
    </div>
  );
}
