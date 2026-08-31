import type {
  InterfaceEdit,
  PlanApprovalTask,
  PlanEdits,
  PlanInterface,
  PlanSquad,
  SquadEdit,
  TaskEdit,
} from '@/types';

/** A task in the editor's draft. `_removed` is UI state, never sent. */
export type TaskDraft = PlanApprovalTask & { _removed?: boolean; _new?: boolean };

/** A team in the editor's draft. `_removed` / `_new` are UI state. */
export type SquadDraft = PlanSquad & { _removed?: boolean; _new?: boolean };

/**
 * A contract clause in the draft.
 *
 * `_key` is the id the SERVER knows this clause by, held separately from `id`
 * so the name itself stays editable: an interface id IS its name — a route, an
 * exported symbol — and correcting a wrong one is a rename, not a delete plus
 * an add that loses the spec.
 */
export type InterfaceDraft = PlanInterface & { _key: string; _removed?: boolean; _new?: boolean };

function sameList(a?: string[], b?: string[]): boolean {
  const x = a ?? [];
  const y = b ?? [];
  return x.length === y.length && x.every((v, i) => v === y[i]);
}

export interface PlanDraft {
  tasks: TaskDraft[];
  squads: SquadDraft[];
  interfaces: InterfaceDraft[];
}

export interface PlanOriginal {
  tasks: PlanApprovalTask[];
  squads: PlanSquad[];
  interfaces: PlanInterface[];
}

/**
 * buildEdits diffs the draft against what the ask proposed.
 *
 * Only changed fields are included. The Go side treats an absent field as
 * "untouched" and a present one as "set to this", so sending an unchanged value
 * would be indistinguishable from an intentional overwrite.
 *
 * A NEW task keeps its placeholder id in `add_tasks`. That id is a client
 * reference, not a board id — the harness strips it, lets the board assign a
 * real one, and rewrites every dependency that named the placeholder. It is the
 * only way "this existing task now waits on the one I just added" can be
 * expressed before the board has named it.
 */
export function buildEdits(original: PlanOriginal, draft: PlanDraft): PlanEdits {
  const edits: PlanEdits = {};

  const byID = new Map(original.tasks.map((t) => [t.id, t]));
  const taskEdits: TaskEdit[] = [];
  const removes: string[] = [];
  const adds: PlanApprovalTask[] = [];

  for (const d of draft.tasks) {
    const before = byID.get(d.id);
    if (!before) {
      // Blank ones are dropped rather than sent for the harness to reject.
      if (d.title.trim() && !d._removed) {
        adds.push({
          id: d.id,
          title: d.title.trim(),
          role: d.role,
          description: d.description,
          squad: d.squad,
          files: d.files,
          acceptance: d.acceptance,
          priority: d.priority,
          depends_on: d.depends_on,
        });
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
    if ((d.description ?? '') !== (before.description ?? '')) {
      e.description = d.description ?? '';
      touched = true;
    }
    if ((d.role ?? '') !== (before.role ?? '')) {
      e.role = d.role;
      touched = true;
    }
    if ((d.squad ?? '') !== (before.squad ?? '')) {
      e.squad = d.squad ?? '';
      touched = true;
    }
    if ((d.acceptance ?? '') !== (before.acceptance ?? '')) {
      e.acceptance = d.acceptance ?? '';
      touched = true;
    }
    if ((d.priority ?? 0) !== (before.priority ?? 0)) {
      e.priority = d.priority ?? 0;
      touched = true;
    }
    if (!sameList(d.files, before.files)) {
      e.files = d.files ?? [];
      e.files_set = true;
      touched = true;
    }
    if (!sameList(d.depends_on, before.depends_on)) {
      // Ordering is expressed as dependencies, not as a position in a list: the
      // board dispatches waves, and "later" only means "after these finished".
      e.depends_on = d.depends_on ?? [];
      e.depends_set = true;
      touched = true;
    }
    if (touched) taskEdits.push(e);
  }

  const squadEdits: SquadEdit[] = [];
  const removeSquads: string[] = [];
  const squadBefore = new Map(original.squads.map((s) => [s.id, s]));
  for (const d of draft.squads) {
    const before = squadBefore.get(d.id);
    if (!before) {
      if (d._removed || !d.id.trim()) continue;
      // A team added from the library. Everything travels: the harness has
      // nothing to merge a new team against.
      squadEdits.push({
        id: d.id,
        new: true,
        name: d.name ?? '',
        charter: d.charter ?? '',
        acceptance: d.acceptance ?? '',
        worker: d.worker ?? '',
        reviewer: d.reviewer ?? '',
        tester: d.tester ?? '',
        manager: d.manager ?? '',
        owns: d.owns ?? [],
        owns_set: true,
        agents: d.agents ?? [],
        agents_set: true,
        skills: d.skills ?? [],
        skills_set: true,
      });
      continue;
    }
    if (d._removed) {
      removeSquads.push(d.id);
      continue;
    }
    const e: SquadEdit = { id: d.id };
    let touched = false;
    if ((d.name ?? '') !== (before.name ?? '')) {
      e.name = d.name;
      touched = true;
    }
    if ((d.charter ?? '') !== (before.charter ?? '')) {
      e.charter = d.charter ?? '';
      touched = true;
    }
    if ((d.acceptance ?? '') !== (before.acceptance ?? '')) {
      e.acceptance = d.acceptance;
      touched = true;
    }
    if ((d.worker ?? '') !== (before.worker ?? '')) {
      e.worker = d.worker ?? '';
      touched = true;
    }
    if ((d.reviewer ?? '') !== (before.reviewer ?? '')) {
      e.reviewer = d.reviewer ?? '';
      touched = true;
    }
    if ((d.tester ?? '') !== (before.tester ?? '')) {
      e.tester = d.tester ?? '';
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
    if (!sameList(d.agents, before.agents)) {
      e.agents = d.agents ?? [];
      e.agents_set = true;
      touched = true;
    }
    if (!sameList(d.skills, before.skills)) {
      e.skills = d.skills ?? [];
      e.skills_set = true;
      touched = true;
    }
    if (touched) squadEdits.push(e);
  }

  const ifaceBefore = new Map(original.interfaces.map((i) => [i.id, i]));
  const interfaces: InterfaceEdit[] = [];
  const removeInterfaces: string[] = [];
  for (const d of draft.interfaces) {
    if (d._new) {
      if (d._removed || !d.id.trim() || !d.provider) continue;
      interfaces.push({
        id: d.id.trim(),
        new: true,
        provider: d.provider,
        spec: d.spec ?? '',
        consumers: d.consumers ?? [],
        consumers_set: true,
      });
      continue;
    }
    if (d._removed) {
      removeInterfaces.push(d._key);
      continue;
    }
    const before = ifaceBefore.get(d._key);
    if (!before) continue;
    const e: InterfaceEdit = { id: d._key };
    let touched = false;
    if (d.id.trim() !== before.id) {
      e.rename = d.id.trim();
      touched = true;
    }
    if ((d.provider ?? '') !== (before.provider ?? '')) {
      e.provider = d.provider;
      touched = true;
    }
    if ((d.spec ?? '') !== (before.spec ?? '')) {
      e.spec = d.spec ?? '';
      touched = true;
    }
    if (!sameList(d.consumers, before.consumers)) {
      e.consumers = d.consumers ?? [];
      e.consumers_set = true;
      touched = true;
    }
    if (touched) interfaces.push(e);
  }

  if (taskEdits.length) edits.tasks = taskEdits;
  if (removes.length) edits.remove_tasks = removes;
  if (adds.length) edits.add_tasks = adds;
  if (squadEdits.length) edits.squads = squadEdits;
  if (removeSquads.length) edits.remove_squads = removeSquads;
  if (interfaces.length) edits.interfaces = interfaces;
  if (removeInterfaces.length) edits.remove_interfaces = removeInterfaces;
  return edits;
}

export function countChanges(original: PlanOriginal, draft: PlanDraft): number {
  const e = buildEdits(original, draft);
  return (
    (e.tasks?.length ?? 0) +
    (e.remove_tasks?.length ?? 0) +
    (e.add_tasks?.length ?? 0) +
    (e.squads?.length ?? 0) +
    (e.remove_squads?.length ?? 0) +
    (e.interfaces?.length ?? 0) +
    (e.remove_interfaces?.length ?? 0)
  );
}
