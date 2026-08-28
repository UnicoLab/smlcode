import { describe, expect, it } from 'vitest';
import { buildEdits, countChanges, type TaskDraft } from './planEdits';
import type { PlanApprovalTask, PlanSquad } from '@/types';

const original: PlanApprovalTask[] = [
  { id: 'T1', title: 'api', role: 'go-worker', squad: 'backend', files: ['cmd/main.go'] },
  { id: 'T2', title: 'ui', role: 'worker', squad: 'frontend', files: ['web/App.tsx'] },
];

const squads: PlanSquad[] = [
  { id: 'backend', name: 'Backend', owns: ['cmd/**'], acceptance: 'go test ./...', worker: 'go-worker', task_count: 1 },
  { id: 'frontend', name: 'Frontend', owns: ['web/**'], acceptance: 'npm run build', worker: 'react-worker', task_count: 1 },
];

const draft = (): TaskDraft[] => original.map((t) => ({ ...t }));
const squadDraft = (): PlanSquad[] => squads.map((s) => ({ ...s }));

describe('buildEdits', () => {
  // The Go side treats an absent field as "untouched" and a present one as
  // "set to this". Sending unchanged values would be indistinguishable from an
  // intentional overwrite.
  it('sends nothing when nothing changed', () => {
    expect(buildEdits(original, draft(), squads, squadDraft())).toEqual({});
    expect(countChanges(original, draft(), squads, squadDraft())).toBe(0);
  });

  it('sends only the fields the user touched', () => {
    const d = draft();
    d[0].title = 'serve the todo API';
    const edits = buildEdits(original, d, squads, squadDraft());

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
    d[0].files = ['cmd/server/main.go', 'internal/store/todo.go'];
    const edits = buildEdits(original, d, squads, squadDraft());
    expect(edits.tasks![0].files_set).toBe(true);
    expect(edits.tasks![0].files).toEqual(['cmd/server/main.go', 'internal/store/todo.go']);
  });

  it('marks a cleared file list as set rather than omitting it', () => {
    const d = draft();
    d[0].files = [];
    const edits = buildEdits(original, d, squads, squadDraft());
    expect(edits.tasks![0].files_set).toBe(true);
    expect(edits.tasks![0].files).toEqual([]);
  });

  it('reports a reassigned team and agent', () => {
    const d = draft();
    d[1].role = 'react-worker';
    d[1].squad = 'backend';
    const edits = buildEdits(original, d, squads, squadDraft());
    expect(edits.tasks![0]).toMatchObject({ id: 'T2', role: 'react-worker', squad: 'backend' });
  });

  it('collects removals separately from edits', () => {
    const d = draft();
    d[0]._removed = true;
    d[1].title = 'build the SPA';
    const edits = buildEdits(original, d, squads, squadDraft());

    expect(edits.remove_tasks).toEqual(['T1']);
    // A removed task contributes no field edits — the harness is deleting it.
    expect(edits.tasks).toHaveLength(1);
    expect(edits.tasks![0].id).toBe('T2');
  });

  // "Edited, then edited back" must produce no diff. An accumulated diff
  // cannot express that and would send a no-op the harness has to reason about.
  it('produces nothing after an edit is reverted', () => {
    const d = draft();
    d[0].title = 'changed';
    expect(buildEdits(original, d, squads, squadDraft()).tasks).toHaveLength(1);
    d[0].title = 'api';
    expect(buildEdits(original, d, squads, squadDraft())).toEqual({});
  });

  it('sends added tasks and drops blank ones', () => {
    const d = draft();
    d.push({ id: 'NEW-3', title: '  wire the router  ', role: 'go-worker', files: ['cmd/router.go'] });
    d.push({ id: 'NEW-4', title: '   ', role: 'worker' });
    const edits = buildEdits(original, d, squads, squadDraft());

    expect(edits.add_tasks).toHaveLength(1);
    expect(edits.add_tasks![0]).toMatchObject({ title: 'wire the router', role: 'go-worker' });
  });

  it('does not send an added task the user then removed', () => {
    const d = draft();
    d.push({ id: 'NEW-3', title: 'never mind', role: 'worker', _removed: true });
    expect(buildEdits(original, d, squads, squadDraft()).add_tasks).toBeUndefined();
  });

  it('reports squad edits with an explicit owns flag', () => {
    const sd = squadDraft();
    sd[0].acceptance = 'go test ./... -race';
    sd[0].owns = ['cmd/**', 'internal/**'];
    const edits = buildEdits(original, draft(), squads, sd);

    expect(edits.squads).toHaveLength(1);
    expect(edits.squads![0]).toEqual({
      id: 'backend',
      acceptance: 'go test ./... -race',
      owns: ['cmd/**', 'internal/**'],
      owns_set: true,
    });
  });

  it('counts every kind of change', () => {
    const d = draft();
    const sd = squadDraft();
    d[0].title = 'x';
    d[1]._removed = true;
    d.push({ id: 'NEW-3', title: 'added', role: 'worker' });
    sd[0].worker = 'worker';
    expect(countChanges(original, d, squads, sd)).toBe(4);
  });
});
