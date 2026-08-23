import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import ConnectionBadge from './ConnectionBadge';
import type { ConnectionState } from '@/types';

describe('ConnectionBadge', () => {
  // Kill the server and the SPA used to look perfectly idle and healthy:
  // refresh() ran once at mount and swallowed its failure with `catch {}`.
  it.each<[ConnectionState, RegExp]>([
    ['connecting', /connecting/i],
    ['live', /live/i],
    ['reconnecting', /reconnecting/i],
    ['down', /api disconnected/i],
  ])('announces the %s state', (state, label) => {
    render(<ConnectionBadge state={state} />);
    expect(screen.getByRole('status')).toHaveTextContent(label);
  });

  it('uses a polite live region so a state change is announced', () => {
    render(<ConnectionBadge state="down" />);
    expect(screen.getByRole('status')).toHaveAttribute('aria-live', 'polite');
  });

  it('offers a retry action only while degraded', async () => {
    const onRetry = vi.fn();
    const { rerender } = render(<ConnectionBadge state="live" onRetry={onRetry} />);
    expect(screen.queryByRole('button', { name: /reconnect/i })).not.toBeInTheDocument();

    rerender(<ConnectionBadge state="down" onRetry={onRetry} />);
    const button = screen.getByRole('button', { name: /reconnect/i });
    await userEvent.click(button);
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it('explains what to do when the API is down', () => {
    render(<ConnectionBadge state="down" />);
    expect(screen.getByTitle(/slmcode studio.*still running/i)).toBeInTheDocument();
  });
});
