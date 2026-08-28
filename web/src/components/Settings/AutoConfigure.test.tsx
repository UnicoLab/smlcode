import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import AutoConfigure from './AutoConfigure';
import { ApiError } from '@/api/client';
import type { ConfigureResult } from '@/types';

const scanForModelServer = vi.fn();
const applyModelServerConfig = vi.fn();
const reportError = vi.fn();
const success = vi.fn();

vi.mock('@/api/client', async () => {
  const actual = await vi.importActual<typeof import('@/api/client')>('@/api/client');
  return {
    ...actual,
    scanForModelServer: (...a: unknown[]) => scanForModelServer(...a),
    applyModelServerConfig: (...a: unknown[]) => applyModelServerConfig(...a),
  };
});

vi.mock('@/components/ui/Toast', () => ({
  useToast: () => ({ reportError, success, info: vi.fn(), push: vi.fn(), dismiss: vi.fn(), toasts: [] }),
}));

const found: ConfigureResult = {
  ok: true,
  applied: false,
  choice: {
    provider: 'lmstudio',
    endpoint: 'http://127.0.0.1:1234/v1',
    model: 'Qwen2.5-Coder-14B-Instruct',
    why: 'tuned for code (coder), instruction-tuned, 14B',
    others: ['Qwen2.5-1.5B-Instruct'],
  },
  tried: [
    { provider: 'omlx', endpoint: 'http://127.0.0.1:8000/v1', reason: 'already configured', live: false, error: 'nothing is listening', latency_ms: 1, models: null },
    { provider: 'lmstudio', endpoint: 'http://127.0.0.1:1234/v1', reason: 'default lmstudio address', live: true, error: '', latency_ms: 3, models: ['Qwen2.5-Coder-14B-Instruct', 'Qwen2.5-1.5B-Instruct'] },
  ],
};

beforeEach(() => {
  vi.clearAllMocks();
  scanForModelServer.mockResolvedValue(structuredClone(found));
  applyModelServerConfig.mockResolvedValue({ ...structuredClone(found), applied: true });
});

describe('AutoConfigure', () => {
  // Looking is free and reversible; writing is neither. A panel that rewrote
  // the configuration the moment you clicked it is one people are afraid to
  // click.
  it('looks without writing', async () => {
    const user = userEvent.setup();
    const onApplied = vi.fn();
    render(<AutoConfigure onApplied={onApplied} />);

    await user.click(screen.getByRole('button', { name: /Look around/ }));

    await waitFor(() => expect(scanForModelServer).toHaveBeenCalledTimes(1));
    expect(applyModelServerConfig).not.toHaveBeenCalled();
    expect(onApplied).not.toHaveBeenCalled();
    expect(screen.getByText('Would configure')).toBeInTheDocument();
    expect(screen.getByText('Qwen2.5-Coder-14B-Instruct')).toBeInTheDocument();
  });

  it('shows every address it tried and what happened', async () => {
    const user = userEvent.setup();
    render(<AutoConfigure />);
    await user.click(screen.getByRole('button', { name: /Look around/ }));

    const list = await screen.findByRole('list', { name: 'Addresses tried' });
    const rows = within(list).getAllByRole('listitem').map((li) => li.textContent ?? '');
    expect(rows).toHaveLength(2);
    // The dead one says why, the live one says what it serves. "No endpoint
    // found" over a list of addresses tells nobody which to go and start.
    expect(rows[0]).toContain('http://127.0.0.1:8000/v1');
    expect(rows[0]).toContain('nothing is listening');
    expect(rows[1]).toContain('http://127.0.0.1:1234/v1');
    expect(rows[1]).toContain('2 model(s)');
  });

  it('writes when asked and tells the page to refresh', async () => {
    const user = userEvent.setup();
    const onApplied = vi.fn();
    render(<AutoConfigure onApplied={onApplied} />);

    await user.click(screen.getByRole('button', { name: /Configure for me/ }));

    await waitFor(() => expect(applyModelServerConfig).toHaveBeenCalledTimes(1));
    expect(onApplied).toHaveBeenCalledTimes(1);
    expect(success).toHaveBeenCalled();
    expect(screen.getByText('Configured')).toBeInTheDocument();
  });

  // A 422 is the harness saying it looked and found nothing usable — that is
  // information, and the body names which of the three problems it is. Showing
  // it as a bare failure throws the diagnosis away.
  it('shows why nothing was found instead of a bare error', async () => {
    const user = userEvent.setup();
    applyModelServerConfig.mockRejectedValue(
      new ApiError(422, JSON.stringify({
        ok: false, applied: false, tried: [],
        reason: 'A server answered, but none of the models it serves can write code — load a coder-tuned instruct model.',
      }), 'Unprocessable'),
    );
    render(<AutoConfigure />);

    await user.click(screen.getByRole('button', { name: /Configure for me/ }));

    expect(await screen.findByRole('alert')).toHaveTextContent('none of the models it serves can write code');
    expect(reportError).not.toHaveBeenCalled();
  });

  it('reports a real failure as a toast', async () => {
    const user = userEvent.setup();
    applyModelServerConfig.mockRejectedValue(new ApiError(409, 'cannot update configuration while a run is active', 'Conflict'));
    render(<AutoConfigure />);

    await user.click(screen.getByRole('button', { name: /Configure for me/ }));

    await waitFor(() => expect(reportError).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('says nothing before it has looked', () => {
    render(<AutoConfigure />);
    expect(screen.queryByText('Would configure')).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
});
