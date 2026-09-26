import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Task } from '@/types';
import RunProgress from './RunProgress';

const task = (id: string, column: string, status = column): Task => ({
  id, column, status, title: id, description: '', role: 'worker', assignee: '', priority: 0,
  depends_on: [], files: [], acceptance: '', checklist: [], output: '', review: '', retries: 0, error: '', updated_at: '', notes: '',
});

describe('RunProgress', () => {
  it('counts completion separately from active and failed work and opens tasks', () => {
    const onTasks = vi.fn();
    render(<RunProgress tasks={[task('1', 'done'), task('2', 'in_progress'), task('3', 'blocked'), task('4', 'ready_to_dev')]}
      running connection="live" onTasks={onTasks} />);
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '1');
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuemax', '4');
    expect(screen.getByText('1 blocked / failed')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '1 of 4 tasks complete' }));
    expect(onTasks).toHaveBeenCalledOnce();
  });
  it('never presents completed tasks as a successful delivery before verification', () => {
    render(<RunProgress tasks={[task('1', 'done')]} running connection="live" onTasks={vi.fn()} />);
    expect(screen.getByText('Tasks complete · finishing checks')).toBeInTheDocument();
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuetext', expect.stringContaining('does not replace final verification'));
  });
  it('shows stale connection state and lets the user reconnect without starting another run', () => {
    const reconnect = vi.fn();
    render(<RunProgress tasks={[]} running connection="reconnecting" onTasks={vi.fn()} onReconnect={reconnect} />);
    expect(screen.getByText('Updates delayed')).toBeInTheDocument();
    expect(screen.queryByText('Live updates')).not.toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('last known state');
    fireEvent.click(screen.getByRole('button', { name: 'Reconnect now' }));
    expect(reconnect).toHaveBeenCalledOnce();
  });
  it('does not fabricate a percentage before a plan exists', () => {
    render(<RunProgress tasks={[]} running connection="live" onTasks={vi.fn()} />);
    expect(screen.getByText('Preparing the work')).toBeInTheDocument();
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
  });
});
