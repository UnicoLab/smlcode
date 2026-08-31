import type { InterfaceEdit, PlanEdits, PlanInterface, SquadEdit, SquadStatus } from '@/types';

/** A contract clause in the editor's draft. `_removed` is UI state, never sent. */
export type InterfaceDraft = PlanInterface & { _removed?: boolean; _new?: boolean; _key: string };

/**
 * buildSquadEdits diffs the draft org chart and contract against the server's.
 *
 * Only changed fields travel: the harness reads an absent field as "leave it
 * alone" and a present one as "set to this", so echoing unchanged values back is
 * indistinguishable from an intentional overwrite.
 *
 * `owns_set` / `consumers_set` ride with any list change because a bare empty
 * list cannot say whether the user cleared it or never opened the field.
 */
export function buildSquadEdits(
  original: SquadStatus[],
  draft: SquadStatus[],
  originalInterfaces: PlanInterface[] = [],
  draftInterfaces: InterfaceDraft[] = [],
): PlanEdits {
  const out: PlanEdits = {};

  const before = new Map(original.map((s) => [s.id, s]));
  const squads: SquadEdit[] = [];
  const seen = new Set<string>();
  for (const d of draft) {
    seen.add(d.id);
    const b = before.get(d.id);
    const e: SquadEdit = { id: d.id };
    let touched = false;
    if (!b) {
      // A team added from the library. Everything travels, because the harness
      // has nothing to merge it against.
      e.new = true;
      e.name = d.name ?? '';
      e.charter = d.charter ?? '';
      e.acceptance = d.acceptance ?? '';
      e.worker = d.worker ?? '';
      e.reviewer = d.reviewer ?? '';
      e.tester = d.tester ?? '';
      e.manager = d.manager ?? '';
      e.owns = d.owns ?? [];
      e.owns_set = true;
      e.agents = d.agents ?? [];
      e.agents_set = true;
      e.skills = d.skills ?? [];
      e.skills_set = true;
      squads.push(e);
      continue;
    }
    if ((d.name ?? '') !== (b.name ?? '')) {
      e.name = d.name;
      touched = true;
    }
    if ((d.charter ?? '') !== (b.charter ?? '')) {
      e.charter = d.charter ?? '';
      touched = true;
    }
    if ((d.acceptance ?? '') !== (b.acceptance ?? '')) {
      e.acceptance = d.acceptance;
      touched = true;
    }
    if ((d.worker ?? '') !== (b.worker ?? '')) {
      e.worker = d.worker ?? '';
      touched = true;
    }
    if ((d.reviewer ?? '') !== (b.reviewer ?? '')) {
      e.reviewer = d.reviewer ?? '';
      touched = true;
    }
    if ((d.tester ?? '') !== (b.tester ?? '')) {
      e.tester = d.tester ?? '';
      touched = true;
    }
    if ((d.manager ?? '') !== (b.manager ?? '')) {
      // Empty is meaningful: it hands the team back to the run's default
      // manager, so it travels as '' rather than being dropped as falsy.
      e.manager = d.manager ?? '';
      touched = true;
    }
    if (!sameList(d.owns, b.owns)) {
      e.owns = d.owns ?? [];
      e.owns_set = true;
      touched = true;
    }
    if (!sameList(d.agents, b.agents)) {
      e.agents = d.agents ?? [];
      e.agents_set = true;
      touched = true;
    }
    if (!sameList(d.skills, b.skills)) {
      e.skills = d.skills ?? [];
      e.skills_set = true;
      touched = true;
    }
    if (touched) squads.push(e);
  }

  const removeSquads = original.filter((s) => !seen.has(s.id)).map((s) => s.id);

  const beforeIface = new Map(originalInterfaces.map((i) => [i.id, i]));
  const interfaces: InterfaceEdit[] = [];
  const removeInterfaces: string[] = [];
  for (const d of draftInterfaces) {
    const id = d._new ? '' : d._key;
    if (d._removed) {
      if (id) removeInterfaces.push(id);
      continue;
    }
    if (d._new) {
      // A clause with no name names nothing; dropping it here beats sending the
      // harness something it can only refuse.
      if (!d.id.trim() || !d.provider) continue;
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
    const b = beforeIface.get(id);
    if (!b) continue;
    const e: InterfaceEdit = { id };
    let touched = false;
    if (d.id.trim() !== b.id) {
      // An interface id IS its name — a route, a symbol — so a correction is a
      // rename, not a delete plus an add that loses the spec.
      e.rename = d.id.trim();
      touched = true;
    }
    if ((d.provider ?? '') !== (b.provider ?? '')) {
      e.provider = d.provider;
      touched = true;
    }
    if ((d.spec ?? '') !== (b.spec ?? '')) {
      e.spec = d.spec ?? '';
      touched = true;
    }
    if (!sameList(d.consumers, b.consumers)) {
      e.consumers = d.consumers ?? [];
      e.consumers_set = true;
      touched = true;
    }
    if (touched) interfaces.push(e);
  }

  if (squads.length) out.squads = squads;
  if (removeSquads.length) out.remove_squads = removeSquads;
  if (interfaces.length) out.interfaces = interfaces;
  if (removeInterfaces.length) out.remove_interfaces = removeInterfaces;
  return out;
}

/** countSquadEdits is the badge on the Save button. */
export function countSquadEdits(edits: PlanEdits): number {
  return (
    (edits.squads?.length ?? 0) +
    (edits.remove_squads?.length ?? 0) +
    (edits.interfaces?.length ?? 0) +
    (edits.remove_interfaces?.length ?? 0)
  );
}

function sameList(a?: string[], b?: string[]): boolean {
  const x = a ?? [];
  const y = b ?? [];
  return x.length === y.length && x.every((v, i) => v === y[i]);
}
