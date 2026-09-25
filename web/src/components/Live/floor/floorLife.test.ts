import { describe, expect, it } from 'vitest';
import { MOOD_SLOT_MS, PARTY_SLOT_MS, SLEEPY_MS, WATCH_MS, floorShipped, isFootballRound, isParty, moodFor, moodShows, seedOf, tableParties, type LifeInputs, type Mood } from './floorLife';
import type { FloorTeam } from './floorModel';

const T0 = Date.parse('2026-09-10T10:00:00Z');

function life(over: Partial<LifeInputs> = {}): LifeInputs {
  return { active: false, running: true, partying: false, since: T0, now: T0, seed: seedOf('go-worker'), ...over };
}

function team(over: Partial<FloorTeam> = {}): FloorTeam {
  return { id: 'backend', name: 'Backend', manager: 'triage', managerDefault: false, agents: [], tickets: [], total: 2, done: 2, blocked: 0, inFlight: 0, complete: true, gate: '', waitingOn: [], ...over };
}

describe('moodFor', () => {
  it('works while working, whatever else is true', () => {
    expect(moodFor(life({ active: true, partying: true, since: T0 - SLEEPY_MS * 5 }))).toBe('work');
  });

  it('watches the worker when just idle', () => {
    expect(moodFor(life({ now: T0 + WATCH_MS - 1 }))).toBe('watch');
  });

  it('is deterministic: the same inputs, the same mood', () => {
    const at = life({ now: T0 + 40_000 });
    expect(moodFor(at)).toBe(moodFor({ ...at }));
  });

  it('takes breaks while idle in a live run — coffee, stretches, walks', () => {
    const seen = new Set<Mood>();
    for (let k = 0; k < 60; k++) seen.add(moodFor(life({ now: T0 + WATCH_MS + k * MOOD_SLOT_MS })));
    expect(seen.has('coffee')).toBe(true);
    expect(seen.has('stroll')).toBe(true);
    expect(seen.has('work')).toBe(false);
  });

  it('never has the whole room change mood at once', () => {
    const now = T0 + 30_000;
    const moods = new Set(Array.from({ length: 20 }, (_, i) => moodFor(life({ seed: seedOf(`agent-${i}`), now }))));
    expect(moods.size).toBeGreaterThan(1);
  });

  it('dozes off after a long wait, and sleeps after a very long one', () => {
    let naps = 0;
    for (let k = 0; k < 40; k++) if (moodFor(life({ now: T0 + SLEEPY_MS + 1 + k * MOOD_SLOT_MS })) === 'nap') naps++;
    expect(naps).toBeGreaterThan(10);
    expect(moodFor(life({ now: T0 + SLEEPY_MS * 2 + 1 }))).toBe('nap');
  });

  it('naps at the desk once the run has stopped', () => {
    expect(moodFor(life({ running: false, now: T0 + 60_000 }))).toBe('nap');
  });

  it('parties at a done table: beers, a dance, and football every third round', () => {
    const football = T0 - (T0 % (PARTY_SLOT_MS * 3)) + PARTY_SLOT_MS + 1;
    expect(isFootballRound(football)).toBe(true);
    expect(moodFor(life({ partying: true, now: football }))).toBe('football');
    const beers = football + PARTY_SLOT_MS;
    expect(['cheers', 'dance']).toContain(moodFor(life({ partying: true, now: beers })));
    expect(isParty(moodFor(life({ partying: true, now: beers })))).toBe(true);
  });

  it('badges every mood but working and watching', () => {
    expect(moodShows('work')).toBe(false);
    expect(moodShows('watch')).toBe(false);
    expect(moodShows('nap')).toBe(true);
    expect(moodShows('cheers')).toBe(true);
  });
});

describe('parties', () => {
  it('a done table parties; an unfinished one or the harness does not, until the run ships', () => {
    expect(tableParties(team(), false)).toBe(true);
    expect(tableParties(team({ complete: false, done: 1 }), false)).toBe(false);
    expect(tableParties(team({ id: 'harness', internal: true, total: 0, complete: false }), false)).toBe(false);
    expect(tableParties(team({ id: 'harness', internal: true, total: 0, complete: false }), true)).toBe(true);
  });

  it('the floor ships when the run is over and every work table is done', () => {
    const harness = team({ id: 'harness', internal: true, total: 0, complete: false });
    expect(floorShipped({ teams: [harness, team()] }, false)).toBe(true);
    expect(floorShipped({ teams: [harness, team()] }, true)).toBe(false);
    expect(floorShipped({ teams: [harness, team(), team({ id: 'web', complete: false, done: 0 })] }, false)).toBe(false);
    expect(floorShipped({ teams: [harness] }, false)).toBe(false);
  });
});
