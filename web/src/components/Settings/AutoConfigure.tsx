import { useState } from 'react';
import { Wand2, CheckCircle2, XCircle, Loader2, AlertTriangle } from 'lucide-react';
import clsx from 'clsx';
import { scanForModelServer, applyModelServerConfig, ApiError } from '@/api/client';
import { useToast } from '@/components/ui/Toast';
import type { ConfigureResult } from '@/types';

// ── Finding a model server from the Studio ───────────────────────────────
//
// A user whose endpoint is not answering is looking at a Studio that cannot run
// anything, and the three fields above this panel — provider, endpoint, model —
// are exactly the ones they have no way to fill in correctly by guessing. The
// answer is on their own machine: whatever server is running knows its own
// address and can list what it serves.
//
// Two steps rather than one. Looking is free and reversible; writing is
// neither, and a panel that rewrote the configuration the moment you clicked it
// is one people are afraid to click.

export default function AutoConfigure({ onApplied }: { onApplied?: () => void }) {
  const toast = useToast();
  const [busy, setBusy] = useState<'scan' | 'apply' | null>(null);
  const [result, setResult] = useState<ConfigureResult | null>(null);

  const scan = async () => {
    setBusy('scan');
    try {
      setResult(await scanForModelServer());
    } catch (err) {
      toast.reportError(err, 'Could not look for a model server');
    } finally {
      setBusy(null);
    }
  };

  const apply = async () => {
    setBusy('apply');
    try {
      const res = await applyModelServerConfig();
      setResult(res);
      toast.success('Configured ' + res.choice?.provider, res.choice?.model);
      onApplied?.();
    } catch (err) {
      // A 422 is the harness saying it looked and found nothing usable, which
      // is information rather than a failure — the body says which of the
      // three problems it is.
      if (err instanceof ApiError && err.status === 422) {
        try {
          setResult(JSON.parse(err.body) as ConfigureResult);
        } catch {
          toast.reportError(err, 'Could not configure');
        }
      } else {
        toast.reportError(err, 'Could not configure');
      }
    } finally {
      setBusy(null);
    }
  };

  return (
    <section className="card space-y-3 p-6">
      <div className="mb-1 flex flex-wrap items-center gap-3">
        <Wand2 size={20} className="shrink-0 text-gray-400" aria-hidden="true" />
        <h2 className="font-bold">Find my model server</h2>
        <div className="ml-auto flex gap-2">
          <button type="button" onClick={scan} disabled={busy !== null} className="btn-ghost focus-ring h-8 px-3 text-xs">
            {busy === 'scan' ? <Loader2 size={13} className="animate-spin" /> : null}
            Look around
          </button>
          <button type="button" onClick={apply} disabled={busy !== null} className="btn-primary focus-ring h-8 px-3 text-xs">
            {busy === 'apply' ? <Loader2 size={13} className="animate-spin" /> : null}
            Configure for me
          </button>
        </div>
      </div>
      <p className="text-xs text-gray-500 dark:text-gray-400">
        Checks the endpoint above, then the addresses local model servers listen on (oMLX, Ollama,
        LM Studio, vLLM), then any hosted provider whose API key is already set. Whatever answers is
        asked what it serves, and the model best suited to writing code is chosen — embedding,
        speech and vision models are ruled out. Your API key is only ever sent to the endpoint you
        configured.
      </p>

      {result && !result.ok && result.reason && (
        <p
          role="alert"
          className="flex items-start gap-2 rounded-md border border-amber-300 px-3 py-2 text-xs
                     text-amber-700 dark:border-amber-800 dark:text-amber-300"
        >
          <AlertTriangle size={13} className="mt-px shrink-0" aria-hidden="true" />
          {result.reason}
        </p>
      )}

      {result?.choice && (
        <div
          className={clsx(
            'rounded-md border px-3 py-2 text-xs',
            result.applied
              ? 'border-emerald-300 dark:border-emerald-800'
              : 'border-gray-200 dark:border-gray-800',
          )}
        >
          <div className="mb-1 font-semibold text-gray-800 dark:text-gray-100">
            {result.applied ? 'Configured' : 'Would configure'}
          </div>
          <dl className="grid grid-cols-[auto,1fr] gap-x-3 gap-y-0.5">
            <dt className="text-gray-500">provider</dt>
            <dd className="font-mono">{result.choice.provider}</dd>
            <dt className="text-gray-500">endpoint</dt>
            <dd className="break-all font-mono">{result.choice.endpoint}</dd>
            <dt className="text-gray-500">model</dt>
            <dd className="break-all font-mono">{result.choice.model}</dd>
          </dl>
          <p className="mt-1 text-[11px] text-gray-500 dark:text-gray-400">{result.choice.why}</p>
        </div>
      )}

      {result && result.tried.length > 0 && (
        <ul aria-label="Addresses tried" className="space-y-1">
          {result.tried.map((c) => (
            <li key={c.endpoint} className="flex items-start gap-1.5 text-[11px]">
              {c.live ? (
                <CheckCircle2 size={12} className="mt-0.5 shrink-0 text-emerald-500" aria-hidden="true" />
              ) : (
                <XCircle size={12} className="mt-0.5 shrink-0 text-gray-300 dark:text-gray-600" aria-hidden="true" />
              )}
              <span className="min-w-0 break-all font-mono text-gray-600 dark:text-gray-300">{c.endpoint}</span>
              <span className="text-gray-400">
                {c.live ? `${c.models?.length ?? 0} model(s) · ${c.reason}` : c.error || 'no answer'}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
