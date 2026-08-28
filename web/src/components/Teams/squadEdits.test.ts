import { describe, expect, it } from 'vitest';
import { buildSquadEdits } from './squadEdits';
import type { SquadStatus } from '@/types';

const squad = (over: Partial<SquadStatus> & { id: string }): SquadStatus => ({
  total: 0,
  done: 0,
  blocked: 0,
  in_flight: 0,
  complete: false,
  stuck: false,
  ...over,
});

const original: SquadStatus[] = [
  squad({ id: 'backend', name: 'Backend', owns: ['cmd/**', 'internal/**'], acceptance: 'go test ./...' }),
  squad({ id: 'frontend', name: 'Frontend', owns: ['web/**'], acceptance: 'npm run build' }),
];

const draft = (): SquadStatus[] => original.map((s) => ({ ...s, owns: [...(s.owns ?? [])] }));

describe('buildSquadEdits', () => {
  // An absent field means "leave it alone" on the Go side. Echoing unchanged
  // values back would be indistinguishable from an intentional overwrite.
  it('sends nothing when nothing changed', () => {
    expect(buildSquadEdits(original, draft())).toEqual([]);
  });

  it('sends only the fields the user touched', () => {
    const d = draft();
    d[0].name = 'Backend · Go API';
    const edits = buildSquadEdits(original, d);

    expect(edits).toEqual([{ id: 'backend', name: 'Backend · Go API' }]);
    expect(edits[0].owns).toBeUndefined();
    expect(edits[0].acceptance).toBeUndefined();
  });

  // A bare `owns: []` cannot say whether the user cleared the list or never
  // opened the field. The flag is what distinguishes them.
  it('marks a changed ownership list as explicitly set', () => {
    const d = draft();
    d[0].owns = ['cmd/**'];
    const edits = buildSquadEdits(original, d);

    expect(edits).toHaveLength(1);
    expect(edits[0].owns).toEqual(['cmd/**']);
    expect(edits[0].owns_set).toBe(true);
  });

  it('marks a cleared ownership list as explicitly set', () => {
    const d = draft();
    d[0].owns = [];
    const edits = buildSquadEdits(original, d);

    expect(edits[0].owns).toEqual([]);
    expect(edits[0].owns_set).toBe(true);
  });

  // Reordering globs is not a change of ownership, but it IS a change of the
  // list, and the harness stores what it is given.
  it('treats a reordered ownership list as a change', () => {
    const d = draft();
    d[0].owns = ['internal/**', 'cmd/**'];
    expect(buildSquadEdits(original, d)).toHaveLength(1);
  });

  it('carries one edit per touched team and skips the rest', () => {
    const d = draft();
    d[0].acceptance = 'go test -race ./...';
    d[1].name = 'Web';
    const edits = buildSquadEdits(original, d);

    expect(edits).toHaveLength(2);
    expect(edits[0]).toEqual({ id: 'backend', acceptance: 'go test -race ./...' });
    expect(edits[1]).toEqual({ id: 'frontend', name: 'Web' });
  });

  it('sends an attached manager', () => {
    const d = draft();
    d[0].manager = 'backend-triage';
    expect(buildSquadEdits(original, d)).toEqual([{ id: 'backend', manager: 'backend-triage' }]);
  });

  // Empty is a real choice, not an unset field: it hands the team back to the
  // run's default manager, so it has to travel rather than be dropped as falsy.
  it('sends a cleared manager so the run default takes over', () => {
    const withPM = original.map((s) => (s.id === 'backend' ? { ...s, manager: 'backend-triage' } : s));
    const d = withPM.map((s) => ({ ...s }));
    d[0].manager = '';
    expect(buildSquadEdits(withPM, d)).toEqual([{ id: 'backend', manager: '' }]);
  });

  // A team the server does not know about cannot be patched — PATCH edits an
  // existing plan, and inventing an id would be silently dropped server-side.
  it('ignores a draft team the server never reported', () => {
    const d = [...draft(), squad({ id: 'ghost', name: 'Ghost' })];
    expect(buildSquadEdits(original, d)).toEqual([]);
  });
});
