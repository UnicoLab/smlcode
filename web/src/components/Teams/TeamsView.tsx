import { useCallback, useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { Users, Save, RotateCcw, AlertTriangle, ArrowRight, ShieldCheck } from 'lucide-react';
import { getSquads, patchSquads, ApiError } from '@/api/client';
import { useToast } from '@/components/ui/Toast';
import { buildSquadEdits } from './squadEdits';
import type { SquadStatus, SquadsView } from '@/types';

// ── The Teams page ───────────────────────────────────────────────────────
//
// The rail's Teams tab answers "how are the teams doing right now". This page
// answers "are the teams right at all" — a different question, asked at a
// different time, with a consequence the rail does not have: a team structure
// outlives the run that proposed it, so the ownership boundaries and staffing
// edited here are what the NEXT run inherits.
//
// The one rule it cannot let a user break is disjoint ownership. Two teams
// sharing a path means two agents writing one file in parallel and one edit
// silently disappearing. The harness refuses such an edit whole; this page's job
// is to show WHY, because "refused" tells the user nothing and "backend and
// frontend both claim web/**" tells them exactly what to change.

export default function TeamsView() {
  const toast = useToast();
  const [view, setView] = useState<SquadsView | null>(null);
  const [draft, setDraft] = useState<SquadStatus[]>([]);
  const [problems, setProblems] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const v = await getSquads();
      setView(v);
      setDraft((v.squads ?? []).map((s) => ({ ...s })));
      setLoadError(null);
    } catch (err) {
      setLoadError(err instanceof ApiError ? err.displayMessage : 'Could not load the teams.');
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const original = view?.squads ?? [];
  const edits = buildSquadEdits(original, draft);
  const dirty = edits.length > 0;

  const patch = (id: string, next: Partial<SquadStatus>) => {
    setDraft((prev) => prev.map((s) => (s.id === id ? { ...s, ...next } : s)));
    setProblems([]);
  };

  const save = async () => {
    setSaving(true);
    setProblems([]);
    try {
      const res = await patchSquads(edits);
      toast.success(res.summary || 'Teams updated');
      await load();
    } catch (err) {
      if (err instanceof ApiError && err.problems.length > 0) {
        setProblems(err.problems);
      } else {
        toast.reportError(err, 'Could not save the teams');
      }
    } finally {
      setSaving(false);
    }
  };

  if (loadError) {
    return (
      <Shell>
        <p className="text-sm text-red-600 dark:text-red-400">{loadError}</p>
      </Shell>
    );
  }
  if (!view) {
    return (
      <Shell>
        <p className="text-sm text-gray-400">Loading teams…</p>
      </Shell>
    );
  }
  if (!view.ok || original.length === 0) {
    return (
      <Shell>
        <div className="rounded-lg border border-dashed border-gray-300 px-6 py-10 text-center dark:border-gray-700">
          <Users size={28} className="mx-auto mb-3 text-gray-300 dark:text-gray-600" aria-hidden="true" />
          <h2 className="text-sm font-semibold text-gray-700 dark:text-gray-200">No teams assembled</h2>
          <p className="mx-auto mt-1 max-w-md text-xs text-gray-500 dark:text-gray-400">
            Squads assemble when a request genuinely has two halves different people could build at
            the same time — a backend and a frontend, a service and its CLI. A single-domain request
            runs as one stream, which is most of them.
          </p>
        </div>
      </Shell>
    );
  }

  return (
    <Shell>
      <header className="mb-4 flex flex-wrap items-center gap-2">
        <Users size={16} className="shrink-0 text-brand-500" aria-hidden="true" />
        <h1 className="text-base font-bold text-gray-900 dark:text-gray-100">Teams</h1>
        {view.summary && (
          <span className="min-w-0 flex-1 truncate text-xs text-gray-500 dark:text-gray-400">{view.summary}</span>
        )}
        <button
          type="button"
          onClick={() => {
            setDraft(original.map((s) => ({ ...s })));
            setProblems([]);
          }}
          disabled={!dirty || saving}
          className="btn-ghost focus-ring h-8 gap-1.5 px-2.5 text-xs"
        >
          <RotateCcw size={13} aria-hidden="true" />
          Revert
        </button>
        <button
          type="button"
          onClick={save}
          disabled={!dirty || saving}
          className="btn-primary focus-ring h-8 gap-1.5 px-3 text-xs"
        >
          <Save size={13} aria-hidden="true" />
          {saving ? 'Saving…' : `Save${dirty ? ` (${edits.length})` : ''}`}
        </button>
      </header>

      {problems.length > 0 && (
        <div
          role="alert"
          className="mb-4 rounded-lg border border-red-300 bg-red-50 px-3 py-2.5 dark:border-red-800 dark:bg-red-950/30"
        >
          <div className="flex items-center gap-1.5 text-xs font-semibold text-red-700 dark:text-red-300">
            <AlertTriangle size={13} aria-hidden="true" />
            These teams cannot run — nothing was saved
          </div>
          <ul className="mt-1.5 space-y-1">
            {problems.map((p) => (
              <li key={p} className="text-[11px] text-red-700 dark:text-red-300">
                {p}
              </li>
            ))}
          </ul>
        </div>
      )}

      <div className="grid gap-3 [grid-template-columns:repeat(auto-fit,minmax(20rem,1fr))]">
        {draft.map((s) => (
          <TeamCard
            key={s.id}
            squad={s}
            managers={view.managers ?? []}
            onChange={(n) => patch(s.id, n)}
            disabled={saving}
          />
        ))}
      </div>

      {(view.interfaces?.length ?? 0) > 0 && (
        <section className="mt-5">
          <h2 className="mb-2 flex items-center gap-1.5 text-xs font-bold text-gray-800 dark:text-gray-100">
            <ShieldCheck size={13} className="text-brand-500" aria-hidden="true" />
            Frozen contract
          </h2>
          <p className="mb-2 text-[11px] text-gray-500 dark:text-gray-400">
            What the teams owe each other. Both halves build against this text, agreed before either
            starts — and a provider that drifts from it fails its own acceptance criteria, not just
            integration.
          </p>
          <ul className="space-y-2">
            {view.interfaces!.map((i) => (
              <li key={i.id} className="rounded-lg border border-gray-200 p-2.5 dark:border-gray-800">
                <div className="flex flex-wrap items-center gap-1.5">
                  <code className="font-mono text-xs font-semibold text-gray-800 dark:text-gray-100">{i.id}</code>
                  <span className="badge-brand text-[10px]">{i.provider}</span>
                  {(i.consumers ?? []).length > 0 && (
                    <>
                      <ArrowRight size={11} className="text-gray-400" aria-hidden="true" />
                      {i.consumers!.map((c) => (
                        <span key={c} className="badge-neutral text-[10px]">
                          {c}
                        </span>
                      ))}
                    </>
                  )}
                </div>
                {i.spec && (
                  <pre className="mt-1 overflow-x-auto rounded bg-gray-50 px-2 py-1 font-mono text-[11px] text-gray-600 dark:bg-gray-800/60 dark:text-gray-300">
                    {i.spec}
                  </pre>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}
    </Shell>
  );
}

function Shell({ children }: { children: ReactNode }) {
  return <div className="mx-auto h-full w-full max-w-[1600px] overflow-auto p-4">{children}</div>;
}

function TeamCard({
  squad,
  managers,
  onChange,
  disabled,
}: {
  squad: SquadStatus;
  managers: string[];
  onChange: (next: Partial<SquadStatus>) => void;
  disabled?: boolean;
}) {
  const pct = squad.total > 0 ? Math.round((squad.done / squad.total) * 100) : 0;
  return (
    <div className="rounded-lg border border-gray-200 p-3 dark:border-gray-800">
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <span className="badge-brand shrink-0 text-[10px]">{squad.id}</span>
        <input
          value={squad.name ?? ''}
          onChange={(e) => onChange({ name: e.target.value })}
          disabled={disabled}
          aria-label={`Name of team ${squad.id}`}
          className="input h-7 min-w-0 flex-1 text-xs"
        />
        <span className="shrink-0 font-mono text-[10px] text-gray-500">
          {squad.done}/{squad.total}
        </span>
      </div>
      <div className="mb-2 h-1 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-800">
        <div className="h-full rounded-full bg-brand-500" style={{ width: `${pct}%` }} />
      </div>

      <label htmlFor={`team-${squad.id}-owns`} className="label mb-1 block text-[10px]">
        {`Owns — ${squad.id}`} (one glob per line; teams may never share a path)
      </label>
      <textarea
        id={`team-${squad.id}-owns`}
        value={(squad.owns ?? []).join('\n')}
        onChange={(e) =>
          onChange({ owns: e.target.value.split('\n').map((v) => v.trim()).filter(Boolean) })
        }
        disabled={disabled}
        rows={Math.min(5, Math.max(2, (squad.owns ?? []).length))}
        className="input-mono w-full text-[11px]"
      />

      <label htmlFor={`team-${squad.id}-acceptance`} className="label mb-1 mt-2 block text-[10px]">
        {`Acceptance — ${squad.id}`} (proves this half works on its own)
      </label>
      <input
        id={`team-${squad.id}-acceptance`}
        value={squad.acceptance ?? ''}
        onChange={(e) => onChange({ acceptance: e.target.value })}
        disabled={disabled}
        className="input-mono h-7 w-full text-[11px]"
      />

      <label htmlFor={`team-${squad.id}-manager`} className="label mb-1 mt-2 block text-[10px]">
        {`Project manager — ${squad.id}`} (decides who takes a rejected delivery)
      </label>
      <select
        id={`team-${squad.id}-manager`}
        value={squad.manager ?? ''}
        onChange={(e) => onChange({ manager: e.target.value })}
        disabled={disabled}
        className="input h-7 w-full text-[11px]"
      >
        <option value="">Run default</option>
        {managers.map((m) => (
          <option key={m} value={m}>
            {m}
          </option>
        ))}
        {/* A plan can name a manager the factory no longer registers. Showing
            it is how the user finds out; dropping it would silently rewrite
            their choice on the next save. */}
        {squad.manager && !managers.includes(squad.manager) && (
          <option value={squad.manager}>{squad.manager} (not registered)</option>
        )}
      </select>

      {squad.charter && <p className="mt-2 text-[11px] text-gray-500 dark:text-gray-400">{squad.charter}</p>}
      {squad.blocked > 0 && (
        <p className="mt-1 text-[11px] font-semibold text-red-600 dark:text-red-400">{squad.blocked} blocked</p>
      )}
    </div>
  );
}
