import { useCallback, useMemo, useState } from 'react';
import { Plus, Trash2, Undo2, Users, ListChecks, AlertTriangle } from 'lucide-react';
import clsx from 'clsx';
import type { PlanAsk, PlanApprovalTask, PlanEdits, PlanSquad } from '@/types';
import { buildEdits, countChanges, type TaskDraft } from './planEdits';

// ── Editing the proposed plan before approving it ────────────────────────
//
// The gate offered approve or replan. Replan throws the whole board away and
// pays for another planning pass to fix one wrong file path, so people approved
// plans they could see were slightly wrong and let the run find out.
//
// This edits a DRAFT and emits a diff. Two consequences that shape the whole
// component:
//
//   • only fields the user actually changed are sent, so the harness can tell
//     "I did not touch this" from "set this to empty" — the Go side keys off
//     exactly that distinction (pointers plus `*_set` flags);
//   • the role picker offers the agents the ASK carried, not a hardcoded list.
//     Naming an agent the harness cannot dispatch produces a task that never
//     starts, and the harness is the only thing that knows the real roster.

interface Props {
  ask: PlanAsk;
  /** Called with the current diff whenever the draft changes. */
  onChange: (edits: PlanEdits) => void;
  disabled?: boolean;
}

export default function PlanEditor({ ask, onChange, disabled }: Props) {
  const original = useMemo<PlanApprovalTask[]>(() => ask.task_details ?? [], [ask.task_details]);
  const originalSquads = useMemo<PlanSquad[]>(() => ask.squads?.squads ?? [], [ask.squads]);

  const [tasks, setTasks] = useState<TaskDraft[]>(() => original.map((t) => ({ ...t })));
  const [squads, setSquads] = useState<PlanSquad[]>(() => originalSquads.map((s) => ({ ...s })));

  const agents = ask.agents ?? [];
  const managers = ask.managers ?? [];
  const squadIds = squads.map((s) => s.id);

  // The diff is recomputed from both drafts on every change rather than
  // accumulated: an accumulated diff cannot represent "edited, then edited
  // back", and would send a no-op edit the harness has to reason about.
  const emit = useCallback(
    (nextTasks: TaskDraft[], nextSquads: PlanSquad[]) => {
      onChange(buildEdits(original, nextTasks, originalSquads, nextSquads));
    },
    [onChange, original, originalSquads],
  );

  const patchTask = (id: string, patch: Partial<TaskDraft>) => {
    setTasks((prev) => {
      const next = prev.map((t) => (t.id === id ? { ...t, ...patch } : t));
      emit(next, squads);
      return next;
    });
  };

  const patchSquad = (id: string, patch: Partial<PlanSquad>) => {
    setSquads((prev) => {
      const next = prev.map((s) => (s.id === id ? { ...s, ...patch } : s));
      emit(tasks, next);
      return next;
    });
  };

  const toggleRemoved = (id: string) => {
    setTasks((prev) => {
      const next = prev.map((t) => (t.id === id ? { ...t, _removed: !t._removed } : t));
      emit(next, squads);
      return next;
    });
  };

  const addTask = () => {
    setTasks((prev) => {
      const next = [
        ...prev,
        {
          id: `NEW-${prev.length + 1}`,
          title: '',
          role: agents.includes('worker') ? 'worker' : agents[0] || 'worker',
          description: '',
          files: [],
        } as TaskDraft,
      ];
      emit(next, squads);
      return next;
    });
  };

  const changed = countChanges(original, tasks, originalSquads, squads);

  return (
    <div className="space-y-3">
      <header className="flex flex-wrap items-center gap-2">
        <ListChecks size={14} className="shrink-0 text-fuchsia-500" aria-hidden="true" />
        <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">Adjust before approving</h3>
        <span className={clsx('badge text-[10px]', changed > 0 ? 'badge-brand' : 'badge-neutral')}>
          {changed === 0 ? 'no changes' : `${changed} change${changed === 1 ? '' : 's'}`}
        </span>
        <button
          type="button"
          onClick={addTask}
          disabled={disabled}
          className="btn-ghost focus-ring ml-auto h-7 gap-1 px-2 text-[11px]"
        >
          <Plus size={12} aria-hidden="true" />
          Add task
        </button>
      </header>

      {/* Teams first: ownership decides which squad a task can belong to, so
          seeing the org chart before the task list is the order the decisions
          are actually made in. */}
      {squads.length > 0 && (
        <section className="space-y-2">
          <div className="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">
            <Users size={12} aria-hidden="true" />
            Teams
          </div>
          {squads.map((s) => (
            <div key={s.id} className="rounded-lg border border-gray-200 p-2.5 dark:border-gray-800">
              <div className="mb-2 flex flex-wrap items-center gap-2">
                <span className="badge-brand text-[10px]">{s.id}</span>
                <input
                  value={s.name ?? ''}
                  onChange={(e) => patchSquad(s.id, { name: e.target.value })}
                  disabled={disabled}
                  aria-label={`Name of squad ${s.id}`}
                  className="input h-7 min-w-0 flex-1 text-xs"
                  placeholder="Team name"
                />
                <span className="badge-neutral shrink-0 text-[10px]">
                  {s.task_count} task{s.task_count === 1 ? '' : 's'}
                </span>
              </div>
              <label htmlFor={`squad-${s.id}-owns`} className="label mb-1 block text-[10px]">
                {`Owns — ${s.id}`} (one glob per line — teams may not overlap)
              </label>
              <textarea
                id={`squad-${s.id}-owns`}
                value={(s.owns ?? []).join('\n')}
                onChange={(e) => patchSquad(s.id, { owns: splitLines(e.target.value) })}
                disabled={disabled}
                rows={Math.min(4, Math.max(2, (s.owns ?? []).length))}
                className="input-mono w-full text-[11px]"
                placeholder="web/**"
              />
              <div className="mt-2 grid gap-2 [grid-template-columns:repeat(auto-fit,minmax(11rem,1fr))]">
                <div>
                  <label htmlFor={`squad-${s.id}-acceptance`} className="label mb-1 block text-[10px]">
                    {`Acceptance — ${s.id}`}
                  </label>
                  <input
                    id={`squad-${s.id}-acceptance`}
                    value={s.acceptance ?? ''}
                    onChange={(e) => patchSquad(s.id, { acceptance: e.target.value })}
                    disabled={disabled}
                    className="input-mono h-7 w-full text-[11px]"
                    placeholder="go test ./..."
                  />
                </div>
                <AgentPicker
                  id={`squad-${s.id}-worker`}
                  label={`Worker — ${s.id}`}
                  value={s.worker ?? ''}
                  agents={agents}
                  disabled={disabled}
                  allowEmpty
                  onChange={(v) => patchSquad(s.id, { worker: v })}
                />
                {/* Who decides where a rejected delivery goes next. A narrower
                    roster than the worker's: only agents that answer the triage
                    contract can produce a verdict the harness can read. */}
                <AgentPicker
                  id={`squad-${s.id}-manager`}
                  label={`Project manager — ${s.id}`}
                  value={s.manager ?? ''}
                  agents={managers}
                  disabled={disabled}
                  allowEmpty
                  onChange={(v) => patchSquad(s.id, { manager: v })}
                />
              </div>
            </div>
          ))}
        </section>
      )}

      <section className="space-y-2">
        <div className="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">
          <ListChecks size={12} aria-hidden="true" />
          Tasks
        </div>
        {tasks.length === 0 && (
          <p className="rounded-md border border-dashed border-gray-200 px-3 py-4 text-center text-xs text-gray-400 dark:border-gray-800">
            The plan has no structured tasks to edit.
          </p>
        )}
        {tasks.map((t) => (
          <div
            key={t.id}
            className={clsx(
              'rounded-lg border p-2.5 transition-opacity',
              t._removed
                ? 'border-red-200 bg-red-50/50 opacity-60 dark:border-red-900 dark:bg-red-950/20'
                : 'border-gray-200 dark:border-gray-800',
            )}
          >
            <div className="mb-2 flex flex-wrap items-center gap-2">
              <span className="badge-neutral shrink-0 text-[10px]">{t.id}</span>
              <input
                value={t.title}
                onChange={(e) => patchTask(t.id, { title: e.target.value })}
                disabled={disabled || t._removed}
                aria-label={`Title of task ${t.id}`}
                className="input h-7 min-w-0 flex-1 text-xs"
                placeholder="What this task does"
              />
              <button
                type="button"
                onClick={() => toggleRemoved(t.id)}
                disabled={disabled}
                aria-label={t._removed ? `Keep task ${t.id}` : `Remove task ${t.id}`}
                className="btn-ghost focus-ring h-7 shrink-0 gap-1 px-2 text-[11px]"
              >
                {t._removed ? <Undo2 size={12} aria-hidden="true" /> : <Trash2 size={12} aria-hidden="true" />}
                {t._removed ? 'Keep' : 'Remove'}
              </button>
            </div>

            {!t._removed && (
              <div className="grid gap-2 [grid-template-columns:repeat(auto-fit,minmax(11rem,1fr))]">
                <AgentPicker
                  id={`task-${t.id}-role`}
                  label={`Agent — ${t.id}`}
                  value={t.role ?? ''}
                  agents={agents}
                  disabled={disabled}
                  onChange={(v) => patchTask(t.id, { role: v })}
                />
                {squadIds.length > 0 && (
                  <div>
                    <label htmlFor={`task-${t.id}-squad`} className="label mb-1 block text-[10px]">
                      {`Team — ${t.id}`}
                    </label>
                    <select
                      id={`task-${t.id}-squad`}
                      value={t.squad ?? ''}
                      onChange={(e) => patchTask(t.id, { squad: e.target.value })}
                      disabled={disabled}
                      aria-label={`Team for task ${t.id}`}
                      className="input h-7 w-full text-[11px]"
                    >
                      <option value="">unassigned</option>
                      {squadIds.map((id) => (
                        <option key={id} value={id}>
                          {id}
                        </option>
                      ))}
                    </select>
                  </div>
                )}
                <div className="col-span-full">
                  <label htmlFor={`task-${t.id}-files`} className="label mb-1 block text-[10px]">
                    {`Files — ${t.id}`} (one per line)
                  </label>
                  <textarea
                    id={`task-${t.id}-files`}
                    value={(t.files ?? []).join('\n')}
                    onChange={(e) => patchTask(t.id, { files: splitLines(e.target.value) })}
                    disabled={disabled}
                    rows={Math.min(4, Math.max(2, (t.files ?? []).length))}
                    className="input-mono w-full text-[11px]"
                    placeholder="cmd/server/main.go"
                  />
                </div>
              </div>
            )}
          </div>
        ))}
      </section>

      {squads.length > 0 && (
        <p className="flex items-start gap-1.5 text-[11px] text-gray-500 dark:text-gray-400">
          <AlertTriangle size={12} className="mt-0.5 shrink-0" aria-hidden="true" />
          <span>
            Two teams may never own the same path. An overlapping edit is refused whole and the
            original org chart is kept — the run will say so.
          </span>
        </p>
      )}
    </div>
  );
}

/**
 * AgentPicker offers only agents the harness reported it can dispatch.
 *
 * It renders its own label and owns the id that binds them: a label wrapped
 * around a custom component associates with nothing a screen reader can find,
 * and no linter can see through the component boundary to tell you so.
 */
function AgentPicker({
  id,
  label,
  value,
  agents,
  onChange,
  disabled,
  allowEmpty,
}: {
  id: string;
  label: string;
  value: string;
  agents: string[];
  onChange: (v: string) => void;
  disabled?: boolean;
  allowEmpty?: boolean;
}) {
  // A role already on the task that is not in the roster still has to be
  // selectable, or opening the editor would silently rewrite it.
  const options = value && !agents.includes(value) ? [value, ...agents] : agents;
  return (
    <div>
      <label htmlFor={id} className="label mb-1 block text-[10px]">
        {label}
      </label>
      {options.length === 0 ? (
        <input
          id={id}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled}
          className="input h-7 w-full text-[11px]"
        />
      ) : (
        <select
          id={id}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled}
          className="input h-7 w-full text-[11px]"
        >
          {allowEmpty && <option value="">pipeline default</option>}
          {options.map((a) => (
            <option key={a} value={a}>
              {a}
            </option>
          ))}
        </select>
      )}
    </div>
  );
}

function splitLines(v: string): string[] {
  return v
    .split('\n')
    .map((s) => s.trim())
    .filter(Boolean);
}
