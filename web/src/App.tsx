import React, { Suspense, lazy, useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { Routes, Route } from 'react-router-dom';
import Layout from './components/Layout';
import { getHealth, getConfig, errorText } from './api/client';
import { useLiveStream } from './hooks/useLiveStream';
import { ToastProvider } from './components/ui/Toast';
import { ConfirmProvider } from './components/ui/Modal';
import ErrorBoundary from './components/ui/ErrorBoundary';
import type { Config, ConnectionState, Health, RunEvent, LatestRunResponse } from './types';

const LiveView = lazy(() => import('./components/Live/LiveView'));
const KanbanBoard = lazy(() => import('./components/Board/KanbanBoard'));
const PipelineEditor = lazy(() => import('./components/Pipeline/PipelineEditor'));
const AgentManager = lazy(() => import('./components/Agents/AgentManager'));
const BlockManager = lazy(() => import('./components/Blocks/BlockManager'));
const FileInspector = lazy(() => import('./components/Files/FileInspector'));
const SkillManager = lazy(() => import('./components/Skills/SkillManager'));
const MarkdownEditorView = lazy(() => import('./components/Docs/MarkdownEditor'));
const RunHistory = lazy(() => import('./components/Runs/RunHistory'));
const SettingsPanel = lazy(() => import('./components/Settings/SettingsPanel'));
const ReviewView = lazy(() => import('./components/Review/ReviewView'));
const TeamsView = lazy(() => import('./components/Teams/TeamsView'));

export interface AppContextValue {
  health: Health | null;
  config: Config | null;
  dark: boolean;
  toggleDark: () => void;
  refresh: () => void;
  /** Last error from refresh(), surfaced instead of being swallowed. */
  refreshError: string | null;
  liveEvents: RunEvent[];
  liveRunning: boolean;
  setLiveRunning: (r: boolean) => void;
  liveResult: LatestRunResponse | null;
  setLiveResult: (r: LatestRunResponse | null) => void;
  /** Clear the live log (a new run starting from this tab). */
  resetLiveEvents: () => void;
  // ── Stream health ──
  connection: ConnectionState;
  reconnect: () => void;
  /** Non-null when the server reported dropped events. */
  streamGap: string | null;
  clearStreamGap: () => void;
  /** Bumps whenever an `ask` event arrives — drives the HITL modal. */
  askSignal: number;
  /** Accumulated token deltas for the active agent turn. */
  tokenStream: string;
}

export const AppContext = React.createContext<AppContextValue | null>(null);

/** Trailing delay for mirroring the live log into sessionStorage. */
const PERSIST_DEBOUNCE_MS = 500;

export default function App() {
  return (
    <ToastProvider>
      <ConfirmProvider>
        <AppInner />
      </ConfirmProvider>
    </ToastProvider>
  );
}

function AppInner() {
  const [health, setHealth] = useState<Health | null>(null);
  const [config, setConfig] = useState<Config | null>(null);
  const [refreshError, setRefreshError] = useState<string | null>(null);
  const [dark, setDark] = useState(() => {
    try {
      const stored = localStorage.getItem('slmcode-theme');
      if (stored) return stored === 'dark';
    } catch {
      /* storage may be unavailable in private mode */
    }
    return window.matchMedia('(prefers-color-scheme: dark)').matches;
  });
  const [liveResult, setLiveResult] = useState<LatestRunResponse | null>(null);

  // ── The single live stream for the whole app ──
  //
  // It used to live inside LiveView, so navigating away tore the connection
  // down (and HITL gates went unanswered). It is now owned here and shared.

  // The sessionStorage mirror is debounced, because it runs on every stream flush and JSON.stringify of
  // 200 events is not free. At 60 flushes a second it was a measurable share of
  // each frame — main-thread work that showed up as the log stuttering. The
  // cache only has to be right by the time the tab is navigated away from, so
  // a trailing write is exactly as correct and costs ~1% as much.
  const persistTimerRef = useRef<number | null>(null);
  const pendingEventsRef = useRef<RunEvent[]>([]);

  const flushPersist = useCallback(() => {
    persistTimerRef.current = null;
    try {
      sessionStorage.setItem('slmcode:events', JSON.stringify(pendingEventsRef.current.slice(-200)));
    } catch {
      /* over quota or private mode — the in-memory log is still correct */
    }
  }, []);

  const persistEvents = useCallback(
    (events: RunEvent[]) => {
      pendingEventsRef.current = events;
      if (persistTimerRef.current !== null) return;
      persistTimerRef.current = window.setTimeout(flushPersist, PERSIST_DEBOUNCE_MS);
    },
    [flushPersist],
  );

  // A pending write must still land if the tab is closed or hidden mid-run.
  useEffect(() => {
    const flushNow = () => {
      if (persistTimerRef.current === null) return;
      window.clearTimeout(persistTimerRef.current);
      flushPersist();
    };
    window.addEventListener('pagehide', flushNow);
    document.addEventListener('visibilitychange', flushNow);
    return () => {
      window.removeEventListener('pagehide', flushNow);
      document.removeEventListener('visibilitychange', flushNow);
      flushNow();
    };
  }, [flushPersist]);

  const persistRunning = useCallback((running: boolean) => {
    try {
      sessionStorage.setItem('slmcode:running', String(running));
    } catch {
      /* ignore */
    }
  }, []);

  const initialEventsRef = useRef<RunEvent[]>(readStoredEvents());
  const stream = useLiveStream({
    initialEvents: initialEventsRef.current,
    initialRunning: readStoredRunning(),
    onEvents: persistEvents,
    onRunning: persistRunning,
  });

  useEffect(() => {
    if (stream.latest) setLiveResult(stream.latest);
  }, [stream.latest]);

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark);
    try {
      localStorage.setItem('slmcode-theme', dark ? 'dark' : 'light');
    } catch {
      /* ignore */
    }
  }, [dark]);

  const toggleDark = useCallback(() => setDark((d) => !d), []);

  const refresh = useCallback(async () => {
    try {
      const [h, c] = await Promise.all([getHealth(), getConfig()]);
      setHealth(h);
      setConfig(c);
      setRefreshError(null);
    } catch (err) {
      // Previously `catch {}` — the SPA then looked idle and healthy with a
      // dead backend. Keep the last-known values but record why.
      setRefreshError(errorText(err, 'Could not reach the Studio API'));
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  // Re-read config/health whenever the connection comes back.
  useEffect(() => {
    if (stream.connection === 'live') refresh();
  }, [stream.connection, refresh]);

  // MUST be memoised. A context value rebuilt on every render is a new object
  // identity every time, which forces EVERY consumer in the tree to re-render
  // whether or not anything it reads actually changed — TopBar, Sidebar,
  // Layout, LiveView, HITLPopup, the task panel, all of them. During a run this
  // component re-renders on every stream flush, so an unmemoised value turned
  // one arriving token into a full-app re-render. That was the page flicker.
  const value = useMemo<AppContextValue>(
    () => ({
      health,
      config,
      dark,
      toggleDark,
      refresh,
      refreshError,
      liveEvents: stream.events,
      liveRunning: stream.running,
      setLiveRunning: stream.setRunning,
      liveResult,
      setLiveResult,
      resetLiveEvents: stream.reset,
      connection: stream.connection,
      reconnect: stream.reconnect,
      streamGap: stream.gap,
      clearStreamGap: stream.clearGap,
      askSignal: stream.askSignal,
      tokenStream: stream.tokenStream,
    }),
    [
      health,
      config,
      dark,
      toggleDark,
      refresh,
      refreshError,
      liveResult,
      stream.events,
      stream.running,
      stream.setRunning,
      stream.reset,
      stream.connection,
      stream.reconnect,
      stream.gap,
      stream.clearGap,
      stream.askSignal,
      stream.tokenStream,
    ],
  );

  return (
    <AppContext.Provider value={value}>
      <ErrorBoundary label="Studio">
        <Suspense fallback={<PageLoading />}>
          <Routes>
            <Route element={<Layout />}>
              <Route index element={<LiveView />} />
              <Route path="board" element={<KanbanBoard />} />
              <Route path="review" element={<ReviewView />} />
              <Route path="pipeline" element={<PipelineEditor />} />
              <Route path="agents" element={<AgentManager />} />
              <Route path="teams" element={<TeamsView />} />
              <Route path="blocks" element={<BlockManager />} />
              <Route path="files" element={<FileInspector events={stream.events} running={stream.running} />} />
              <Route path="skills" element={<SkillManager />} />
              <Route path="docs/:docId" element={<MarkdownEditorView />} />
              <Route path="runs" element={<RunHistory />} />
              <Route path="settings" element={<SettingsPanel />} />
            </Route>
          </Routes>
        </Suspense>
      </ErrorBoundary>
    </AppContext.Provider>
  );
}

function readStoredEvents(): RunEvent[] {
  try {
    const raw = JSON.parse(sessionStorage.getItem('slmcode:events') || '[]');
    return Array.isArray(raw) ? raw : [];
  } catch {
    return [];
  }
}

function readStoredRunning(): boolean {
  try {
    return sessionStorage.getItem('slmcode:running') === 'true';
  } catch {
    return false;
  }
}

function PageLoading() {
  return (
    <div className="h-screen flex items-center justify-center bg-surface text-gray-500 dark:text-gray-400">
      <div className="flex items-center gap-3 text-sm">
        <div className="h-4 w-4 rounded-full border-2 border-brand-500 border-t-transparent animate-spin" />
        Loading Studio
      </div>
    </div>
  );
}
