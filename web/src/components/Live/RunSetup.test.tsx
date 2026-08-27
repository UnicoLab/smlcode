import { beforeEach, describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import RunSetup from './RunSetup';
import type { AgentSpec, DynamicComposition } from '@/types';

const COMPOSITION: DynamicComposition = {
  summary: 'Deterministic dynamic pipeline for this task',
  complexity: 'critical',
  kind: 'task',
  handoff: ['target only the listed files', 'verify with go test ./...'],
  phases: [
    { id: 'context', enabled: true, when: 'always' },
    { id: 'execute', agent: 'go-worker', enabled: true, when: 'always' },
    { id: 'polish', enabled: false, when: 'never' },
  ],
  execute: { default_role: 'go-worker', reviewer: 'reviewer', corrector: 'corrector', max_waves: 4 },
  team: [{ role: 'go-worker', skills: ['atomic-coding'] }],
};

const AGENTS: AgentSpec[] = [
  { id: 'go-worker', title: 'Go Worker' } as AgentSpec,
];

// The default fixture is a FINISHED runtime composition, which is the one
// state the disclosure opens in — so content assertions see content. Tests
// about the preview pass `mode: 'preview'` explicitly.
function setup(props: Partial<React.ComponentProps<typeof RunSetup>> = {}) {
  return render(
    <RunSetup
      composition={COMPOSITION}
      mode="runtime"
      fit={[]}
      agents={AGENTS}
      running={false}
      compositionError=""
      {...props}
    />,
  );
}

describe('RunSetup', () => {
  // The disclosure state is persisted, and jsdom shares localStorage across
  // tests in a file — so without this the tests that CLICK the toggle leak an
  // expanded panel into every test after them, and the suite passes or fails on
  // declaration order.
  beforeEach(() => localStorage.clear());

  it('renders nothing without a composition or an error', () => {
    const { container } = setup({ composition: null });
    expect(container).toBeEmptyDOMElement();
  });

  // A preview and a real composition are not the same thing, so the disclosure
  // has three states rather than two.
  it('is collapsed for a preview, which is only a guess', () => {
    setup({ mode: 'preview', running: false });
    expect(screen.getByRole('button', { expanded: false })).toBeInTheDocument();
    expect(screen.queryByText('Handoff contract')).not.toBeInTheDocument();
  });

  it('is closed while running', () => {
    setup({ mode: 'runtime', running: true });
    expect(screen.getByRole('button', { expanded: false })).toBeInTheDocument();
    expect(screen.queryByText('Handoff contract')).not.toBeInTheDocument();
  });

  it('is open for the finished run, which is what actually happened', () => {
    setup({ mode: 'runtime', running: false });
    expect(screen.getByRole('button', { expanded: true })).toBeInTheDocument();
    expect(screen.getByText('Handoff contract')).toBeInTheDocument();
  });

  // The reported confusion: a deterministic guess assembled from the query text
  // presented itself as a decision, so a plainly wrong team read as the plan.
  it('says a preview is a guess the composer will replace', async () => {
    const user = userEvent.setup();
    setup({ mode: 'preview' });
    expect(screen.getByText('guess')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { expanded: false }));
    expect(screen.getByText(/composer agent picks the real phases/)).toBeInTheDocument();
  });

  it('does not call a real composition a guess', async () => {
    const user = userEvent.setup();
    setup({ mode: 'runtime', running: true });
    expect(screen.queryByText('guess')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { expanded: false }));
    expect(screen.queryByText(/composer agent picks the real phases/)).not.toBeInTheDocument();
  });

  it('keeps the summary line visible either way', () => {
    setup({ mode: 'runtime', running: true });
    expect(screen.getByText(COMPOSITION.summary)).toBeInTheDocument();
    expect(screen.getByText('2 phases')).toBeInTheDocument();
  });

  it('can be toggled by the user', async () => {
    const user = userEvent.setup();
    setup({ mode: 'runtime', running: true });
    await user.click(screen.getByRole('button', { expanded: false }));
    expect(screen.getByText('Handoff contract')).toBeInTheDocument();
  });

  it('shows the budget class', () => {
    setup();
    expect(screen.getByText('critical:task')).toBeInTheDocument();
  });

  it('omits the budget class when the backend did not send one', () => {
    setup({ composition: { ...COMPOSITION, complexity: undefined, kind: undefined } });
    expect(screen.queryByText(/critical/)).not.toBeInTheDocument();
  });

  it('names the pipeline by mode', () => {
    setup({ mode: 'preview' });
    expect(screen.getByText('Provisional pipeline')).toBeInTheDocument();
    setup({ mode: 'runtime' });
    expect(screen.getByText('Active pipeline')).toBeInTheDocument();
  });

  // A phase the composer switched off is not part of this run and must not be
  // counted or drawn — the rail and this panel have to agree on the shape.
  it('counts and lists only enabled phases', () => {
    setup();
    expect(screen.getByText('2 phases')).toBeInTheDocument();
    expect(screen.getByText('context')).toBeInTheDocument();
    expect(screen.getByText('execute')).toBeInTheDocument();
    expect(screen.queryByText('polish')).not.toBeInTheDocument();
  });

  it('shows the loop roles and the wave budget', () => {
    setup();
    expect(screen.getByText('worker: go-worker')).toBeInTheDocument();
    expect(screen.getByText('reviewer: reviewer')).toBeInTheDocument();
    expect(screen.getByText('waves: 4')).toBeInTheDocument();
  });

  it('resolves team members to their agent titles', () => {
    setup();
    expect(screen.getByText('Go Worker')).toBeInTheDocument();
  });

  it('falls back to the raw role when the agent is unknown', () => {
    setup({ agents: [] });
    expect(screen.getByText('go-worker')).toBeInTheDocument();
  });

  it('surfaces small-model fit hints', () => {
    setup({ fit: ['12 enabled phases: consider a narrower request for 7B-14B local models'] });
    expect(screen.getByText('Small-model fit')).toBeInTheDocument();
    expect(screen.getByText(/12 enabled phases/)).toBeInTheDocument();
  });

  // Outside the disclosure, so a collapsed panel cannot hide the one state the
  // user has to act on.
  it('reports an unreadable saved composition even when collapsed', () => {
    setup({ mode: 'preview', composition: null, compositionError: 'unexpected end of JSON input' });
    expect(screen.getByRole('button', { expanded: false })).toBeInTheDocument();
    expect(screen.getByText(/unexpected end of JSON input/)).toBeInTheDocument();
  });

  it('survives a composition with no phases, team or handoff', () => {
    setup({ composition: { summary: 'bare' } });
    expect(screen.getByText('0 phases')).toBeInTheDocument();
    // The loop section still renders with its defaults rather than vanishing:
    // "which worker is implementing this" is never a question with no answer.
    expect(screen.getByText('worker: worker')).toBeInTheDocument();
  });
});
