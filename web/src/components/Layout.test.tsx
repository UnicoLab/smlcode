import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import Layout, { runEndVerdict } from './Layout';
import { ToastProvider } from './ui/Toast';
import { AppContext, type AppContextValue } from '@/App';
import type { RunEvent } from '@/types';

vi.mock('./TopBar', () => ({ default: () => <div data-testid="topbar" /> }));
vi.mock('./Sidebar', () => ({ default: () => <div data-testid="sidebar" /> }));
vi.mock('./Live/HITLPopup', () => ({ default: () => null }));
vi.mock('./ui/CommandPalette', () => ({ default: () => null }));
vi.mock('@/api/client', () => ({ errorText: (e: unknown) => String(e) }));

const ev = (over: Partial<RunEvent>): RunEvent => ({
  phase: 'execute',
  kind: 'log',
  message: '',
  time: new Date().toISOString(),
  ...over,
});

function ctx(over: Partial<AppContextValue>): AppContextValue {
  return {
    health: null,
    config: null,
    dark: false,
    toggleDark: () => {},
    refresh: () => {},
    refreshError: null,
    liveEvents: [],
    liveRunning: false,
    setLiveRunning: () => {},
    liveResult: null,
    setLiveResult: () => {},
    resetLiveEvents: () => {},
    connection: 'live',
    reconnect: () => {},
    streamGap: null,
    clearStreamGap: () => {},
    askSignal: 0,
    tokenStream: '',
    ...over,
  };
}

function renderLayout(value: AppContextValue) {
  return render(
    <ToastProvider>
      <AppContext.Provider value={value}>
        <MemoryRouter>
          <Routes>
            <Route element={<Layout />}>
              <Route index element={<div>page</div>} />
            </Route>
          </Routes>
        </MemoryRouter>
      </AppContext.Provider>
    </ToastProvider>,
  );
}

describe('runEndVerdict', () => {
  it('reads failed-task counts out of the summary line', () => {
    expect(runEndVerdict(ev({ kind: 'run_end', phase: 'done', message: '3/3 tasks done, 0 failed' }))).toEqual({ ok: true, failedTasks: 0 });
    expect(runEndVerdict(ev({ kind: 'run_end', phase: 'done', message: '1/3 tasks done, 2 failed' }))).toEqual({ ok: false, failedTasks: 2 });
    expect(runEndVerdict(ev({ kind: 'run_end', phase: 'error', message: 'context deadline exceeded' }))).toEqual({ ok: false, failedTasks: 0 });
  });
});

describe('Layout — the run’s end reaches the user', () => {
  it('celebrates a green run with a toast and a short burst', () => {
    const running = ctx({ liveRunning: true, liveEvents: [ev({ kind: 'run_start' })] });
    const { rerender } = renderLayout(running);
    const ended = ctx({
      liveRunning: false,
      liveEvents: [ev({ kind: 'run_start' }), ev({ kind: 'run_end', phase: 'done', message: '2/2 tasks done, 0 failed' })],
    });
    rerender(
      <ToastProvider>
        <AppContext.Provider value={ended}>
          <MemoryRouter>
            <Routes>
              <Route element={<Layout />}>
                <Route index element={<div>page</div>} />
              </Route>
            </Routes>
          </MemoryRouter>
        </AppContext.Provider>
      </ToastProvider>,
    );
    expect(screen.getByText('Run finished')).toBeInTheDocument();
    expect(screen.getByTestId('confetti')).toBeInTheDocument();
  });

  it('reports a failed run as an error toast, without confetti', () => {
    const { rerender } = renderLayout(ctx({ liveRunning: true }));
    rerender(
      <ToastProvider>
        <AppContext.Provider value={ctx({ liveRunning: false, liveEvents: [ev({ kind: 'run_end', phase: 'done', message: '1/3 tasks done, 2 failed' })] })}>
          <MemoryRouter>
            <Routes>
              <Route element={<Layout />}>
                <Route index element={<div>page</div>} />
              </Route>
            </Routes>
          </MemoryRouter>
        </AppContext.Provider>
      </ToastProvider>,
    );
    expect(screen.getByRole('alert')).toHaveTextContent('Run finished with 2 failed tasks');
    expect(screen.queryByTestId('confetti')).not.toBeInTheDocument();
  });

  // The server replays the last run_end on page load. That is history.
  it('stays quiet about a run_end it never saw running', () => {
    renderLayout(ctx({ liveRunning: false, liveEvents: [ev({ kind: 'run_end', phase: 'done', message: '2/2 tasks done, 0 failed' })] }));
    expect(screen.queryByText('Run finished')).not.toBeInTheDocument();
    expect(screen.queryByTestId('confetti')).not.toBeInTheDocument();
  });
});
