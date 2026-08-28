import { render, screen } from '@testing-library/react';
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
    interfaces: [{ id: 'GET /api/todos', provider: 'backend', consumers: ['frontend'] }],
    integration: 'go test ./... && npm run build',
  },
  agents: ['worker', 'go-worker', 'react-worker', 'tester'],
};

function renderEditor() {
  const onChange = vi.fn<(e: PlanEdits) => void>();
  render(<PlanEditor ask={ask} onChange={onChange} />);
  const last = () => {
    const calls = onChange.mock.calls;
    return calls.length ? calls[calls.length - 1][0] : {};
  };
  return { onChange, last };
}

describe('PlanEditor', () => {
  it('shows the teams and every task', () => {
    renderEditor();
    // Team names are editable inputs, so assert on their values.
    expect(screen.getByLabelText('Name of squad backend')).toHaveValue('Backend');
    expect(screen.getByLabelText('Name of squad frontend')).toHaveValue('Frontend');
    for (const id of ['T1', 'T2', 'T3']) {
      expect(screen.getByText(id)).toBeInTheDocument();
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
  it('offers only the agents the ask carried', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();

    const role = screen.getByLabelText(/^Agent — T2/);
    const options = Array.from(role.querySelectorAll('option')).map((o) => o.getAttribute('value'));
    expect(options).toEqual(ask.agents);

    await user.selectOptions(role, 'go-worker');
    expect(last().tasks![0]).toMatchObject({ id: 'T2', role: 'go-worker' });
  });

  it('reassigns a task to another team', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();
    await user.selectOptions(screen.getByLabelText(/^Team — T3/), 'backend');
    expect(last().tasks![0]).toMatchObject({ id: 'T3', squad: 'backend' });
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

  it('adds a task', async () => {
    const user = userEvent.setup();
    const { last } = renderEditor();

    await user.click(screen.getByRole('button', { name: /Add task/ }));
    const blank = screen.getByLabelText('Title of task NEW-4');
    await user.type(blank, 'wire the router');

    expect(last().add_tasks).toHaveLength(1);
    expect(last().add_tasks![0].title).toBe('wire the router');
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
    const onChange = vi.fn();
    render(<PlanEditor ask={{ ...ask, squads: null }} onChange={onChange} />);
    expect(screen.queryByText(/^Teams$/)).not.toBeInTheDocument();
    expect(screen.getByLabelText('Title of task T1')).toBeInTheDocument();
  });
});
