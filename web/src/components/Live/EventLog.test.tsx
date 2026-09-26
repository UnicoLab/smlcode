import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { RunEvent } from '@/types';
import EventLog from './EventLog';

describe('EventLog live insights', () => {
  it('does not diagnose an ongoing run as missing its terminal event', () => {
    const events: RunEvent[] = [{ kind: 'agent_start', phase: 'execute', agent: 'worker', message: 'Working', time: new Date().toISOString() }];
    const { rerender } = render(<EventLog events={events} running />);
    expect(screen.queryByText('No successful terminal event')).not.toBeInTheDocument();
    expect(screen.queryByText('Inspect the final phase')).not.toBeInTheDocument();
    // Same event identity: finishing must recompute the summary without
    // folding those events twice or leaving the old cached result behind.
    rerender(<EventLog events={events} running={false} />);
    expect(screen.getByText('No successful terminal event')).toBeVisible();
    expect(screen.getByText('Inspect the final phase')).toBeVisible();
  });
});
