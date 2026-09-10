import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import TeamsView from './TeamsView';
import { ApiError } from '@/api/client';
import BoardStoreRoot from '@/components/shared/BoardStoreRoot';
import type { SquadsView, TeamActivity, TeamPreselect, TeamSpec, TeamsLibrary } from '@/types';

const getSquads = vi.fn<() => Promise<SquadsView>>();
const getTeams = vi.fn<() => Promise<TeamsLibrary>>();
const patchSquads = vi.fn();
const createTeam = vi.fn();
const updateTeam = vi.fn();
const deleteTeam = vi.fn();
const preselectTeams = vi.fn<(query: string, pinned?: string[]) => Promise<TeamPreselect>>();
const activateTeams = vi.fn();
const previewComposition = vi.fn();
const startRun = vi.fn();
const getTeamActivity = vi.fn<() => Promise<TeamActivity>>();
const createTeamManager = vi.fn();
const navigate = vi.fn();
const reportError = vi.fn();
const success = vi.fn();
const confirm = vi.fn<() => Promise<boolean>>();

vi.mock('@/api/client', async () => {
  const actual = await vi.importActual<typeof import('@/api/client')>('@/api/client');
  return {
    ...actual,
    getSquads: (...a: []) => getSquads(...a),
    getTeams: (...a: unknown[]) => getTeams(...(a as [])),
    patchSquads: (...a: unknown[]) => patchSquads(...a),
    createTeam: (...a: unknown[]) => createTeam(...a),
    updateTeam: (...a: unknown[]) => updateTeam(...a),
    deleteTeam: (...a: unknown[]) => deleteTeam(...a),
    preselectTeams: (...a: unknown[]) => preselectTeams(...(a as [string, string[]?])),
    activateTeams: (...a: unknown[]) => activateTeams(...a),
    previewComposition: (...a: unknown[]) => previewComposition(...a),
    startRun: (...a: unknown[]) => startRun(...a),
    getTeamActivity: (...a: unknown[]) => getTeamActivity(...(a as [])),
    createTeamManager: (...a: unknown[]) => createTeamManager(...a),
    getSkills: async () => [],
    // The org chart rides the board store, which reads the board alongside it.
    getTasks: async () => ({ plan: null, tasks: [], columns: [], by_column: {} }),
  };
});

vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...actual, useNavigate: () => navigate };
});

vi.mock('@/components/ui/Toast', () => ({
  useToast: () => ({ reportError, success, info: vi.fn(), push: vi.fn(), dismiss: vi.fn(), toasts: [] }),
}));

vi.mock('@/components/ui/Modal', async () => {
  const actual = await vi.importActual<typeof import('@/components/ui/Modal')>('@/components/ui/Modal');
  return { ...actual, useConfirm: () => confirm };
});

const backendTeam: TeamSpec = {
  id: 'backend-go',
  name: 'Backend · Go',
  charter: 'own the Go service',
  owns: ['cmd/**', 'internal/**'],
  acceptance: 'go test ./...',
  worker: 'go-worker',
  source: 'builtin',
  builtin: true,
  match: { keywords: ['backend', 'api'], files: ['go.mod'], extensions: ['.go'] },
};

const frontendTeam: TeamSpec = {
  id: 'frontend-react',
  name: 'Frontend · React',
  owns: ['web/**'],
  acceptance: 'npm --prefix web run build',
  worker: 'react-worker',
  agents: ['react-reviewer', 'worker'],
  skills: ['react-components'],
  source: 'project',
  builtin: false,
  match: { keywords: ['frontend', 'ui'] },
};

const library: TeamsLibrary = {
  ok: true,
  teams: [backendTeam, frontendTeam],
  agents: ['worker', 'go-worker', 'react-worker'],
  managers: ['backend-triage', 'triage'],
  library_enabled: true,
  squads_enabled: true,
  pinned: [],
  pipeline_teams: [],
};

const chart: SquadsView = {
  ok: true,
  summary: 'Go API + React SPA',
  squads: [
    {
      id: 'backend',
      name: 'Backend',
      owns: ['cmd/**', 'internal/**'],
      acceptance: 'go test ./...',
      total: 4,
      done: 2,
      blocked: 0,
      in_flight: 1,
      complete: false,
      stuck: false,
    },
    {
      id: 'frontend',
      name: 'Frontend',
      owns: ['web/**'],
      acceptance: 'npm run build',
      total: 2,
      done: 2,
      blocked: 0,
      in_flight: 0,
      complete: true,
      stuck: false,
    },
  ],
  interfaces: [{ id: 'GET /api/todos', provider: 'backend', consumers: ['frontend'], spec: '200 -> [{id,title}]' }],
  managers: ['backend-triage', 'triage'],
};

beforeEach(() => {
  vi.clearAllMocks();
  getTeams.mockResolvedValue(structuredClone(library));
  getSquads.mockResolvedValue(structuredClone(chart));
  patchSquads.mockResolvedValue({ ok: true, summary: 'teams updated' });
  activateTeams.mockResolvedValue({ ok: true, summary: 'backend-go + frontend-react' });
  previewComposition.mockResolvedValue({ ok: true, dynamic_enabled: true, composition: null });
  startRun.mockResolvedValue({ status: 'started' });
  getTeamActivity.mockResolvedValue({ ok: true, entries: [], teams: [], managers: [] });
  createTeamManager.mockResolvedValue({
    ok: true, manager: 'frontend-react-triage', created: true,
    team: { ...frontendTeam, manager: 'frontend-react-triage' },
  });
  confirm.mockResolvedValue(true);
});

async function renderPage() {
  // The page reads the org chart from the shared board store; a test mounts
  // one of its own the way App does.
  render(
    <MemoryRouter>
      <BoardStoreRoot>
        <TeamsView />
      </BoardStoreRoot>
    </MemoryRouter>,
  );
  expect(await screen.findByRole('heading', { name: 'Teams' })).toBeInTheDocument();
  await screen.findByRole('heading', { name: /Team library/ });
}

describe('TeamsView library', () => {
  // The complaint the whole page rewrite answers: it was empty unless a run
  // happened to have assembled teams.
  it('shows the library with nothing running', async () => {
    getSquads.mockResolvedValue({ ok: false });
    await renderPage();

    expect(screen.getByRole('heading', { name: 'Backend · Go' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Frontend · React' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'No org chart yet' })).toBeInTheDocument();
  });

  it('shows a builtin as undeletable and a project team as deletable', async () => {
    await renderPage();
    const builtin = screen.getByRole('heading', { name: 'Backend · Go' }).closest('article')!;
    const project = screen.getByRole('heading', { name: 'Frontend · React' }).closest('article')!;

    expect(within(builtin).getByText('builtin')).toBeInTheDocument();
    expect(within(builtin).getByRole('button', { name: /Delete/ })).toBeDisabled();
    expect(within(project).getByRole('button', { name: /Delete/ })).toBeEnabled();
  });

  // A card showing only the four seats would say a team IS four people, which
  // is exactly the model the open roster exists to replace.
  it('shows the whole team on the card, not just the seats', async () => {
    await renderPage();
    const card = screen.getByRole('heading', { name: 'Frontend · React' }).closest('article')!;
    expect(within(card).getByText('also on it')).toBeInTheDocument();
    expect(within(card).getByText('react-reviewer')).toBeInTheDocument();
    expect(within(card).getByText('react-components')).toBeInTheDocument();
  });

  // A team is "these people", and how many there are is its author's business.
  it('puts as many agents and skills on a team as the user wants', async () => {
    const user = userEvent.setup();
    createTeam.mockResolvedValue({ ...frontendTeam, id: 'platform', name: 'Platform' });
    await renderPage();

    await user.click(screen.getByRole('button', { name: /New team/ }));
    await user.type(screen.getByLabelText('Id'), 'platform');
    await user.type(screen.getByLabelText('Owns'), 'platform/**');

    await user.click(screen.getByRole('button', { name: 'Add agent to Also on this team' }));
    for (const id of ['go-worker', 'react-worker']) {
      await user.click(screen.getByRole('button', { name: new RegExp(`^${id}$`) }));
    }
    await user.click(screen.getByRole('button', { name: 'Create team' }));

    await waitFor(() => expect(createTeam).toHaveBeenCalledTimes(1));
    expect(createTeam.mock.calls[0][0]).toMatchObject({
      id: 'platform',
      agents: ['go-worker', 'react-worker'],
    });
  });

  it('creates a team from the editor', async () => {
    const user = userEvent.setup();
    createTeam.mockResolvedValue({ ...frontendTeam, id: 'payments', name: 'Payments' });
    await renderPage();

    await user.click(screen.getByRole('button', { name: /New team/ }));
    await user.type(screen.getByLabelText('Id'), 'payments');
    await user.type(screen.getByLabelText('Name'), 'Payments');
    await user.type(screen.getByLabelText('Owns'), 'billing/**');
    await user.click(screen.getByRole('button', { name: 'Create team' }));

    await waitFor(() => expect(createTeam).toHaveBeenCalledTimes(1));
    expect(createTeam.mock.calls[0][0]).toMatchObject({ id: 'payments', name: 'Payments', owns: ['billing/**'] });
  });

  // A team owning nothing can never be routed a task; it would sit idle for a
  // whole run. The form has to say so rather than let it be saved.
  it('refuses to save a team that owns nothing', async () => {
    const user = userEvent.setup();
    await renderPage();

    await user.click(screen.getByRole('button', { name: /New team/ }));
    await user.type(screen.getByLabelText('Id'), 'ghost');

    expect(screen.getByRole('alert')).toHaveTextContent('can never be routed a task');
    expect(screen.getByRole('button', { name: 'Create team' })).toBeDisabled();
    expect(createTeam).not.toHaveBeenCalled();
  });

  it('refuses an id that already exists rather than overwriting it', async () => {
    const user = userEvent.setup();
    await renderPage();

    await user.click(screen.getByRole('button', { name: /New team/ }));
    await user.type(screen.getByLabelText('Id'), 'backend-go');
    await user.type(screen.getByLabelText('Owns'), 'x/**');

    expect(screen.getByRole('alert')).toHaveTextContent('already exists');
    expect(screen.getByRole('button', { name: 'Create team' })).toBeDisabled();
  });

  // Editing a builtin is a PUT that writes a project override — a POST would
  // collide with the id the builtin already holds.
  it('edits a builtin into a project override', async () => {
    const user = userEvent.setup();
    updateTeam.mockResolvedValue({ ...backendTeam, source: 'project', builtin: false });
    await renderPage();

    const card = screen.getByRole('heading', { name: 'Backend · Go' }).closest('article')!;
    await user.click(within(card).getByRole('button', { name: /Edit/ }));
    const acceptance = screen.getByLabelText('Acceptance');
    await user.clear(acceptance);
    await user.type(acceptance, 'make test');
    await user.click(screen.getByRole('button', { name: 'Save team' }));

    await waitFor(() => expect(updateTeam).toHaveBeenCalledTimes(1));
    expect(updateTeam.mock.calls[0][0]).toBe('backend-go');
    expect(updateTeam.mock.calls[0][1]).toMatchObject({ id: 'backend-go', acceptance: 'make test' });
    expect(createTeam).not.toHaveBeenCalled();
  });

  // A copy must differ in the two things that have to be unique: its id and its
  // territory. Copying the globs would make the two teams unselectable together.
  it('duplicates a team with a free id and no ownership', async () => {
    const user = userEvent.setup();
    await renderPage();

    const card = screen.getByRole('heading', { name: 'Backend · Go' }).closest('article')!;
    await user.click(within(card).getByRole('button', { name: /Duplicate/ }));

    expect(screen.getByLabelText('Id')).toHaveValue('backend-go-copy');
    expect(screen.getByLabelText('Owns')).toHaveValue('');
    expect(screen.getByRole('button', { name: 'Save team' })).toBeDisabled();
  });

  it('deletes a project team after confirming', async () => {
    const user = userEvent.setup();
    deleteTeam.mockResolvedValue({ ok: true });
    await renderPage();

    const card = screen.getByRole('heading', { name: 'Frontend · React' }).closest('article')!;
    await user.click(within(card).getByRole('button', { name: /Delete/ }));

    await waitFor(() => expect(deleteTeam).toHaveBeenCalledWith('frontend-react'));
    expect(confirm).toHaveBeenCalled();
  });

  it('does not delete when the confirm is declined', async () => {
    const user = userEvent.setup();
    confirm.mockResolvedValue(false);
    await renderPage();

    const card = screen.getByRole('heading', { name: 'Frontend · React' }).closest('article')!;
    await user.click(within(card).getByRole('button', { name: /Delete/ }));

    await waitFor(() => expect(confirm).toHaveBeenCalled());
    expect(deleteTeam).not.toHaveBeenCalled();
  });
});

describe('TeamsView preselection', () => {
  // "Which teams would this request get, and why" — answerable before anything
  // is started, from the same code the run uses.
  it('previews the teams a request would get, with the evidence', async () => {
    const user = userEvent.setup();
    preselectTeams.mockResolvedValue({
      query: 'a Go API and a React page',
      selected: ['backend-go', 'frontend-react'],
      enabled: true,
      evidence: [
        { team_id: 'backend-go', score: 9, selected: true, reasons: ['workspace has "go.mod"'] },
        { team_id: 'frontend-react', score: 6, selected: true, reasons: ['query mentions "react"'] },
      ],
    });
    await renderPage();

    await user.type(
      screen.getByLabelText('Request to preselect teams for'),
      'a Go API and a React page',
    );
    await user.click(screen.getByRole('button', { name: 'Preselect' }));

    expect(await screen.findByText(/2 teams would run in parallel/)).toBeInTheDocument();
    // The reason shows in two places on purpose — once in the summary, once on
    // the card it explains — so assert it is present rather than unique.
    expect(screen.getAllByText(/workspace has "go.mod"/).length).toBeGreaterThan(0);
  });

  // One team means a single stream. Saying so — and why — is the difference
  // between a feature that looks broken and one that explained itself.
  it('says plainly when a request would run as a single stream', async () => {
    const user = userEvent.setup();
    preselectTeams.mockResolvedValue({
      selected: ['backend-go'],
      enabled: false,
      evidence: [{ team_id: 'backend-go', score: 5, selected: true, reasons: ['workspace has "go.mod"'] }],
    });
    await renderPage();

    await user.type(screen.getByLabelText('Request to preselect teams for'), 'tidy the handlers');
    await user.click(screen.getByRole('button', { name: 'Preselect' }));

    expect(await screen.findByText(/would run as one stream/)).toBeInTheDocument();
  });

  it('shows which team took a contested path from another', async () => {
    const user = userEvent.setup();
    preselectTeams.mockResolvedValue({
      selected: ['backend-go'],
      enabled: false,
      evidence: [
        { team_id: 'backend-go', score: 9, selected: true, reasons: [] },
        { team_id: 'frontend-react', score: 3, selected: false, conflict: 'backend-go', reasons: [] },
      ],
    });
    await renderPage();

    await user.type(screen.getByLabelText('Request to preselect teams for'), 'anything');
    await user.click(screen.getByRole('button', { name: 'Preselect' }));

    await user.click(await screen.findByText(/1 team\(s\) considered and not selected/));
    expect(screen.getByText(/territory already claimed by/)).toBeInTheDocument();
  });

  // An explicit pin is an instruction, not a hypothesis — and it invalidates a
  // preview computed against a different pin set.
  it('pins a team and drops the now-stale preview', async () => {
    const user = userEvent.setup();
    preselectTeams.mockResolvedValue({ selected: ['backend-go'], enabled: false, evidence: [] });
    await renderPage();

    await user.type(screen.getByLabelText('Request to preselect teams for'), 'x');
    await user.click(screen.getByRole('button', { name: 'Preselect' }));
    expect(await screen.findByText(/would run as one stream/)).toBeInTheDocument();

    const card = screen.getByRole('heading', { name: 'Frontend · React' }).closest('article')!;
    await user.click(within(card).getByRole('button', { name: 'Pin' }));

    expect(screen.queryByText(/would run as one stream/)).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Preselect' }));
    await waitFor(() => expect(preselectTeams).toHaveBeenCalledTimes(2));
    expect(preselectTeams.mock.calls[1][1]).toEqual(['frontend-react']);
  });

  it('activates the pinned teams into an org chart', async () => {
    const user = userEvent.setup();
    await renderPage();

    for (const name of ['Backend · Go', 'Frontend · React']) {
      const card = screen.getByRole('heading', { name }).closest('article')!;
      await user.click(within(card).getByRole('button', { name: 'Pin' }));
    }
    await user.click(screen.getByRole('button', { name: /Activate/ }));

    await waitFor(() => expect(activateTeams).toHaveBeenCalledTimes(1));
    expect(activateTeams.mock.calls[0][0]).toEqual(['backend-go', 'frontend-react']);
  });

  // Two teams minimum: one is the single-stream pipeline wearing a hat, and
  // paying the contract overhead for it buys nothing.
  it('will not activate a single team', async () => {
    const user = userEvent.setup();
    await renderPage();

    const card = screen.getByRole('heading', { name: 'Backend · Go' }).closest('article')!;
    await user.click(within(card).getByRole('button', { name: 'Pin' }));

    expect(screen.getByRole('button', { name: /Activate/ })).toBeDisabled();
  });
});

describe('TeamsView send a request', () => {
  // The manager is resolved the way the run resolves it, so a team that names
  // nobody shows the run default rather than an empty seat.
  it('shows who staffs and manages each selected team', async () => {
    const user = userEvent.setup();
    preselectTeams.mockResolvedValue({
      selected: ['backend-go', 'frontend-react'],
      enabled: true,
      mode: 'parallel',
      evidence: [
        { team_id: 'backend-go', score: 9, selected: true, reasons: ['workspace has "go.mod"'] },
        { team_id: 'frontend-react', score: 6, selected: true, pinned: true, reasons: ['selected by hand'] },
      ],
      teams: [
        {
          id: 'backend-go', worker: 'go-worker', manager: 'triage', manager_default: true,
          seats: [
            { role: 'worker', agent: 'go-worker', source: 'team' },
            { role: 'reviewer', agent: 'reviewer', source: 'pipeline' },
            { role: 'tester', agent: 'go-tester', source: 'pipeline' },
            { role: 'manager', agent: 'triage', source: 'default' },
          ],
          gaps: ['team backend-go names no tester — the pipeline\'s go-tester takes its tester seat'],
        },
        { id: 'frontend-react', worker: 'react-worker', manager: 'fe-triage', manager_default: false, skills: ['react-components'] },
      ],
    });
    previewComposition.mockResolvedValue({
      ok: true,
      dynamic_enabled: true,
      composition: {
        summary: 'two halves',
        team_mode: 'parallel',
        team_note: '2 teams build in parallel behind a frozen contract: backend-go, frontend-react',
        phases: [
          { id: 'plan', agent: 'planner', enabled: true },
          { id: 'execute', agent: 'go-worker', enabled: true },
        ],
        execute: { default_role: 'go-worker', reviewer: 'reviewer', corrector: 'corrector' },
      },
    });
    await renderPage();

    await user.type(screen.getByLabelText('Request to preselect teams for'), 'a Go API and a React page');
    await user.click(screen.getByRole('button', { name: 'Preselect' }));

    const backend = await screen.findByTestId('staffing-backend-go');
    expect(within(backend).getByText('triage')).toBeInTheDocument();
    expect(within(backend).getByText('(run default)')).toBeInTheDocument();
    // The seat the team left empty is named as the pipeline will fill it.
    expect(within(backend).getAllByText(/go-tester/).length).toBeGreaterThan(0);
    expect(within(backend).getByText(/names no tester/)).toBeInTheDocument();
    const frontend = screen.getByTestId('staffing-frontend-react');
    expect(within(frontend).getByText('fe-triage')).toBeInTheDocument();
    expect(within(frontend).queryByText('(run default)')).not.toBeInTheDocument();
    expect(within(frontend).getByText('pinned')).toBeInTheDocument();

    // The pipeline the composer would assemble rides along, with the team note.
    const preview = screen.getByTestId('composition-preview');
    expect(within(preview).getByText(/2 teams build in parallel/)).toBeInTheDocument();
    expect(within(preview).getByText('execute')).toBeInTheDocument();
  });

  // "Send this request to these teams": the run starts with exactly the
  // selected teams pinned, and the page hands over to the Live view.
  it('runs the request with the selected teams pinned', async () => {
    const user = userEvent.setup();
    preselectTeams.mockResolvedValue({
      selected: ['backend-go', 'frontend-react'],
      enabled: true,
      evidence: [],
    });
    await renderPage();

    await user.type(screen.getByLabelText('Request to preselect teams for'), 'a Go API and a React page');
    await user.click(screen.getByRole('button', { name: 'Preselect' }));
    await screen.findByText(/2 teams would run in parallel/);
    await user.click(screen.getByRole('button', { name: /Run with 2 teams/ }));

    await waitFor(() => expect(startRun).toHaveBeenCalledTimes(1));
    expect(startRun.mock.calls[0][0]).toMatchObject({
      query: 'a Go API and a React page',
      teams: ['backend-go', 'frontend-react'],
    });
    expect(navigate).toHaveBeenCalledWith('/');
  });

  // One pinned team is still a request to that team — its people staff the
  // run — so Run is offered even though Activate is not.
  it('sends a request to a single pinned team', async () => {
    const user = userEvent.setup();
    await renderPage();
    const card = screen.getByRole('heading', { name: 'Backend · Go' }).closest('article')!;
    await user.click(within(card).getByRole('button', { name: 'Pin' }));
    await user.type(screen.getByLabelText('Request to preselect teams for'), 'tidy the handlers');

    expect(screen.getByRole('button', { name: /Activate/ })).toBeDisabled();
    await user.click(screen.getByRole('button', { name: /Run with 1 team/ }));
    await waitFor(() => expect(startRun).toHaveBeenCalledTimes(1));
    expect(startRun.mock.calls[0][0]).toMatchObject({ teams: ['backend-go'] });
  });

  it('cannot start a run while one is in flight', async () => {
    getTeams.mockResolvedValue({ ...structuredClone(library), running: true });
    const user = userEvent.setup();
    await renderPage();
    await user.type(screen.getByLabelText('Request to preselect teams for'), 'anything');
    expect(screen.getByRole('button', { name: /^Run/ })).toBeDisabled();
    expect(screen.getByRole('button', { name: /New team/ })).toBeDisabled();
    expect(screen.getByText(/A run is in flight/)).toBeInTheDocument();
  });
});

describe('TeamsView managers', () => {
  it('shows the run default on a team that names no manager, and offers a dedicated one', async () => {
    const user = userEvent.setup();
    await renderPage();
    const card = screen.getByRole('heading', { name: 'Frontend · React' }).closest('article')!;
    expect(within(card).getByText('triage')).toBeInTheDocument();
    expect(within(card).getByText('run default')).toBeInTheDocument();

    await user.click(within(card).getByRole('button', { name: /Give it a manager/ }));
    await waitFor(() => expect(createTeamManager).toHaveBeenCalledWith('frontend-react'));
    expect(success).toHaveBeenCalled();
  });

  it('does not offer a second manager to a team that has one', async () => {
    getTeams.mockResolvedValue({
      ...structuredClone(library),
      teams: [{ ...frontendTeam, manager: 'fe-triage', effective_manager: 'fe-triage', manager_default: false }],
    });
    await renderPage();
    const card = screen.getByRole('heading', { name: 'Frontend · React' }).closest('article')!;
    expect(within(card).getByText('fe-triage')).toBeInTheDocument();
    expect(within(card).queryByText('run default')).not.toBeInTheDocument();
    expect(within(card).queryByRole('button', { name: /Give it a manager/ })).not.toBeInTheDocument();
  });

  // A manager that cannot answer the triage contract is not a manager; the
  // card says who actually decides, and why.
  it('says when a named manager cannot triage', async () => {
    getTeams.mockResolvedValue({
      ...structuredClone(library),
      teams: [{ ...frontendTeam, manager: 'react-worker', effective_manager: 'triage', manager_default: true }],
    });
    await renderPage();
    const card = screen.getByRole('heading', { name: 'Frontend · React' }).closest('article')!;
    expect(within(card).getByText('(react-worker cannot triage)')).toBeInTheDocument();
  });

  it('creates a manager from the editor and adopts it into the draft', async () => {
    const user = userEvent.setup();
    await renderPage();
    const card = screen.getByRole('heading', { name: 'Frontend · React' }).closest('article')!;
    await user.click(within(card).getByRole('button', { name: /Edit/ }));
    await user.click(screen.getByRole('button', { name: 'Create frontend-react-triage' }));
    await waitFor(() => expect(createTeamManager).toHaveBeenCalledWith('frontend-react'));
    await waitFor(() =>
      expect(screen.getByLabelText('Project manager')).toHaveValue('frontend-react-triage'),
    );
  });
});

describe('TeamsView activity', () => {
  it('shows what the managers decided, filterable by team', async () => {
    const user = userEvent.setup();
    getTeamActivity.mockResolvedValue({
      ok: true,
      teams: ['backend', 'frontend'],
      counts: { triage: 1, reassign: 1, stall: 1, gate: 1 },
      managers: [
        { team: 'backend', manager: 'backend-triage', decisions: 1, moved: 1, stalls: 0, gate: 'green' },
        { team: 'frontend', manager: 'triage', default: true, decisions: 0, moved: 0, stalls: 1, gate: 'red' },
      ],
      entries: [
        { time: '2026-09-09T10:00:00Z', kind: 'triage', team: 'backend', agent: 'backend-triage', task_id: 'T3', message: 'backend-triage proposes go-corrector — compile error' },
        { time: '2026-09-09T10:00:01Z', kind: 'reassign', team: 'backend', agent: 'go-corrector', task_id: 'T3', message: 'T3 reassigned from go-worker to go-corrector — compile error' },
        { time: '2026-09-09T10:00:02Z', kind: 'stall', team: 'frontend', level: 'warning', message: 'frontend is waiting on backend to deliver "GET /api/todos"' },
        { time: '2026-09-09T10:00:03Z', kind: 'gate', team: 'backend', message: 'team backend is green: go test ./...' },
      ],
    });
    await renderPage();

    expect(await screen.findByText(/4 events · 2 manager decisions/)).toBeInTheDocument();
    const timeline = screen.getByRole('list', { name: 'Team activity timeline' });
    expect(within(timeline).getAllByRole('listitem')).toHaveLength(4);
    expect(screen.getByText('backend-triage', { selector: 'td' })).toBeInTheDocument();
    expect(screen.getByText('(run default)')).toBeInTheDocument();

    // Filter to the frontend's lane: only its stall remains.
    // The team appears twice as a button — the manager table row and the
    // filter chip — and either one filters the lane.
    await user.click(screen.getAllByRole('button', { name: 'frontend', pressed: false })[0]);
    expect(within(timeline).getAllByRole('listitem')).toHaveLength(1);
    expect(within(timeline).getByText(/is waiting on backend/)).toBeInTheDocument();
  });

  it('explains an empty timeline', async () => {
    await renderPage();
    expect(await screen.findByText(/No team activity on record/)).toBeInTheDocument();
  });
});

describe('TeamsView org chart', () => {
  it('shows each team, its progress and the frozen contract', async () => {
    await renderPage();
    expect(screen.getByLabelText('Name of team backend')).toHaveValue('Backend');
    expect(screen.getByLabelText('Name of team frontend')).toHaveValue('Frontend');
    expect(screen.getByText('2/4')).toBeInTheDocument();
    expect(screen.getByDisplayValue('GET /api/todos')).toBeInTheDocument();
    expect(screen.getByDisplayValue('200 -> [{id,title}]')).toBeInTheDocument();
  });

  // Ownership globs are per-team, so labels must name the team. Two textareas
  // both labeled "Owns" is a page a screen reader cannot navigate.
  it('labels every field with the team it belongs to', async () => {
    await renderPage();
    expect(screen.getByLabelText(/Owns — backend/)).toHaveValue('cmd/**\ninternal/**');
    expect(screen.getByLabelText(/Owns — frontend/)).toHaveValue('web/**');
    expect(screen.getByLabelText(/Acceptance — backend/)).toHaveValue('go test ./...');
  });

  it('sends only the team the user edited', async () => {
    const user = userEvent.setup();
    await renderPage();

    await user.clear(screen.getByLabelText(/Acceptance — backend/));
    await user.type(screen.getByLabelText(/Acceptance — backend/), 'go test -race ./...');
    await user.click(screen.getByRole('button', { name: /^Save/ }));

    await waitFor(() => expect(patchSquads).toHaveBeenCalledTimes(1));
    expect(patchSquads.mock.calls[0][0]).toEqual({
      squads: [{ id: 'backend', acceptance: 'go test -race ./...' }],
    });
    expect(success).toHaveBeenCalled();
  });

  it('cannot save when nothing changed', async () => {
    await renderPage();
    expect(screen.getByRole('button', { name: /^Save/ })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Revert' })).toBeDisabled();
  });

  it('reverts the draft back to what the server reported', async () => {
    const user = userEvent.setup();
    await renderPage();

    await user.type(screen.getByLabelText('Name of team backend'), ' API');
    expect(screen.getByRole('button', { name: /^Save/ })).toBeEnabled();

    await user.click(screen.getByRole('button', { name: 'Revert' }));
    expect(screen.getByLabelText('Name of team backend')).toHaveValue('Backend');
    expect(screen.getByRole('button', { name: /^Save/ })).toBeDisabled();
  });

  // The one rule a user cannot be allowed to break: two teams owning one path
  // means two agents writing one file in parallel. "Refused" tells them
  // nothing; naming the collision tells them exactly what to change.
  it('shows why an overlapping org chart was refused, and keeps the draft', async () => {
    const user = userEvent.setup();
    patchSquads.mockRejectedValue(
      new ApiError(422, JSON.stringify({ problems: ['backend and frontend both own web/**'] }), 'Unprocessable'),
    );
    await renderPage();

    const owns = screen.getByLabelText(/Owns — backend/);
    await user.clear(owns);
    await user.type(owns, 'web/**');
    await user.click(screen.getByRole('button', { name: /^Save/ }));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('These teams cannot run — nothing was saved');
    expect(alert).toHaveTextContent('backend and frontend both own web/**');
    // The edit stays on screen: the user has to fix it, and a wiped form makes
    // them retype the thing they were told to change.
    expect(owns).toHaveValue('web/**');
    expect(reportError).not.toHaveBeenCalled();
  });

  it('reports a non-validation failure as a toast', async () => {
    const user = userEvent.setup();
    patchSquads.mockRejectedValue(new ApiError(409, 'cannot edit teams while a run is active', 'Conflict'));
    await renderPage();

    await user.type(screen.getByLabelText('Name of team backend'), ' API');
    await user.click(screen.getByRole('button', { name: /^Save/ }));

    await waitFor(() => expect(reportError).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  // Attaching a manager to a team is what makes a rejected delivery somebody's
  // decision rather than a re-run by the agent that just failed at it.
  it('attaches a project manager to a team', async () => {
    const user = userEvent.setup();
    await renderPage();

    const picker = screen.getByLabelText(/Project manager — backend/);
    expect(picker).toHaveValue('');
    await user.selectOptions(picker, 'backend-triage');
    await user.click(screen.getByRole('button', { name: /^Save/ }));

    await waitFor(() => expect(patchSquads).toHaveBeenCalledTimes(1));
    expect(patchSquads.mock.calls[0][0]).toEqual({ squads: [{ id: 'backend', manager: 'backend-triage' }] });
  });

  // A saved plan can name a manager the factory no longer registers. Dropping
  // it from the list would silently rewrite the user's choice on the next save.
  it('keeps a manager the harness no longer offers', async () => {
    getSquads.mockResolvedValue({
      ...structuredClone(chart),
      squads: chart.squads!.map((s) => (s.id === 'backend' ? { ...s, manager: 'retired-pm' } : s)),
    });
    await renderPage();

    expect(screen.getByLabelText(/Project manager — backend/)).toHaveValue('retired-pm');
    expect(screen.getByRole('option', { name: /retired-pm \(not registered\)/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^Save/ })).toBeDisabled();
  });

  // Removing a team used to be impossible from this page; the chart could only
  // grow. A clause its provider no longer exists for goes with it, because a
  // clause owed by nobody fails validation for the whole plan.
  it('removes a team and the contract clauses it provided', async () => {
    const user = userEvent.setup();
    await renderPage();

    await user.click(screen.getByRole('button', { name: 'Remove team backend' }));
    await user.click(screen.getByRole('button', { name: /^Save/ }));

    await waitFor(() => expect(patchSquads).toHaveBeenCalledTimes(1));
    const sent = patchSquads.mock.calls[0][0];
    expect(sent.remove_squads).toEqual(['backend']);
    expect(sent.remove_interfaces).toEqual(['GET /api/todos']);
  });

  it('adds a team from the library to the org chart', async () => {
    const user = userEvent.setup();
    await renderPage();

    await user.selectOptions(screen.getByLabelText('Add a team from the library'), 'frontend-react');
    await user.click(screen.getByRole('button', { name: /^Save/ }));

    await waitFor(() => expect(patchSquads).toHaveBeenCalledTimes(1));
    const added = patchSquads.mock.calls[0][0].squads.find((s: { id: string }) => s.id === 'frontend-react');
    expect(added).toMatchObject({ id: 'frontend-react', new: true, owns: ['web/**'], owns_set: true });
  });

  // The contract is the one artifact a two-team run cannot recover from getting
  // wrong. Renaming keeps the spec the user did not want to retype.
  it('renames a contract clause', async () => {
    const user = userEvent.setup();
    await renderPage();

    const name = screen.getByDisplayValue('GET /api/todos');
    await user.clear(name);
    await user.type(name, 'GET /api/v2/todos');
    await user.click(screen.getByRole('button', { name: /^Save/ }));

    await waitFor(() => expect(patchSquads).toHaveBeenCalledTimes(1));
    expect(patchSquads.mock.calls[0][0].interfaces).toEqual([
      { id: 'GET /api/todos', rename: 'GET /api/v2/todos' },
    ]);
  });

  it('adds a contract clause', async () => {
    const user = userEvent.setup();
    await renderPage();

    await user.click(screen.getByRole('button', { name: /Add interface/ }));
    const blank = screen.getByLabelText('Interface name');
    await user.type(blank, 'POST /api/todos');
    await user.click(screen.getByRole('button', { name: /^Save/ }));

    await waitFor(() => expect(patchSquads).toHaveBeenCalledTimes(1));
    expect(patchSquads.mock.calls[0][0].interfaces).toEqual([
      {
        id: 'POST /api/todos',
        new: true,
        provider: 'backend',
        consumers: [],
        consumers_set: true,
        spec: '',
      },
    ]);
  });
});
