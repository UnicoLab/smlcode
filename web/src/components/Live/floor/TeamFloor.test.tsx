import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import TeamFloor from './TeamFloor';
import { buildFloor } from './floorModel';
import { resetFloorStore } from './floorStore';
import type { RunEvent, SquadsView, Task } from '@/types';

// jsdom has no WebGL, so the wrapper renders the flat stage — which draws the
// same model, and is what these tests read. The 3D scene is exercised in a
// browser; its inputs are the model, which floorModel.test covers.

const T0 = Date.parse('2026-09-10T10:00:00Z');
const at = (s: number) => new Date(T0 + s * 1000).toISOString();

function task(id: string, over: Partial<Task> = {}): Task {
  return {
    id, title: `Task ${id}`, description: '', role: 'worker', assignee: '', column: 'ready_to_dev', status: 'ready',
    priority: 0, depends_on: [], files: [], acceptance: '', checklist: [], output: '', review: '', retries: 0,
    error: '', updated_at: '', notes: '', ...over,
  };
}

const chart: SquadsView = {
  ok: true,
  summary: 'Go API + React SPA',
  squads: [
    { id: 'backend-go', name: 'Backend', owns: ['cmd/**'], acceptance: 'go test ./...', worker: 'go-worker', manager: 'backend-triage', total: 2, done: 1, blocked: 0, in_flight: 1, complete: false, stuck: false },
    { id: 'frontend-react', name: 'Frontend', owns: ['web/**'], acceptance: 'npm run build', worker: 'react-worker', total: 1, done: 0, blocked: 1, in_flight: 0, complete: false, stuck: true },
  ],
  task_teams: { T1: 'backend-go', T2: 'backend-go', T3: 'frontend-react' },
  interfaces: [{ id: 'GET /api/todos', provider: 'backend-go', consumers: ['frontend-react'] }],
  stalls: [{ squad: 'frontend-react', interface: 'GET /api/todos', provider: 'backend-go' }],
  gates: [{ team: 'backend-go', ran: true, ok: true }],
  integration: { acceptance: 'make e2e', ready: false, reason: 'frontend-react is not complete' },
};

const tasks = [task('T1', { status: 'done', column: 'done' }), task('T2', { status: 'running', column: 'in_progress' }), task('T3', { status: 'blocked', column: 'blocked' })];
const events: RunEvent[] = [
  { phase: 'execute', kind: 'agent_start', agent: 'go-worker', task_id: 'T2', message: 'implementing T2', time: at(4) },
];

beforeEach(() => {
  localStorage.clear();
  // The floor remembers pulses and the last floor across mounts, by design;
  // each test starts from an empty memory.
  resetFloorStore();
});

describe('TeamFloor', () => {
  it('draws every team as an island with its people and tickets', () => {
    // The same clock the floor was built with: the bubble reads "T2 · 1s" off
    // it. Without it the stage ticks on the real Date.now(), and the elapsed
    // assertion below only holds in the minute after the fixed event time.
    const floor = buildFloor({ squads: chart, tasks, events, composition: null, running: true, now: T0 + 5000 });
    render(<TeamFloor floor={floor} running now={T0 + 5000} />);

    expect(screen.getByTestId('team-floor')).toBeInTheDocument();
    const backend = screen.getByTestId('island-backend-go');
    expect(within(backend).getByText('Backend')).toBeInTheDocument();
    expect(within(backend).getByTestId('agent-backend-triage')).toBeInTheDocument();
    expect(within(backend).getByTestId('agent-go-worker')).toHaveAttribute('data-active', 'true');
    expect(within(backend).getByTestId('ticket-T2')).toHaveAttribute('data-state', 'working');
    expect(within(backend).getByTestId('ticket-T1')).toHaveAttribute('data-state', 'done');
    expect(within(backend).getByText(/^T2 · \d+s$/)).toBeInTheDocument();

    const frontend = screen.getByTestId('island-frontend-react');
    expect(within(frontend).getByText(/manager · run default/)).toBeInTheDocument();
    expect(within(frontend).getByText(/waiting on GET \/api\/todos/)).toBeInTheDocument();
  });

  it('draws the contract as a conduit and marks the stalled one', () => {
    const floor = buildFloor({ squads: chart, tasks, events, composition: null, running: true, now: T0 });
    render(<TeamFloor floor={floor} running />);
    const conduit = screen.getByTestId('conduit-backend-go-frontend-react');
    expect(conduit).toHaveAttribute('data-stalled', 'true');
    expect(within(conduit).getByText(/GET \/api\/todos · waiting/)).toBeInTheDocument();
    expect(screen.getByTestId('integration-plate')).toHaveTextContent('integration waits');
  });

  it('animates a recent manager handoff between two people', () => {
    const withHandoff: RunEvent[] = [
      ...events,
      { phase: 'plan', kind: 'output', agent: 'go-corrector', task_id: 'T2', message: 'T2 reassigned from go-worker to go-corrector — compile error', time: at(11) },
    ];
    const withCorrector: SquadsView = {
      ...chart,
      squads: [{ ...chart.squads![0], agents: ['go-corrector'] }, chart.squads![1]],
    };
    const floor = buildFloor({ squads: withCorrector, tasks, events: withHandoff, composition: null, running: true, now: T0 + 12_000 });
    render(<TeamFloor floor={floor} running />);
    expect(screen.getByTestId('handoff-T2')).toHaveTextContent('T2 → go-corrector');
  });

  it('opens a ticket’s dossier on click, and jumps to the Tasks rail from there', () => {
    const onTicket = vi.fn();
    const floor = buildFloor({ squads: chart, tasks, events, composition: null, running: true, now: T0 });
    render(<TeamFloor floor={floor} running onTicket={onTicket} now={T0 + 10_000} />);
    fireEvent.click(screen.getByTestId('ticket-T2'));
    const dossier = screen.getByTestId('floor-dossier');
    expect(within(dossier).getByText('Task T2')).toBeInTheDocument();
    expect(within(dossier).getByTestId('dossier-ticket-state')).toHaveTextContent('in progress');
    expect(within(dossier).getByTestId('dossier-holder')).toHaveTextContent('In the hands of');
    expect(within(dossier).getByTestId('dossier-holder')).toHaveTextContent('go-worker');
    expect(screen.getByTestId('ticket-T2')).toHaveAttribute('data-selected', 'true');
    expect(onTicket).not.toHaveBeenCalled();
    fireEvent.click(within(dossier).getByRole('button', { name: /open in Tasks/ }));
    expect(onTicket).toHaveBeenCalledWith('T2');
  });

  it('opens a person’s dossier: status, manager, tickets, recent lines', () => {
    const log: RunEvent[] = [
      { phase: 'execute', kind: 'agent_start', agent: 'go-worker', task_id: 'T1', message: 'implementing T1', time: at(1) },
      { phase: 'execute', kind: 'agent_end', agent: 'go-worker', task_id: 'T1', message: 'done', time: at(2) },
      ...events,
    ];
    const floor = buildFloor({ squads: chart, tasks, events: log, composition: null, running: true, now: T0 + 5000 });
    render(<TeamFloor floor={floor} running events={log} now={T0 + 34_000} />);
    fireEvent.click(screen.getByTestId('agent-go-worker'));
    const dossier = screen.getByTestId('floor-dossier');
    expect(dossier).toHaveAttribute('aria-label', 'About go-worker');
    expect(within(dossier).getByTestId('dossier-status')).toHaveTextContent(/working on T2/);
    expect(within(dossier).getByTestId('dossier-status')).toHaveTextContent('0:30');
    expect(within(dossier).getByTestId('dossier-status')).toHaveTextContent('implementing T2');
    expect(within(dossier).getByTestId('dossier-management')).toHaveTextContent('Managed by');
    expect(within(dossier).getByTestId('dossier-management')).toHaveTextContent('backend-triage');
    expect(within(dossier).getByTestId('dossier-tickets')).toHaveTextContent('1 in hand · 2 touched');
    expect(within(dossier).getByTestId('dossier-trail')).toHaveTextContent('implementing T1');
    expect(screen.getByTestId('agent-go-worker')).toHaveAttribute('data-selected', 'true');

    // Every name links on: the manager's dossier says who they manage.
    fireEvent.click(within(dossier).getByRole('button', { name: /backend-triage/ }));
    expect(screen.getByTestId('floor-dossier')).toHaveAttribute('aria-label', 'About backend-triage');
    expect(within(screen.getByTestId('dossier-management')).getByText(/Manages 1 person/)).toBeInTheDocument();

    // Esc closes.
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByTestId('floor-dossier')).not.toBeInTheDocument();
  });

  it('lists what just changed in the feed, and each card opens its dossier', () => {
    const before = buildFloor({ squads: chart, tasks, events: [], composition: null, running: true, now: T0 });
    const { rerender } = render(<TeamFloor floor={before} running now={T0} />);
    expect(screen.queryByTestId('floor-feed')).not.toBeInTheDocument();

    const after = buildFloor({
      squads: chart,
      tasks: [...tasks.slice(0, 1), task('T2', { status: 'done', column: 'done' }), tasks[2], task('T4', { title: 'a new one' })],
      events,
      composition: null,
      running: true,
      now: T0 + 6000,
    });
    rerender(<TeamFloor floor={after} running now={T0 + 6000} />);
    const feed = screen.getByTestId('floor-feed');
    expect(within(feed).getByText(/^T2 done ✓/)).toBeInTheDocument();
    expect(within(feed).getByText(/T4 appeared/)).toBeInTheDocument();
    expect(within(feed).getByText('go-worker started on T2')).toBeInTheDocument();
    expect(screen.getByTestId('ticket-T2')).toHaveClass('floor-ticket-flash');

    fireEvent.click(within(feed).getByText(/^T2 done ✓/));
    expect(screen.getByTestId('floor-dossier')).toHaveAttribute('aria-label', 'About T2');
  });

  it('shows the pipeline crew on one island when there is no org chart', () => {
    const floor = buildFloor({
      squads: { ok: false },
      tasks: tasks.slice(0, 2),
      events,
      composition: { summary: 'x', team_note: 'no team matched this request — it runs as one stream', execute: { default_role: 'go-worker', reviewer: 'reviewer' } },
      running: true,
      now: T0,
    });
    render(<TeamFloor floor={floor} running />);
    const crew = screen.getByTestId('island-crew');
    expect(within(crew).getByText('Pipeline crew')).toBeInTheDocument();
    expect(within(crew).getByTestId('agent-go-worker')).toHaveAttribute('data-active', 'true');
    expect(within(crew).getByTestId('agent-reviewer')).toBeInTheDocument();
  });

  it('shows the pipeline’s own people beside the tables, and their dossier', () => {
    const log: RunEvent[] = [
      { phase: 'plan', kind: 'agent_start', agent: 'planner', message: 'reading the request', time: at(1) },
      { phase: 'plan', kind: 'output', agent: 'planner', message: 'three tickets, two teams', time: at(2) },
    ];
    const floor = buildFloor({
      squads: chart,
      tasks,
      events: log,
      composition: { summary: 'x', phases: [{ id: 'plan', agent: 'planner', enabled: true, when: 'auto' }, { id: 'split', agent: 'splitter', enabled: true, when: 'auto' }] },
      running: true,
      now: T0 + 3000,
    });
    render(<TeamFloor floor={floor} running events={log} now={T0 + 3000} />);
    expect(screen.getByTestId('pipeline-phase')).toHaveTextContent('plan');
    expect(screen.getByTestId('stage-planner')).toHaveAttribute('data-active', 'true');
    expect(screen.getByTestId('stage-splitter')).not.toHaveAttribute('data-active');
    fireEvent.click(screen.getByTestId('stage-planner'));
    const dossier = screen.getByTestId('floor-dossier');
    expect(dossier).toHaveAttribute('aria-label', 'About planner');
    expect(within(dossier).getByTestId('dossier-status')).toHaveTextContent('speaking');
    expect(within(dossier).getByTestId('dossier-trail')).toHaveTextContent('three tickets, two teams');
  });

  it('explains an empty floor', () => {
    const floor = buildFloor({ squads: null, tasks: [], events: [], composition: null, running: false, now: T0 });
    render(<TeamFloor floor={floor} running={false} />);
    expect(screen.getByTestId('team-floor-idle')).toHaveTextContent('The floor is empty');
  });

  // The page keeps the selection in the URL and passes it down; the floor
  // draws it and reports clicks, but does not own it.
  it('draws a controlled selection and reports changes instead of keeping its own', () => {
    const onSelect = vi.fn();
    const floor = buildFloor({ squads: chart, tasks, events, composition: null, running: true, now: T0 });
    const { rerender } = render(<TeamFloor floor={floor} running selection={{ kind: 'ticket', id: 'T2', team: 'backend-go' }} onSelect={onSelect} now={T0} />);
    expect(screen.getByTestId('floor-dossier')).toHaveAttribute('aria-label', 'About T2');
    expect(screen.getByTestId('ticket-T2')).toHaveAttribute('data-selected', 'true');

    fireEvent.click(screen.getByTestId('agent-go-worker'));
    expect(onSelect).toHaveBeenCalledWith({ kind: 'agent', id: 'go-worker', team: 'backend-go' });
    // Still T2 until the owner says otherwise.
    expect(screen.getByTestId('floor-dossier')).toHaveAttribute('aria-label', 'About T2');

    rerender(<TeamFloor floor={floor} running selection={{ kind: 'agent', id: 'go-worker', team: 'backend-go' }} onSelect={onSelect} now={T0} />);
    expect(screen.getByTestId('floor-dossier')).toHaveAttribute('aria-label', 'About go-worker');

    rerender(<TeamFloor floor={floor} running selection={null} onSelect={onSelect} now={T0} />);
    expect(screen.queryByTestId('floor-dossier')).not.toBeInTheDocument();
  });

  // Leaving the Live page unmounts the floor. What happened while the user was
  // on the Board must still be in the feed when they come back, and the first
  // floor after the return must be diffed against the last one seen.
  it('keeps its pulses and its last floor across an unmount', () => {
    const before = buildFloor({ squads: chart, tasks, events: [], composition: null, running: true, now: T0 });
    const { unmount } = render(<TeamFloor floor={before} running now={T0} />);
    unmount();

    const after = buildFloor({
      squads: chart,
      tasks: [...tasks.slice(0, 1), task('T2', { status: 'done', column: 'done' }), tasks[2]],
      events,
      composition: null,
      running: true,
      now: T0 + 3000,
    });
    render(<TeamFloor floor={after} running now={T0 + 3000} />);
    const feed = screen.getByTestId('floor-feed');
    expect(within(feed).getByText(/^T2 done ✓/)).toBeInTheDocument();
  });

  // A keyboard user who opens a dossier lands inside it, and is handed back to
  // where they were when it closes — not dropped at the top of the document.
  it('moves focus into the dossier on open and back on close', () => {
    const floor = buildFloor({ squads: chart, tasks, events, composition: null, running: true, now: T0 });
    render(
      <>
        <button type="button">opener</button>
        <TeamFloor floor={floor} running now={T0} />
      </>,
    );
    // jsdom does not focus SVG elements, so the opener stands in for the
    // focused ticket, feed card or sr-only button a real keyboard would use.
    const opener = screen.getByRole('button', { name: 'opener' });
    opener.focus();
    fireEvent.click(screen.getByTestId('ticket-T2'));
    const close = within(screen.getByTestId('floor-dossier')).getByRole('button', { name: 'Close' });
    expect(document.activeElement).toBe(close);
    fireEvent.click(close);
    expect(screen.queryByTestId('floor-dossier')).not.toBeInTheDocument();
    expect(document.activeElement).toBe(opener);
  });

  // Esc in a text field means "clear what I typed", not "close the dossier".
  it('ignores Esc typed into a field', () => {
    const floor = buildFloor({ squads: chart, tasks, events, composition: null, running: true, now: T0 });
    render(
      <>
        <input aria-label="a field" />
        <TeamFloor floor={floor} running now={T0} />
      </>,
    );
    fireEvent.click(screen.getByTestId('ticket-T2'));
    const field = screen.getByLabelText('a field');
    field.focus();
    fireEvent.keyDown(field, { key: 'Escape' });
    expect(screen.getByTestId('floor-dossier')).toBeInTheDocument();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByTestId('floor-dossier')).not.toBeInTheDocument();
  });

  it('puts the feed in a bottom sheet for a phone', () => {
    const floor = buildFloor({ squads: chart, tasks, events, composition: null, running: true, now: T0 });
    render(<TeamFloor floor={floor} running now={T0} />);
    const strip = screen.getByTestId('floor-mobile-strip');
    expect(strip).toHaveClass('sm:hidden');
    expect(screen.queryByTestId('floor-sheet')).not.toBeInTheDocument();
    fireEvent.click(within(strip).getByRole('button', { name: /feed/ }));
    expect(screen.getByTestId('floor-sheet')).toBeInTheDocument();
  });

  it('offers no 3D toggle where WebGL is absent', () => {
    const floor = buildFloor({ squads: chart, tasks, events, composition: null, running: true, now: T0 });
    render(<TeamFloor floor={floor} running />);
    expect(screen.queryByRole('button', { name: /3D/ })).not.toBeInTheDocument();
  });
});
