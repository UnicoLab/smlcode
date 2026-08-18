import React, { Suspense, lazy, useState, useEffect, useCallback } from 'react';
import { Routes, Route } from 'react-router-dom';
import Layout from './components/Layout';
import { getHealth, getConfig } from './api/client';
import type { Config, Health, RunEvent, LatestRunResponse } from './types';

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

export interface AppContextValue {
  health: Health | null;
  config: Config | null;
  dark: boolean;
  toggleDark: () => void;
  refresh: () => void;
  liveEvents: RunEvent[];
  setLiveEvents: (events: RunEvent[] | ((prev: RunEvent[]) => RunEvent[])) => void;
  liveRunning: boolean;
  setLiveRunning: (r: boolean) => void;
  liveResult: LatestRunResponse | null;
  setLiveResult: (r: LatestRunResponse | null) => void;
}

export const AppContext = React.createContext<AppContextValue | null>(null);

export default function App() {
  const [health, setHealth] = useState<Health | null>(null);
  const [config, setConfig] = useState<Config | null>(null);
  const [dark, setDark] = useState(() => {
    const stored = localStorage.getItem('slmcode-theme');
    if (stored) return stored === 'dark';
    return window.matchMedia('(prefers-color-scheme: dark)').matches;
  });
  const [liveEvents, setLiveEventsInternal] = useState<RunEvent[]>(() => {
    try { return JSON.parse(sessionStorage.getItem('slmcode:events') || '[]'); } catch { return []; }
  });
  const [liveRunning, setLiveRunningInternal] = useState(() => sessionStorage.getItem('slmcode:running') === 'true');
  const [liveResult, setLiveResult] = useState<LatestRunResponse | null>(null);

  const setLiveEvents = useCallback((events: RunEvent[] | ((prev: RunEvent[]) => RunEvent[])) => {
    setLiveEventsInternal((prev) => {
      const next = typeof events === 'function' ? events(prev) : events;
      try { sessionStorage.setItem('slmcode:events', JSON.stringify(next.slice(-200))); } catch { /* ignore */ }
      return next;
    });
  }, []);

  const setLiveRunning = useCallback((r: boolean) => {
    setLiveRunningInternal(r);
    try { sessionStorage.setItem('slmcode:running', String(r)); } catch { /* ignore */ }
  }, []);

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark);
    localStorage.setItem('slmcode-theme', dark ? 'dark' : 'light');
  }, [dark]);

  const toggleDark = useCallback(() => setDark((d) => !d), []);

  const refresh = useCallback(async () => {
    try {
      const [h, c] = await Promise.all([getHealth(), getConfig()]);
      setHealth(h);
      setConfig(c);
    } catch {
      // backend may not be running yet
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  return (
    <AppContext.Provider value={{ health, config, dark, toggleDark, refresh, liveEvents, setLiveEvents, liveRunning, setLiveRunning, liveResult, setLiveResult }}>
      <Suspense fallback={<PageLoading />}>
        <Routes>
          <Route element={<Layout />}>
            <Route index element={<LiveView />} />
            <Route path="board" element={<KanbanBoard />} />
            <Route path="pipeline" element={<PipelineEditor />} />
            <Route path="agents" element={<AgentManager />} />
            <Route path="blocks" element={<BlockManager />} />
            <Route path="files" element={<FileInspector events={liveEvents} running={liveRunning} />} />
            <Route path="skills" element={<SkillManager />} />
            <Route path="docs/:docId" element={<MarkdownEditorView />} />
            <Route path="runs" element={<RunHistory />} />
            <Route path="settings" element={<SettingsPanel />} />
          </Route>
        </Routes>
      </Suspense>
    </AppContext.Provider>
  );
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
