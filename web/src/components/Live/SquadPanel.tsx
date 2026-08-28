import { useEffect, useState } from 'react';
import { Users, ArrowRight, AlertTriangle, CheckCircle2, Loader2 } from 'lucide-react';
import clsx from 'clsx';
import { getSquads } from '@/api/client';
import type { SquadStatus, SquadsView } from '@/types';

// ── The org chart, live ──────────────────────────────────────────────────
//
// With two teams running at once, the aggregate "12 of 20 tasks done" hides the
// thing a watcher actually needs: one squad finished twenty minutes ago and the
// other has been blocked since. This shows per-team progress, the frozen
// contract between them, and — the part no task-level view can express — a
// squad stalled on an interface the other has not delivered.
//
// Renders nothing on a single-stream run, which is most of them.

interface Props {
  /** Bumped by the caller to refetch — e.g. the live event count. */
  refreshKey?: number;
}

export default function SquadPanel({ refreshKey = 0 }: Props) {
  const [view, setView] = useState<SquadsView | null>(null);

  useEffect(() => {
    let cancelled = false;
    getSquads()
      .then((v) => {
        if (!cancelled) setView(v);
      })
      .catch(() => {
        // The connection badge already reports API trouble; a squad panel that
        // cannot load is not worth a second error surface.
        if (!cancelled) setView(null);
      });
    return () => {
      cancelled = true;
    };
  }, [refreshKey]);

  const squads = view?.squads ?? [];
  if (!view?.ok || squads.length === 0) return null;

  const stalls = view.stalls ?? [];
  const integration = view.integration;

  return (
    <section className="space-y-2" aria-label="Virtual dev teams">
      <header className="flex flex-wrap items-center gap-2">
        <Users size={13} className="shrink-0 text-brand-500" aria-hidden="true" />
        <h3 className="text-xs font-bold text-gray-800 dark:text-gray-100">Teams</h3>
        {view.summary && (
          <span className="min-w-0 flex-1 truncate text-[11px] text-gray-500 dark:text-gray-400" title={view.summary}>
            {view.summary}
          </span>
        )}
      </header>

      <div className="grid gap-2 [grid-template-columns:repeat(auto-fit,minmax(14rem,1fr))]">
        {squads.map((s) => (
          <SquadCard key={s.id} squad={s} />
        ))}
      </div>

      {/* A consumer blocked on an undelivered interface is not a task defect,
          and retrying its tasks forever is the wrong response. Say which. */}
      {stalls.length > 0 && (
        <ul className="space-y-1">
          {stalls.map((st) => (
            <li
              key={`${st.squad}-${st.interface}`}
              className="flex items-start gap-1.5 rounded-md border border-amber-200 bg-amber-50 px-2 py-1.5 text-[11px] text-amber-900 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-100"
            >
              <AlertTriangle size={12} className="mt-0.5 shrink-0" aria-hidden="true" />
              <span className="min-w-0">
                <strong>{st.squad}</strong> is waiting on <strong>{st.provider}</strong> to deliver{' '}
                <code className="font-mono">{st.interface}</code> — a contract dependency, not a task defect.
              </span>
            </li>
          ))}
        </ul>
      )}

      {(view.interfaces?.length ?? 0) > 0 && (
        <details className="rounded-lg border border-gray-200 dark:border-gray-800">
          <summary className="focus-ring cursor-pointer px-2.5 py-1.5 text-[11px] font-semibold text-gray-600 dark:text-gray-300">
            Frozen contract ({view.interfaces!.length})
          </summary>
          <ul className="space-y-1.5 border-t border-gray-200 px-2.5 py-2 dark:border-gray-800">
            {view.interfaces!.map((i) => (
              <li key={i.id} className="text-[11px]">
                <div className="flex flex-wrap items-center gap-1.5">
                  <code className="font-mono font-semibold text-gray-800 dark:text-gray-100">{i.id}</code>
                  <span className="badge-neutral text-[10px]">{i.provider}</span>
                  {(i.consumers ?? []).length > 0 && (
                    <>
                      <ArrowRight size={10} className="text-gray-400" aria-hidden="true" />
                      {i.consumers!.map((c) => (
                        <span key={c} className="badge-neutral text-[10px]">
                          {c}
                        </span>
                      ))}
                    </>
                  )}
                </div>
                {i.spec && (
                  <pre className="mt-0.5 overflow-x-auto rounded bg-gray-50 px-2 py-1 font-mono text-[10px] text-gray-600 dark:bg-gray-800/60 dark:text-gray-300">
                    {i.spec}
                  </pre>
                )}
              </li>
            ))}
          </ul>
        </details>
      )}

      {integration?.acceptance && (
        <div
          className={clsx(
            'flex items-start gap-1.5 rounded-md border px-2 py-1.5 text-[11px]',
            integration.ready
              ? 'border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950/30 dark:text-emerald-200'
              : 'border-gray-200 text-gray-500 dark:border-gray-800 dark:text-gray-400',
          )}
        >
          {integration.ready ? (
            <CheckCircle2 size={12} className="mt-0.5 shrink-0" aria-hidden="true" />
          ) : (
            <Loader2 size={12} className="mt-0.5 shrink-0" aria-hidden="true" />
          )}
          <span className="min-w-0">
            {integration.ready ? 'Ready to join the halves: ' : `Integration waits — ${integration.reason}. `}
            <code className="font-mono break-all">{integration.acceptance}</code>
          </span>
        </div>
      )}
    </section>
  );
}

function SquadCard({ squad }: { squad: SquadStatus }) {
  const pct = squad.total > 0 ? Math.round((squad.done / squad.total) * 100) : 0;
  const state = squad.total === 0 ? 'idle' : squad.complete ? 'done' : squad.stuck ? 'stuck' : 'working';
  const tone = {
    idle: 'border-gray-200 dark:border-gray-800',
    working: 'border-brand-200 dark:border-brand-900',
    done: 'border-emerald-200 dark:border-emerald-900',
    stuck: 'border-red-200 dark:border-red-900',
  }[state];

  return (
    <div className={clsx('rounded-lg border p-2.5', tone)}>
      <div className="mb-1.5 flex flex-wrap items-center gap-1.5">
        <span className="min-w-0 truncate text-xs font-bold text-gray-800 dark:text-gray-100">
          {squad.name || squad.id}
        </span>
        <span
          className={clsx(
            'badge text-[10px]',
            state === 'done' && 'badge-success',
            state === 'stuck' && 'badge-error',
            state === 'working' && 'badge-brand',
            state === 'idle' && 'badge-neutral',
          )}
        >
          {state}
        </span>
        <span className="ml-auto shrink-0 font-mono text-[10px] text-gray-500 dark:text-gray-400">
          {squad.done}/{squad.total}
        </span>
      </div>

      <div className="h-1.5 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-800">
        <div
          className={clsx(
            'h-full rounded-full transition-[width] duration-500',
            state === 'stuck' ? 'bg-red-500' : state === 'done' ? 'bg-emerald-500' : 'bg-brand-500',
          )}
          style={{ width: `${pct}%` }}
        />
      </div>

      <p className="mt-1.5 truncate text-[10px] text-gray-500 dark:text-gray-400" title={(squad.owns ?? []).join(', ')}>
        {(squad.owns ?? []).join(', ') || 'owns nothing'}
      </p>
      {squad.blocked > 0 && (
        <p className="mt-0.5 text-[10px] font-semibold text-red-600 dark:text-red-400">
          {squad.blocked} blocked
        </p>
      )}
      {squad.acceptance && (
        <p className="mt-0.5 truncate font-mono text-[10px] text-gray-400" title={squad.acceptance}>
          {squad.acceptance}
        </p>
      )}
    </div>
  );
}
