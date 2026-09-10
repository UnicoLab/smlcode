import { describe, expect, it } from 'vitest';
import { agentTrail, buildFloor, diffFloors, freshPulses, ticketState, HANDOFF_TTL_MS, PULSE_TTL_MS } from './floorModel';
import type { DynamicComposition, RunEvent, SquadsView, Task } from '@/types';

const T0 = Date.parse('2026-09-10T10:00:00Z');
const at = (s: number) => new Date(T0 + s * 1000).toISOString();

function task(id: string, over: Partial<Task> = {}): Task {
  return {
    id,
    title: `Task ${id}`,
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
  };
}

const chart: SquadsView = {
  ok: true,
  summary: 'Go API + React SPA',
  squads: [
    {
      id: 'backend-go', name: 'Backend', owns: ['cmd/**'], acceptance: 'go test ./...',
      worker: 'go-worker', reviewer: 'go-reviewer', tester: 'go-tester', manager: 'backend-triage',
      agents: ['go-corrector'], total: 2, done: 1, blocked: 0, in_flight: 1, complete: false, stuck: false,
    },
    {
      id: 'frontend-react', name: 'Frontend', owns: ['web/**'], acceptance: 'npm run build',
      worker: 'react-worker', total: 1, done: 0, blocked: 1, in_flight: 0, complete: false, stuck: true,
    },
  ],
  task_teams: { T1: 'backend-go', T2: 'backend-go', T3: 'frontend-react' },
  interfaces: [{ id: 'GET /api/todos', provider: 'backend-go', consumers: ['frontend-react'], spec: '200 -> []' }],
  stalls: [{ squad: 'frontend-react', interface: 'GET /api/todos', provider: 'backend-go' }],
  gates: [{ team: 'backend-go', command: 'go test ./...', ran: true, ok: true }],
  integration: { acceptance: 'make e2e', ready: false, reason: 'frontend-react is not complete' },
};

const tasks = [
  task('T1', { status: 'done', column: 'done' }),
  task('T2', { status: 'running', column: 'in_progress' }),
  task('T3', { status: 'blocked', column: 'blocked' }),
  task('T9', { title: 'the seam' }),
];

const events: RunEvent[] = [
  { phase: 'charter', kind: 'agent_start', agent: 'manager', message: 'team backend-go owns cmd/**', time: at(1) },
  { phase: 'execute', kind: 'agent_start', agent: 'go-worker', task_id: 'T1', message: 'implementing T1', time: at(2) },
  { phase: 'execute', kind: 'agent_end', agent: 'go-worker', task_id: 'T1', message: 'done', time: at(3) },
  { phase: 'execute', kind: 'agent_start', agent: 'go-worker', task_id: 'T2', message: 'implementing T2', time: at(4) },
];

describe('ticketState', () => {
  it('folds column and status into the six the floor draws', () => {
    expect(ticketState({ column: 'done', status: '' })).toBe('done');
    expect(ticketState({ column: 'in_progress', status: '' })).toBe('working');
    expect(ticketState({ column: 'in_review', status: '' })).toBe('review');
    expect(ticketState({ column: 'ready_to_dev', status: 'correcting' })).toBe('review');
    expect(ticketState({ column: 'blocked', status: '' })).toBe('blocked');
    expect(ticketState({ column: 'ready_to_dev', status: 'failed' })).toBe('failed');
    expect(ticketState({ column: 'to_scope', status: 'todo' })).toBe('queued');
  });
});

describe('buildFloor with an org chart', () => {
  const floor = buildFloor({ squads: chart, tasks, events, composition: null, running: true, now: T0 + 5000 });

  it('puts each team on the floor with its seats and manager', () => {
    expect(floor.mode).toBe('teams');
    expect(floor.teams.map((t) => t.id)).toEqual(['backend-go', 'frontend-react']);
    const backend = floor.teams[0];
    expect(backend.manager).toBe('backend-triage');
    expect(backend.managerDefault).toBe(false);
    expect(backend.agents.map((a) => `${a.seat}:${a.id}`)).toEqual([
      'manager:backend-triage', 'worker:go-worker', 'reviewer:go-reviewer', 'tester:go-tester', 'member:go-corrector',
    ]);
    const frontend = floor.teams[1];
    expect(frontend.manager).toBe('triage');
    expect(frontend.managerDefault).toBe(true);
  });

  it('attaches tickets to their team and keeps the seam apart', () => {
    const backend = floor.teams[0];
    expect(backend.tickets.map((t) => `${t.id}:${t.state}`)).toEqual(['T1:done', 'T2:working']);
    expect(floor.teams[1].tickets.map((t) => t.id)).toEqual(['T3']);
    expect(floor.unassigned.map((t) => t.id)).toEqual(['T9']);
  });

  it('marks the agent the log says is working, on its ticket, on its team only', () => {
    const backend = floor.teams[0];
    const worker = backend.agents.find((a) => a.id === 'go-worker')!;
    expect(worker.active).toBe(true);
    expect(worker.task).toBe('T2');
    expect(worker.touched).toBe(2);
    expect(worker.tickets).toEqual(['T1', 'T2']);
    expect(worker.lastMessage).toBe('implementing T2');
    expect(worker.lastAt).toBe(T0 + 4000);
    expect(backend.tickets.find((t) => t.id === 'T2')!.agent).toBe('go-worker');
    expect(backend.tickets.find((t) => t.id === 'T2')!.touchedBy).toEqual(['go-worker']);
    // T1 was released by agent_end.
    expect(backend.tickets.find((t) => t.id === 'T1')!.agent).toBeUndefined();
    expect(floor.teams[1].agents.every((a) => !a.active)).toBe(true);
    expect(floor.now).toMatchObject({ agent: 'go-worker', task: 'T2', team: 'backend-go' });
  });

  it('draws the contract as links and marks the stalled one', () => {
    expect(floor.links).toEqual([
      { id: 'backend-go→frontend-react:GET /api/todos', from: 'backend-go', to: 'frontend-react', interface: 'GET /api/todos', stalled: true },
    ]);
    expect(floor.teams[1].waitingOn).toEqual(['GET /api/todos']);
  });

  it('carries the gates and the integration state', () => {
    expect(floor.teams[0].gate).toBe('green');
    expect(floor.teams[1].gate).toBe('');
    expect(floor.integration).toMatchObject({ acceptance: 'make e2e', ready: false });
  });

  it('prefers the composition’s resolved manager over the chart’s wish', () => {
    const composition: DynamicComposition = {
      summary: 'x',
      teams: [{ id: 'frontend-react', manager: 'fe-triage', manager_default: false }],
    };
    const f = buildFloor({ squads: chart, tasks, events, composition, running: true, now: T0 });
    expect(f.teams[1].manager).toBe('fe-triage');
    expect(f.teams[1].managerDefault).toBe(false);
  });

  it('keeps recent manager handoffs and forgets old ones', () => {
    const withHandoff: RunEvent[] = [
      ...events,
      { phase: 'coord', kind: 'debug', agent: 'backend-triage', task_id: 'T2', message: 'backend-triage proposes go-corrector — compile error', time: at(10) },
      { phase: 'plan', kind: 'output', agent: 'go-corrector', task_id: 'T2', message: 'T2 reassigned from go-worker to go-corrector — compile error', time: at(11) },
    ];
    const fresh = buildFloor({ squads: chart, tasks, events: withHandoff, composition: null, running: true, now: T0 + 12_000 });
    expect(fresh.handoffs.map((h) => `${h.from}→${h.to}`)).toEqual(['backend-triage→go-corrector', 'go-worker→go-corrector']);
    expect(fresh.handoffs[1]).toMatchObject({ task: 'T2', team: 'backend-go', reason: 'compile error' });

    const stale = buildFloor({ squads: chart, tasks, events: withHandoff, composition: null, running: true, now: T0 + 12_000 + HANDOFF_TTL_MS });
    expect(stale.handoffs).toEqual([]);
  });
});

describe('buildFloor with one team', () => {
  it('seats the lone team at its own table with every ticket, seam or not', () => {
    const one: SquadsView = { ok: true, summary: 'a Python service', squads: [{ id: 'python-api', name: 'Python API', owns: ['**'], acceptance: 'pytest', worker: 'py-worker', reviewer: 'py-reviewer', manager: 'py-lead', total: 2, done: 0, blocked: 0, in_flight: 1, complete: false, stuck: false }], task_teams: { T1: 'python-api' } };
    const f = buildFloor({ squads: one, tasks: [task('T1', { squad: 'python-api' }), task('T2', { status: 'running', column: 'in_progress' })], events: [{ phase: 'execute', kind: 'agent_start', agent: 'py-worker', task_id: 'T2', message: 'on it', time: at(1) }], composition: null, running: true, now: T0 + 2000 });
    expect(f.mode).toBe('teams');
    expect(f.teams).toHaveLength(1);
    expect(f.teams[0].manager).toBe('py-lead');
    expect(f.teams[0].tickets.map((t) => t.id)).toEqual(['T1', 'T2']);
    expect(f.unassigned).toEqual([]);
    expect(f.teams[0].agents.find((a) => a.id === 'py-worker')).toMatchObject({ active: true, task: 'T2' });
  });
});

describe('buildFloor without an org chart', () => {
  it('is idle with nothing to show', () => {
    const f = buildFloor({ squads: { ok: false }, tasks: [], events: [], composition: null, running: false, now: T0 });
    expect(f.mode).toBe('idle');
    expect(f.teams).toEqual([]);
  });

  it('shows the pipeline crew from the composition and the log', () => {
    const composition: DynamicComposition = {
      summary: 'a fix',
      team_note: 'no team matched this request — it runs as one stream',
      execute: { default_role: 'go-worker', reviewer: 'reviewer', corrector: 'corrector' },
      phases: [{ id: 'plan', agent: 'planner', enabled: true }, { id: 'polish', agent: 'reviewer', enabled: false }],
      team: [{ role: 'go-tester' }],
    };
    const f = buildFloor({ squads: { ok: false }, tasks: tasks.slice(0, 2), events, composition, running: true, now: T0 });
    expect(f.mode).toBe('crew');
    expect(f.teams).toHaveLength(1);
    const crew = f.teams[0];
    expect(crew.name).toBe('Pipeline crew');
    expect(crew.crew).toBe(true);
    expect(crew.agents.map((a) => a.id)).toEqual(['go-worker', 'reviewer', 'corrector', 'go-tester', 'planner']);
    expect(crew.agents.find((a) => a.id === 'go-worker')).toMatchObject({ seat: 'worker', active: true, task: 'T2' });
    expect(crew.tickets).toHaveLength(2);
    expect(crew.done).toBe(1);
    expect(f.summary).toContain('one stream');
  });

  it('shows a single library team as the island when one staffs the run', () => {
    const composition: DynamicComposition = {
      summary: 'x',
      team_mode: 'single',
      team_note: 'team backend-go staffs this run as one stream',
      teams: [{ id: 'backend-go', name: 'Backend', worker: 'go-worker', tester: 'go-tester', manager: 'backend-triage', agents: ['deep'] }],
    };
    const f = buildFloor({ squads: { ok: false }, tasks: [], events: [], composition, running: false, now: T0 });
    expect(f.mode).toBe('crew');
    expect(f.teams[0]).toMatchObject({ id: 'backend-go', name: 'Backend', manager: 'backend-triage', managerDefault: false });
    expect(f.teams[0].agents.map((a) => `${a.seat}:${a.id}`)).toEqual([
      'manager:backend-triage', 'worker:go-worker', 'tester:go-tester', 'member:deep',
    ]);
  });

  it('adds whoever the log heard from, even when the plan never named them', () => {
    const ev: RunEvent[] = [{ phase: 'explore', kind: 'agent_start', agent: 'explorer', message: 'looking', time: at(1) }];
    const f = buildFloor({ squads: null, tasks: [], events: ev, composition: null, running: true, now: T0 });
    expect(f.mode).toBe('crew');
    expect(f.teams[0].agents).toMatchObject([{ id: 'explorer', seat: 'member', active: true, task: undefined, touched: 0 }]);
    expect(f.now?.agent).toBe('explorer');
  });
});

describe('borrowed seats', () => {
  it('seats the pipeline’s tester at a team that names none, marked as borrowed', () => {
    const noTester: SquadsView = {
      ...chart,
      squads: [{ ...chart.squads![0], tester: undefined, reviewer: undefined }, chart.squads![1]],
    };
    const composition: DynamicComposition = {
      summary: 'x',
      teams: [
        {
          id: 'backend-go',
          worker: 'go-worker',
          manager: 'backend-triage',
          seats: [
            { role: 'worker', agent: 'go-worker', source: 'team' },
            { role: 'reviewer', agent: 'reviewer', source: 'pipeline' },
            { role: 'tester', agent: 'go-tester', source: 'pipeline' },
            { role: 'manager', agent: 'backend-triage', source: 'team' },
          ],
          gaps: ['team backend-go names no tester — the pipeline\'s go-tester takes its tester seat'],
        },
      ],
    };
    const f = buildFloor({ squads: noTester, tasks, events, composition, running: true, now: T0 });
    const backend = f.teams[0];
    expect(backend.agents.map((a) => `${a.seat}:${a.id}${a.borrowed ? `(${a.borrowed})` : ''}`)).toEqual([
      'manager:backend-triage', 'worker:go-worker', 'member:go-corrector', 'reviewer:reviewer(pipeline)', 'tester:go-tester(pipeline)',
    ]);
    // A borrowed seat never duplicates a member the team already has.
    const dup: DynamicComposition = { summary: 'x', teams: [{ id: 'backend-go', seats: [{ role: 'tester', agent: 'go-corrector', source: 'pipeline' }] }] };
    const g = buildFloor({ squads: noTester, tasks, events, composition: dup, running: true, now: T0 });
    expect(g.teams[0].agents.filter((a) => a.id === 'go-corrector')).toHaveLength(1);
  });
});

describe('agentTrail', () => {
  it('lists one agent’s recent lines, newest first, skipping token noise', () => {
    const withNoise: RunEvent[] = [
      ...events,
      { phase: 'execute', kind: 'token', agent: 'go-worker', task_id: 'T2', message: 'fn', time: at(5) },
      { phase: 'execute', kind: 'output', agent: 'go-worker', task_id: 'T2', message: 'wrote handler.go', time: at(6) },
      { phase: 'execute', kind: 'output', agent: 'go-reviewer', task_id: 'T2', message: 'looks fine', time: at(7) },
    ];
    const trail = agentTrail(withNoise, 'go-worker');
    expect(trail.map((l) => l.message)).toEqual(['wrote handler.go', 'implementing T2', 'done', 'implementing T1']);
    expect(trail[0]).toMatchObject({ task: 'T2', kind: 'output', at: T0 + 6000 });
    expect(agentTrail(withNoise, 'go-worker', 2)).toHaveLength(2);
    expect(agentTrail(withNoise, 'nobody')).toEqual([]);
  });
});

describe('diffFloors', () => {
  const base = { squads: chart, composition: null, running: true, now: T0 + 5000 };

  it('says nothing about the first floor', () => {
    const next = buildFloor({ ...base, tasks, events });
    expect(diffFloors(null, next, T0)).toEqual([]);
  });

  it('flashes a ticket that appears, one that finishes, and a person who starts', () => {
    const before = buildFloor({ ...base, tasks: tasks.slice(0, 3), events: events.slice(0, 3) });
    const after = buildFloor({
      ...base,
      tasks: [task('T1', { status: 'done', column: 'done' }), task('T2', { status: 'done', column: 'done' }), task('T3', { status: 'blocked', column: 'blocked' }), task('T4', { title: 'a new one' })],
      events,
    });
    const pulses = diffFloors(before, after, T0 + 9000);
    const kinds = pulses.map((p) => [p.kind, p.kind === 'agent-start' ? p.agent : p.ticket]);
    expect(kinds).toEqual(
      expect.arrayContaining([
        ['ticket-done', 'T2'],
        ['ticket-new', 'T4'],
        ['agent-start', 'go-worker'],
      ]),
    );
    const done = pulses.find((p) => p.kind === 'ticket-done')!;
    expect(done).toMatchObject({ tone: 'good', team: 'backend-go', text: 'T2 done ✓ · go-worker', detail: 'Task T2', at: T0 + 9000 });
    const fresh = pulses.find((p) => p.kind === 'ticket-new')!;
    expect(fresh.text).toBe('T4 appeared'); // T4 is not on any team: the seam
    const start = pulses.find((p) => p.kind === 'agent-start')!;
    expect(start).toMatchObject({ team: 'backend-go', ticket: 'T2', text: 'go-worker started on T2', detail: 'implementing T2' });
    // Unchanged tickets produce nothing.
    expect(pulses.some((p) => p.ticket === 'T1' || p.ticket === 'T3')).toBe(false);
  });

  it('notices a gate, a finished team and a fresh handoff', () => {
    const before = buildFloor({ ...base, squads: { ...chart, gates: [] }, tasks, events });
    const allDone: SquadsView = {
      ...chart,
      squads: [{ ...chart.squads![0], done: 2, in_flight: 0, complete: true }, chart.squads![1]],
    };
    const handoff: RunEvent = { phase: 'plan', kind: 'output', agent: 'go-corrector', task_id: 'T2', message: 'T2 reassigned from go-worker to go-corrector — compile error', time: at(8) };
    const after = buildFloor({
      ...base,
      squads: allDone,
      tasks: [task('T1', { status: 'done', column: 'done' }), task('T2', { status: 'done', column: 'done' }), task('T3', { status: 'blocked', column: 'blocked' }), task('T9')],
      events: [...events, handoff],
      now: T0 + 9000,
    });
    const pulses = diffFloors(before, after, T0 + 9000);
    expect(pulses.map((p) => p.kind)).toEqual(expect.arrayContaining(['gate', 'team-complete', 'handoff']));
    expect(pulses.find((p) => p.kind === 'gate')).toMatchObject({ tone: 'good', text: 'Backend: gate green', detail: 'go test ./...' });
    expect(pulses.find((p) => p.kind === 'team-complete')).toMatchObject({ text: 'Backend finished every ticket', detail: '2/2' });
    expect(pulses.find((p) => p.kind === 'handoff')).toMatchObject({ ticket: 'T2', agent: 'go-corrector', text: 'T2: go-worker → go-corrector', detail: 'compile error' });
  });

  it('forgets pulses after their time', () => {
    const p = { id: 'x', kind: 'ticket-new' as const, tone: 'brand' as const, at: T0, team: '', text: 'x' };
    expect(freshPulses([p], T0 + PULSE_TTL_MS - 1)).toHaveLength(1);
    expect(freshPulses([p], T0 + PULSE_TTL_MS)).toHaveLength(0);
  });
});

describe('several people at once, and the stage', () => {
  const log: RunEvent[] = [
    { phase: 'plan', kind: 'agent_start', agent: 'planner', message: 'reading the request', time: at(1) },
    { phase: 'plan', kind: 'agent_end', agent: 'planner', message: 'planned 3 tickets', time: at(2) },
    { phase: 'split', kind: 'agent_start', agent: 'splitter', message: 'splitting', time: at(3) },
    { phase: 'split', kind: 'agent_end', agent: 'splitter', message: 'split', time: at(4) },
    { phase: 'execute', kind: 'agent_start', agent: 'go-worker', task_id: 'T2', message: 'implementing T2', time: at(5) },
    { phase: 'execute', kind: 'agent_start', agent: 'react-worker', task_id: 'T3', message: 'implementing T3', time: at(6) },
    { phase: 'execute', kind: 'token', agent: 'go-worker', task_id: 'T2', message: 'x', time: at(7) },
  ];
  const composition: DynamicComposition = {
    summary: 'x',
    phases: [
      { id: 'plan', agent: 'planner', enabled: true, when: 'auto' },
      { id: 'split', agent: 'splitter', enabled: true, when: 'auto' },
      { id: 'docs', agent: 'docs', enabled: true, when: 'never' },
    ],
    execute: { default_role: 'go-worker' },
  };

  it('lights every agent inside a start/end pair, each on its own table', () => {
    const f = buildFloor({ squads: chart, tasks, events: log, composition, running: true, now: T0 + 8000 });
    const go = f.teams[0].agents.find((a) => a.id === 'go-worker')!;
    const react = f.teams[1].agents.find((a) => a.id === 'react-worker')!;
    expect(go).toMatchObject({ active: true, task: 'T2' });
    expect(react).toMatchObject({ active: true, task: 'T3' });
    expect(f.teams[0].agents.filter((a) => a.active)).toHaveLength(1);
  });

  it('puts the phase agents on the stage, in phase order, and knows the phase', () => {
    const f = buildFloor({ squads: chart, tasks, events: log, composition, running: true, now: T0 + 8000 });
    expect(f.stage.map((a) => a.id)).toEqual(['planner', 'splitter']);
    expect(f.stage[0]).toMatchObject({ phase: 'plan', active: false, spoke: true, lastMessage: 'reading the request' });
    expect(f.phase).toMatchObject({ id: 'execute', agent: 'react-worker', message: 'implementing T3', since: T0 + 5000 });
  });

  it('keeps the newest voice active even without a start/end pair', () => {
    const f = buildFloor({ squads: chart, tasks, events: log.slice(0, 1), composition, running: true, now: T0 + 8000 });
    expect(f.stage[0]).toMatchObject({ id: 'planner', active: true });
    expect(f.phase?.id).toBe('plan');
  });

  it('has no stage at the crew table — the crew already seats those roles', () => {
    const f = buildFloor({ squads: null, tasks: [], events: log, composition, running: true, now: T0 + 8000 });
    expect(f.mode).toBe('crew');
    expect(f.stage).toEqual([]);
    expect(f.teams[0].agents.map((a) => a.id)).toEqual(expect.arrayContaining(['planner', 'splitter', 'go-worker', 'react-worker']));
  });

  it('pulses a phase change and a stage agent starting', () => {
    const before = buildFloor({ squads: chart, tasks, events: log.slice(0, 2), composition, running: true, now: T0 + 3000 });
    const after = buildFloor({ squads: chart, tasks, events: log.slice(0, 3), composition, running: true, now: T0 + 4000 });
    const pulses = diffFloors(before, after, T0 + 4000);
    expect(pulses.find((p) => p.kind === 'phase')).toMatchObject({ text: 'phase: split', detail: 'splitting' });
    expect(pulses.find((p) => p.kind === 'agent-start')).toMatchObject({ agent: 'splitter', text: 'splitter is on · split' });
  });
});
