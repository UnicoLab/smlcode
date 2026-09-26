import { workTables, type FloorModel, type FloorPulse, type FloorTeam } from './floorModel';

// ── Life on the floor ────────────────────────────────────────────────────
//
// A run is mostly waiting: one agent types while the rest of the table has
// nothing to do. Drawn as statues, that reads as a stuck page; drawn as
// people, it reads as an office. So everyone who is not working has a mood.
// They watch whoever is, chat with a neighbour, scroll a phone, think, take
// a walk, and go to the shared break room for a coffee or a round of
// foosball — where people from other tables are too. The manager walks the
// table through the board. Now and then someone dozes off. A table reacts to
// what happens at it: applause when a ticket lands, a wince when one fails,
// heads turning when a teammate starts. Click someone and they wave.
//
// No two tables are copies: each has its own habits (a culture drawn from
// its id) and its own party programme on its own clock, and each person has
// a favourite pastime. A table whose every ticket is done throws a party —
// beers, a kick-about, a dance — and when the whole run ships, the whole
// floor joins in.
//
// It is pure and deterministic — a mood is a function of the time, how long
// someone has been idle, their seed and their table's — so both stages (and
// the dossier) agree without sharing state, a re-render never reshuffles
// anybody, and it is testable.

export type Mood =
  | 'work'
  | 'watch'
  | 'coffee'
  | 'stretch'
  | 'stroll'
  | 'chat'
  | 'phone'
  | 'think'
  | 'game'
  | 'present'
  | 'nap'
  | 'cheer'
  | 'facepalm'
  | 'wave'
  | 'away'
  | 'cheers'
  | 'football'
  | 'dance';

export const MOOD_GLYPH: Record<Mood, string> = {
  work: '⌨️',
  watch: '👀',
  coffee: '☕',
  stretch: '🙆',
  stroll: '🚶',
  chat: '💬',
  phone: '📱',
  think: '🤔',
  game: '🕹️',
  present: '📊',
  nap: '💤',
  cheer: '👏',
  facepalm: '🤦',
  wave: '👋',
  away: '📤',
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
  chat: 'chatting with a neighbour',
  phone: 'scrolling their phone',
  think: 'thinking it over',
  game: 'playing foosball in the break room',
  present: 'walking the team through the board',
  nap: 'having a nap',
  cheer: 'applauding — a ticket just landed',
  facepalm: 'wincing — a ticket just failed',
  wave: 'waving at you',
  away: 'away, working at another table',
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

/** Moods spent on your feet, away from the desk. */
export function isStanding(mood: Mood): boolean {
  return isParty(mood) || mood === 'present';
}

/** Moods spent in the shared break room: a coffee at the bar, a round of foosball. */
export function isBreak(mood: Mood): boolean {
  return mood === 'coffee' || mood === 'game';
}

/** Idle for less than this, a person just watches whoever is working. */
export const WATCH_MS = 12_000;
/** How long one idle mood lasts before the next is drawn. */
export const MOOD_SLOT_MS = 9_000;
/** Idle past this, a nap becomes possible… */
export const DROWSY_MS = 60_000;
/** …and past this, likelier (never certain while the run is live). */
export const SLEEPY_MS = 150_000;
/** One round of the party; each table runs its own programme on its own clock. */
export const PARTY_SLOT_MS = 14_000;
/** How long a table reacts to something that just happened at it. */
export const REACT_MS = 6_000;

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
  /** From seedOf(team id): every table has its own habits and its own party. */
  table?: number;
  /** The table's manager: presents at the board rather than scrolls a phone. */
  manager?: boolean;
  /** Clicked on the floor: they wave back. */
  poked?: boolean;
  /** Working another table's ticket: their chair here is empty. */
  away?: boolean;
  /** When a ticket at this table last landed / last failed, and when a teammate last started. */
  cheerAt?: number;
  groanAt?: number;
  buzzAt?: number;
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

const PARTY_PROGRAMMES: Mood[][] = [
  ['cheers', 'football', 'dance'],
  ['dance', 'cheers', 'football'],
  ['football', 'dance', 'cheers'],
  ['cheers', 'dance', 'football'],
];

/** What a table's party is doing right now: its own programme, on its own clock. */
export function partyActivity(now: number, table = 0): Mood {
  const programme = PARTY_PROGRAMMES[table % PARTY_PROGRAMMES.length];
  const round = Math.floor((now + (table % (PARTY_SLOT_MS * 3))) / PARTY_SLOT_MS);
  return programme[round % programme.length];
}

/** Whether a table is in its kick-about round. */
export function isFootballRound(now: number, table = 0): boolean {
  return partyActivity(now, table) === 'football';
}

const IDLE: Mood[] = ['watch', 'coffee', 'stretch', 'stroll', 'chat', 'phone', 'think', 'game'];
const BASE: Record<string, number> = { watch: 3, coffee: 2.2, stretch: 1.2, stroll: 1.8, chat: 2.4, phone: 1.8, think: 1.4, game: 1.3 };

/** A person's idle habits: the room's baseline, their favourite, their table's culture. */
function habits(seed: number, table: number, manager: boolean): [Mood, number][] {
  const w: Record<string, number> = { ...BASE };
  w[IDLE[seed % IDLE.length]] += 3;
  w[IDLE[(seed >>> 7) % IDLE.length]] += 1;
  w[IDLE[table % IDLE.length]] += 3;
  w[IDLE[(table >>> 5) % IDLE.length]] += 1.5;
  if (manager) {
    w.present = 3.5;
    w.phone = 0.6;
  }
  return Object.entries(w) as [Mood, number][];
}

function pick(list: [Mood, number][], r: number): Mood {
  const total = list.reduce((n, [, w]) => n + w, 0);
  let x = (r / 0x100000000) * total;
  for (const [m, w] of list) {
    x -= w;
    if (x < 0) return m;
  }
  return list[list.length - 1][0];
}

/**
 * moodFor is what a person is doing right now.
 *
 * Working beats everything; a person working another table's ticket is away
 * from this one. Clicked, they wave. A done table parties — together, on its
 * own programme (one table dances while the next is mid-kick-about) — and a
 * few dance whatever the round. A table reacts to what just happened at it:
 * applause when a ticket lands, a wince when one fails, heads turning when a
 * teammate starts. Otherwise idle people watch at first, then draw a new
 * habit every MOOD_SLOT_MS on their own offset — from weights that are theirs
 * (a favourite pastime) and their table's (a culture), so no two tables look
 * alike — with naps growing likelier the longer nothing comes their way.
 */
export function moodFor(i: LifeInputs): Mood {
  if (i.active && i.running) return 'work';
  if (i.away && i.running) return 'away';
  if (i.poked) return 'wave';
  const table = i.table ?? 0;
  if (i.partying) {
    const act = partyActivity(i.now, table);
    if (act === 'football') return 'football';
    const round = Math.floor(i.now / PARTY_SLOT_MS);
    const odd = roll(i.seed, round) % 4 === 0;
    return odd ? (act === 'dance' ? 'cheers' : 'dance') : act;
  }
  if (i.running) {
    if (i.groanAt !== undefined && i.now - i.groanAt < REACT_MS && i.now >= i.groanAt) return 'facepalm';
    if (i.cheerAt !== undefined && i.now - i.cheerAt < REACT_MS && i.now >= i.cheerAt) return 'cheer';
    if (i.buzzAt !== undefined && i.now - i.buzzAt < REACT_MS && i.now >= i.buzzAt) return 'watch';
  }
  const idle = Math.max(0, i.now - i.since);
  if (!i.running) {
    // The run stopped: people drift off one by one, not in unison.
    return idle > 4_000 + (i.seed % 20_000) ? 'nap' : 'watch';
  }
  if (idle < WATCH_MS) return 'watch';
  const offset = i.seed % MOOD_SLOT_MS;
  const slot = Math.floor((i.now + offset) / MOOD_SLOT_MS);
  const r = roll(i.seed ^ table, slot);
  // A nap is a doze, not a coma: however long a table waits for its turn in a
  // live run, most of it stays up — on a break, at the foosball table.
  const napChance = idle > SLEEPY_MS ? 30 : idle > DROWSY_MS ? 15 : 0;
  if (r % 100 < napChance) return 'nap';
  return pick(habits(i.seed, table, !!i.manager), roll(i.seed + 0x51ed27, slot));
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

/** A table's signals for its people's moods: its seed, and what just happened at it. */
export interface TableSignals {
  table: number;
  cheerAt?: number;
  groanAt?: number;
  buzzAt?: number;
}

/**
 * tableSignals reads a table's recent pulses into what its people react to:
 * the last ticket that landed (or a green gate), the last that failed or
 * blocked (or a red gate), and the last time a teammate started on something.
 */
export function tableSignals(teamId: string, pulses: FloorPulse[], crew = false): TableSignals {
  const out: TableSignals = { table: seedOf(teamId) };
  for (const p of pulses) {
    if (p.team !== teamId && !(crew && !p.team)) continue;
    const good = p.kind === 'ticket-done' || p.kind === 'team-complete' || (p.kind === 'gate' && p.tone === 'good');
    const bad = p.kind === 'ticket-failed' || p.kind === 'ticket-blocked' || (p.kind === 'gate' && p.tone === 'bad');
    if (good) out.cheerAt = Math.max(out.cheerAt ?? 0, p.at);
    if (bad) out.groanAt = Math.max(out.groanAt ?? 0, p.at);
    if (p.kind === 'agent-start') out.buzzAt = Math.max(out.buzzAt ?? 0, p.at);
  }
  return out;
}
