import { describe, expect, it } from 'vitest';
import { MOOD_SLOT_MS, PARTY_SLOT_MS, REACT_MS, SLEEPY_MS, WATCH_MS, floorShipped, isBreak, isFootballRound, isParty, moodFor, moodShows, partyActivity, seedOf, tableParties, tableSignals, type LifeInputs, type Mood } from './floorLife';
import type { FloorPulse, FloorTeam } from './floorModel';

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

  it('dozes off now and then after a long wait, but a live run never puts a table to sleep for good', () => {
    let naps = 0;
    for (let k = 0; k < 100; k++) if (moodFor(life({ now: T0 + SLEEPY_MS + 1 + k * MOOD_SLOT_MS })) === 'nap') naps++;
    expect(naps).toBeGreaterThan(10);
    expect(naps).toBeLessThan(60);
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

describe('variety', () => {
  const tables = ['backend-go', 'frontend-react', 'docs', 'infra', 'backend-python'].map(seedOf);

  it('gives every table its own party programme and clock', () => {
    // Not every table doing the same thing at the same time.
    const sameMoment = tables.map((t) => partyActivity(T0, t));
    expect(new Set(sameMoment).size).toBeGreaterThan(1);
  });

  it('gives tables different habits: the same person idles differently at two tables', () => {
    const tally = (table: number) => {
      const m = new Map<string, number>();
      for (let k = 0; k < 200; k++) {
        const now = T0 + WATCH_MS + k * MOOD_SLOT_MS;
        const mood = moodFor(life({ table, now, since: now - 30_000, seed: seedOf(`p${k % 7}`) }));
        m.set(mood, (m.get(mood) ?? 0) + 1);
      }
      return m;
    };
    const a = tally(tables[0]);
    const b = tally(tables[1]);
    const differs = [...new Set([...a.keys(), ...b.keys()])].some((k) => Math.abs((a.get(k) ?? 0) - (b.get(k) ?? 0)) > 10);
    expect(differs).toBe(true);
  });

  it('reacts to what just happened at the table, waves when clicked, and is away when working elsewhere', () => {
    const now = T0 + 60_000;
    expect(moodFor(life({ now, cheerAt: now - 1000 }))).toBe('cheer');
    expect(moodFor(life({ now, groanAt: now - 1000 }))).toBe('facepalm');
    expect(moodFor(life({ now, cheerAt: now - REACT_MS - 1 }))).not.toBe('cheer');
    expect(moodFor(life({ now, poked: true }))).toBe('wave');
    expect(moodFor(life({ now, away: true }))).toBe('away');
    expect(moodFor(life({ now, active: true, poked: true }))).toBe('work');
  });

  it('sends people to the break room and the manager to the board', () => {
    const seen = new Set<Mood>();
    for (let k = 0; k < 120; k++) seen.add(moodFor(life({ manager: true, now: T0 + WATCH_MS + k * MOOD_SLOT_MS })));
    expect(seen.has('present')).toBe(true);
    expect([...seen].some(isBreak)).toBe(true);
  });

  it('reads a table’s pulses into its reactions', () => {
    const pulse = (kind: FloorPulse['kind'], at: number, team = 'docs', tone: FloorPulse['tone'] = 'info'): FloorPulse => ({ id: `${kind}${at}`, kind, tone, at, team, text: '' });
    const s = tableSignals('docs', [pulse('ticket-done', 10), pulse('ticket-failed', 20), pulse('agent-start', 30), pulse('ticket-done', 99, 'other')]);
    expect(s).toMatchObject({ table: seedOf('docs'), cheerAt: 10, groanAt: 20, buzzAt: 30 });
  });
});
