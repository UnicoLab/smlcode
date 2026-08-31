import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Save,
  RotateCcw,
  AlertTriangle,
  ArrowRight,
  ShieldCheck,
  Trash2,
  Undo2,
  Plus,
  Users,
} from 'lucide-react';
import clsx from 'clsx';
import { patchSquads, ApiError } from '@/api/client';
import { useToast } from '@/components/ui/Toast';
import { buildSquadEdits, countSquadEdits, type InterfaceDraft } from './squadEdits';
import MultiPicker from './MultiPicker';
import type { PlanInterface, Skill, SquadStatus, SquadsView, TeamSpec } from '@/types';

// ── The org chart this project runs with ─────────────────────────────────
//
// Everything here outlives the run that proposed it: what is saved is what the
// NEXT run inherits, and what a run in flight is executing against.
//
// Two rules shape the whole component:
//
//   • Ownership must be disjoint. Two teams sharing a path means two agents
//     writing one file in parallel and one edit silently gone. An overlapping
//     edit is refused WHOLE, and this page's job is to say which pair collided —
//     "refused" tells the user nothing they can act on.
//   • The contract is frozen text both halves build against without being able
//     to ask each other anything. A wrong route here is the one mistake a
//     two-team run cannot recover from, so it has to be correctable by hand.

export interface ActiveTeamsProps {
  view: SquadsView;
  /** The library, so a team can be ADDED to the chart rather than retyped. */
  library: TeamSpec[];
  agents: string[];
  managers: string[];
  /** Installed skills, so a team's pack list is picked rather than typed. */
  skills?: Skill[];
  onSaved: () => void;
}

export default function ActiveTeams({ view, library, agents, managers, skills = [], onSaved }: ActiveTeamsProps) {
  const toast = useToast();
  const original = useMemo(() => view.squads ?? [], [view.squads]);
  const originalInterfaces = useMemo(() => view.interfaces ?? [], [view.interfaces]);

  const [draft, setDraft] = useState<SquadStatus[]>(() => original.map((s) => ({ ...s })));
  const [ifaces, setIfaces] = useState<InterfaceDraft[]>(() => toDrafts(originalInterfaces));
  const [problems, setProblems] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);

  const reset = useCallback(() => {
    setDraft(original.map((s) => ({ ...s })));
    setIfaces(toDrafts(originalInterfaces));
    setProblems([]);
  }, [original, originalInterfaces]);

  // A reload that brought new server state must not leave a stale draft on
  // screen claiming to be dirty against it.
  useEffect(() => {
    reset();
  }, [reset]);

  const edits = buildSquadEdits(original, draft, originalInterfaces, ifaces);
  const changes = countSquadEdits(edits);

  const patch = (id: string, next: Partial<SquadStatus>) => {
    setDraft((prev) => prev.map((s) => (s.id === id ? { ...s, ...next } : s)));
    setProblems([]);
  };

  const removeSquad = (id: string) => {
    setDraft((prev) => prev.filter((s) => s.id !== id));
    // A clause whose provider is gone is owed by nobody. Dropping it with the
    // team keeps the user from having to discover that from a refusal.
    setIfaces((prev) =>
      prev.map((i) => (i.provider === id ? { ...i, _removed: true } : { ...i, consumers: (i.consumers ?? []).filter((c) => c !== id) })),
    );
    setProblems([]);
  };

  const addFromLibrary = (id: string) => {
    const t = library.find((x) => x.id === id);
    if (!t || draft.some((s) => s.id === id)) return;
    setDraft((prev) => [
      ...prev,
      {
        id: t.id,
        name: t.name,
        charter: t.charter,
        owns: t.owns ?? [],
        acceptance: t.acceptance,
        worker: t.worker,
        reviewer: t.reviewer,
        tester: t.tester,
        manager: t.manager,
        agents: t.agents ?? [],
        skills: t.skills ?? [],
        total: 0,
        done: 0,
        blocked: 0,
        in_flight: 0,
        complete: false,
        stuck: false,
      },
    ]);
    setProblems([]);
  };

  const patchIface = (key: string, next: Partial<InterfaceDraft>) => {
    setIfaces((prev) => prev.map((i) => (i._key === key ? { ...i, ...next } : i)));
    setProblems([]);
  };

  const addIface = () => {
    setIfaces((prev) => [
      ...prev,
      {
        _key: `new-${prev.length + 1}-${Date.now()}`,
        _new: true,
        id: '',
        provider: draft[0]?.id ?? '',
        consumers: [],
        spec: '',
      },
    ]);
  };

  const save = async () => {
    setSaving(true);
    setProblems([]);
    try {
      const res = await patchSquads(edits);
      toast.success(res.summary || 'Teams updated');
      onSaved();
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

  const addable = library.filter((t) => !draft.some((s) => s.id === t.id));

  return (
    <section className="space-y-3">
      <header className="flex flex-wrap items-center gap-2">
        <Users size={14} className="shrink-0 text-brand-500" aria-hidden="true" />
        <h2 className="text-sm font-bold text-gray-900 dark:text-gray-100">This project&rsquo;s org chart</h2>
        {view.summary && (
          <span className="min-w-0 flex-1 truncate text-[11px] text-gray-500 dark:text-gray-400">{view.summary}</span>
        )}
        {addable.length > 0 && (
          <label className="flex items-center gap-1 text-[11px] text-gray-500 dark:text-gray-400">
            <span className="sr-only">Add a team from the library</span>
            <select
              value=""
              onChange={(e) => e.target.value && addFromLibrary(e.target.value)}
              aria-label="Add a team from the library"
              className="input h-8 text-xs"
            >
              <option value="">Add team…</option>
              {addable.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name || t.id}
                </option>
              ))}
            </select>
          </label>
        )}
        <button
          type="button"
          onClick={reset}
          disabled={changes === 0 || saving}
          className="btn-ghost focus-ring h-8 gap-1.5 px-2.5 text-xs"
        >
          <RotateCcw size={13} aria-hidden="true" />
          Revert
        </button>
        <button
          type="button"
          onClick={save}
          disabled={changes === 0 || saving}
          className="btn-primary focus-ring h-8 gap-1.5 px-3 text-xs"
        >
          <Save size={13} aria-hidden="true" />
          {saving ? 'Saving…' : `Save${changes ? ` (${changes})` : ''}`}
        </button>
      </header>

      {problems.length > 0 && (
        <div
          role="alert"
          className="rounded-lg border border-red-300 bg-red-50 px-3 py-2.5 dark:border-red-800 dark:bg-red-950/30"
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

      {(view.stalls ?? []).length > 0 && (
        <ul className="space-y-1">
          {view.stalls!.map((s) => (
            <li
              key={`${s.squad}-${s.interface}`}
              className="rounded-md bg-amber-50 px-2.5 py-1.5 text-[11px] text-amber-800 dark:bg-amber-950/40 dark:text-amber-200"
            >
              <span className="font-mono font-semibold">{s.squad}</span> is waiting on{' '}
              <span className="font-mono">{s.interface}</span> from{' '}
              <span className="font-mono">{s.provider}</span> — not a defect in its own work
            </li>
          ))}
        </ul>
      )}

      <div className="grid gap-3 [grid-template-columns:repeat(auto-fill,minmax(21rem,1fr))]">
        {draft.map((s) => (
          <TeamCard
            key={s.id}
            squad={s}
            agents={agents}
            managers={managers}
            skills={skills}
            disabled={saving}
            onChange={(n) => patch(s.id, n)}
            onRemove={() => removeSquad(s.id)}
          />
        ))}
      </div>

      <section>
        <div className="mb-2 flex flex-wrap items-center gap-1.5">
          <ShieldCheck size={13} className="text-brand-500" aria-hidden="true" />
          <h3 className="text-xs font-bold text-gray-800 dark:text-gray-100">Frozen contract</h3>
          <button
            type="button"
            onClick={addIface}
            disabled={draft.length === 0 || saving}
            className="btn-ghost focus-ring ml-auto h-7 gap-1 px-2 text-[11px]"
          >
            <Plus size={11} aria-hidden="true" />
            Add interface
          </button>
        </div>
        <p className="mb-2 text-[11px] text-gray-500 dark:text-gray-400">
          What the teams owe each other, agreed before either starts. Both halves build against this
          text and neither can ask the other later — so a wrong route here is the one mistake a
          two-team run cannot recover from. Renaming a clause keeps its spec.
        </p>
        {ifaces.length === 0 && (
          <p className="rounded-md border border-dashed border-gray-200 px-3 py-4 text-center text-[11px] text-amber-700 dark:border-gray-800 dark:text-amber-300">
            Nothing frozen. With no contract the teams each invent their own version of the seam,
            and integration is where you find out.
          </p>
        )}
        <ul className="space-y-2">
          {ifaces.map((i) => (
            <InterfaceRow
              key={i._key}
              iface={i}
              squadIds={draft.map((s) => s.id)}
              disabled={saving}
              onChange={(n) => patchIface(i._key, n)}
              onToggleRemoved={() => patchIface(i._key, { _removed: !i._removed })}
            />
          ))}
        </ul>
      </section>

      {view.integration && (
        <p className="text-[11px] text-gray-500 dark:text-gray-400">
          Integration gate:{' '}
          {view.integration.acceptance ? (
            <code className="font-mono">{view.integration.acceptance}</code>
          ) : (
            <span className="text-amber-700 dark:text-amber-400">
              none — every team can be green with the assembled application still broken
            </span>
          )}
          {view.integration.reason && <> · {view.integration.reason}</>}
        </p>
      )}
    </section>
  );
}

function TeamCard({
  squad,
  agents,
  managers,
  skills,
  disabled,
  onChange,
  onRemove,
}: {
  squad: SquadStatus;
  agents: string[];
  managers: string[];
  skills: Skill[];
  disabled?: boolean;
  onChange: (next: Partial<SquadStatus>) => void;
  onRemove: () => void;
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
        <button
          type="button"
          onClick={onRemove}
          disabled={disabled}
          aria-label={`Remove team ${squad.id}`}
          title="Remove this team from the org chart"
          className="btn-ghost focus-ring h-7 shrink-0 px-1.5 text-red-600 dark:text-red-400"
        >
          <Trash2 size={12} aria-hidden="true" />
        </button>
      </div>
      <div className="mb-2 h-1 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-800">
        <div className="h-full rounded-full bg-brand-500" style={{ width: `${pct}%` }} />
      </div>

      <label htmlFor={`team-${squad.id}-charter`} className="label mb-1 block text-[10px]">
        {`Charter — ${squad.id}`}
      </label>
      <textarea
        id={`team-${squad.id}-charter`}
        value={squad.charter ?? ''}
        onChange={(e) => onChange({ charter: e.target.value })}
        disabled={disabled}
        rows={2}
        className="input w-full text-[11px]"
      />

      <label htmlFor={`team-${squad.id}-owns`} className="label mb-1 mt-2 block text-[10px]">
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

      <div className="mt-2 grid gap-2 [grid-template-columns:repeat(auto-fit,minmax(9rem,1fr))]">
        <RolePicker
          id={`team-${squad.id}-worker`}
          label={`Worker — ${squad.id}`}
          value={squad.worker ?? ''}
          options={agents}
          disabled={disabled}
          onChange={(v) => onChange({ worker: v })}
        />
        <RolePicker
          id={`team-${squad.id}-reviewer`}
          label={`Reviewer — ${squad.id}`}
          value={squad.reviewer ?? ''}
          options={agents}
          disabled={disabled}
          onChange={(v) => onChange({ reviewer: v })}
        />
        <RolePicker
          id={`team-${squad.id}-tester`}
          label={`Tester — ${squad.id}`}
          value={squad.tester ?? ''}
          options={agents}
          disabled={disabled}
          onChange={(v) => onChange({ tester: v })}
        />
        <RolePicker
          id={`team-${squad.id}-manager`}
          label={`Project manager — ${squad.id}`}
          value={squad.manager ?? ''}
          options={managers}
          disabled={disabled}
          onChange={(v) => onChange({ manager: v })}
        />
      </div>

      {/* The rest of the team, in whatever number it needs. Its members are
          listed first when this team's manager triages a rejected delivery. */}
      <div className="mt-2 space-y-2">
        <MultiPicker
          id={`team-${squad.id}-roster`}
          label={`Also on ${squad.id}`}
          addLabel="Add agent"
          emptyLabel="just the seats above"
          options={agents}
          value={squad.agents ?? []}
          onChange={(next) => onChange({ agents: next })}
          disabled={disabled}
        />
        <MultiPicker
          id={`team-${squad.id}-skills`}
          label={`Skills — ${squad.id}`}
          addLabel="Add skill"
          emptyLabel="none pinned"
          options={skills.map((sk) => sk.name)}
          value={squad.skills ?? []}
          describe={(name) => skills.find((sk) => sk.name === name)?.description}
          onChange={(next) => onChange({ skills: next })}
          disabled={disabled}
        />
      </div>

      {squad.blocked > 0 && (
        <p className="mt-1 text-[11px] font-semibold text-red-600 dark:text-red-400">{squad.blocked} blocked</p>
      )}
    </div>
  );
}

function InterfaceRow({
  iface,
  squadIds,
  disabled,
  onChange,
  onToggleRemoved,
}: {
  iface: InterfaceDraft;
  squadIds: string[];
  disabled?: boolean;
  onChange: (next: Partial<InterfaceDraft>) => void;
  onToggleRemoved: () => void;
}) {
  const toggleConsumer = (id: string) => {
    const have = iface.consumers ?? [];
    onChange({ consumers: have.includes(id) ? have.filter((c) => c !== id) : [...have, id] });
  };
  return (
    <li
      className={clsx(
        'rounded-lg border p-2.5 transition-opacity',
        iface._removed
          ? 'border-red-200 bg-red-50/50 opacity-60 dark:border-red-900 dark:bg-red-950/20'
          : 'border-gray-200 dark:border-gray-800',
      )}
    >
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <input
          value={iface.id}
          onChange={(e) => onChange({ id: e.target.value })}
          disabled={disabled || iface._removed}
          aria-label={`Interface name${iface._new ? '' : ` (was ${iface._key})`}`}
          placeholder="GET /api/todos"
          className="input-mono h-7 min-w-0 flex-1 text-[11px]"
        />
        <label className="flex items-center gap-1 text-[10px] text-gray-500">
          <span className="sr-only">Provider of {iface.id || 'this interface'}</span>
          <select
            value={iface.provider ?? ''}
            onChange={(e) => onChange({ provider: e.target.value })}
            disabled={disabled || iface._removed}
            aria-label={`Provider of ${iface.id || 'this interface'}`}
            className="input h-7 text-[11px]"
          >
            <option value="">provider…</option>
            {squadIds.map((id) => (
              <option key={id} value={id}>
                {id}
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          onClick={onToggleRemoved}
          disabled={disabled}
          aria-label={iface._removed ? `Keep ${iface.id}` : `Remove ${iface.id}`}
          className="btn-ghost focus-ring h-7 shrink-0 gap-1 px-2 text-[11px]"
        >
          {iface._removed ? <Undo2 size={11} aria-hidden="true" /> : <Trash2 size={11} aria-hidden="true" />}
          {iface._removed ? 'Keep' : 'Remove'}
        </button>
      </div>

      {!iface._removed && (
        <>
          <div className="mb-1.5 flex flex-wrap items-center gap-1.5">
            <ArrowRight size={11} className="text-gray-400" aria-hidden="true" />
            <span className="text-[10px] text-gray-500 dark:text-gray-400">consumed by</span>
            {squadIds
              .filter((id) => id !== iface.provider)
              .map((id) => (
                <button
                  key={id}
                  type="button"
                  onClick={() => toggleConsumer(id)}
                  disabled={disabled}
                  aria-pressed={(iface.consumers ?? []).includes(id)}
                  className={clsx(
                    'focus-ring rounded px-1.5 py-0.5 text-[10px]',
                    (iface.consumers ?? []).includes(id)
                      ? 'bg-brand-500 text-white'
                      : 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300',
                  )}
                >
                  {id}
                </button>
              ))}
          </div>
          <textarea
            value={iface.spec ?? ''}
            onChange={(e) => onChange({ spec: e.target.value })}
            disabled={disabled}
            rows={2}
            aria-label={`Spec for ${iface.id || 'this interface'}`}
            placeholder={'200 -> [{"id":string,"title":string,"done":bool}]'}
            className="input-mono w-full text-[11px]"
          />
        </>
      )}
    </li>
  );
}

function RolePicker({
  id,
  label,
  value,
  options,
  disabled,
  onChange,
}: {
  id: string;
  label: string;
  value: string;
  options: string[];
  disabled?: boolean;
  onChange: (v: string) => void;
}) {
  // A value the factory no longer registers stays selectable. Dropping it would
  // silently rewrite the user's choice on the next save.
  const missing = value !== '' && !options.includes(value);
  return (
    <div>
      <label htmlFor={id} className="label mb-1 block text-[10px]">
        {label}
      </label>
      <select
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className="input h-7 w-full text-[11px]"
      >
        <option value="">pipeline default</option>
        {missing && <option value={value}>{value} (not registered)</option>}
        {options.map((o) => (
          <option key={o} value={o}>
            {o}
          </option>
        ))}
      </select>
    </div>
  );
}

function toDrafts(list: PlanInterface[]): InterfaceDraft[] {
  return list.map((i) => ({ ...i, _key: i.id, consumers: [...(i.consumers ?? [])] }));
}
