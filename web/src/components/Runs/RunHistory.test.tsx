import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import RunHistory, { formatCost, formatDuration, formatTokens } from './RunHistory';
import type { QuerySession, QueryView } from '@/types';

const runs: QuerySession[] = [
  {
    id: 'q-20260910-1',
    query: 'add a health endpoint',
    success: false,
    summary: '',
    updated_at: '2026-09-10T08:00:00Z',
    duration_ms: 754_000,
    tokens: 48_200,
    cost_usd: 0.0132,
    tasks_total: 5,
    tasks_done: 3,
    failed_tasks: 2,
    teams: ['api', 'web'],
  },
  // A run from an older archive: none of the new fields.
  { id: 'q-old', query: 'old run', success: true, summary: '', updated_at: '2026-09-01T08:00:00Z' },
];

const view: QueryView = {
  id: 'q-20260910-1',
  query: 'add a health endpoint',
  success: false,
  updated_at: '',
  summary_md: '',
  plan_md: '',
  tasks_md: '',
  summary: '',
  board: { plan: { summary: '', goals: [], assumptions: [], risks: [], steps: [], raw: '' }, tasks: [], columns: [], by_column: {} },
};

vi.mock('@/api/client', () => ({
  errorText: (e: unknown) => String(e),
  getQueries: vi.fn(async () => runs),
  getQuery: vi.fn(async () => view),
  getQueryEvents: vi.fn(async () => ({ id: 'q-20260910-1', events: [] })),
  getQueryTrace: vi.fn(async () => ({ id: 'q-20260910-1', phases: [], totals: {} })),
  resumeRun: vi.fn(),
}));
vi.mock('@/components/Live/EventLog', () => ({ default: () => <div data-testid="eventlog" /> }));
vi.mock('./TraceView', () => ({ default: () => <div data-testid="trace" /> }));

function Location() {
  const loc = useLocation();
  return <div data-testid="location">{loc.pathname + loc.search}</div>;
}

describe('formatters', () => {
  it('render durations, tokens and cost at a glance', () => {
    expect(formatDuration(754_000)).toBe('12m 34s');
    expect(formatDuration(42_000)).toBe('42s');
    expect(formatDuration(3_720_000)).toBe('1h 2m');
    expect(formatTokens(48_200)).toBe('48k tok');
    expect(formatTokens(1_250_000)).toBe('1.3M tok');
    expect(formatCost(0.0132)).toBe('$0.01');
    expect(formatCost(0.0004)).toBe('$0.0004');
  });
});

describe('RunHistory list', () => {
  it('shows the archive numbers and team chips when the server sends them', async () => {
    render(
      <MemoryRouter initialEntries={['/runs']}>
        <RunHistory />
      </MemoryRouter>,
    );
    const item = (await screen.findByText('add a health endpoint', { selector: 'div' })).closest('button')!;
    expect(item).toHaveTextContent('12m 34s');
    expect(item).toHaveTextContent('3/5 done');
    expect(item).toHaveTextContent('2 failed');
    expect(item).toHaveTextContent('48k tok');
    expect(item).toHaveTextContent('$0.01');
    expect(item).toHaveTextContent('api');
    expect(item).toHaveTextContent('web');

    // The older archive shows only what it has.
    const old = screen.getByText('old run').closest('button')!;
    expect(old).not.toHaveTextContent('done');
    expect(old).not.toHaveTextContent('tok');
  });

  it('replays the selected run on the Live floor', async () => {
    render(
      <MemoryRouter initialEntries={['/runs']}>
        <Routes>
          <Route path="/runs" element={<RunHistory />} />
          <Route path="/" element={<Location />} />
        </Routes>
      </MemoryRouter>,
    );
    await userEvent.click(await screen.findByRole('button', { name: /Replay on the floor/ }));
    expect(await screen.findByTestId('location')).toHaveTextContent('/?run=q-20260910-1&replay=1');
  });

  it('opens the run named by ?run=', async () => {
    render(
      <MemoryRouter initialEntries={['/runs?run=q-old']}>
        <RunHistory />
      </MemoryRouter>,
    );
    const old = await screen.findByText('old run');
    expect(old.closest('button')).toHaveAttribute('aria-current', 'true');
  });
});
