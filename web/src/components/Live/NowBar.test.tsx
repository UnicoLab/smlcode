import { render, screen } from '@testing-library/react';
import { act } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import NowBar from './NowBar';
import type { RunEvent, SquadsView } from '@/types';

const at = (isoOffsetSeconds: number, over: Partial<RunEvent> = {}): RunEvent => ({
  phase: 'execute',
  kind: 'agent_start',
  message: 'writing the handler',
  time: new Date(Date.now() - isoOffsetSeconds * 1000).toISOString(),
  ...over,
});

const squads: SquadsView = {
  ok: true,
  squads: [
    {
      id: 'backend-go',
      name: 'Backend',
      manager: 'triage',
      total: 2,
      done: 1,
      blocked: 0,
      in_flight: 1,
      complete: false,
      stuck: false,
    },
  ],
  task_teams: { T1: 'backend-go' },
};

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
});
afterEach(() => {
  vi.useRealTimers();
});

describe('NowBar', () => {
  // The question a person actually asks while a local 30B works for eleven
  // minutes: what is it doing, and is it stuck? The log answers neither — its
  // last line scrolls away and the next can be four minutes out.
  it('names the agent, the task and the team in flight', () => {
    render(
      <NowBar
        events={[at(30, { agent: 'go-worker', task_id: 'T1', model: 'Qwen3-Coder-30B' })]}
        running
        squads={squads}
      />,
    );

    expect(screen.getByText('@go-worker')).toBeInTheDocument();
    expect(screen.getByText('T1')).toBeInTheDocument();
    expect(screen.getByText('backend-go')).toBeInTheDocument();
    expect(screen.getByText('writing the handler')).toBeInTheDocument();
  });

  // The clock is the point. A number that ticks is the difference between
  // "thinking" and "hung", and no amount of log output provides it — least of
  // all a log that has stopped producing lines.
  it('keeps counting while nothing new arrives', () => {
    const events = [at(5, { agent: 'go-worker' })];
    render(<NowBar events={events} running squads={null} />);
    expect(screen.getByText('5s')).toBeInTheDocument();

    act(() => {
      vi.advanceTimersByTime(20_000);
    });
    expect(screen.getByText('25s')).toBeInTheDocument();
  });

  // Past two minutes on one step, say so in a colour rather than leaving the
  // reader to do the arithmetic. Local 30B calls really do take this long, and
  // a user who knows that is a user who waits instead of hitting stop.
  it('marks a long step so a slow local model does not read as a hang', () => {
    render(<NowBar events={[at(200, { agent: 'go-worker' })]} running squads={null} />);
    const clock = screen.getByTitle(/A local 30B routinely takes minutes/);
    expect(clock).toHaveTextContent('3m20s');
    expect(clock.className).toMatch(/amber/);
  });

  it('sums the run’s token usage across every event', () => {
    render(
      <NowBar
        events={[
          at(60, { agent: 'planner', tokens: 1200, cost_usd: 0 }),
          at(10, { agent: 'go-worker', tokens: 3400, cost_usd: 0 }),
        ]}
        running
        squads={null}
      />,
    );
    expect(screen.getByTitle(/4,600 tokens this run/)).toBeInTheDocument();
  });

  // The clock keys on the floor's working set. A worker whose agent_end has
  // arrived is finished, whatever the newest line names; the bar says nobody
  // is working and stops counting, instead of timing a step that is over.
  it('stops the clock once the working agent has ended', () => {
    render(
      <NowBar
        events={[
          at(40, { agent: 'go-worker', task_id: 'T1', message: 'writing the handler' }),
          at(10, { agent: 'go-worker', task_id: 'T1', kind: 'agent_end', message: 'worker finished' }),
        ]}
        running
        squads={null}
      />,
    );
    const bar = screen.getByTestId('now-bar');
    expect(bar).toHaveAttribute('data-working', 'false');
    expect(bar).toHaveTextContent(/nobody is working/);
    expect(screen.queryByTitle(/A local 30B routinely takes minutes/)).not.toBeInTheDocument();
    expect(screen.queryByText('@go-worker')).not.toBeInTheDocument();
  });

  // The floor's own reading wins when the page passes it: one read of the log
  // per flush, and the ticker and the floor never disagree about who is on.
  it('uses the floor model’s now when given one, and links it', () => {
    render(
      <NowBar
        events={[at(5, { agent: 'planner' })]}
        running
        squads={squads}
        now={{ agent: 'go-worker', task: 'T1', team: 'backend-go', message: 'from the floor', model: 'm', since: Date.now() - 12_000 }}
        totals={{ tokens: 2500, cost: 0 }}
      />,
    );
    expect(screen.getByText('@go-worker').closest('a')).toHaveAttribute('href', '/?agent=go-worker&team=backend-go');
    expect(screen.getByText('T1').closest('a')).toHaveAttribute('href', '/?task=T1&team=backend-go');
    expect(screen.getByText('backend-go').closest('a')).toHaveAttribute('href', '/teams?team=backend-go');
    expect(screen.getByText('from the floor')).toBeInTheDocument();
    expect(screen.getByText('12s')).toBeInTheDocument();
    expect(screen.getByTitle(/2,500 tokens this run/)).toBeInTheDocument();
  });

  // It is an activity indicator, not a summary. A finished run has a result
  // panel; a spinner over it would say the run is still going.
  it('shows nothing when no run is going', () => {
    const { container } = render(
      <NowBar events={[at(5, { agent: 'go-worker' })]} running={false} squads={null} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('shows nothing before the first event', () => {
    const { container } = render(<NowBar events={[]} running squads={null} />);
    expect(container).toBeEmptyDOMElement();
  });

  // A team badge on the wrong half is worse than no badge, so an unknown task
  // gets none rather than a guess.
  it('omits the team when the task is not in the org chart', () => {
    render(<NowBar events={[at(5, { agent: 'go-worker', task_id: 'T9' })]} running squads={squads} />);
    expect(screen.queryByText('backend-go')).not.toBeInTheDocument();
  });

  // The most recent thing that STARTED, not the most recent line: a wave runs
  // several agents and the last one announced is the one being waited on.
  it('reports the latest agent even when plain lines followed it', () => {
    render(
      <NowBar
        events={[
          at(90, { agent: 'planner', message: 'planning' }),
          at(40, { agent: 'go-worker', message: 'writing the handler' }),
          at(5, { message: 'wrote cmd/server/main.go', kind: 'file_change' }),
        ]}
        running
        squads={null}
      />,
    );
    expect(screen.getByText('@go-worker')).toBeInTheDocument();
    expect(screen.getByText('writing the handler')).toBeInTheDocument();
  });
});
