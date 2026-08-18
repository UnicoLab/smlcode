import { useContext, useEffect, useMemo, useState } from 'react';
import { AlertTriangle, CheckCircle2, Gauge, RefreshCw, ShieldCheck, Wand2, XCircle } from 'lucide-react';
import { AppContext } from '@/App';
import { getReadiness, updateConfig } from '@/api/client';
import type { ConfigPatch, ReadinessCheck, ReadinessReport } from '@/types';
import clsx from 'clsx';

export default function ReadinessPanel({ refreshKey = '' }: { refreshKey?: string }) {
  const ctx = useContext(AppContext);
  const [report, setReport] = useState<ReadinessReport | null>(null);
  const [loading, setLoading] = useState(true);
  const [applying, setApplying] = useState('');
  const [error, setError] = useState('');

  const load = async () => {
    setLoading(true);
    setError('');
    try {
      setReport(await getReadiness());
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load readiness');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
  }, [refreshKey]);

  const failed = useMemo(() => (report?.checks || []).filter((c) => !c.ok), [report]);
  const fixable = useMemo(() => failed.filter((c) => c.fix_patch && Object.keys(c.fix_patch).length > 0), [failed]);
  const score = report?.score ?? 0;

  const applyPatch = async (label: string, patch: ConfigPatch) => {
    setApplying(label);
    setError('');
    try {
      await updateConfig(patch);
      await ctx?.refresh?.();
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to apply readiness fix');
    } finally {
      setApplying('');
    }
  };

  const applyAll = async () => {
    const patch = mergeFixes(fixable);
    if (Object.keys(patch).length === 0) return;
    await applyPatch('all', patch);
  };

  return (
    <section className="card p-6 space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <div className={clsx(
            'w-10 h-10 rounded-xl flex items-center justify-center',
            score >= 90 ? 'bg-emerald-100 dark:bg-emerald-900/30' : score >= 75 ? 'bg-amber-100 dark:bg-amber-900/30' : 'bg-red-100 dark:bg-red-900/30',
          )}>
            <Gauge size={20} className={score >= 90 ? 'text-emerald-600' : score >= 75 ? 'text-amber-600' : 'text-red-600'} />
          </div>
          <div>
            <h2 className="text-lg font-bold">SLM Readiness</h2>
            <p className="text-sm text-gray-500">Model profile, guardrails, and traceability state</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {report && (
            <span className={clsx(
              score >= 90 ? 'badge-success' : score >= 75 ? 'badge-warn' : 'badge-error',
            )}>
              {score}/100 · {report.status}
            </span>
          )}
          {fixable.length > 1 && (
            <button className="btn-secondary text-xs gap-2" onClick={applyAll} disabled={!!applying}>
              <Wand2 size={14} />
              {applying === 'all' ? 'Applying' : 'Fix All'}
            </button>
          )}
          <button className="btn-ghost p-2" onClick={load} disabled={loading} title="Refresh readiness">
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

      {report && (
        <>
          <div className="grid gap-3 md:grid-cols-3">
            <Metric label="Model" value={report.model || 'unset'} sub={`${report.provider} · ${report.backend}`} />
            <Metric label="Profile" value={`${report.model_profile.context_limit} ctx`} sub={`${report.model_profile.max_tokens} max · ${report.model_profile.max_turns} turns`} />
            <Metric label="Stack" value={report.active_stack || 'manual'} sub={report.active_pack || report.active_pipeline || 'no pack pinned'} />
          </div>

          <div className="grid gap-2 md:grid-cols-2">
            {report.checks.map((check) => (
              <div
                key={check.id}
                className={clsx(
                  'rounded-lg border px-3 py-2',
                  check.ok
                    ? 'border-emerald-200 bg-emerald-50/60 dark:border-emerald-900 dark:bg-emerald-950/20'
                    : check.severity === 'critical'
                      ? 'border-red-200 bg-red-50/70 dark:border-red-900 dark:bg-red-950/20'
                      : 'border-amber-200 bg-amber-50/70 dark:border-amber-900 dark:bg-amber-950/20',
                )}
              >
                <div className="flex items-center gap-2">
                  {check.ok ? (
                    <CheckCircle2 size={15} className="text-emerald-500 shrink-0" />
                  ) : check.severity === 'critical' ? (
                    <XCircle size={15} className="text-red-500 shrink-0" />
                  ) : (
                    <AlertTriangle size={15} className="text-amber-500 shrink-0" />
                  )}
                  <span className="text-sm font-semibold">{check.label}</span>
                  {!check.ok && <span className="ml-auto text-[10px] uppercase tracking-wider text-gray-400">{check.severity}</span>}
                </div>
                <p className="mt-1 text-xs text-gray-600 dark:text-gray-400">{check.message}</p>
                {(check.endpoint || typeof check.latency_ms === 'number' || check.fix_hint) && (
                  <div className="mt-2 space-y-1 text-[11px] text-gray-500 dark:text-gray-400">
                    {check.endpoint && <div className="truncate font-mono">{check.endpoint}</div>}
                    {typeof check.latency_ms === 'number' && <div>{check.latency_ms} ms</div>}
                    {check.fix_hint && <div>{check.fix_hint}</div>}
                  </div>
                )}
                {!check.ok && check.fix_patch && Object.keys(check.fix_patch).length > 0 && (
                  <button
                    className="btn-secondary mt-2 px-2 py-1 text-xs"
                    onClick={() => applyPatch(check.id, check.fix_patch || {})}
                    disabled={!!applying}
                  >
                    <Wand2 size={13} />
                    {applying === check.id ? 'Applying' : check.fix_label || 'Apply Fix'}
                  </button>
                )}
              </div>
            ))}
          </div>

          {failed.length === 0 ? (
            <div className="flex items-center gap-2 text-sm text-emerald-600 dark:text-emerald-400">
              <ShieldCheck size={16} />
              Production guardrails are active for local-model runs.
            </div>
          ) : (
            <div className="flex items-center gap-2 text-sm text-amber-600 dark:text-amber-400">
              <AlertTriangle size={16} />
              {failed.length} readiness check{failed.length === 1 ? '' : 's'} need attention.
            </div>
          )}
        </>
      )}
    </section>
  );
}

function mergeFixes(checks: ReadinessCheck[]): ConfigPatch {
  const out: ConfigPatch = {};
  for (const check of checks) {
    Object.assign(out, check.fix_patch || {});
  }
  return out;
}

function Metric({ label, value, sub }: { label: string; value: string; sub: string }) {
  return (
    <div className="rounded-lg border border-gray-200 bg-gray-50 p-3 dark:border-gray-800 dark:bg-gray-800/50">
      <div className="text-[10px] uppercase tracking-wider text-gray-400">{label}</div>
      <div className="mt-1 truncate text-sm font-semibold text-gray-900 dark:text-gray-100">{value}</div>
      <div className="mt-0.5 truncate text-xs text-gray-500">{sub}</div>
    </div>
  );
}
