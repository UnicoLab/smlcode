import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import ResultPanel from './ResultPanel';
import type { LatestRunResponse, RunRepairs } from '@/types';

const run = (repairs?: RunRepairs | null): LatestRunResponse => ({
  running: false,
  events: [],
  result: {
    success: true,
    summary: 'Todo app: Go API + React SPA — 2/2 tasks done, 0 failed',
    duration: 21_000_000_000,
    failed_tasks: 0,
    repairs,
  },
});

describe('ResultPanel', () => {
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
});
