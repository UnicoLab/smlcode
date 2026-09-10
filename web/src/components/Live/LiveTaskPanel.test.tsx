import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import LiveTaskPanel from './LiveTaskPanel';
import type { Board, Task } from '@/types';

const retryTask = vi.fn(async (id: string) => ({ id }) as unknown as Task);

const task = (over: Partial<Task>): Task => ({
  id: 'T1',
  title: 'task',
  description: '',
  role: 'worker',
  assignee: '',
  column: 'in_progress',
  status: 'in_progress',
  priority: 0,
  depends_on: [],
  files: [],
  acceptance: '',
  checklist: [],
  output: '',
  review: '',
  retries: 0,
  error: '',
  updated_at: '',
  notes: '',
  ...over,
});

const tasks: Task[] = [
  task({ id: 'T1', title: 'Healthy task', column: 'in_progress', status: 'in_progress' }),
  task({
    id: 'T2',
    title: 'Stuck task',
    column: 'blocked',
    status: 'blocked',
    error: 'attempt 1 failed because tests did not compile',
    attempt_log: ['attempt 1 failed because tests did not compile'],
  }),
];

const board: Board = {
  plan: { summary: '', goals: [], assumptions: [], risks: [], steps: [], raw: '' },
  tasks,
  columns: ['in_progress', 'blocked'],
  by_column: { in_progress: [tasks[0]], blocked: [tasks[1]] },
};

vi.mock('@/api/client', () => {
  class ApiError extends Error {
    status = 500;
    get isConflict() {
      return false;
    }
  }
  return {
    ApiError,
    errorText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
    getTasks: vi.fn(async () => board),
    getAgents: vi.fn(async () => []),
    getDoc: vi.fn(async () => ({ name: 'CONTEXT.md', content: '' })),
    updateDoc: vi.fn(async () => ({ ok: 'true' })),
    addTask: vi.fn(),
    patchTask: vi.fn(async () => tasks[1]),
    deleteTask: vi.fn(),
    retryTask: (...a: unknown[]) => retryTask(...(a as [string])),
  };
});

function renderPanel(props?: { focusTaskId?: string }) {
  return render(
    <MemoryRouter>
      <LiveTaskPanel {...props} />
    </MemoryRouter>,
  );
}

describe('LiveTaskPanel — blocked tasks have a next step', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('turns the Blocked counter into a filter over the list', async () => {
    renderPanel();
    await screen.findByText('Healthy task');
    const blocked = screen.getByRole('button', { name: /Blocked/ });
    expect(blocked).toHaveAttribute('aria-pressed', 'false');

    await userEvent.click(blocked);
    expect(blocked).toHaveAttribute('aria-pressed', 'true');
    expect(screen.queryByText('Healthy task')).not.toBeInTheDocument();
    // The attention row and the list both name the stuck task.
    expect(screen.getAllByText('Stuck task').length).toBeGreaterThan(0);

    await userEvent.click(screen.getByRole('button', { name: 'Show all' }));
    expect(await screen.findByText('Healthy task')).toBeInTheDocument();
  });

  it('retries a blocked task from its attention row', async () => {
    renderPanel();
    const row = (await screen.findByText('Stuck task', { selector: 'span' })).closest('div')!;
    await userEvent.click(within(row).getByRole('button', { name: /^Retry$/ }));
    await waitFor(() => expect(retryTask).toHaveBeenCalledWith('T2'));
  });

  it('expands, scrolls to and flashes the focused task', async () => {
    const scrollIntoView = vi.spyOn(Element.prototype, 'scrollIntoView');
    renderPanel({ focusTaskId: 'T2' });
    await screen.findByText('Healthy task');
    await waitFor(() => expect(scrollIntoView).toHaveBeenCalled());
    const row = document.querySelector('[data-task-id="T2"]');
    expect(row).toHaveClass('flash-focus');
    // Expanded: the attempts timeline is visible for the focused task.
    expect(await screen.findByTestId('attempts-timeline')).toHaveTextContent('tests did not compile');
  });
});
