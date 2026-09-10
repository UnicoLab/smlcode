import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import TaskCard from './TaskCard';
import type { Task } from '@/types';

const retryTask = vi.fn(async (id: string) => ({ id }) as unknown as Task);
const patchTask = vi.fn(async (id: string, patch: Partial<Task>) => ({ id, ...patch }) as Task);

vi.mock('@/api/client', () => {
  class ApiError extends Error {
    status: number;
    constructor(status: number, body: string) {
      super(body);
      this.status = status;
    }
    get isConflict() {
      return this.status === 409;
    }
  }
  return {
    ApiError,
    errorText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
    retryTask: (...a: unknown[]) => retryTask(...(a as [string])),
    patchTask: (...a: unknown[]) => patchTask(...(a as [string, Partial<Task>])),
    deleteTask: vi.fn(async () => ({ ok: 'true' })),
  };
});

const stuck: Task = {
  id: 'T3',
  title: 'Wire the retry endpoint',
  description: '',
  role: 'worker',
  assignee: '',
  column: 'blocked',
  status: 'blocked',
  priority: 0,
  depends_on: [],
  files: ['pkg/server/tasks.go'],
  acceptance: '',
  checklist: [],
  output: '',
  review: 'Reviewer: the handler returns 200 on an unknown id.',
  retries: 2,
  error: 'attempt 2 failed because go test ./pkg/server timed out',
  updated_at: '',
  notes: '',
  attempt_log: [
    'attempt 1 failed because handler not registered on the mux',
    'attempt 2 failed because go test ./pkg/server timed out',
  ],
  gate_retries: 1,
  criteria: [
    { id: 'AC1', text: 'POST /api/tasks/{id}/retry returns 404 for an unknown id', met: false },
    { id: 'AC2', text: 'Attempt log survives the retry', priority: 'should' },
  ],
};

function renderCard(task: Task, onUpdate = vi.fn()) {
  render(
    <MemoryRouter>
      <TaskCard task={task} columns={['ready_to_dev', 'blocked', 'done']} columnLabels={{ blocked: 'Blocked' }} teams={['api', 'web']} onUpdate={onUpdate} />
    </MemoryRouter>,
  );
  return onUpdate;
}

describe('TaskCard — the story of a stuck task', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows the attempts timeline, the verdict and the criteria once expanded', async () => {
    renderCard(stuck);
    await userEvent.click(screen.getByTitle('Show full task details'));

    const timeline = screen.getByTestId('attempts-timeline');
    expect(timeline).toHaveTextContent('Attempts');
    expect(timeline).toHaveTextContent('2 failed');
    expect(timeline).toHaveTextContent('1 gate retry');
    // Each attempt is numbered and its reason is shown without the boilerplate.
    expect(screen.getByText('#1')).toBeInTheDocument();
    expect(screen.getByText('handler not registered on the mux')).toBeInTheDocument();
    expect(screen.getByText('go test ./pkg/server timed out')).toBeInTheDocument();

    expect(screen.getByText('Review verdict')).toBeInTheDocument();
    expect(screen.getByText(/returns 200 on an unknown id/)).toBeInTheDocument();

    expect(screen.getByText('Criteria')).toBeInTheDocument();
    expect(screen.getByText(/returns 404 for an unknown id/)).toBeInTheDocument();
    expect(screen.getByText('not met:')).toBeInTheDocument();
    expect(screen.getByText('not yet checked:')).toBeInTheDocument();
  });

  it('offers the next step: Retry calls the endpoint and refreshes the board', async () => {
    const onUpdate = renderCard(stuck);
    await userEvent.click(screen.getByTitle('Show full task details'));

    const group = screen.getByRole('group', { name: 'Next steps for T3' });
    expect(group).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /^Retry$/ }));

    await waitFor(() => expect(retryTask).toHaveBeenCalledWith('T3'));
    expect(onUpdate).toHaveBeenCalled();
  });

  it('sends the task back to Ready through the board patch', async () => {
    const onUpdate = renderCard(stuck);
    await userEvent.click(screen.getByTitle('Show full task details'));
    await userEvent.click(screen.getByRole('button', { name: /Send back to Ready/ }));
    await waitFor(() =>
      expect(patchTask).toHaveBeenCalledWith('T3', expect.objectContaining({ column: 'ready_to_dev' })),
    );
    expect(onUpdate).toHaveBeenCalled();
  });

  it('opens the edit form on the team control for Reassign', async () => {
    renderCard(stuck);
    await userEvent.click(screen.getByTitle('Show full task details'));
    await userEvent.click(screen.getByRole('button', { name: /Reassign team/ }));
    const select = await screen.findByLabelText('Team of T3');
    await waitFor(() => expect(select).toHaveFocus());
  });

  it('keeps the action row off a healthy task', async () => {
    renderCard({ ...stuck, column: 'done', status: 'done', attempt_log: [], error: '' });
    await userEvent.click(screen.getByTitle('Show full task details'));
    expect(screen.queryByRole('group', { name: /Next steps/ })).not.toBeInTheDocument();
    expect(screen.queryByTestId('attempts-timeline')).not.toBeInTheDocument();
  });
});
