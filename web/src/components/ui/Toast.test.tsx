import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { ToastProvider, useToast } from './Toast';
import { ConfirmProvider, useConfirm } from './Modal';
import { ApiError } from '@/api/client';

function ErrorButton({ err }: { err: unknown }) {
  const toast = useToast();
  return (
    <button type="button" onClick={() => toast.reportError(err, 'Could not switch model')}>
      trigger
    </button>
  );
}

describe('config-save error surfacing', () => {
  // The audit's headline example: TopBar.handleModelSelect swallowed the
  // server's `409 cannot update configuration while a run is active` in a bare
  // `catch {}`, so the user got no feedback whatsoever.
  it('shows the server message for a 409 instead of swallowing it', async () => {
    const user = userEvent.setup();
    render(
      <ToastProvider>
        <ErrorButton err={new ApiError(409, 'cannot update configuration while a run is active', 'Conflict')} />
      </ToastProvider>,
    );

    await user.click(screen.getByRole('button', { name: 'trigger' }));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Could not switch model');
    expect(alert).toHaveTextContent('cannot update configuration while a run is active');
  });

  it('explains a 401 in terms the user can act on', async () => {
    const user = userEvent.setup();
    render(
      <ToastProvider>
        <ErrorButton err={new ApiError(401, 'unauthorized', 'Unauthorized')} />
      </ToastProvider>,
    );
    await user.click(screen.getByRole('button', { name: 'trigger' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/session expired/i);
  });

  it('lets the user dismiss an error toast', async () => {
    const user = userEvent.setup();
    render(
      <ToastProvider>
        <ErrorButton err={new Error('boom')} />
      </ToastProvider>,
    );
    await user.click(screen.getByRole('button', { name: 'trigger' }));
    await screen.findByRole('alert');
    await user.click(screen.getByRole('button', { name: /dismiss notification/i }));
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
  });

  it('errors persist (ttl 0) rather than disappearing before they are read', async () => {
    const user = userEvent.setup();
    render(
      <ToastProvider>
        <ErrorButton err={new Error('boom')} />
      </ToastProvider>,
    );
    await user.click(screen.getByRole('button', { name: 'trigger' }));
    const alert = await screen.findByRole('alert');
    await new Promise((r) => setTimeout(r, 50));
    expect(alert).toBeInTheDocument();
  });
});

function DeleteButton({ onResult }: { onResult: (ok: boolean) => void }) {
  const confirm = useConfirm();
  return (
    <button
      type="button"
      onClick={async () => onResult(await confirm({ title: 'Delete task "Fix the flaky test"?' }))}
    >
      delete
    </button>
  );
}

describe('accessible confirm replaces window.confirm', () => {
  it('renders a labelled modal dialog and resolves true on confirm', async () => {
    const user = userEvent.setup();
    const onResult = vi.fn();
    render(
      <ConfirmProvider>
        <DeleteButton onResult={onResult} />
      </ConfirmProvider>,
    );

    await user.click(screen.getByRole('button', { name: 'delete' }));

    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAccessibleName('Delete task "Fix the flaky test"?');

    await user.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(onResult).toHaveBeenCalledWith(true));
  });

  it('resolves false on cancel and on Escape', async () => {
    const user = userEvent.setup();
    const onResult = vi.fn();
    render(
      <ConfirmProvider>
        <DeleteButton onResult={onResult} />
      </ConfirmProvider>,
    );

    await user.click(screen.getByRole('button', { name: 'delete' }));
    await user.click(await screen.findByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(onResult).toHaveBeenLastCalledWith(false));

    await user.click(screen.getByRole('button', { name: 'delete' }));
    await screen.findByRole('dialog');
    await user.keyboard('{Escape}');
    await waitFor(() => expect(onResult).toHaveBeenLastCalledWith(false));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('moves focus into the dialog and restores it on close', async () => {
    const user = userEvent.setup();
    render(
      <ConfirmProvider>
        <DeleteButton onResult={() => {}} />
      </ConfirmProvider>,
    );
    const trigger = screen.getByRole('button', { name: 'delete' });
    await user.click(trigger);

    await waitFor(() => expect(screen.getByRole('button', { name: 'Delete' })).toHaveFocus());
    await user.keyboard('{Escape}');
    await waitFor(() => expect(trigger).toHaveFocus());
  });
});
