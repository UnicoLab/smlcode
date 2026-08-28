import { describe, expect, it } from 'vitest';
import { buildRecovery, recoveryTally } from './recovery';
import type { RunEvent } from '@/types';

let clock = 0;
const loop = (action: string, reason: string, extra: Record<string, unknown> = {}): RunEvent => ({
  phase: 'test',
  kind: 'loop',
  message: reason,
  scope: action,
  output: JSON.stringify({ action, reason, wave: 1, ...extra }),
  time: new Date(1_700_000_000_000 + clock++ * 1000).toISOString(),
});

const line = (message: string): RunEvent => ({
  phase: 'plan',
  kind: 'output',
  message,
  time: new Date(1_700_000_000_000 + clock++ * 1000).toISOString(),
});

// The run this panel exists for: a defect found, ticketed, repeated, moved by
// the project manager to a specialist, and fixed.
const fullRecovery = (): RunEvent[] => {
  clock = 0;
  return [
    loop('tester_reject', 'cmd/server/main.go', { failures: ['undefined: json.NewEncoder'] }),
    loop('rewrite', 'reopened/added corrective tasks from tester failures'),
    loop('corrective_wave', 'tester not satisfied — running corrective execute wave'),
    loop('reverify', 're-verifying after corrective wave'),
    loop('tester_reject', 'cmd/server/main.go', { failures: ['undefined: json.NewEncoder'] }),
    line('T1 reassigned from go-worker to go-corrector — the worker cannot see its own encoder bug'),
    loop('restaffed_wave', 'the project manager moved the ticket to a different specialist'),
    loop('resolved', 'the re-staffed specialist fixed it'),
  ];
};

describe('buildRecovery', () => {
  it('is empty on a run where nothing went wrong', () => {
    expect(buildRecovery([])).toEqual([]);
    expect(buildRecovery([line('tester passed'), { ...line('x'), kind: 'agent_end' }])).toEqual([]);
  });

  it('tells the whole story of one defect as a single row', () => {
    const got = buildRecovery(fullRecovery());
    expect(got).toHaveLength(1);
    const ep = got[0];
    expect(ep.state).toBe('resolved');
    expect(ep.failures).toEqual(['undefined: json.NewEncoder']);
    expect(ep.steps.map((s) => s.action)).toEqual([
      'tester_reject',
      'rewrite',
      'corrective_wave',
      'reverify',
      'tester_reject',
      'restaffed_wave',
      'resolved',
    ]);
  });

  // A defect that comes back is the SAME problem, not a new one. Two rows for
  // one defect makes a run look twice as broken as it is.
  it('counts a repeat as another attempt at one defect', () => {
    const ep = buildRecovery(fullRecovery())[0];
    expect(ep.attempts).toBe(2);
  });

  // "The manager moved it" without saying where to is the kind of half answer
  // that makes a user distrust the whole panel.
  it('names who the work was moved to', () => {
    expect(buildRecovery(fullRecovery())[0].handedTo).toBe('go-corrector');
  });

  it('marks an episode nobody has closed as still healing', () => {
    const events = fullRecovery().slice(0, -1);
    const ep = buildRecovery(events)[0];
    expect(ep.state).toBe('healing');
  });

  it('marks an episode that needs a person', () => {
    const events = [...fullRecovery().slice(0, -1), loop('unresolved', 'still failing after reassignment')];
    expect(buildRecovery(events)[0].state).toBe('needs-you');
  });

  it('treats an awaiting loop event as needing a person', () => {
    clock = 0;
    const events = [
      loop('tester_reject', 'boom'),
      { ...loop('continue_wave', 'waiting on you'), scope: 'continue_wave:awaiting', output: JSON.stringify({ action: 'continue_wave', awaiting: true }) },
    ];
    expect(buildRecovery(events)[0].state).toBe('needs-you');
  });

  // A second defect after the first closed is its own row.
  it('separates defects that do not overlap', () => {
    clock = 0;
    const events = [
      loop('tester_reject', 'first'),
      loop('resolved', 'fixed'),
      loop('tester_reject', 'second'),
      loop('resolved', 'fixed too'),
    ];
    const got = buildRecovery(events);
    expect(got).toHaveLength(2);
    expect(got.map((e) => e.found)).toEqual(['first', 'second']);
    expect(got.every((e) => e.state === 'resolved')).toBe(true);
  });

  // Repeated re-verifies say nothing the first did not.
  it('collapses a step repeated back to back', () => {
    clock = 0;
    const events = [loop('tester_reject', 'boom'), loop('reverify', 'a'), loop('reverify', 'b')];
    expect(buildRecovery(events)[0].steps.filter((s) => s.action === 'reverify')).toHaveLength(1);
  });

  // A loop event whose body did not survive the stream is still a loop event.
  it('falls back to the scope when the payload is unreadable', () => {
    clock = 0;
    const broken: RunEvent = { ...loop('tester_reject', 'boom'), output: 'not json' };
    const got = buildRecovery([broken, { ...loop('resolved', 'fixed'), output: '{' }]);
    expect(got).toHaveLength(1);
    expect(got[0].state).toBe('resolved');
  });

  // Events before the episode opened are not its handoff.
  it('does not attribute an earlier reassignment to a later episode', () => {
    clock = 0;
    const events = [
      line('T9 reassigned from a to b'),
      loop('tester_reject', 'boom'),
      loop('resolved', 'fixed'),
    ];
    // The reassignment predates the wave, but there is no wave step here at
    // all — nothing should claim a handoff.
    expect(buildRecovery(events)[0].handedTo).toBeUndefined();
  });
});

describe('recoveryTally', () => {
  it('counts each state', () => {
    const eps = buildRecovery(fullRecovery());
    expect(recoveryTally(eps)).toEqual({ healing: 0, resolved: 1, needsYou: 0 });
    expect(recoveryTally([])).toEqual({ healing: 0, resolved: 0, needsYou: 0 });
  });
});
