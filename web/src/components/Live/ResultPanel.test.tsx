import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import ResultPanel from './ResultPanel';
import { RUN_PROMPT_EVENT } from '@/components/ui/events';
import type { InterruptedRun, LatestRunResponse, QuerySession, RunRepairs } from '@/types';

const getInterruptedRuns = vi.fn(async (): Promise<InterruptedRun[]> => []);
const getQueries = vi.fn(async (): Promise<QuerySession[]> => []);
const resumeRun = vi.fn(async () => ({ status: 'started' }));
const startRun = vi.fn(async () => ({ status: 'started' }));

vi.mock('@/api/client', () => ({
  errorText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
  getInterruptedRuns: () => getInterruptedRuns(),
  getQueries: () => getQueries(),
  resumeRun: (...a: unknown[]) => resumeRun(...(a as [])),
  startRun: (...a: unknown[]) => startRun(...(a as [])),
}));

const run = (repairs?: RunRepairs | null, over?: Partial<LatestRunResponse['result']>): LatestRunResponse => ({
  running: false,
  events: [],
  result: {
    success: true,
    summary: 'Todo app: Go API + React SPA — 2/2 tasks done, 0 failed',
    duration: 21_000_000_000,
    failed_tasks: 0,
    repairs,
    ...over,
  },
});

describe('ResultPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    getInterruptedRuns.mockResolvedValue([]);
    getQueries.mockResolvedValue([]);
  });

  it('says there is nothing to show before a run finishes', () => {
    render(<ResultPanel result={null} />);
    expect(screen.getByText('No result yet')).toBeInTheDocument();
  });

  // A run where nothing went wrong should not talk about repairs at all.
  it('stays quiet on a run that never had to fix anything', () => {
    render(<ResultPanel result={run(null)} />);
    expect(screen.getByText(/2\/2 tasks done/)).toBeInTheDocument();
    expect(screen.queryByText(/defect/i)).not.toBeInTheDocument();
  });

  // The headline: after a stream full of loud red failures, the last screen has
  // to say the run handled them. Otherwise the failures read as swallowed.
  it('says the run fixed the defect without you', () => {
    render(<ResultPanel result={run({ found: 1, resolved: 1, restaffed: 1, needs_human: 0 })} />);
    expect(screen.getByText(/Fixed the 1 defect without you/)).toBeInTheDocument();
    expect(screen.getByText(/1 reassigned by the project manager/)).toBeInTheDocument();
  });

  it('pluralizes a run that fixed several', () => {
    render(<ResultPanel result={run({ found: 3, resolved: 3, restaffed: 0, needs_human: 0 })} />);
    expect(screen.getByText(/Fixed all 3 defects without you/)).toBeInTheDocument();
    expect(screen.queryByText(/reassigned/)).not.toBeInTheDocument();
  });

  // Partial repair must not read as a clean sweep.
  it('says plainly what is still open', () => {
    render(<ResultPanel result={run({ found: 3, resolved: 1, restaffed: 1, needs_human: 2 })} />);
    expect(screen.getByText(/1 of 3 defects fixed/)).toBeInTheDocument();
    expect(screen.getByText(/2 still open/)).toBeInTheDocument();
    expect(screen.queryByText(/without you/)).not.toBeInTheDocument();
  });

  it('keeps the existing counters', () => {
    render(<ResultPanel result={run(null)} />);
    expect(screen.getByText('Failed')).toBeInTheDocument();
    expect(screen.getByText('21.0s')).toBeInTheDocument();
  });

  // ── The run's end is not a dead end ──

  it('links the failed count to the blocked column and the queue to Review', () => {
    render(
      <MemoryRouter>
        <ResultPanel result={run(null, { success: false, failed_tasks: 2 })} pending={3} />
      </MemoryRouter>,
    );
    expect(screen.getByRole('link', { name: /Open blocked on Board/ })).toHaveAttribute('href', '/board?column=blocked');
    expect(screen.getByRole('link', { name: /Review 3 pending/ })).toHaveAttribute('href', '/review');
  });

  it('hides the board and review links when there is nothing behind them', () => {
    render(<ResultPanel result={run(null)} pending={0} />);
    expect(screen.queryByText(/Open blocked/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Review .* pending/)).not.toBeInTheDocument();
  });

  it('offers Resume when an interrupted run exists and calls the handler it was given', async () => {
    const onResume = vi.fn();
    render(
      <ResultPanel
        result={run(null)}
        interrupted={[{ id: 'q-9', query: 'ship it', updated_at: '', tasks: 4, done: 2, blocked: 1, react_resume: false }]}
        onResume={onResume}
      />,
    );
    await userEvent.click(screen.getByRole('button', { name: /Resume interrupted run/ }));
    expect(onResume).toHaveBeenCalledWith('q-9');
  });

  it('finds the resumable run itself when nobody passed one', async () => {
    getInterruptedRuns.mockResolvedValue([
      { id: 'q-1', query: 'x', updated_at: '', tasks: 1, done: 0, blocked: 0, react_resume: false },
    ]);
    render(<ResultPanel result={run(null)} />);
    expect(await screen.findByRole('button', { name: /Resume interrupted run/ })).toBeInTheDocument();
  });

  it('runs the prompt again through the shared event, and starts it itself when nobody listens', async () => {
    getQueries.mockResolvedValue([
      { id: 'q-2', query: 'add a health endpoint', success: true, summary: '', updated_at: '' },
    ]);
    render(<ResultPanel result={run(null)} />);
    const again = await screen.findByRole('button', { name: /Run again with this prompt/ });

    // A listener (the Live view) claims the event: no direct request.
    const claim = (ev: Event) => ev.preventDefault();
    window.addEventListener(RUN_PROMPT_EVENT, claim);
    await userEvent.click(again);
    expect(startRun).not.toHaveBeenCalled();
    window.removeEventListener(RUN_PROMPT_EVENT, claim);

    // Nobody listening: the panel starts the run directly.
    await userEvent.click(again);
    await waitFor(() => expect(startRun).toHaveBeenCalledWith(expect.objectContaining({ query: 'add a health endpoint' })));
  });
});
