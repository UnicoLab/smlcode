import { render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import RecoveryPanel from './RecoveryPanel';
import type { RunEvent } from '@/types';

let clock = 0;
const loop = (action: string, reason: string, extra: Record<string, unknown> = {}): RunEvent => ({
  phase: 'test',
  kind: 'loop',
  message: reason,
  scope: action,
  output: JSON.stringify({ action, reason, wave: 1, ...extra }),
  time: new Date(1_700_000_000_000 + clock++ * 1000).toISOString(),
});

const line = (message: string): RunEvent => ({
  phase: 'plan',
  kind: 'output',
  message,
  time: new Date(1_700_000_000_000 + clock++ * 1000).toISOString(),
});

const healed = (): RunEvent[] => {
  clock = 0;
  return [
    loop('tester_reject', 'cmd/server/main.go', { failures: ['undefined: json.NewEncoder'] }),
    loop('rewrite', 'raised a ticket'),
    loop('corrective_wave', 'corrective wave'),
    loop('reverify', 're-verify'),
    loop('tester_reject', 'cmd/server/main.go', { failures: ['undefined: json.NewEncoder'] }),
    line('T1 reassigned from go-worker to go-corrector — the worker cannot see its own encoder bug'),
    loop('restaffed_wave', 'the project manager moved the ticket'),
    loop('resolved', 'the re-staffed specialist fixed it'),
  ];
};

describe('RecoveryPanel', () => {
  // The default state of a healthy run. Saying "nothing has needed fixing" is
  // information; an empty box is a bug the user has to rule out.
  it('says so when nothing has gone wrong', () => {
    render(<RecoveryPanel events={[]} />);
    expect(screen.getByText('Nothing has needed fixing')).toBeInTheDocument();
  });

  // The headline: a user who saw the red "tester found 1 failure" can see, in
  // one place, that it was handled.
  it('shows a defect that the harness fixed by itself', () => {
    render(<RecoveryPanel events={healed()} />);
    expect(screen.getByText('1 fixed automatically')).toBeInTheDocument();
    expect(screen.getByText('Fixed automatically')).toBeInTheDocument();
    expect(screen.getByText('undefined: json.NewEncoder')).toBeInTheDocument();
  });

  // "Moved it to somebody" without the somebody is the kind of half answer that
  // makes a user distrust the whole panel.
  it('names the specialist the ticket was moved to', () => {
    render(<RecoveryPanel events={healed()} />);
    expect(screen.getByText('go-corrector')).toBeInTheDocument();
    expect(screen.getByText('the project manager moved the ticket')).toBeInTheDocument();
  });

  it('counts the attempts at one defect rather than showing two problems', () => {
    render(<RecoveryPanel events={healed()} />);
    expect(screen.getAllByRole('listitem').filter((li) => within(li).queryByText(/Fixed automatically/))).toHaveLength(1);
    expect(screen.getByText('2 attempts')).toBeInTheDocument();
  });

  it('marks work still in progress as being fixed, not as a failure', () => {
    render(<RecoveryPanel events={healed().slice(0, -1)} />);
    expect(screen.getByText('Being fixed')).toBeInTheDocument();
    expect(screen.queryByText('Needs you')).not.toBeInTheDocument();
  });

  // The one state that must stand out: the harness has run out of moves.
  it('flags a defect that needs a person', () => {
    const events = [...healed().slice(0, -1), loop('unresolved', 'still failing after reassignment')];
    render(<RecoveryPanel events={events} />);
    expect(screen.getByText('Needs you')).toBeInTheDocument();
    expect(screen.getByText('1 needs you')).toBeInTheDocument();
  });

  it('lists what happened, in order', () => {
    render(<RecoveryPanel events={healed()} />);
    const steps = screen.getAllByRole('listitem').map((li) => li.textContent ?? '');
    const joined = steps.join(' | ');
    expect(joined).toContain('the tester rejected the delivery');
    expect(joined).toContain('raised a correction ticket');
    expect(joined).toContain('the project manager moved the ticket');
    expect(joined).toContain('resolved');
  });
});
