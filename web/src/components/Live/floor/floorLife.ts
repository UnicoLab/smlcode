import { workTables, type FloorModel, type FloorTeam } from './floorModel';

// ── Life on the floor ────────────────────────────────────────────────────
//
// A run is mostly waiting: one agent types while the rest of the table has
// nothing to do. Drawn as statues, that reads as a stuck page; drawn as
// people, it reads as an office. So everyone who is not working has a mood —
// they watch whoever is, fetch a coffee, stretch, take a walk round the room,
// and doze off when nothing has come their way for a while. A table whose
// every ticket is done throws a party: beers, a kick-about, a dance. When the
// whole run is over and everything shipped, the whole floor joins in.
//
// It is pure and deterministic — a person's mood is a function of the time,
// how long they have been idle and a seed from their name — so both stages
// (and the dossier) agree on what someone is doing without sharing state, a
// re-render never reshuffles anybody, and it is testable.

export type Mood = 'work' | 'watch' | 'coffee' | 'stretch' | 'stroll' | 'nap' | 'cheers' | 'football' | 'dance';

export const MOOD_GLYPH: Record<Mood, string> = {
  work: '⌨️',
  watch: '👀',
  coffee: '☕',
  stretch: '🙆',
  stroll: '🚶',
  nap: '💤',
  cheers: '🍻',
  football: '⚽',
  dance: '🕺',
};

export const MOOD_LABEL: Record<Mood, string> = {
  work: 'working',
  watch: 'watching the others work',
  coffee: 'on a coffee break',
  stretch: 'stretching their legs',
  stroll: 'taking a walk round the room',
  nap: 'having a nap',
  cheers: 'having a beer — the table is done',
  football: 'playing football — the table is done',
  dance: 'dancing — the table is done',
};

/** Moods worth a badge: watching is the default, and working has its own bubble. */
export function moodShows(mood: Mood): boolean {
  return mood !== 'work' && mood !== 'watch';
}

/** A party mood: the table is done. */
export function isParty(mood: Mood): boolean {
  return mood === 'cheers' || mood === 'football' || mood === 'dance';
}

/** Idle for less than this, a person just watches whoever is working. */
export const WATCH_MS = 12_000;
/** How long one idle mood lasts before the next is drawn. */
export const MOOD_SLOT_MS = 9_000;
/** Idle past this, a nap becomes likely… */
export const DROWSY_MS = 60_000;
/** …and past this, the likeliest thing of all. */
export const SLEEPY_MS = 150_000;
/** One round of the party: beers, then a kick-about, then beers again. */
export const PARTY_SLOT_MS = 14_000;

export interface LifeInputs {
  /** Working right now, in a live run. */
  active: boolean;
  running: boolean;
  /** Every ticket at this person's table is done (or the whole run shipped). */
  partying: boolean;
  /** When this person last did anything, in ms — or when the floor was first seen. */
  since: number;
  now: number;
  /** From seedOf(id): who they are decides what they like doing. */
  seed: number;
}

/** A stable 32-bit seed from a name (FNV-1a). */
export function seedOf(id: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < id.length; i++) {
    h ^= id.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

/** Mixes a seed with a slot number into a fresh roll, 0..2^32. */
function roll(seed: number, slot: number): number {
  let h = (seed ^ Math.imul(slot + 0x9e3779b9, 0x85ebca6b)) >>> 0;
  h = Math.imul(h ^ (h >>> 16), 0x7feb352d);
  h = Math.imul(h ^ (h >>> 15), 0x846ca68b);
  return (h ^ (h >>> 16)) >>> 0;
}

/** Which party round it is — shared by the whole table, so they play together. */
export function partyRound(now: number): number {
  return Math.floor(now / PARTY_SLOT_MS);
}

/** Every third party round is a kick-about. */
export function isFootballRound(now: number): boolean {
  return partyRound(now) % 3 === 1;
}

/**
 * moodFor is what a person is doing right now.
 *
 * Working beats everything. A done table parties — everyone at it together,
 * the round decides whether it is beers or football, and a few dance. A live
 * run's idle people watch at first, then draw a new mood every MOOD_SLOT_MS
 * (each on their own offset, so the room never changes all at once), with
 * naps growing likelier the longer nothing comes their way. A run that is not
 * live and did not ship is a room of people napping at their desks.
 */
export function moodFor(i: LifeInputs): Mood {
  if (i.active && i.running) return 'work';
  if (i.partying) {
    const round = partyRound(i.now);
    if (round % 3 === 1) return 'football';
    return roll(i.seed, round) % 4 === 0 ? 'dance' : 'cheers';
  }
  const idle = Math.max(0, i.now - i.since);
  if (!i.running) {
    // The run stopped: people drift off one by one, not in unison.
    return idle > 4_000 + (i.seed % 20_000) ? 'nap' : 'watch';
  }
  if (idle < WATCH_MS) return 'watch';
  if (idle > SLEEPY_MS * 2) return 'nap';
  const offset = i.seed % MOOD_SLOT_MS;
  const slot = Math.floor((i.now + offset) / MOOD_SLOT_MS);
  const r = roll(i.seed, slot) % 100;
  const napChance = idle > SLEEPY_MS ? 60 : idle > DROWSY_MS ? 22 : 0;
  if (r < napChance) return 'nap';
  const rest = (r - napChance) / (100 - napChance);
  if (rest < 0.34) return 'watch';
  if (rest < 0.58) return 'coffee';
  if (rest < 0.74) return 'stretch';
  return 'stroll';
}

/**
 * floorShipped: the run is over and every work table finished — the whole
 * floor, harness included, can put its feet up.
 */
export function floorShipped(floor: Pick<FloorModel, 'teams'>, running: boolean): boolean {
  if (running) return false;
  const tables = workTables(floor);
  return tables.length > 0 && tables.every((t) => t.total > 0 && t.complete);
}

/** A table parties when its own tickets are all done, or when the whole run shipped. */
export function tableParties(team: FloorTeam, shipped: boolean): boolean {
  if (shipped) return true;
  return !team.internal && team.total > 0 && team.complete;
}
