import { useEffect, useMemo, useState } from 'react';
import { Activity, AlertTriangle, ChevronDown, ChevronRight, Cpu, Gauge, RefreshCw, Ruler } from 'lucide-react';
import { getCalibration } from '@/api/client';
import type { CalibrationView } from '@/types';
import clsx from 'clsx';

// What this model was MEASURED to do, and the budgets derived from it.
//
// Studio is where the model gets switched, so it is where "why is the skill
// budget 1,024?" gets asked. The panel answers with evidence rather than a
// number: the probe's own concurrency ladder, the window and how it was
// learned, and the rendered report that names every budget the measurement did
// NOT get to set because the configuration pinned it.
//
// It never triggers a probe. Measurement happens at studio startup and before a
// run (ensureCalibrated); a refresh button that could spend a minute of GPU is
// a button that gets clicked twice.
export default function CalibrationPanel({ refreshKey = '' }: { refreshKey?: string }) {
  const [view, setView] = useState<CalibrationView | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [showReport, setShowReport] = useState(false);

  const load = async () => {
    setLoading(true);
    setError('');
    try {
      setView(await getCalibration());
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load calibration');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void load();
  }, [refreshKey]);

  // The knee is the concurrency level the probe stopped at. Marking it in the
  // ladder is what turns a column of numbers into a decision the user can
  // disagree with.
  const knee = view?.max_parallel ?? 0;
  const levels = useMemo(() => view?.levels || [], [view]);

  const state = calibrationState(view);

  return (
    <section className="card p-6 space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <div className={clsx(
            'w-10 h-10 rounded-xl flex items-center justify-center',
            state.tone === 'ok' ? 'bg-emerald-100 dark:bg-emerald-900/30'
              : state.tone === 'warn' ? 'bg-amber-100 dark:bg-amber-900/30'
                : 'bg-gray-100 dark:bg-gray-800',
          )}>
            <Ruler size={20} className={
              state.tone === 'ok' ? 'text-emerald-600'
                : state.tone === 'warn' ? 'text-amber-600' : 'text-gray-500'
            } />
          </div>
          <div>
            <h2 className="text-lg font-bold">Model Calibration</h2>
            <p className="text-sm text-gray-500">
              {view?.model ? `${view.model} · ${view.provider}` : 'Measured limits and the budgets derived from them'}
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <span className={clsx(
            state.tone === 'ok' ? 'badge-success' : state.tone === 'warn' ? 'badge-warn' : 'badge-neutral',
          )}>
            {state.label}
          </span>
          <button className="btn-secondary px-2 py-1 text-xs" onClick={() => void load()} disabled={loading} title="Re-read the stored measurement">
            <RefreshCw size={16} className={clsx(loading && 'animate-spin')} />
          </button>
        </div>
      </div>

      {error && (
        <div className="flex items-center gap-2 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300">
          <AlertTriangle size={16} />
          {error}
        </div>
      )}

      {view && !view.present && (
        <div className="rounded-lg border border-dashed border-gray-300 px-4 py-6 text-center dark:border-gray-700">
          <Cpu size={22} className="mx-auto mb-2 text-gray-400" />
          <p className="text-sm font-medium">This model has not been measured yet</p>
          <p className="mt-1 text-xs text-gray-500">
            Calibration starts automatically on the first run — the limits below are static defaults until then.
          </p>
        </div>
      )}

      {view?.present && (
        <>
          <div className="grid gap-3 md:grid-cols-4">
            <Metric label="Context window" value={fmtInt(view.context_limit)} sub={view.context_source || 'measured'} />
            <Metric label="Concurrency knee" value={String(view.max_parallel || 1)} sub="parallel calls before throughput flattens" />
            <Metric label="Latency" value={`${fmtInt(view.p50_ms)} ms`} sub={`p95 ${fmtInt(view.p95_ms)} ms`} />
            <Metric label="Throughput" value={`${(view.tokens_per_sec || 0).toFixed(1)} tok/s`} sub={view.queue_inflation ? `${view.queue_inflation.toFixed(2)}× under queue` : 'single stream'} />
          </div>

          {levels.length > 0 && (
            <div>
              <h3 className="mb-2 flex items-center gap-2 text-sm font-semibold">
                <Activity size={14} className="text-gray-400" />
                Concurrency ladder
              </h3>
              <div className="overflow-x-auto rounded-lg border border-gray-200 dark:border-gray-800">
                <table className="w-full text-sm tabular-nums">
                  <thead className="bg-gray-50 text-xs uppercase tracking-wide text-gray-500 dark:bg-gray-900/50">
                    <tr>
                      <th className="px-3 py-2 text-left font-medium">Parallel</th>
                      <th className="px-3 py-2 text-right font-medium">Efficiency</th>
                      <th className="px-3 py-2 text-right font-medium">Throughput</th>
                    </tr>
                  </thead>
                  <tbody>
                    {levels.map((l) => (
                      <tr
                        key={l.concurrency}
                        className={clsx(
                          'border-t border-gray-100 dark:border-gray-800',
                          l.concurrency === knee && 'bg-emerald-50/60 font-medium dark:bg-emerald-950/20',
                        )}
                      >
                        <td className="px-3 py-1.5">
                          {l.concurrency}
                          {l.concurrency === knee && <span className="ml-2 badge-success text-[10px]">chosen</span>}
                        </td>
                        <td className="px-3 py-1.5 text-right">{(l.efficiency * 100).toFixed(0)}%</td>
                        <td className="px-3 py-1.5 text-right">{l.throughput.toFixed(2)} tok/s</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </>
      )}

      {view && (
        <div>
          <h3 className="mb-2 flex items-center gap-2 text-sm font-semibold">
            <Gauge size={14} className="text-gray-400" />
            Budgets in force
          </h3>
          <div className="grid gap-2 md:grid-cols-3">
            <Budget label="Context limit" value={view.budgets.context_limit} />
            <Budget label="Max tokens" value={view.budgets.max_tokens} />
            <Budget label="Thinking budget" value={view.budgets.thinking_budget_tokens} />
            <Budget label="Skills" value={view.budgets.skill_token_budget} />
            <Budget label="Knowledge" value={view.budgets.knowledge_token_budget} />
            <Budget label="Max turns" value={view.budgets.max_turns} />
          </div>
        </div>
      )}

      {view?.report && (
        <div>
          <button
            className="flex items-center gap-1.5 text-sm font-semibold hover:text-brand-600"
            onClick={() => setShowReport((v) => !v)}
          >
            {showReport ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
            Evidence report
          </button>
          {showReport && (
            <pre className="mt-2 max-h-96 overflow-auto rounded-lg bg-gray-50 p-3 text-xs leading-relaxed dark:bg-gray-900/50">
              {view.report}
            </pre>
          )}
        </div>
      )}
    </section>
  );
}

// calibrationState folds present/current/partial into one badge.
//
// "Stale" and "partial" are deliberately different words. A stale profile was
// measured properly and has aged out; a partial one ran out of probe budget, so
// its knee may be an underestimate and it expires in an hour rather than a
// month. Collapsing them would hide which number to distrust.
function calibrationState(v: CalibrationView | null): { label: string; tone: 'ok' | 'warn' | 'idle' } {
  if (!v) return { label: 'loading', tone: 'idle' };
  if (!v.present) return { label: 'not measured', tone: 'idle' };
  if (v.partial) return { label: 'partial probe', tone: 'warn' };
  if (!v.current) return { label: 'stale · remeasured next run', tone: 'warn' };
  return { label: `measured ${fmtAge(v.age_seconds)}`, tone: 'ok' };
}

function fmtAge(seconds?: number): string {
  const s = seconds ?? 0;
  if (s < 90) return 'just now';
  if (s < 5400) return `${Math.round(s / 60)}m ago`;
  if (s < 172800) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}

function fmtInt(n?: number): string {
  return (n ?? 0).toLocaleString('en-US');
}

function Metric({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="rounded-lg border border-gray-200 px-3 py-2 dark:border-gray-800">
      <div className="text-[11px] uppercase tracking-wide text-gray-500">{label}</div>
      <div className="truncate text-base font-semibold tabular-nums" title={value}>{value}</div>
      {sub && <div className="truncate text-xs text-gray-500" title={sub}>{sub}</div>}
    </div>
  );
}

function Budget({ label, value }: { label: string; value: number }) {
  return (
    <div className="flex items-center justify-between rounded-lg border border-gray-200 px-3 py-1.5 text-sm dark:border-gray-800">
      <span className="text-gray-500">{label}</span>
      <span className="font-semibold tabular-nums">{fmtInt(value)}</span>
    </div>
  );
}
