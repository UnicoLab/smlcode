import { describe, expect, it } from 'vitest';
import {
  buildEdits,
  countChanges,
  type InterfaceDraft,
  type PlanDraft,
  type PlanOriginal,
  type SquadDraft,
  type TaskDraft,
} from './planEdits';
import type { PlanApprovalTask, PlanInterface, PlanSquad } from '@/types';

const tasks: PlanApprovalTask[] = [
  { id: 'T1', title: 'api', role: 'go-worker', squad: 'backend', files: ['cmd/main.go'] },
  { id: 'T2', title: 'ui', role: 'worker', squad: 'frontend', files: ['web/App.tsx'] },
];

const squads: PlanSquad[] = [
  { id: 'backend', name: 'Backend', owns: ['cmd/**'], acceptance: 'go test ./...', worker: 'go-worker', task_count: 1 },
  { id: 'frontend', name: 'Frontend', owns: ['web/**'], acceptance: 'npm run build', worker: 'react-worker', task_count: 1 },
];

const interfaces: PlanInterface[] = [
  { id: 'GET /api/todos', provider: 'backend', consumers: ['frontend'], spec: '200 -> []' },
];

const original: PlanOriginal = { tasks, squads, interfaces };

const draft = (): PlanDraft => ({
  tasks: tasks.map((t) => ({ ...t })) as TaskDraft[],
  squads: squads.map((s) => ({ ...s })) as SquadDraft[],
  interfaces: interfaces.map((i) => ({ ...i, _key: i.id, consumers: [...(i.consumers ?? [])] })) as InterfaceDraft[],
});

describe('buildEdits', () => {
  // The Go side treats an absent field as "untouched" and a present one as
  // "set to this". Sending unchanged values would be indistinguishable from an
  // intentional overwrite.
  it('sends nothing when nothing changed', () => {
    expect(buildEdits(original, draft())).toEqual({});
    expect(countChanges(original, draft())).toBe(0);
  });

  it('sends only the fields the user touched', () => {
    const d = draft();
    d.tasks[0].title = 'serve the todo API';
    const edits = buildEdits(original, d);

    expect(edits.tasks).toHaveLength(1);
    expect(edits.tasks![0]).toEqual({ id: 'T1', title: 'serve the todo API' });
    // Untouched fields are absent, not echoed back.
    expect(edits.tasks![0].role).toBeUndefined();
    expect(edits.tasks![0].files).toBeUndefined();
    expect(edits.remove_tasks).toBeUndefined();
  });

  // A bare `files: []` cannot say whether the user cleared the list or never
  // opened it. The flag is what distinguishes them.
  it('marks a changed file list as explicitly set', () => {
    const d = draft();
    d.tasks[0].files = ['cmd/server/main.go', 'internal/store/todo.go'];
    const edits = buildEdits(original, d);
    expect(edits.tasks![0].files_set).toBe(true);
    expect(edits.tasks![0].files).toEqual(['cmd/server/main.go', 'internal/store/todo.go']);
  });

  it('marks a cleared file list as set rather than omitting it', () => {
    const d = draft();
    d.tasks[0].files = [];
    const edits = buildEdits(original, d);
    expect(edits.tasks![0].files_set).toBe(true);
    expect(edits.tasks![0].files).toEqual([]);
  });

  it('reports a reassigned team and agent', () => {
    const d = draft();
    d.tasks[1].role = 'react-worker';
    d.tasks[1].squad = 'backend';
    const edits = buildEdits(original, d);
    expect(edits.tasks![0]).toMatchObject({ id: 'T2', role: 'react-worker', squad: 'backend' });
  });

  // The full set of task fields, because "I cannot edit much" was the actual
  // complaint: description, acceptance and priority are things a plan gets
  // subtly wrong, and every one of them used to require a full replan to fix.
  it('sends description, acceptance and priority', () => {
    const d = draft();
    d.tasks[0].description = 'serve GET and POST for todos';
    d.tasks[0].acceptance = 'go test ./internal/store/...';
    d.tasks[0].priority = 3;
    const edits = buildEdits(original, d);

    expect(edits.tasks![0]).toMatchObject({
      id: 'T1',
      description: 'serve GET and POST for todos',
      acceptance: 'go test ./internal/store/...',
      priority: 3,
    });
  });

  // "Delay this task" has exactly one meaning on a wave-dispatching board:
  // it runs after the tasks it depends on. Ordering is dependencies.
  it('delays a task by making it wait on another', () => {
    const d = draft();
    d.tasks[1].depends_on = ['T1'];
    const edits = buildEdits(original, d);

    expect(edits.tasks![0]).toMatchObject({ id: 'T2', depends_on: ['T1'], depends_set: true });
  });

  it('marks a cleared dependency list as set so the task is freed', () => {
    const withDep: PlanOriginal = {
      ...original,
      tasks: [tasks[0], { ...tasks[1], depends_on: ['T1'] }],
    };
    const d: PlanDraft = { ...draft(), tasks: withDep.tasks.map((t) => ({ ...t })) };
    d.tasks[1].depends_on = [];
    const edits = buildEdits(withDep, d);

    expect(edits.tasks![0]).toMatchObject({ id: 'T2', depends_on: [], depends_set: true });
  });

  it('collects removals separately from edits', () => {
    const d = draft();
    d.tasks[0]._removed = true;
    d.tasks[1].title = 'build the SPA';
    const edits = buildEdits(original, d);

    expect(edits.remove_tasks).toEqual(['T1']);
    // A removed task contributes no field edits — the harness is deleting it.
    expect(edits.tasks).toHaveLength(1);
    expect(edits.tasks![0].id).toBe('T2');
  });

  // "Edited, then edited back" must produce no diff. An accumulated diff
  // cannot express that and would send a no-op the harness has to reason about.
  it('produces nothing after an edit is reverted', () => {
    const d = draft();
    d.tasks[0].title = 'changed';
    expect(buildEdits(original, d).tasks).toHaveLength(1);
    d.tasks[0].title = 'api';
    expect(buildEdits(original, d)).toEqual({});
  });

  it('sends added tasks and drops blank ones', () => {
    const d = draft();
    d.tasks.push({ id: 'NEW-1', title: '  wire the router  ', role: 'go-worker', files: ['cmd/router.go'] });
    d.tasks.push({ id: 'NEW-2', title: '   ', role: 'worker' });
    const edits = buildEdits(original, d);

    expect(edits.add_tasks).toHaveLength(1);
    expect(edits.add_tasks![0]).toMatchObject({ title: 'wire the router', role: 'go-worker' });
  });

  // The placeholder id has to survive to the harness: it is the reference that
  // makes "T1 now waits on the task I just added" expressible before the board
  // has assigned a real id.
  it('keeps a new task’s placeholder id so others can depend on it', () => {
    const d = draft();
    d.tasks.push({ id: 'NEW-1', title: 'migrate the schema', role: 'go-worker' });
    d.tasks[0].depends_on = ['NEW-1'];
    const edits = buildEdits(original, d);

    expect(edits.add_tasks![0].id).toBe('NEW-1');
    expect(edits.tasks![0]).toMatchObject({ id: 'T1', depends_on: ['NEW-1'], depends_set: true });
  });

  it('does not send an added task the user then removed', () => {
    const d = draft();
    d.tasks.push({ id: 'NEW-1', title: 'never mind', role: 'worker', _removed: true });
    expect(buildEdits(original, d).add_tasks).toBeUndefined();
  });

  it('reports squad edits with an explicit owns flag', () => {
    const d = draft();
    d.squads[0].acceptance = 'go test ./... -race';
    d.squads[0].owns = ['cmd/**', 'internal/**'];
    const edits = buildEdits(original, d);

    expect(edits.squads).toHaveLength(1);
    expect(edits.squads![0]).toEqual({
      id: 'backend',
      acceptance: 'go test ./... -race',
      owns: ['cmd/**', 'internal/**'],
      owns_set: true,
    });
  });

  it('sends every staffing seat a team can fill', () => {
    const d = draft();
    d.squads[0].worker = 'go-worker';
    d.squads[0].reviewer = 'go-reviewer';
    d.squads[0].tester = 'go-tester';
    d.squads[0].manager = 'backend-triage';
    expect(buildEdits(original, d).squads).toEqual([
      {
        id: 'backend',
        reviewer: 'go-reviewer',
        tester: 'go-tester',
        manager: 'backend-triage',
      },
    ]);
  });

  // A team added from the library has nothing on the harness side to merge
  // against, so everything travels and `new` says which case this is.
  it('sends a whole team when one is added from the library', () => {
    const d = draft();
    d.squads.push({
      id: 'docs',
      name: 'Docs',
      charter: 'the prose',
      owns: ['docs/**'],
      acceptance: '',
      task_count: 0,
      _new: true,
    } as SquadDraft);
    const edits = buildEdits(original, d);

    const added = edits.squads!.find((s) => s.id === 'docs');
    expect(added).toMatchObject({ id: 'docs', new: true, owns: ['docs/**'], owns_set: true, charter: 'the prose' });
  });

  it('removes a team', () => {
    const d = draft();
    d.squads[1]._removed = true;
    expect(buildEdits(original, d).remove_squads).toEqual(['frontend']);
  });

  // The contract is the one artifact a two-team run cannot recover from
  // getting wrong: both halves build against it and neither can ask the other.
  it('renames a contract clause rather than replacing it', () => {
    const d = draft();
    d.interfaces[0].id = 'GET /api/v2/todos';
    const edits = buildEdits(original, d);

    expect(edits.interfaces).toEqual([{ id: 'GET /api/todos', rename: 'GET /api/v2/todos' }]);
    expect(edits.remove_interfaces).toBeUndefined();
  });

  it('edits a clause spec and its consumers', () => {
    const d = draft();
    d.interfaces[0].spec = '200 -> [{id,title,done}]';
    d.interfaces[0].consumers = [];
    const edits = buildEdits(original, d);

    expect(edits.interfaces![0]).toMatchObject({
      id: 'GET /api/todos',
      spec: '200 -> [{id,title,done}]',
      consumers: [],
      consumers_set: true,
    });
  });

  it('adds and removes contract clauses', () => {
    const d = draft();
    d.interfaces.push({
      _key: 'new-1',
      _new: true,
      id: 'POST /api/todos',
      provider: 'backend',
      consumers: ['frontend'],
      spec: '{title} -> 201',
    });
    d.interfaces[0]._removed = true;
    const edits = buildEdits(original, d);

    expect(edits.remove_interfaces).toEqual(['GET /api/todos']);
    expect(edits.interfaces).toEqual([
      {
        id: 'POST /api/todos',
        new: true,
        provider: 'backend',
        consumers: ['frontend'],
        consumers_set: true,
        spec: '{title} -> 201',
      },
    ]);
  });

  // A clause with no name names nothing, and one with no provider is owed by
  // nobody. Dropping them beats sending the harness something it can only
  // refuse — and a refusal costs the user every other edit in the same pass.
  it('drops an unnamed or unowned new clause', () => {
    const d = draft();
    d.interfaces.push({ _key: 'new-1', _new: true, id: '   ', provider: 'backend' });
    d.interfaces.push({ _key: 'new-2', _new: true, id: 'GET /x', provider: '' });
    expect(buildEdits(original, d).interfaces).toBeUndefined();
  });

  it('counts every kind of change', () => {
    const d = draft();
    d.tasks[0].title = 'x';
    d.tasks[1]._removed = true;
    d.tasks.push({ id: 'NEW-1', title: 'added', role: 'worker' });
    d.squads[0].worker = 'worker';
    d.interfaces[0].spec = 'changed';
    expect(countChanges(original, d)).toBe(5);
  });
});
