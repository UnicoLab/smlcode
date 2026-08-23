import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import HITLPopup from './HITLPopup';

const notPending = { pending: false } as const;

const shellPending = {
  pending: true,
  ask: {
    id: 'ask-shell-1',
    kind: 'shell',
    command: 'rm -rf build/',
    reason: 'clean the build directory',
    created_at: new Date().toISOString(),
    timeout_sec: 120,
    on_timeout: 'deny',
  },
};

const approveShell = vi.fn(async () => ({ ok: true }));
const getShellPending = vi.fn(async () => shellPending);
const getClarifyPending = vi.fn(async () => notPending);
const getPlanPending = vi.fn(async () => notPending);
const getContinuePending = vi.fn(async () => notPending);
const getEscalatePending = vi.fn(async () => notPending);

vi.mock('@/api/client', () => ({
  getClarifyPending: (...a: unknown[]) => getClarifyPending(...(a as [])),
  getPlanPending: (...a: unknown[]) => getPlanPending(...(a as [])),
  getContinuePending: (...a: unknown[]) => getContinuePending(...(a as [])),
  getEscalatePending: (...a: unknown[]) => getEscalatePending(...(a as [])),
  getShellPending: (...a: unknown[]) => getShellPending(...(a as [])),
  answerClarify: vi.fn(async () => ({ ok: true })),
  clarifyUseRecommended: vi.fn(async () => ({ ok: true })),
  approvePlan: vi.fn(async () => ({ ok: true })),
  answerContinue: vi.fn(async () => ({ ok: true })),
  answerEscalate: vi.fn(async () => ({ ok: true })),
  approveShell: (...a: unknown[]) => approveShell(...(a as [])),
}));

describe('HITLPopup', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    getShellPending.mockResolvedValue(shellPending);
  });

  it('renders nothing when no run is active', () => {
    const { container } = render(<HITLPopup running={false} askSignal={0} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders a labelled modal dialog for a pending shell gate', async () => {
    render(<HITLPopup running askSignal={0} />);
    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAccessibleName();
    expect(screen.getByText('rm -rf build/')).toBeInTheDocument();
  });

  it('submits the decision with the ask id so a stale answer is rejected', async () => {
    const user = userEvent.setup();
    render(<HITLPopup running askSignal={0} />);
    await screen.findByRole('dialog');

    const approve = await screen.findByRole('button', { name: /approve/i });
    await user.click(approve);

    await waitFor(() => expect(approveShell).toHaveBeenCalledTimes(1));
    expect(approveShell).toHaveBeenCalledWith('approve', 'ask-shell-1');
  });

  it('closes the gate after a successful answer', async () => {
    const user = userEvent.setup();
    render(<HITLPopup running askSignal={0} />);
    await screen.findByRole('dialog');
    // The next poll finds nothing pending, as the harness has consumed it.
    getShellPending.mockResolvedValue(notPending);

    await user.click(await screen.findByRole('button', { name: /approve/i }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  });

  it('surfaces a submit failure instead of losing the gate', async () => {
    const user = userEvent.setup();
    approveShell.mockRejectedValueOnce(new Error('409: shell ask already answered'));
    render(<HITLPopup running askSignal={0} />);
    await screen.findByRole('dialog');

    await user.click(await screen.findByRole('button', { name: /approve/i }));
    expect(await screen.findByText(/already answered/i)).toBeInTheDocument();
    // The dialog stays open so the user can retry or deny.
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });

  // The gate used to be discovered only by polling five endpoints every 2s.
  it('re-checks for gates immediately when the SSE ask signal fires', async () => {
    const { rerender } = render(<HITLPopup running askSignal={0} />);
    await waitFor(() => expect(getShellPending).toHaveBeenCalled());
    const before = getShellPending.mock.calls.length;

    rerender(<HITLPopup running askSignal={1} />);
    await waitFor(() => expect(getShellPending.mock.calls.length).toBeGreaterThan(before));
  });
});
