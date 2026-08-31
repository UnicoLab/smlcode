// ── One team, one colour, everywhere ─────────────────────────────────────
//
// A team's colour has to be the SAME on the board, on the task card and on the
// live rail, or the colour stops being information and becomes decoration. It
// also has to survive a team being added or removed: assigning colours by
// position means adding `docs` recolours the frontend, and every screenshot,
// every memory of "the green team", every glance goes stale at once.
//
// So the colour is derived from the team's own id. Stable for the life of the
// team, independent of how many others exist, and identical in every view that
// renders it.

/** Tailwind classes for one team, in the two places a team gets painted. */
export interface TeamColor {
  /** A small badge — the task card, the strip's chip. */
  badge: string;
  /** A 2–3px accent — the left edge of a card, a progress bar. */
  accent: string;
  /** The bare colour name, for anything that needs to build its own class. */
  name: string;
}

// Hues chosen to stay distinguishable from the COLUMN colours, which already
// own sky/indigo/violet/amber/orange/red/emerald. A team badge that reads as a
// column state is worse than a grey one.
const PALETTE: TeamColor[] = [
  {
    name: 'teal',
    badge: 'bg-teal-100 text-teal-800 dark:bg-teal-950/60 dark:text-teal-200',
    accent: 'bg-teal-500',
  },
  {
    name: 'fuchsia',
    badge: 'bg-fuchsia-100 text-fuchsia-800 dark:bg-fuchsia-950/60 dark:text-fuchsia-200',
    accent: 'bg-fuchsia-500',
  },
  {
    name: 'cyan',
    badge: 'bg-cyan-100 text-cyan-800 dark:bg-cyan-950/60 dark:text-cyan-200',
    accent: 'bg-cyan-500',
  },
  {
    name: 'rose',
    badge: 'bg-rose-100 text-rose-800 dark:bg-rose-950/60 dark:text-rose-200',
    accent: 'bg-rose-500',
  },
  {
    name: 'lime',
    badge: 'bg-lime-100 text-lime-800 dark:bg-lime-950/60 dark:text-lime-200',
    accent: 'bg-lime-500',
  },
  {
    name: 'purple',
    badge: 'bg-purple-100 text-purple-800 dark:bg-purple-950/60 dark:text-purple-200',
    accent: 'bg-purple-500',
  },
];

/** The colour an unassigned task gets: deliberately grey, never a team hue. */
export const UNASSIGNED: TeamColor = {
  name: 'gray',
  badge: 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300',
  accent: 'bg-gray-400',
};

/**
 * teamColor maps a team id to its colour, stably.
 *
 * A trivial hash rather than an index: `teamColor("frontend")` must answer the
 * same thing whether the plan holds two teams or five, and whether this view
 * has seen the others at all.
 */
export function teamColor(id: string | undefined): TeamColor {
  const key = (id ?? '').trim().toLowerCase();
  if (key === '') return UNASSIGNED;
  let hash = 0;
  for (let i = 0; i < key.length; i++) {
    hash = (hash * 31 + key.charCodeAt(i)) >>> 0;
  }
  return PALETTE[hash % PALETTE.length];
}
