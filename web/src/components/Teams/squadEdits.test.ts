import { describe, expect, it } from 'vitest';
import { buildSquadEdits, countSquadEdits, type InterfaceDraft } from './squadEdits';
import type { PlanInterface, SquadStatus } from '@/types';

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

const originalIfaces: PlanInterface[] = [
  { id: 'GET /api/todos', provider: 'backend', consumers: ['frontend'], spec: '200 -> []' },
];

const draft = (): SquadStatus[] => original.map((s) => ({ ...s, owns: [...(s.owns ?? [])] }));
const ifaceDraft = (): InterfaceDraft[] =>
  originalIfaces.map((i) => ({ ...i, _key: i.id, consumers: [...(i.consumers ?? [])] }));

describe('buildSquadEdits', () => {
  // An absent field means "leave it alone" on the Go side. Echoing unchanged
  // values back would be indistinguishable from an intentional overwrite.
  it('sends nothing when nothing changed', () => {
    expect(buildSquadEdits(original, draft(), originalIfaces, ifaceDraft())).toEqual({});
    expect(countSquadEdits(buildSquadEdits(original, draft(), originalIfaces, ifaceDraft()))).toBe(0);
  });

  it('sends only the fields the user touched', () => {
    const d = draft();
    d[0].name = 'Backend · Go API';
    const edits = buildSquadEdits(original, d, originalIfaces, ifaceDraft());

    expect(edits.squads).toEqual([{ id: 'backend', name: 'Backend · Go API' }]);
    expect(edits.squads![0].owns).toBeUndefined();
    expect(edits.squads![0].acceptance).toBeUndefined();
  });

  // A bare `owns: []` cannot say whether the user cleared the list or never
  // opened the field. The flag is what distinguishes them.
  it('marks a changed ownership list as explicitly set', () => {
    const d = draft();
    d[0].owns = ['cmd/**'];
    const edits = buildSquadEdits(original, d, originalIfaces, ifaceDraft());

    expect(edits.squads).toHaveLength(1);
    expect(edits.squads![0].owns).toEqual(['cmd/**']);
    expect(edits.squads![0].owns_set).toBe(true);
  });

  it('marks a cleared ownership list as explicitly set', () => {
    const d = draft();
    d[0].owns = [];
    const edits = buildSquadEdits(original, d, originalIfaces, ifaceDraft());

    expect(edits.squads![0].owns).toEqual([]);
    expect(edits.squads![0].owns_set).toBe(true);
  });

  // Reordering globs is not a change of ownership, but it IS a change of the
  // list, and the harness stores what it is given.
  it('treats a reordered ownership list as a change', () => {
    const d = draft();
    d[0].owns = ['internal/**', 'cmd/**'];
    expect(buildSquadEdits(original, d, originalIfaces, ifaceDraft()).squads).toHaveLength(1);
  });

  it('carries one edit per touched team and skips the rest', () => {
    const d = draft();
    d[0].acceptance = 'go test -race ./...';
    d[1].name = 'Web';
    const edits = buildSquadEdits(original, d, originalIfaces, ifaceDraft());

    expect(edits.squads).toHaveLength(2);
    expect(edits.squads![0]).toEqual({ id: 'backend', acceptance: 'go test -race ./...' });
    expect(edits.squads![1]).toEqual({ id: 'frontend', name: 'Web' });
  });

  it('sends an attached manager', () => {
    const d = draft();
    d[0].manager = 'backend-triage';
    expect(buildSquadEdits(original, d, originalIfaces, ifaceDraft()).squads).toEqual([
      { id: 'backend', manager: 'backend-triage' },
    ]);
  });

  // Empty is a real choice, not an unset field: it hands the team back to the
  // run's default manager, so it has to travel rather than be dropped as falsy.
  it('sends a cleared manager so the run default takes over', () => {
    const withPM = original.map((s) => (s.id === 'backend' ? { ...s, manager: 'backend-triage' } : s));
    const d = withPM.map((s) => ({ ...s }));
    d[0].manager = '';
    expect(buildSquadEdits(withPM, d, originalIfaces, ifaceDraft()).squads).toEqual([
      { id: 'backend', manager: '' },
    ]);
  });

  // Every seat a team can staff has to travel. A seat the form offers and the
  // diff drops is a control that does nothing and says nothing.
  it('sends every staffing seat the form offers', () => {
    const d = draft();
    d[0].worker = 'go-worker';
    d[0].reviewer = 'go-reviewer';
    d[0].tester = 'go-tester';
    expect(buildSquadEdits(original, d, originalIfaces, ifaceDraft()).squads).toEqual([
      { id: 'backend', worker: 'go-worker', reviewer: 'go-reviewer', tester: 'go-tester' },
    ]);
  });

  it('sends the charter, which is what keeps a worker inside its half', () => {
    const d = draft();
    d[0].charter = 'own the Go service and nothing else';
    expect(buildSquadEdits(original, d, originalIfaces, ifaceDraft()).squads).toEqual([
      { id: 'backend', charter: 'own the Go service and nothing else' },
    ]);
  });

  // A team that is not in the draft any more was removed. The old behaviour —
  // silently ignoring it — meant a user could delete a team, save, and watch
  // nothing happen.
  it('removes a team dropped from the draft', () => {
    const d = draft().filter((s) => s.id !== 'frontend');
    const edits = buildSquadEdits(original, d, originalIfaces, ifaceDraft());
    expect(edits.remove_squads).toEqual(['frontend']);
    expect(edits.squads).toBeUndefined();
  });

  // A team added from the library has nothing to merge against, so everything
  // travels and `new` tells the harness which case this is.
  it('sends a whole team when one is added', () => {
    const d = [...draft(), squad({ id: 'docs', name: 'Docs', owns: ['docs/**'], charter: 'the prose' })];
    const added = buildSquadEdits(original, d, originalIfaces, ifaceDraft()).squads!.find((s) => s.id === 'docs');
    expect(added).toMatchObject({ id: 'docs', new: true, owns: ['docs/**'], owns_set: true });
  });

  // An interface id IS its name — a route, a symbol. Correcting a wrong one is
  // a rename; delete-then-add loses the spec the user did not want to retype.
  it('renames a contract clause rather than replacing it', () => {
    const ifaces = ifaceDraft();
    ifaces[0].id = 'GET /api/v2/todos';
    const edits = buildSquadEdits(original, draft(), originalIfaces, ifaces);

    expect(edits.interfaces).toEqual([{ id: 'GET /api/todos', rename: 'GET /api/v2/todos' }]);
    expect(edits.remove_interfaces).toBeUndefined();
  });

  it('adds and removes contract clauses', () => {
    const ifaces = ifaceDraft();
    ifaces[0]._removed = true;
    ifaces.push({
      _key: 'new-1',
      _new: true,
      id: 'POST /api/todos',
      provider: 'backend',
      consumers: ['frontend'],
      spec: '{title} -> 201',
    });
    const edits = buildSquadEdits(original, draft(), originalIfaces, ifaces);

    expect(edits.remove_interfaces).toEqual(['GET /api/todos']);
    expect(edits.interfaces![0]).toMatchObject({ id: 'POST /api/todos', new: true, provider: 'backend' });
  });

  it('drops a new clause with no name or no provider', () => {
    const ifaces = ifaceDraft();
    ifaces.push({ _key: 'new-1', _new: true, id: '  ', provider: 'backend' });
    ifaces.push({ _key: 'new-2', _new: true, id: 'GET /x', provider: '' });
    expect(buildSquadEdits(original, draft(), originalIfaces, ifaces).interfaces).toBeUndefined();
  });

  it('counts teams and contract clauses together', () => {
    const d = draft();
    d[0].name = 'Backend API';
    const ifaces = ifaceDraft();
    ifaces[0].spec = '200 -> [{id,title,done}]';
    expect(countSquadEdits(buildSquadEdits(original, d, originalIfaces, ifaces))).toBe(2);
  });
});
