import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import PhaseRail from './PhaseRail';
import type { PhaseState, RailGroup } from './PhaseRail';

const GROUPS: RailGroup[] = [
  { id: 'prepare', label: 'Prepare', phases: ['init', 'context'] },
  { id: 'build', label: 'Build', phases: ['execute'] },
  { id: 'verify', label: 'Verify', phases: ['test'] },
];

const states = (m: Record<string, PhaseState>) => m;

describe('PhaseRail', () => {
  it('renders every phase of every group', () => {
    render(
      <PhaseRail
        groups={GROUPS}
        phaseState={states({ init: 'completed', context: 'active', execute: 'pending', test: 'pending' })}
        activePhase="context"
        running
      />,
    );
    for (const phase of ['init', 'context', 'execute', 'test']) {
      expect(screen.getByText(phase)).toBeInTheDocument();
    }
  });

  it('counts completed phases against the total', () => {
    render(
      <PhaseRail
        groups={GROUPS}
        phaseState={states({ init: 'completed', context: 'completed', execute: 'active', test: 'pending' })}
        activePhase="execute"
        running
      />,
    );
    expect(screen.getByText('2/4')).toBeInTheDocument();
  });

  // Position along the rail IS the progress bar, so the two are derived from
  // one number and cannot drift apart the way a separate percentage did.
  it('reports 0 of n before anything has run', () => {
    render(
      <PhaseRail
        groups={GROUPS}
        phaseState={states({ init: 'pending', context: 'pending', execute: 'pending', test: 'pending' })}
        activePhase={null}
        running={false}
      />,
    );
    expect(screen.getByText('0/4')).toBeInTheDocument();
  });

  it('renders nothing when no pipeline is known', () => {
    const { container } = render(
      <PhaseRail groups={[]} phaseState={{}} activePhase={null} running={false} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('drops groups whose phases were all filtered out by the composition', () => {
    // A budget class narrows the pipeline; an empty group is not a group.
    render(
      <PhaseRail
        groups={[
          { id: 'prepare', label: 'Prepare', phases: ['init'] },
          { id: 'design', label: 'Design', phases: [] },
        ]}
        phaseState={states({ init: 'active' })}
        activePhase="init"
        running
      />,
    );
    expect(screen.getByText('Prepare')).toBeInTheDocument();
    expect(screen.queryByText('Design')).not.toBeInTheDocument();
    expect(screen.getAllByRole('listitem')).toHaveLength(1);
    expect(screen.getByText('0/1')).toBeInTheDocument();
  });

  it('labels each phase with its group and state for assistive tech', () => {
    render(
      <PhaseRail
        groups={GROUPS}
        phaseState={states({ init: 'completed', context: 'active', execute: 'pending', test: 'pending' })}
        activePhase="context"
        running
      />,
    );
    expect(screen.getByTitle('Prepare · init · completed')).toBeInTheDocument();
    expect(screen.getByTitle('Prepare · context · active')).toBeInTheDocument();
    expect(screen.getByTitle('Build · execute · pending')).toBeInTheDocument();
  });

  it('treats a phase with no recorded state as pending', () => {
    // The rail is fed a state map derived from events; a phase the run has not
    // reached yet is simply absent from it, and absent must not crash or read
    // as done.
    render(<PhaseRail groups={GROUPS} phaseState={{}} activePhase={null} running={false} />);
    expect(screen.getByTitle('Verify · test · pending')).toBeInTheDocument();
    expect(screen.getByText('0/4')).toBeInTheDocument();
  });

  it('exposes the track as a labelled list', () => {
    render(
      <PhaseRail
        groups={GROUPS}
        phaseState={states({ init: 'active' })}
        activePhase="init"
        running
      />,
    );
    expect(screen.getByRole('list', { name: 'Pipeline phases' })).toBeInTheDocument();
    expect(screen.getAllByRole('listitem')).toHaveLength(4);
  });
});
