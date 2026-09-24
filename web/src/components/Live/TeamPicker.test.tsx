import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import TeamPicker from './TeamPicker';
import type { TeamSpec } from '@/types';

const teams = [
  { id: 'backend-python', name: 'Backend · Python', worker: 'python-worker' },
  { id: 'repo-insight', name: "Insight · What's going on", worker: 'architect-worker' },
  { id: 'openshift', name: 'Platform · OpenShift', worker: 'openshift-worker' },
] as TeamSpec[];

describe('TeamPicker', () => {
  it('shows Dynamic as the default and names the dispatcher’s pick', () => {
    render(
      <TeamPicker teams={teams} configPinned={[]} value={[]} mode="dynamic" dispatcherPick={['backend-python', 'repo-insight']} dispatcherNote="2 teams build in parallel" onChange={vi.fn()} onModeChange={vi.fn()} />,
    );
    const button = screen.getByTestId('team-picker');
    expect(button).toHaveAttribute('data-mode', 'dynamic');
    expect(button).toHaveTextContent('Dynamic · backend-python + repo-insight');
    expect(screen.queryByTestId('team-picker-dynamic')).toBeNull();
    fireEvent.click(button);
    expect(screen.getByTestId('team-mode-dynamic')).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByTestId('dispatcher-note')).toHaveTextContent('2 teams build in parallel');
  });

  it('switches to Strict when a team is picked', () => {
    const onChange = vi.fn();
    const onModeChange = vi.fn();
    render(<TeamPicker teams={teams} configPinned={[]} value={[]} mode="dynamic" onChange={onChange} onModeChange={onModeChange} />);
    fireEvent.click(screen.getByTestId('team-picker'));
    fireEvent.click(screen.getByText('openshift'));
    expect(onChange).toHaveBeenCalledWith(['openshift']);
    expect(onModeChange).toHaveBeenCalledWith('strict');
  });

  it('goes back to Dynamic in one click from Strict', () => {
    const onChange = vi.fn();
    const onModeChange = vi.fn();
    render(<TeamPicker teams={teams} configPinned={[]} value={['openshift']} mode="strict" onChange={onChange} onModeChange={onModeChange} />);
    expect(screen.getByTestId('team-picker')).toHaveTextContent('Strict · openshift');
    fireEvent.click(screen.getByTestId('team-picker-dynamic'));
    expect(onChange).toHaveBeenCalledWith([]);
    expect(onModeChange).toHaveBeenCalledWith('dynamic');
  });

  it('drops back to Dynamic when the last Strict team is unpicked', () => {
    const onChange = vi.fn();
    const onModeChange = vi.fn();
    render(<TeamPicker teams={teams} configPinned={['backend-python']} value={[]} mode="strict" onChange={onChange} onModeChange={onModeChange} />);
    fireEvent.click(screen.getByTestId('team-picker'));
    fireEvent.click(screen.getByText('backend-python'));
    expect(onChange).toHaveBeenCalledWith([]);
    expect(onModeChange).toHaveBeenCalledWith('dynamic');
  });

  it('treats the saved pins as the Strict selection when nothing is picked', () => {
    render(<TeamPicker teams={teams} configPinned={['backend-python']} value={[]} mode="strict" onChange={vi.fn()} onModeChange={vi.fn()} />);
    expect(screen.getByTestId('team-picker')).toHaveTextContent('Strict · backend-python');
  });
});
