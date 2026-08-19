import { useContext, useEffect, useMemo, useState } from 'react';
import { AlertTriangle, CheckCircle2, Gauge, RefreshCw, Server, ShieldCheck, Terminal, Wand2, XCircle } from 'lucide-react';
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
  const [lastProbe, setLastProbe] = useState(true);

  const load = async (probe = true) => {
    setLoading(true);
    setError('');
    try {
      setReport(await getReadiness({ probe }));
      setLastProbe(probe);
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
  const providerCheck = useMemo(() => report?.checks.find((c) => c.id === 'provider_model'), [report]);
  const score = report?.score ?? 0;

  const applyPatch = async (label: string, patch: ConfigPatch) => {
    setApplying(label);
    setError('');
    try {
      await updateConfig(patch);
      await ctx?.refresh?.();
      await load(lastProbe);
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
          {report && (
            <span className={clsx('text-[10px]', lastProbe ? 'badge-brand' : 'badge-neutral')}>
              {lastProbe ? 'live probe' : 'config only'}
            </span>
          )}
          <button className="btn-ghost px-2 py-1 text-xs" onClick={() => load(false)} disabled={loading} title="Skip endpoint probe">
            Config Only
          </button>
          <button className="btn-secondary px-2 py-1 text-xs" onClick={() => load(true)} disabled={loading} title="Probe runtime and refresh readiness">
            <RefreshCw size={16} className={clsx(loading && 'animate-spin')} />
            Probe Runtime
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

          <LocalRunRoute
            report={report}
            providerCheck={providerCheck}
            failed={failed}
            fixable={fixable}
            applying={applying}
            lastProbe={lastProbe}
            onApplyAll={applyAll}
          />

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

function LocalRunRoute({
  report,
  providerCheck,
  failed,
  fixable,
  applying,
  lastProbe,
  onApplyAll,
}: {
  report: ReadinessReport;
  providerCheck?: ReadinessCheck;
  failed: ReadinessCheck[];
  fixable: ReadinessCheck[];
  applying: string;
  lastProbe: boolean;
  onApplyAll: () => void;
}) {
  const critical = failed.filter((c) => c.severity === 'critical');
  const warnings = failed.length - critical.length;
  const local = isLocalProvider(report.provider);
  const availableModels = Number(providerCheck?.details?.models_count || 0);
  const parallelBudget = report.checks.find((c) => c.id === 'parallel_budget');
  const maxParallel = detailNumber(parallelBudget, 'max_parallel');
  const recommendedParallel = detailNumber(parallelBudget, 'recommended_max_parallel');
  const runtimeStatus = providerCheck?.ok
    ? 'Connected'
    : providerCheck
      ? 'Needs runtime'
      : lastProbe
        ? 'Probe pending'
        : 'Config only';
  const guardrailStatus = critical.length === 0
    ? warnings === 0
      ? 'Ready'
      : `${warnings} warning${warnings === 1 ? '' : 's'}`
    : `${critical.length} blocker${critical.length === 1 ? '' : 's'}`;
  const commands = providerCommands(report.provider);

  return (
    <div className="rounded-lg border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-gray-900/50">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2">
            <Server size={16} className={providerCheck?.ok ? 'text-emerald-500' : 'text-amber-500'} />
            <h3 className="text-sm font-bold">Local Run Route</h3>
            <span className={clsx(
              'text-[10px]',
              providerCheck?.ok ? 'badge-success' : critical.length > 0 ? 'badge-error' : 'badge-warn',
            )}>
              {runtimeStatus}
            </span>
          </div>
          <p className="mt-1 text-xs text-gray-500">
            {local
              ? 'Local provider path with live endpoint probe and harness guardrails.'
              : 'Cloud-compatible provider path with the same SLM guardrails.'}
          </p>
        </div>
        {fixable.length > 0 && (
          <button className="btn-secondary text-xs gap-2" onClick={onApplyAll} disabled={!!applying}>
            <Wand2 size={14} />
            {applying === 'all' ? 'Applying' : `Apply ${fixable.length} fix${fixable.length === 1 ? '' : 'es'}`}
          </button>
        )}
      </div>

      <div className="mt-4 grid gap-3 md:grid-cols-5">
        <RouteMetric label="Provider" value={report.provider || 'unset'} sub={local ? 'local runtime' : 'remote runtime'} ok={!!report.provider} />
        <RouteMetric
          label="Endpoint"
          value={shortEndpoint(providerCheck?.endpoint || report.endpoint || 'unset')}
          sub={typeof providerCheck?.latency_ms === 'number' ? `${providerCheck.latency_ms} ms` : lastProbe ? 'probe pending' : 'not probed'}
          ok={!!providerCheck?.ok}
        />
        <RouteMetric
          label="Model"
          value={report.model || 'unset'}
          sub={availableModels > 0 ? `${availableModels} listed` : providerCheck?.message || 'not listed yet'}
          ok={!!providerCheck?.ok}
        />
        <RouteMetric
          label="Concurrency"
          value={maxParallel > 0 ? `${maxParallel} worker${maxParallel === 1 ? '' : 's'}` : 'default'}
          sub={recommendedParallel > 0 ? `recommended <=${recommendedParallel}` : 'profile budget'}
          ok={parallelBudget ? parallelBudget.ok : true}
        />
        <RouteMetric label="Guardrails" value={guardrailStatus} sub={`${report.status} · ${report.score}/100`} ok={critical.length === 0} />
      </div>

      {(providerCheck?.fix_hint || commands.length > 0 || critical.length > 0 || parallelBudget?.fix_hint) && (
        <div className="mt-4 grid gap-3 md:grid-cols-2">
          <div className="rounded-lg border border-gray-200 bg-gray-50 p-3 dark:border-gray-800 dark:bg-gray-800/40">
            <div className="mb-2 flex items-center gap-2 text-xs font-semibold text-gray-700 dark:text-gray-200">
              <Terminal size={14} />
              Runtime Checks
            </div>
            <div className="space-y-1 text-xs text-gray-600 dark:text-gray-400">
              {providerCheck?.fix_hint && <p>{providerCheck.fix_hint}</p>}
              {commands.map((cmd) => (
                <code key={cmd} className="block truncate rounded border border-gray-200 bg-white px-2 py-1 font-mono text-[11px] text-gray-700 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-200">
                  {cmd}
                </code>
              ))}
              {!providerCheck?.fix_hint && commands.length === 0 && <p>Endpoint probe succeeded.</p>}
            </div>
          </div>
          <div className="rounded-lg border border-gray-200 bg-gray-50 p-3 dark:border-gray-800 dark:bg-gray-800/40">
            <div className="mb-2 flex items-center gap-2 text-xs font-semibold text-gray-700 dark:text-gray-200">
              <ShieldCheck size={14} />
              Execution Gate
            </div>
            {critical.length === 0 ? (
              <div className="space-y-2 text-xs text-gray-600 dark:text-gray-400">
                <p>Critical execution guardrails are active; warnings can be handled before long runs.</p>
                {parallelBudget && !parallelBudget.ok && (
                  <p className="rounded-md border border-amber-200 bg-amber-50 px-2 py-1.5 text-amber-900 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-100">
                    {parallelBudget.fix_hint || parallelBudget.message}
                  </p>
                )}
              </div>
            ) : (
              <ul className="space-y-1 text-xs text-gray-600 dark:text-gray-400">
                {critical.map((check) => (
                  <li key={check.id} className="flex items-start gap-2">
                    <XCircle size={13} className="mt-0.5 shrink-0 text-red-500" />
                    <span>{check.label}: {check.message}</span>
                  </li>
                ))}
                {parallelBudget && !parallelBudget.ok && (
                  <li className="flex items-start gap-2">
                    <AlertTriangle size={13} className="mt-0.5 shrink-0 text-amber-500" />
                    <span>{parallelBudget.label}: {parallelBudget.message}</span>
                  </li>
                )}
              </ul>
            )}
          </div>
        </div>
      )}
    </div>
  );
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

function RouteMetric({ label, value, sub, ok }: { label: string; value: string; sub: string; ok: boolean }) {
  return (
    <div className="min-w-0">
      <div className="flex items-center gap-1.5 text-[10px] uppercase tracking-wider text-gray-400">
        <span className={clsx('h-1.5 w-1.5 rounded-full', ok ? 'bg-emerald-500' : 'bg-amber-500')} />
        {label}
      </div>
      <div className="mt-1 truncate text-sm font-semibold text-gray-900 dark:text-gray-100">{value}</div>
      <div className="mt-0.5 truncate text-xs text-gray-500">{sub}</div>
    </div>
  );
}

function isLocalProvider(provider: string): boolean {
  return ['ollama', 'lmstudio', 'omlx', 'vllm', 'llamacpp', 'local'].includes(provider.trim().toLowerCase());
}

function providerCommands(provider: string): string[] {
  switch (provider.trim().toLowerCase()) {
    case 'ollama':
      return ['ollama list', 'ollama serve'];
    case 'lmstudio':
      return ['LM Studio: Developer > Start Server', 'curl http://127.0.0.1:1234/v1/models'];
    case 'omlx':
      return ['omlx serve --host 127.0.0.1 --port 8000', 'curl http://127.0.0.1:8000/v1/models'];
    case 'vllm':
      return ['python -m vllm.entrypoints.openai.api_server --model <model>', 'curl http://127.0.0.1:8000/v1/models'];
    default:
      return [];
  }
}

function detailNumber(check: ReadinessCheck | undefined, key: string): number {
  const value = check?.details?.[key];
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

function shortEndpoint(endpoint: string): string {
  return endpoint.replace(/^https?:\/\//, '').replace(/\/$/, '') || 'unset';
}
