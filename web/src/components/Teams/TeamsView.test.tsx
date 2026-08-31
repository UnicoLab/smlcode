import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import TeamsView from './TeamsView';
import { ApiError } from '@/api/client';
import type { SquadsView, TeamPreselect, TeamSpec, TeamsLibrary } from '@/types';

const getSquads = vi.fn<() => Promise<SquadsView>>();
const getTeams = vi.fn<() => Promise<TeamsLibrary>>();
const patchSquads = vi.fn();
const createTeam = vi.fn();
const updateTeam = vi.fn();
const deleteTeam = vi.fn();
const preselectTeams = vi.fn<(query: string, pinned?: string[]) => Promise<TeamPreselect>>();
const activateTeams = vi.fn();
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
  };
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
  confirm.mockResolvedValue(true);
});

async function renderPage() {
  render(<TeamsView />);
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
