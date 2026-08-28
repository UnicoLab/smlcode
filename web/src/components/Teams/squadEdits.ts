import type { SquadEdit, SquadStatus } from '@/types';

/**
 * buildSquadEdits diffs the draft against what the server reported.
 *
 * Only changed fields travel: the harness reads an absent field as "leave it
 * alone" and a present one as "set to this", so echoing unchanged values back
 * is indistinguishable from an intentional overwrite.
 *
 * `owns_set` rides with any ownership change because a bare empty list cannot
 * say whether the user cleared it or never opened the field.
 */
export function buildSquadEdits(original: SquadStatus[], draft: SquadStatus[]): SquadEdit[] {
  const before = new Map(original.map((s) => [s.id, s]));
  const out: SquadEdit[] = [];
  for (const d of draft) {
    const b = before.get(d.id);
    if (!b) continue;
    const e: SquadEdit = { id: d.id };
    let touched = false;
    if ((d.name ?? '') !== (b.name ?? '')) {
      e.name = d.name;
      touched = true;
    }
    if ((d.acceptance ?? '') !== (b.acceptance ?? '')) {
      e.acceptance = d.acceptance;
      touched = true;
    }
    if ((d.manager ?? '') !== (b.manager ?? '')) {
      // Empty is meaningful: it hands the team back to the run's default
      // manager, so it travels as '' rather than being dropped as falsy.
      e.manager = d.manager ?? '';
      touched = true;
    }
    const dOwns = d.owns ?? [];
    const bOwns = b.owns ?? [];
    if (dOwns.length !== bOwns.length || dOwns.some((v, i) => v !== bOwns[i])) {
      e.owns = dOwns;
      e.owns_set = true;
      touched = true;
    }
    if (touched) out.push(e);
  }
  return out;
}
