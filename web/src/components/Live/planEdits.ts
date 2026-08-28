import type { PlanApprovalTask, PlanEdits, PlanSquad, SquadEdit, TaskEdit } from '@/types';

/** A task in the editor's draft. `_removed` is UI state, never sent. */
export type TaskDraft = PlanApprovalTask & { _removed?: boolean };

function sameList(a?: string[], b?: string[]): boolean {
  const x = a ?? [];
  const y = b ?? [];
  return x.length === y.length && x.every((v, i) => v === y[i]);
}

/**
 * buildEdits diffs the draft against what the ask proposed.
 *
 * Only changed fields are included. The Go side treats an absent field as
 * "untouched" and a present one as "set to this", so sending an unchanged value
 * would be indistinguishable from an intentional overwrite — harmless today,
 * and wrong the moment two people edit the same plan.
 */
export function buildEdits(
  originalTasks: PlanApprovalTask[],
  draftTasks: TaskDraft[],
  originalSquads: PlanSquad[],
  draftSquads: PlanSquad[],
): PlanEdits {
  const byID = new Map(originalTasks.map((t) => [t.id, t]));
  const edits: PlanEdits = {};

  const taskEdits: TaskEdit[] = [];
  const removes: string[] = [];
  const adds: PlanApprovalTask[] = [];

  for (const d of draftTasks) {
    const before = byID.get(d.id);
    if (!before) {
      // A task the user added. Blank ones are dropped rather than sent for the
      // harness to reject.
      if (d.title.trim() && !d._removed) {
        adds.push({ id: '', title: d.title.trim(), role: d.role, description: d.description, files: d.files });
      }
      continue;
    }
    if (d._removed) {
      removes.push(d.id);
      continue;
    }
    const e: TaskEdit = { id: d.id };
    let touched = false;
    if (d.title !== before.title) {
      e.title = d.title;
      touched = true;
    }
    if ((d.role ?? '') !== (before.role ?? '')) {
      e.role = d.role;
      touched = true;
    }
    const dSquad = d.squad ?? '';
    const bSquad = before.squad ?? '';
    if (dSquad !== bSquad) {
      e.squad = dSquad;
      touched = true;
    }
    if (!sameList(d.files, before.files)) {
      e.files = d.files ?? [];
      e.files_set = true;
      touched = true;
    }
    if (touched) taskEdits.push(e);
  }

  const squadEdits: SquadEdit[] = [];
  const squadBefore = new Map(originalSquads.map((s) => [s.id, s]));
  for (const d of draftSquads) {
    const before = squadBefore.get(d.id);
    if (!before) continue;
    const e: SquadEdit = { id: d.id };
    let touched = false;
    if ((d.name ?? '') !== (before.name ?? '')) {
      e.name = d.name;
      touched = true;
    }
    if ((d.acceptance ?? '') !== (before.acceptance ?? '')) {
      e.acceptance = d.acceptance;
      touched = true;
    }
    if ((d.worker ?? '') !== (before.worker ?? '')) {
      e.worker = d.worker;
      touched = true;
    }
    if ((d.manager ?? '') !== (before.manager ?? '')) {
      // Empty is meaningful: it hands the team back to the run's default
      // manager, so it travels as '' rather than being dropped as falsy.
      e.manager = d.manager ?? '';
      touched = true;
    }
    if (!sameList(d.owns, before.owns)) {
      e.owns = d.owns ?? [];
      e.owns_set = true;
      touched = true;
    }
    if (touched) squadEdits.push(e);
  }

  if (taskEdits.length) edits.tasks = taskEdits;
  if (removes.length) edits.remove_tasks = removes;
  if (adds.length) edits.add_tasks = adds;
  if (squadEdits.length) edits.squads = squadEdits;
  return edits;
}

export function countChanges(
  originalTasks: PlanApprovalTask[],
  draftTasks: TaskDraft[],
  originalSquads: PlanSquad[],
  draftSquads: PlanSquad[],
): number {
  const e = buildEdits(originalTasks, draftTasks, originalSquads, draftSquads);
  return (
    (e.tasks?.length ?? 0) +
    (e.remove_tasks?.length ?? 0) +
    (e.add_tasks?.length ?? 0) +
    (e.squads?.length ?? 0)
  );
}
