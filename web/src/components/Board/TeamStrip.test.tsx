import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import TeamStrip, { UNASSIGNED_FILTER } from './TeamStrip';
import { teamColor } from './teamColor';
import type { SquadsView, Task } from '@/types';

const task = (over: Partial<Task> & { id: string }): Task => ({
  title: over.id,
  description: '',
  role: 'worker',
  assignee: '',
  column: 'ready_to_dev',
  status: 'ready',
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
  task({ id: 'T1', squad: 'backend' }),
  task({ id: 'T2', squad: 'frontend' }),
  task({ id: 'T3' }),
];

const view: SquadsView = {
  ok: true,
  summary: 'Go API + React SPA',
  squads: [
    {
      id: 'backend',
      name: 'Backend',
      owns: ['cmd/**'],
      acceptance: 'go test ./...',
      manager: 'backend-triage',
      total: 4,
      done: 4,
      blocked: 0,
      in_flight: 0,
      complete: true,
      stuck: false,
    },
    {
      id: 'frontend',
      name: 'Frontend',
      owns: ['web/**'],
      acceptance: '',
      total: 3,
      done: 1,
      blocked: 1,
      in_flight: 1,
      complete: false,
      stuck: false,
    },
  ],
  stalls: [{ squad: 'frontend', interface: 'GET /api/todos', provider: 'backend' }],
  integration: { acceptance: 'go test ./... && npm run build', ready: false, reason: 'still building' },
};

describe('TeamStrip', () => {
  // The three questions a watcher has on a two-team run, none of which the
  // board answered before.
  it('names each team, its manager and what proves it', () => {
    render(<TeamStrip view={view} tasks={tasks} filter="" onFilter={vi.fn()} />);

    const backend = screen.getByRole('button', { name: /Backend/ });
    expect(within(backend).getByText('backend-triage')).toBeInTheDocument();
    expect(within(backend).getByText('go test ./...')).toBeInTheDocument();
    expect(within(backend).getByText('4/4')).toBeInTheDocument();
  });

  // A team with no acceptance command cannot prove its half alone, and the only
  // place that break can then surface is integration.
  it('says when a team has nothing proving its half', () => {
    render(<TeamStrip view={view} tasks={tasks} filter="" onFilter={vi.fn()} />);
    const frontend = screen.getByRole('button', { name: /Frontend/ });
    expect(within(frontend).getByText(/breaks surface at integration/)).toBeInTheDocument();
  });

  // A team blocked on another team's interface is NOT a defect in its own work,
  // and a red badge would say it was.
  it('shows a cross-team wait as a dependency, not a failure', () => {
    render(<TeamStrip view={view} tasks={tasks} filter="" onFilter={vi.fn()} />);
    const frontend = screen.getByRole('button', { name: /Frontend/ });
    expect(within(frontend).getByTitle(/contract dependency, not a defect/)).toBeInTheDocument();
    expect(within(frontend).getByText('1 blocked')).toBeInTheDocument();
  });

  // "Green" from the gate means the half was PROVED. `complete` only means the
  // board finished its tasks, and a half can finish its tasks without building.
  it('distinguishes a proved half from one whose tasks merely finished', () => {
    const { rerender } = render(<TeamStrip view={view} tasks={tasks} filter="" onFilter={vi.fn()} />);
    expect(within(screen.getByRole('button', { name: /Backend/ })).getByText('tasks done')).toBeInTheDocument();

    rerender(
      <TeamStrip
        view={{ ...view, gates: [{ team: 'backend', command: 'go test ./...', ran: true, ok: true }] }}
        tasks={tasks}
        filter=""
        onFilter={vi.fn()}
      />,
    );
    expect(within(screen.getByRole('button', { name: /Backend/ })).getByText('proved green')).toBeInTheDocument();
  });

  // A command that could not START says nothing about the code. Painting the
  // team red for a missing toolchain is how a working run looks broken.
  it('shows an unrunnable acceptance as unverified rather than red', () => {
    render(
      <TeamStrip
        view={{ ...view, gates: [{ team: 'backend', ran: false, ok: false, summary: 'npm is not installed' }] }}
        tasks={tasks}
        filter=""
        onFilter={vi.fn()}
      />,
    );
    const backend = screen.getByRole('button', { name: /Backend/ });
    expect(within(backend).getByText('unverified')).toBeInTheDocument();
    expect(within(backend).queryByText('half is red')).not.toBeInTheDocument();
  });

  it('filters the board to one lane, and back', async () => {
    const user = userEvent.setup();
    const onFilter = vi.fn();
    const { rerender } = render(<TeamStrip view={view} tasks={tasks} filter="" onFilter={onFilter} />);

    await user.click(screen.getByRole('button', { name: /Backend/ }));
    expect(onFilter).toHaveBeenCalledWith('backend');

    // Clicking the active team clears it — the same control both ways, because
    // a separate "clear" button for something you just clicked is a second
    // thing to find.
    rerender(<TeamStrip view={view} tasks={tasks} filter="backend" onFilter={onFilter} />);
    await user.click(screen.getByRole('button', { name: /Backend/ }));
    expect(onFilter).toHaveBeenLastCalledWith('');
  });

  // A task on no team is the seam, or a file nobody owns. It is a real thing to
  // look at, so it gets its own lane rather than being invisible.
  it('offers the unassigned tasks as their own lane', async () => {
    const user = userEvent.setup();
    const onFilter = vi.fn();
    render(<TeamStrip view={view} tasks={tasks} filter="" onFilter={onFilter} />);

    await user.click(screen.getByRole('button', { name: /no team/ }));
    expect(onFilter).toHaveBeenCalledWith(UNASSIGNED_FILTER);
  });

  // A single-stream run has no org chart, and a header about teams that do not
  // exist is a header nobody can act on.
  it('renders nothing without an org chart', () => {
    const { container } = render(<TeamStrip view={{ ok: false }} tasks={tasks} filter="" onFilter={vi.fn()} />);
    expect(container).toBeEmptyDOMElement();
    const { container: c2 } = render(<TeamStrip view={null} tasks={tasks} filter="" onFilter={vi.fn()} />);
    expect(c2).toBeEmptyDOMElement();
  });
});

describe('teamColor', () => {
  // A colour assigned by POSITION means adding `docs` recolours the frontend,
  // and every screenshot, every "the green team", every glance goes stale at
  // once. Derived from the id, it is stable for the life of the team.
  it('is stable for an id regardless of what else exists', () => {
    const a = teamColor('frontend-react');
    const b = teamColor('frontend-react');
    expect(a).toEqual(b);
    expect(teamColor('backend-go')).not.toEqual(a);
  });

  it('gives an unassigned task grey, never a team hue', () => {
    expect(teamColor(undefined).name).toBe('gray');
    expect(teamColor('').name).toBe('gray');
  });
});
