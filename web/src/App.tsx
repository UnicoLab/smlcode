import React, { useState, useEffect, useCallback } from 'react';
import { Routes, Route } from 'react-router-dom';
import Layout from './components/Layout';
import KanbanBoard from './components/Board/KanbanBoard';
import LiveView from './components/Live/LiveView';
import SettingsPanel from './components/Settings/SettingsPanel';
import PipelineEditor from './components/Pipeline/PipelineEditor';
import AgentManager from './components/Agents/AgentManager';
import BlockManager from './components/Blocks/BlockManager';
import SkillManager from './components/Skills/SkillManager';
import MarkdownEditorView from './components/Docs/MarkdownEditor';
import { getHealth, getConfig } from './api/client';
import type { Config, Health } from './types';

export interface AppContextValue {
  health: Health | null;
  config: Config | null;
  dark: boolean;
  toggleDark: () => void;
  refresh: () => void;
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
    <AppContext.Provider value={{ health, config, dark, toggleDark, refresh }}>
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<LiveView />} />
          <Route path="board" element={<KanbanBoard />} />
          <Route path="pipeline" element={<PipelineEditor />} />
          <Route path="agents" element={<AgentManager />} />
          <Route path="blocks" element={<BlockManager />} />
          <Route path="skills" element={<SkillManager />} />
          <Route path="docs/:docId" element={<MarkdownEditorView />} />
          <Route path="settings" element={<SettingsPanel />} />
        </Route>
      </Routes>
    </AppContext.Provider>
  );
}
