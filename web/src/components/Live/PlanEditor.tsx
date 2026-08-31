import { useCallback, useMemo, useState } from 'react';
import {
  Plus,
  Trash2,
  Undo2,
  Users,
  ListChecks,
  AlertTriangle,
  ChevronRight,
  ShieldCheck,
  ArrowRight,
} from 'lucide-react';
import clsx from 'clsx';
import MultiPicker from '@/components/Teams/MultiPicker';
import type { PlanAsk, PlanApprovalTask, PlanEdits, PlanSquad } from '@/types';
import {
  buildEdits,
  countChanges,
  type InterfaceDraft,
  type PlanDraft,
  type PlanOriginal,
  type SquadDraft,
  type TaskDraft,
} from './planEdits';

// ── Editing the proposed plan before approving it ────────────────────────
//
// The gate offered approve or replan. Replan throws the whole board away and
// pays for another planning pass to fix one wrong file path, so people approved
// plans they could see were slightly wrong and let the run find out.
//
// This edits a DRAFT and emits a diff. Everything the harness can apply is
// editable here, because a field that is visible and not editable is worse than
// one that is hidden — it shows the user the mistake and gives them no way to
// fix it:
//
//   tasks       title, description, agent, team, files, acceptance, priority,
//               and DEPENDS ON, which is the only real way to say "later": the
//               board dispatches waves, and later means "after these finished".
//               Tasks can be added (including ones others then wait on) and
//               removed.
//   teams       name, charter, owns, acceptance, worker, reviewer, manager.
//               Teams can be ADDED from the saved library or removed outright.
//   contract    the frozen interfaces — the one artifact a two-team run cannot
//               recover from getting wrong, because both halves build against
//               it and neither can ask the other later.
//
// Two invariants shape the diff:
//
//   • only fields the user actually changed are sent, so the harness can tell
//     "I did not touch this" from "set this to empty" — the Go side keys off
//     exactly that distinction (pointers plus `*_set` flags);
//   • every picker offers what the ASK carried, not a hardcoded list. Naming an
//     agent the harness cannot dispatch produces a task that never starts, and
//     the harness is the only thing that knows the real roster.

/**
 * EXPAND_ALL_UP_TO is where a task list stops being readable fully expanded.
 *
 * Small-model plans are usually 3–8 tasks, and for those the editor should show
 * everything without a click — asking someone to expand each of five cards to
 * discover that description and dependencies are editable is how a feature ends
 * up believed not to exist.
 */
const EXPAND_ALL_UP_TO = 8;

interface Props {
  ask: PlanAsk;
  /** Called with the current diff whenever the draft changes. */
  onChange: (edits: PlanEdits) => void;
  disabled?: boolean;
}

export default function PlanEditor({ ask, onChange, disabled }: Props) {
  const original = useMemo<PlanOriginal>(
    () => ({
      tasks: ask.task_details ?? [],
      squads: ask.squads?.squads ?? [],
      interfaces: ask.squads?.interfaces ?? [],
    }),
    [ask.task_details, ask.squads],
  );

  const [tasks, setTasks] = useState<TaskDraft[]>(() => original.tasks.map((t) => ({ ...t })));
  const [squads, setSquads] = useState<SquadDraft[]>(() => original.squads.map((s) => ({ ...s })));
  const [interfaces, setInterfaces] = useState<InterfaceDraft[]>(() =>
    original.interfaces.map((i) => ({ ...i, _key: i.id, consumers: [...(i.consumers ?? [])] })),
  );

  const agents = ask.agents ?? [];
  const managers = ask.managers ?? [];
  const library = ask.library ?? [];
  const liveSquads = squads.filter((s) => !s._removed);
  const squadIds = liveSquads.map((s) => s.id);

  // The diff is recomputed from the drafts on every change rather than
  // accumulated: an accumulated diff cannot represent "edited, then edited
  // back", and would send a no-op edit the harness has to reason about.
  const emit = useCallback(
    (next: Partial<PlanDraft>) => {
      const draft: PlanDraft = {
        tasks: next.tasks ?? tasks,
        squads: next.squads ?? squads,
        interfaces: next.interfaces ?? interfaces,
      };
      onChange(buildEdits(original, draft));
    },
    [onChange, original, tasks, squads, interfaces],
  );

  const patchTask = (id: string, patch: Partial<TaskDraft>) => {
    setTasks((prev) => {
      const next = prev.map((t) => (t.id === id ? { ...t, ...patch } : t));
      emit({ tasks: next });
      return next;
    });
  };

  const toggleRemovedTask = (id: string) => {
    setTasks((prev) => {
      const next = prev.map((t) => (t.id === id ? { ...t, _removed: !t._removed } : t));
      emit({ tasks: next });
      return next;
    });
  };

  const addTask = () => {
    setTasks((prev) => {
      // The placeholder id is a client reference the harness resolves after the
      // board assigns a real one — which is what lets an existing task depend
      // on this one before it exists.
      const next: TaskDraft[] = [
        ...prev,
        {
          id: nextNewID(prev),
          title: '',
          role: agents.includes('worker') ? 'worker' : agents[0] || 'worker',
          description: '',
          files: [],
          depends_on: [],
          _new: true,
        },
      ];
      emit({ tasks: next });
      return next;
    });
  };

  const patchSquad = (id: string, patch: Partial<SquadDraft>) => {
    setSquads((prev) => {
      const next = prev.map((s) => (s.id === id ? { ...s, ...patch } : s));
      emit({ squads: next });
      return next;
    });
  };

  const toggleRemovedSquad = (id: string) => {
    setSquads((prev) => {
      const next = prev.map((s) => (s.id === id ? { ...s, _removed: !s._removed } : s));
      emit({ squads: next });
      return next;
    });
    // Tasks belonging to a removed team keep their squad field until the user
    // reassigns them; the harness leaves an unowned task in the normal lane,
    // which is the honest outcome rather than a guess.
  };

  const addFromLibrary = (id: string) => {
    const t = library.find((x) => x.id === id);
    if (!t || squads.some((s) => s.id === id && !s._removed)) return;
    setSquads((prev) => {
      const existing = prev.find((s) => s.id === id);
      const next = existing
        ? prev.map((s) => (s.id === id ? { ...s, _removed: false } : s))
        : [
            ...prev,
            {
              id: t.id,
              name: t.name ?? t.id,
              charter: t.charter ?? '',
              owns: t.owns ?? [],
              acceptance: t.acceptance ?? '',
              worker: t.worker ?? '',
              reviewer: t.reviewer ?? '',
              tester: t.tester ?? '',
              manager: t.manager ?? '',
              task_count: 0,
              _new: true,
            } as SquadDraft,
          ];
      emit({ squads: next });
      return next;
    });
  };

  const patchInterface = (key: string, patch: Partial<InterfaceDraft>) => {
    setInterfaces((prev) => {
      const next = prev.map((i) => (i._key === key ? { ...i, ...patch } : i));
      emit({ interfaces: next });
      return next;
    });
  };

  const addInterface = () => {
    setInterfaces((prev) => {
      const next: InterfaceDraft[] = [
        ...prev,
        {
          _key: `new-${prev.length + 1}`,
          _new: true,
          id: '',
          provider: squadIds[0] ?? '',
          consumers: [],
          spec: '',
        },
      ];
      emit({ interfaces: next });
      return next;
    });
  };

  const changed = countChanges(original, { tasks, squads, interfaces });
  const hidden = Math.max(0, (ask.task_count ?? 0) - original.tasks.length);
  // A short plan opens fully: every field is one glance away, which is the
  // point of an editor. A long one opens collapsed, because sixty expanded
  // tasks is a wall nobody scrolls and the title is what people scan by.
  const expandAll = tasks.length <= EXPAND_ALL_UP_TO;
  const addable = library.filter((t) => !squads.some((s) => s.id === t.id && !s._removed));

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
      {(squads.length > 0 || addable.length > 0) && (
        <section className="space-y-2">
          <div className="flex flex-wrap items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">
            <Users size={12} aria-hidden="true" />
            Teams
            {addable.length > 0 && (
              <label className="ml-auto flex items-center gap-1 normal-case tracking-normal">
                <span className="sr-only">Add a team from the library</span>
                <select
                  value=""
                  onChange={(e) => e.target.value && addFromLibrary(e.target.value)}
                  disabled={disabled}
                  aria-label="Add a team from the library"
                  className="input h-7 text-[11px]"
                >
                  <option value="">Add team from library…</option>
                  {addable.map((t) => (
                    <option key={t.id} value={t.id}>
                      {t.name || t.id}
                    </option>
                  ))}
                </select>
              </label>
            )}
          </div>
          {squads.map((s) => (
            <SquadCard
              key={s.id}
              squad={s}
              agents={agents}
              managers={managers}
              disabled={disabled}
              onChange={(patch) => patchSquad(s.id, patch)}
              onToggleRemoved={() => toggleRemovedSquad(s.id)}
            />
          ))}
        </section>
      )}

      {liveSquads.length > 1 && (
        <section className="space-y-2">
          <div className="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">
            <ShieldCheck size={12} aria-hidden="true" />
            Frozen contract
            <button
              type="button"
              onClick={addInterface}
              disabled={disabled}
              className="btn-ghost focus-ring ml-auto h-7 gap-1 px-2 text-[11px] normal-case tracking-normal"
            >
              <Plus size={11} aria-hidden="true" />
              Add interface
            </button>
          </div>
          <p className="text-[11px] text-gray-500 dark:text-gray-400">
            Both halves build against this text and neither can ask the other later. Renaming a
            clause keeps its spec.
          </p>
          {interfaces.length === 0 && (
            <p className="rounded-md border border-dashed border-gray-200 px-3 py-3 text-center text-[11px] text-amber-700 dark:border-gray-800 dark:text-amber-300">
              Nothing frozen — the teams will each invent their own version of the seam, and
              integration is where you find out.
            </p>
          )}
          {interfaces.map((i) => (
            <InterfaceRow
              key={i._key}
              iface={i}
              squadIds={squadIds}
              disabled={disabled}
              onChange={(patch) => patchInterface(i._key, patch)}
              onToggleRemoved={() => patchInterface(i._key, { _removed: !i._removed })}
            />
          ))}
        </section>
      )}

      <section className="space-y-2">
        <div className="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">
          <ListChecks size={12} aria-hidden="true" />
          Tasks
          {hidden > 0 && (
            <span className="normal-case tracking-normal text-gray-400">
              — showing {original.tasks.length} of {ask.task_count}
            </span>
          )}
        </div>
        {tasks.length === 0 && (
          <p className="rounded-md border border-dashed border-gray-200 px-3 py-4 text-center text-xs text-gray-400 dark:border-gray-800">
            The plan has no structured tasks to edit.
          </p>
        )}
        {tasks.map((t) => (
          <TaskCard
            key={t.id}
            task={t}
            agents={agents}
            squadIds={squadIds}
            otherTaskIDs={tasks.filter((x) => x.id !== t.id && !x._removed).map((x) => x.id)}
            teamRoster={rosterOf(liveSquads, t.squad)}
            defaultOpen={expandAll}
            disabled={disabled}
            onChange={(patch) => patchTask(t.id, patch)}
            onToggleRemoved={() => toggleRemovedTask(t.id)}
          />
        ))}
      </section>

      {liveSquads.length > 0 && (
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

function SquadCard({
  squad,
  agents,
  managers,
  disabled,
  onChange,
  onToggleRemoved,
}: {
  squad: SquadDraft;
  agents: string[];
  managers: string[];
  disabled?: boolean;
  onChange: (patch: Partial<PlanSquad>) => void;
  onToggleRemoved: () => void;
}) {
  return (
    <div
      className={clsx(
        'rounded-lg border p-2.5 transition-opacity',
        squad._removed
          ? 'border-red-200 bg-red-50/50 opacity-60 dark:border-red-900 dark:bg-red-950/20'
          : 'border-gray-200 dark:border-gray-800',
      )}
    >
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <span className="badge-brand text-[10px]">{squad.id}</span>
        <input
          value={squad.name ?? ''}
          onChange={(e) => onChange({ name: e.target.value })}
          disabled={disabled || squad._removed}
          aria-label={`Name of squad ${squad.id}`}
          className="input h-7 min-w-0 flex-1 text-xs"
          placeholder="Team name"
        />
        <span className="badge-neutral shrink-0 text-[10px]">
          {squad.task_count} task{squad.task_count === 1 ? '' : 's'}
        </span>
        <button
          type="button"
          onClick={onToggleRemoved}
          disabled={disabled}
          aria-label={squad._removed ? `Keep team ${squad.id}` : `Remove team ${squad.id}`}
          className="btn-ghost focus-ring h-7 shrink-0 gap-1 px-2 text-[11px]"
        >
          {squad._removed ? <Undo2 size={11} aria-hidden="true" /> : <Trash2 size={11} aria-hidden="true" />}
          {squad._removed ? 'Keep' : 'Remove'}
        </button>
      </div>

      {!squad._removed && (
        <>
          <label htmlFor={`squad-${squad.id}-charter`} className="label mb-1 block text-[10px]">
            {`Charter — ${squad.id}`} (injected into every task pack for this team)
          </label>
          <textarea
            id={`squad-${squad.id}-charter`}
            value={squad.charter ?? ''}
            onChange={(e) => onChange({ charter: e.target.value })}
            disabled={disabled}
            rows={2}
            className="input w-full text-[11px]"
          />

          <label htmlFor={`squad-${squad.id}-owns`} className="label mb-1 mt-2 block text-[10px]">
            {`Owns — ${squad.id}`} (one glob per line — teams may not overlap)
          </label>
          <textarea
            id={`squad-${squad.id}-owns`}
            value={(squad.owns ?? []).join('\n')}
            onChange={(e) => onChange({ owns: splitLines(e.target.value) })}
            disabled={disabled}
            rows={Math.min(4, Math.max(2, (squad.owns ?? []).length))}
            className="input-mono w-full text-[11px]"
            placeholder="web/**"
          />
          <div className="mt-2 grid gap-2 [grid-template-columns:repeat(auto-fit,minmax(11rem,1fr))]">
            <div>
              <label htmlFor={`squad-${squad.id}-acceptance`} className="label mb-1 block text-[10px]">
                {`Acceptance — ${squad.id}`}
              </label>
              <input
                id={`squad-${squad.id}-acceptance`}
                value={squad.acceptance ?? ''}
                onChange={(e) => onChange({ acceptance: e.target.value })}
                disabled={disabled}
                className="input-mono h-7 w-full text-[11px]"
                placeholder="go test ./..."
              />
            </div>
            <AgentPicker
              id={`squad-${squad.id}-worker`}
              label={`Worker — ${squad.id}`}
              value={squad.worker ?? ''}
              agents={agents}
              disabled={disabled}
              allowEmpty
              onChange={(v) => onChange({ worker: v })}
            />
            <AgentPicker
              id={`squad-${squad.id}-reviewer`}
              label={`Reviewer — ${squad.id}`}
              value={squad.reviewer ?? ''}
              agents={agents}
              disabled={disabled}
              allowEmpty
              onChange={(v) => onChange({ reviewer: v })}
            />
            <AgentPicker
              id={`squad-${squad.id}-tester`}
              label={`Tester — ${squad.id}`}
              value={squad.tester ?? ''}
              agents={agents}
              disabled={disabled}
              allowEmpty
              onChange={(v) => onChange({ tester: v })}
            />
            {/* Who decides where a rejected delivery goes next. A narrower
                roster than the worker's: only agents that answer the triage
                contract can produce a verdict the harness can read. */}
            <AgentPicker
              id={`squad-${squad.id}-manager`}
              label={`Project manager — ${squad.id}`}
              value={squad.manager ?? ''}
              agents={managers}
              disabled={disabled}
              allowEmpty
              onChange={(v) => onChange({ manager: v })}
            />
          </div>
          {/* The rest of the team, in whatever number it needs. Four seats is a
              shape the harness dispatches, not a shape a team has to be. */}
          <div className="mt-2">
            <MultiPicker
              id={`squad-${squad.id}-roster`}
              label={`Also on ${squad.id}`}
              addLabel="Add agent"
              emptyLabel="just the seats above"
              options={agents}
              value={squad.agents ?? []}
              onChange={(next) => onChange({ agents: next })}
              disabled={disabled}
            />
          </div>
        </>
      )}
    </div>
  );
}

function TaskCard({
  task,
  agents,
  squadIds,
  otherTaskIDs,
  teamRoster,
  defaultOpen,
  disabled,
  onChange,
  onToggleRemoved,
}: {
  task: TaskDraft;
  agents: string[];
  squadIds: string[];
  otherTaskIDs: string[];
  /** The task's own team's people, offered before the rest of the roster. */
  teamRoster: string[];
  defaultOpen: boolean;
  disabled?: boolean;
  onChange: (patch: Partial<TaskDraft>) => void;
  onToggleRemoved: () => void;
}) {
  // A new task always opens: it is empty, and a collapsed empty task reads as
  // one that was already filled in.
  const [open, setOpen] = useState(defaultOpen || !!task._new);
  const deps = task.depends_on ?? [];

  return (
    <div
      className={clsx(
        'rounded-lg border p-2.5 transition-opacity',
        task._removed
          ? 'border-red-200 bg-red-50/50 opacity-60 dark:border-red-900 dark:bg-red-950/20'
          : 'border-gray-200 dark:border-gray-800',
      )}
    >
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          aria-label={`${open ? 'Collapse' : 'Expand'} task ${task.id}`}
          className="focus-ring shrink-0 rounded p-0.5"
        >
          <ChevronRight
            size={13}
            className={clsx('text-gray-400 transition-transform', open && 'rotate-90')}
            aria-hidden="true"
          />
        </button>
        <span className="badge-neutral shrink-0 text-[10px]">{task.id}</span>
        <input
          value={task.title}
          onChange={(e) => onChange({ title: e.target.value })}
          disabled={disabled || task._removed}
          aria-label={`Title of task ${task.id}`}
          className="input h-7 min-w-0 flex-1 text-xs"
          placeholder="What this task does"
        />
        {deps.length > 0 && !open && (
          <span className="badge-neutral shrink-0 text-[10px]" title={`Waits on ${deps.join(', ')}`}>
            after {deps.join(', ')}
          </span>
        )}
        <button
          type="button"
          onClick={onToggleRemoved}
          disabled={disabled}
          aria-label={task._removed ? `Keep task ${task.id}` : `Remove task ${task.id}`}
          className="btn-ghost focus-ring h-7 shrink-0 gap-1 px-2 text-[11px]"
        >
          {task._removed ? <Undo2 size={12} aria-hidden="true" /> : <Trash2 size={12} aria-hidden="true" />}
          {task._removed ? 'Keep' : 'Remove'}
        </button>
      </div>

      {!task._removed && open && (
        <div className="grid gap-2 [grid-template-columns:repeat(auto-fit,minmax(11rem,1fr))]">
          <AgentPicker
            id={`task-${task.id}-role`}
            label={`Agent — ${task.id}`}
            value={task.role ?? ''}
            agents={teamFirst(agents, teamRoster)}
            disabled={disabled}
            onChange={(v) => onChange({ role: v })}
          />
          {squadIds.length > 0 && (
            <div>
              <label htmlFor={`task-${task.id}-squad`} className="label mb-1 block text-[10px]">
                {`Team — ${task.id}`}
              </label>
              <select
                id={`task-${task.id}-squad`}
                value={task.squad ?? ''}
                onChange={(e) => onChange({ squad: e.target.value })}
                disabled={disabled}
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
          <div>
            <label htmlFor={`task-${task.id}-priority`} className="label mb-1 block text-[10px]">
              {`Priority — ${task.id}`}
            </label>
            <input
              id={`task-${task.id}-priority`}
              type="number"
              value={task.priority ?? 0}
              onChange={(e) => onChange({ priority: Number(e.target.value) || 0 })}
              disabled={disabled}
              className="input h-7 w-full text-[11px]"
            />
          </div>

          <div className="col-span-full">
            <label htmlFor={`task-${task.id}-description`} className="label mb-1 block text-[10px]">
              {`Description — ${task.id}`} (what the agent is told to do)
            </label>
            <textarea
              id={`task-${task.id}-description`}
              value={task.description ?? ''}
              onChange={(e) => onChange({ description: e.target.value })}
              disabled={disabled}
              rows={3}
              className="input w-full text-[11px]"
            />
          </div>

          <div className="col-span-full">
            <label htmlFor={`task-${task.id}-acceptance`} className="label mb-1 block text-[10px]">
              {`Acceptance — ${task.id}`} (the reviewer's checklist, one condition per line)
            </label>
            <textarea
              id={`task-${task.id}-acceptance`}
              value={task.acceptance ?? ''}
              onChange={(e) => onChange({ acceptance: e.target.value })}
              disabled={disabled}
              rows={2}
              className="input w-full text-[11px]"
            />
          </div>

          <div className="col-span-full">
            <label htmlFor={`task-${task.id}-files`} className="label mb-1 block text-[10px]">
              {`Files — ${task.id}`} (one per line)
            </label>
            <textarea
              id={`task-${task.id}-files`}
              value={(task.files ?? []).join('\n')}
              onChange={(e) => onChange({ files: splitLines(e.target.value) })}
              disabled={disabled}
              rows={Math.min(4, Math.max(2, (task.files ?? []).length))}
              className="input-mono w-full text-[11px]"
              placeholder="cmd/server/main.go"
            />
          </div>

          {/* Ordering is dependencies, not position. The board dispatches waves,
              so "do this later" only means "after these have finished" — a task
              with no dependency runs in the first wave no matter where it sits
              in the list. */}
          {otherTaskIDs.length > 0 && (
            <fieldset className="col-span-full">
              <legend className="label mb-1 block text-[10px]">
                {`Waits for — ${task.id}`} (this task runs only after these finish)
              </legend>
              <div className="flex flex-wrap gap-1">
                {otherTaskIDs.map((id) => {
                  const on = deps.includes(id);
                  return (
                    <button
                      key={id}
                      type="button"
                      onClick={() =>
                        onChange({ depends_on: on ? deps.filter((d) => d !== id) : [...deps, id] })
                      }
                      disabled={disabled}
                      aria-pressed={on}
                      className={clsx(
                        'focus-ring rounded px-1.5 py-0.5 font-mono text-[10px]',
                        on
                          ? 'bg-brand-500 text-white'
                          : 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300',
                      )}
                    >
                      {id}
                    </button>
                  );
                })}
              </div>
            </fieldset>
          )}
        </div>
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
  onChange: (patch: Partial<InterfaceDraft>) => void;
  onToggleRemoved: () => void;
}) {
  const consumers = iface.consumers ?? [];
  return (
    <div
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
          aria-label={iface._new ? 'New interface name' : `Name of interface ${iface._key}`}
          placeholder="GET /api/todos"
          className="input-mono h-7 min-w-0 flex-1 text-[11px]"
        />
        <div>
          <label htmlFor={`iface-${iface._key}-provider`} className="sr-only">
            {`Provider of ${iface.id || 'this interface'}`}
          </label>
          <select
            id={`iface-${iface._key}-provider`}
            value={iface.provider ?? ''}
            onChange={(e) => onChange({ provider: e.target.value })}
            disabled={disabled || iface._removed}
            className="input h-7 text-[11px]"
          >
            <option value="">provider…</option>
            {squadIds.map((id) => (
              <option key={id} value={id}>
                {id}
              </option>
            ))}
          </select>
        </div>
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
                  onClick={() =>
                    onChange({
                      consumers: consumers.includes(id)
                        ? consumers.filter((c) => c !== id)
                        : [...consumers, id],
                    })
                  }
                  disabled={disabled}
                  aria-pressed={consumers.includes(id)}
                  className={clsx(
                    'focus-ring rounded px-1.5 py-0.5 text-[10px]',
                    consumers.includes(id)
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

/** nextNewID names a task the board has not seen, without colliding with one. */
function nextNewID(tasks: PlanApprovalTask[]): string {
  const taken = new Set(tasks.map((t) => t.id.toUpperCase()));
  for (let n = 1; n < 1000; n++) {
    const candidate = `NEW-${n}`;
    if (!taken.has(candidate)) return candidate;
  }
  return `NEW-${tasks.length + 1}`;
}

function splitLines(v: string): string[] {
  return v
    .split('\n')
    .map((s) => s.trim())
    .filter(Boolean);
}

/**
 * rosterOf is a team's own people: its named seats, then its open roster.
 *
 * Order matters — it is the order the picker offers them in, and the first
 * entries are the ones anyone actually reads.
 */
function rosterOf(squads: SquadDraft[], squadID?: string): string[] {
  if (!squadID) return [];
  const s = squads.find((x) => x.id === squadID);
  if (!s) return [];
  return [s.worker, s.reviewer, s.tester, ...(s.agents ?? [])].filter(
    (v): v is string => !!v && v.trim() !== '',
  );
}

/**
 * teamFirst puts a task's own team at the head of the roster it is offered.
 *
 * Nothing is removed. The team's people are the likely answer for its work, but
 * they are not the only allowed one: the reason a task needs reassigning is
 * often that its team lacks the skill, and a picker that hid everyone else
 * would only offer agents that already failed.
 */
function teamFirst(all: string[], team: string[]): string[] {
  if (team.length === 0) return all;
  const seen = new Set<string>();
  const out: string[] = [];
  for (const id of team) {
    if (all.includes(id) && !seen.has(id)) {
      seen.add(id);
      out.push(id);
    }
  }
  for (const id of all) {
    if (!seen.has(id)) out.push(id);
  }
  return out;
}
