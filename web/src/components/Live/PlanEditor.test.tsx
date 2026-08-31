import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import PlanEditor from './PlanEditor';
import type { PlanAsk, PlanEdits } from '@/types';

const ask: PlanAsk = {
  id: 'plan-1',
  query: 'build a todo app: a Go backend serving a React frontend',
  summary: 'Two halves behind a frozen contract.',
  task_count: 3,
  tasks: [],
  task_details: [
    { id: 'T1', title: 'todo store', role: 'go-worker', squad: 'backend', files: ['internal/store/todo.go'] },
    { id: 'T2', title: 'HTTP routes', role: 'worker', squad: 'backend', files: ['cmd/server/main.go'] },
    { id: 'T3', title: 'todo list view', role: 'react-worker', squad: 'frontend', files: ['web/src/TodoList.tsx'] },
  ],
  squads: {
    summary: 'Go API + React SPA',
    squads: [
      { id: 'backend', name: 'Backend', owns: ['cmd/**', 'internal/**'], acceptance: 'go test ./...', worker: 'go-worker', task_count: 2 },
      { id: 'frontend', name: 'Frontend', owns: ['web/**'], acceptance: 'npm run build', worker: 'react-worker', task_count: 1 },
    ],
    interfaces: [{ id: 'GET /api/todos', provider: 'backend', consumers: ['frontend'], spec: '200 -> []' }],
    integration: 'go test ./... && npm run build',
  },
  agents: ['worker', 'go-worker', 'react-worker', 'tester'],
  managers: ['backend-triage', 'triage'],
  library: [
    { id: 'docs', name: 'Docs', charter: 'the prose', owns: ['docs/**'], acceptance: '' },
    { id: 'backend', name: 'Backend · Go', owns: ['cmd/**'], on_run: true },
  ],
};

function renderEditor(over?: Partial<PlanAsk>) {
  const onChange = vi.fn<(e: PlanEdits) => void>();
  render(<PlanEditor ask={{ ...ask, ...over }} onChange={onChange} />);
  const last = () => {
    const calls = onChange.mock.calls;
    return calls.length ? calls[calls.length - 1][0] : {};
  };
  return { onChange, last };
}

/** taskCard scopes a query to one task, since ids now appear as dep toggles too. */
function taskCard(id: string): HTMLElement {
  return screen.getByLabelText(`Title of task ${id}`).closest('div.rounded-lg')!;
}

describe('PlanEditor', () => {
  it('shows the teams and every task', () => {
    renderEditor();
    // Team names are editable inputs, so assert on their values.
    expect(screen.getByLabelText('Name of squad backend')).toHaveValue('Backend');
    expect(screen.getByLabelText('Name of squad frontend')).toHaveValue('Frontend');
    for (const id of ['T1', 'T2', 'T3']) {
      expect(screen.getByLabelText(`Title of task ${id}`)).toBeInTheDocument();
    }
    // Nothing edited yet.
    expect(screen.getByText('no changes')).toBeInTheDocument();
  });

  it('emits only the field the user changed', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();

    const title = screen.getByLabelText('Title of task T2');
    await user.clear(title);
    await user.type(title, 'serve the todo routes');

    const edits = last();
    expect(edits.tasks).toHaveLength(1);
    expect(edits.tasks![0].id).toBe('T2');
    expect(edits.tasks![0].title).toBe('serve the todo routes');
    // Untouched fields are absent, not echoed back — the harness reads an
    // absent field as "leave it alone".
    expect(edits.tasks![0].role).toBeUndefined();
    expect(edits.tasks![0].files).toBeUndefined();
  });

  // The picker must offer the agents the harness said it can dispatch; a
  // hardcoded list would let a user pick one that never starts.
  //
  // The task's own team comes FIRST — its people are the likely answer for its
  // work — but nobody is removed: the reason a task needs reassigning is often
  // that its team lacks the skill, and a picker that hid everyone else would
  // only offer agents that already failed.
  it('offers every agent the ask carried, with the task’s own team first', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();

    const role = screen.getByLabelText(/^Agent — T2/);
    const options = Array.from(role.querySelectorAll('option')).map((o) => o.getAttribute('value'));
    expect([...options].sort()).toEqual([...ask.agents!].sort());
    // T2 is on `backend`, whose worker is go-worker.
    expect(options[0]).toBe('go-worker');

    await user.selectOptions(role, 'go-worker');
    expect(last().tasks![0]).toMatchObject({ id: 'T2', role: 'go-worker' });
  });

  // A team is "these people", and how many there are is its author's business.
  it('puts as many agents on a team as the user wants', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();

    await user.click(screen.getByRole('button', { name: 'Add agent to Also on backend' }));
    await user.click(screen.getByRole('button', { name: /^tester/ }));

    expect(last().squads![0]).toMatchObject({
      id: 'backend',
      agents: ['tester'],
      agents_set: true,
    });
  });

  it('reassigns a task to another team', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();
    await user.selectOptions(screen.getByLabelText(/^Team — T3/), 'backend');
    expect(last().tasks![0]).toMatchObject({ id: 'T3', squad: 'backend' });
  });

  // "I cannot edit much" was the complaint. These four are what a plan gets
  // subtly wrong, and every one of them used to need a full replan to fix.
  it('edits description, acceptance and priority', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();

    await user.type(screen.getByLabelText(/^Description — T1/), 'store todos in memory');
    await user.type(screen.getByLabelText(/^Acceptance — T1/), 'go test ./internal/store/...');
    const priority = screen.getByLabelText(/^Priority — T1/);
    await user.clear(priority);
    await user.type(priority, '3');

    expect(last().tasks![0]).toMatchObject({
      id: 'T1',
      description: 'store todos in memory',
      acceptance: 'go test ./internal/store/...',
      priority: 3,
    });
  });

  // Ordering on a wave-dispatching board is dependencies: "later" only means
  // "after these have finished".
  it('delays a task by making it wait on another', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();

    const t3 = taskCard('T3');
    await user.click(within(t3).getByRole('button', { name: 'T1' }));

    expect(last().tasks![0]).toMatchObject({ id: 'T3', depends_on: ['T1'], depends_set: true });

    await user.click(within(t3).getByRole('button', { name: 'T1' }));
    expect(last()).toEqual({});
  });

  it('removes a task and lets the user take it back', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();

    await user.click(screen.getByRole('button', { name: 'Remove task T3' }));
    expect(last().remove_tasks).toEqual(['T3']);

    await user.click(screen.getByRole('button', { name: 'Keep task T3' }));
    expect(last().remove_tasks).toBeUndefined();
  });

  it('edits a team and marks the ownership list as explicitly set', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();

    const owns = screen.getByLabelText(/^Owns — backend/);
    await user.clear(owns);
    await user.type(owns, 'cmd/**');

    const edits = last();
    expect(edits.squads).toHaveLength(1);
    expect(edits.squads![0]).toMatchObject({ id: 'backend', owns: ['cmd/**'], owns_set: true });
  });

  it('edits a team charter', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();
    await user.type(screen.getByLabelText(/^Charter — backend/), 'own the Go half only');
    expect(last().squads![0]).toMatchObject({ id: 'backend', charter: 'own the Go half only' });
  });

  it('adds a task', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();

    await user.click(screen.getByRole('button', { name: /Add task/ }));
    const blank = screen.getByLabelText('Title of task NEW-1');
    await user.type(blank, 'wire the router');

    expect(last().add_tasks).toHaveLength(1);
    expect(last().add_tasks![0].title).toBe('wire the router');
  });

  // The placeholder id is a client reference the harness resolves once the
  // board assigns a real one — the only way to say "T1 now waits on the task
  // I just added" before that task has an id.
  it('lets an existing task wait on a task that does not exist yet', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();

    await user.click(screen.getByRole('button', { name: /Add task/ }));
    await user.type(screen.getByLabelText('Title of task NEW-1'), 'migrate the schema');
    await user.click(within(taskCard('T1')).getByRole('button', { name: 'NEW-1' }));

    expect(last().add_tasks![0].id).toBe('NEW-1');
    expect(last().tasks![0]).toMatchObject({ id: 'T1', depends_on: ['NEW-1'] });
  });

  it('removes a team from the org chart', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();

    await user.click(screen.getByRole('button', { name: 'Remove team frontend' }));
    expect(last().remove_squads).toEqual(['frontend']);

    await user.click(screen.getByRole('button', { name: 'Keep team frontend' }));
    expect(last().remove_squads).toBeUndefined();
  });

  // A team the run did not select can be added rather than requiring a cancel,
  // a config edit and a replan.
  it('adds a team from the library', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();

    const picker = screen.getByLabelText('Add a team from the library');
    // A team already on the run is not offered — an "add" for it would read as
    // a request to duplicate it.
    const offered = Array.from(picker.querySelectorAll('option')).map((o) => o.getAttribute('value'));
    expect(offered).toEqual(['', 'docs']);

    await user.selectOptions(picker, 'docs');
    expect(last().squads!.find((s) => s.id === 'docs')).toMatchObject({
      id: 'docs',
      new: true,
      owns: ['docs/**'],
      owns_set: true,
    });
  });

  // The frozen contract: both halves build against it and neither can ask the
  // other later, so a wrong route here is unrecoverable at run time.
  it('renames a contract clause and keeps its spec', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();

    const name = screen.getByLabelText('Name of interface GET /api/todos');
    await user.clear(name);
    await user.type(name, 'GET /api/v2/todos');

    expect(last().interfaces).toEqual([{ id: 'GET /api/todos', rename: 'GET /api/v2/todos' }]);
    expect(last().remove_interfaces).toBeUndefined();
  });

  it('adds and removes contract clauses', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();

    await user.click(screen.getByRole('button', { name: /Add interface/ }));
    await user.type(screen.getByLabelText('New interface name'), 'POST /api/todos');
    expect(last().interfaces![0]).toMatchObject({ id: 'POST /api/todos', new: true, provider: 'backend' });

    await user.click(screen.getByRole('button', { name: 'Remove GET /api/todos' }));
    expect(last().remove_interfaces).toEqual(['GET /api/todos']);
  });

  it('reports nothing once an edit is reverted', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();

    const title = screen.getByLabelText('Title of task T1');
    await user.type(title, '!');
    expect(last().tasks).toHaveLength(1);

    await user.clear(title);
    await user.type(title, 'todo store');
    expect(last()).toEqual({});
    expect(screen.getByText('no changes')).toBeInTheDocument();
  });

  // A single-stream run has no org chart, and the editor must still work.
  it('works with no teams at all', () => {
    render(<PlanEditor ask={{ ...ask, squads: null, library: [] }} onChange={vi.fn()} />);
    expect(screen.queryByText(/^Teams$/)).not.toBeInTheDocument();
    expect(screen.getByLabelText('Title of task T1')).toBeInTheDocument();
  });

  // A board bigger than the card's structured list must say so, or the user
  // reads "3 tasks" and approves a plan with thirty.
  it('says how many tasks it is showing when the board is larger', () => {
    renderEditor({ task_count: 30 });
    expect(screen.getByText(/showing 3 of 30/)).toBeInTheDocument();
  });
});

describe('PlanEditor team managers', () => {
  // Who decides where a rejected delivery goes next. A narrower roster than
  // the worker's: only agents that answer the triage contract produce a verdict
  // the harness can read, and offering the rest invites an answer it refuses.
  it('offers only triage-capable agents as a team manager', () => {
    renderEditor();
    const picker = screen.getByLabelText('Project manager — backend');
    const offered = Array.from(picker.querySelectorAll('option')).map((o) => o.textContent);
    expect(offered).toContain('backend-triage');
    expect(offered).toContain('triage');
    expect(offered).not.toContain('go-worker');
    // The worker picker is still the full roster.
    const worker = screen.getByLabelText('Worker — backend');
    const workerOptions = Array.from(worker.querySelectorAll('option')).map((o) => o.textContent);
    expect(workerOptions).toContain('go-worker');
  });

  it('sends the manager the user attached', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();

    await user.selectOptions(screen.getByLabelText('Project manager — backend'), 'backend-triage');

    expect(last().squads).toEqual([{ id: 'backend', manager: 'backend-triage' }]);
    expect(last().tasks).toBeUndefined();
  });
});
