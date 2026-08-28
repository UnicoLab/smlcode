import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import TeamsView from './TeamsView';
import { ApiError } from '@/api/client';
import type { SquadsView } from '@/types';

const getSquads = vi.fn<() => Promise<SquadsView>>();
const patchSquads = vi.fn();
const reportError = vi.fn();
const success = vi.fn();

vi.mock('@/api/client', async () => {
  const actual = await vi.importActual<typeof import('@/api/client')>('@/api/client');
  return {
    ...actual,
    getSquads: (...a: []) => getSquads(...a),
    patchSquads: (...a: unknown[]) => patchSquads(...a),
  };
});

vi.mock('@/components/ui/Toast', () => ({
  useToast: () => ({ reportError, success, info: vi.fn(), push: vi.fn(), dismiss: vi.fn(), toasts: [] }),
}));

const view: SquadsView = {
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
  getSquads.mockResolvedValue(structuredClone(view));
  patchSquads.mockResolvedValue({ ok: true, summary: 'teams updated' });
});

async function renderPage() {
  render(<TeamsView />);
  expect(await screen.findByRole('heading', { name: 'Teams' })).toBeInTheDocument();
}

describe('TeamsView', () => {
  it('shows each team, its progress and the frozen contract', async () => {
    await renderPage();
    expect(screen.getByLabelText('Name of team backend')).toHaveValue('Backend');
    expect(screen.getByLabelText('Name of team frontend')).toHaveValue('Frontend');
    expect(screen.getByText('2/4')).toBeInTheDocument();
    // The contract is what stops the halves inventing different seams, so it
    // has to be visible on the page that edits the boundaries around it.
    expect(screen.getByText('GET /api/todos')).toBeInTheDocument();
    expect(screen.getByText('200 -> [{id,title}]')).toBeInTheDocument();
  });

  // Ownership globs are per-team, so labels must name the team. Two textareas
  // both labelled "Owns" is a page a screen reader cannot navigate.
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
    expect(patchSquads.mock.calls[0][0]).toEqual([{ id: 'backend', acceptance: 'go test -race ./...' }]);
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

    expect(await screen.findByRole('alert')).toHaveTextContent('These teams cannot run — nothing was saved');
    expect(screen.getByRole('alert')).toHaveTextContent('backend and frontend both own web/**');
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
    expect(patchSquads.mock.calls[0][0]).toEqual([{ id: 'backend', manager: 'backend-triage' }]);
  });

  // Empty is a real choice, not an unset field: it hands the team back to the
  // run's default manager, and must travel rather than be dropped as falsy.
  it('hands a team back to the run default', async () => {
    const user = userEvent.setup();
    getSquads.mockResolvedValue({
      ...structuredClone(view),
      squads: view.squads!.map((s) => (s.id === 'backend' ? { ...s, manager: 'backend-triage' } : s)),
    });
    await renderPage();

    await user.selectOptions(screen.getByLabelText(/Project manager — backend/), '');
    await user.click(screen.getByRole('button', { name: /^Save/ }));

    await waitFor(() => expect(patchSquads).toHaveBeenCalledTimes(1));
    expect(patchSquads.mock.calls[0][0]).toEqual([{ id: 'backend', manager: '' }]);
  });

  // A saved plan can name a manager the factory no longer registers. Dropping
  // it from the list would silently rewrite the user's choice on the next save.
  it('keeps a manager the harness no longer offers', async () => {
    getSquads.mockResolvedValue({
      ...structuredClone(view),
      squads: view.squads!.map((s) => (s.id === 'backend' ? { ...s, manager: 'retired-pm' } : s)),
    });
    await renderPage();

    expect(screen.getByLabelText(/Project manager — backend/)).toHaveValue('retired-pm');
    expect(screen.getByRole('option', { name: /retired-pm \(not registered\)/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^Save/ })).toBeDisabled();
  });

  it('explains the empty state rather than showing a blank page', async () => {
    getSquads.mockResolvedValue({ ok: false });
    render(<TeamsView />);
    expect(await screen.findByText('No teams assembled')).toBeInTheDocument();
  });

  it('surfaces a load failure', async () => {
    getSquads.mockRejectedValue(new ApiError(500, 'squads unavailable', 'Server Error'));
    render(<TeamsView />);
    expect(await screen.findByText('squads unavailable')).toBeInTheDocument();
  });
});
