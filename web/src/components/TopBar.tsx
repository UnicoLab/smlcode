import { useState, useContext, useRef, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Play,
  Square,
  Moon,
  Sun,
  Zap,
  Settings,
  PanelLeftClose,
  PanelLeft,
  ChevronDown,
} from 'lucide-react';
import { AppContext } from '@/App';
import { startRun, stopRun, getModels, getConfig, getAgents, updateConfig } from '@/api/client';
import type { AgentSpec } from '@/types';
import clsx from 'clsx';

interface TopBarProps {
  onToggleSidebar: () => void;
  sidebarOpen: boolean;
}

export default function TopBar({ onToggleSidebar, sidebarOpen }: TopBarProps) {
  const ctx = useContext(AppContext);
  const navigate = useNavigate();

  const [query, setQuery] = useState('');
  const [running, setRunning] = useState(false);
  const [models, setModels] = useState<string[]>([]);
  const [currentModel, setCurrentModel] = useState('');
  const [agents, setAgents] = useState<AgentSpec[]>([]);
  const [specialist, setSpecialist] = useState('');
  const [showModelMenu, setShowModelMenu] = useState(false);
  const modelRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function close(e: MouseEvent) {
      if (modelRef.current && !modelRef.current.contains(e.target as Node)) {
        setShowModelMenu(false);
      }
    }
    document.addEventListener('mousedown', close);
    return () => document.removeEventListener('mousedown', close);
  }, []);

  useEffect(() => {
    if (ctx?.config) {
      setCurrentModel(ctx.config.model);
    }
  }, [ctx?.config]);

  const fetchModels = async () => {
    try {
      const res = await getModels();
      setModels(res.models);
      setCurrentModel(res.current);
    } catch { /* ignore */ }
  };

  const handleRun = async () => {
    const q = query.trim();
    if (!q || running) return;
    setRunning(true);
    try {
      await startRun({
        query: q,
        mode: specialist ? 'specialist' : undefined,
        specialist: specialist || undefined,
        skills: ctx?.config?.pinned_skills,
      });
    } catch (e) {
      console.error('Run failed:', e);
    } finally {
      setRunning(false);
    }
  };

  const handleStop = async () => {
    try {
      await stopRun();
    } catch { /* ignore */ }
    setRunning(false);
  };

  const handleModelSelect = async (model: string) => {
    setShowModelMenu(false);
    try {
      const c = await getConfig();
      // Update via config patch
      const nc = await updateConfig({ model });
      setCurrentModel(nc.model);
      ctx?.refresh();
    } catch { /* ignore */ }
  };

  const handleAgentSelect = (id: string) => {
    setSpecialist(id === specialist ? '' : id);
  };

  useEffect(() => {
    getAgents().then(setAgents).catch(() => {});
  }, [ctx?.config]);

  return (
    <header className="h-14 flex items-center gap-3 px-4 border-b border-gray-200 dark:border-gray-800 glass shrink-0 z-10">
      {/* Sidebar toggle */}
      <button
        onClick={onToggleSidebar}
        className="btn-ghost p-2 rounded-lg"
        title={sidebarOpen ? 'Close sidebar' : 'Open sidebar'}
      >
        {sidebarOpen ? <PanelLeftClose size={18} /> : <PanelLeft size={18} />}
      </button>

      {/* Logo */}
      <button
        onClick={() => navigate('/')}
        className="flex items-center gap-2 shrink-0"
      >
        <div className="w-7 h-7 rounded-md bg-brand-600 flex items-center justify-center">
          <Zap size={14} className="text-white" />
        </div>
        <span className="font-bold text-sm tracking-tight logo-glow hidden sm:inline">
          SLMCode
        </span>
        <span className="text-xs text-gray-400 hidden md:inline">Studio</span>
      </button>

      {/* Query input */}
      <div className="flex-1 max-w-2xl mx-auto">
        <div className="relative">
          <input
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && handleRun()}
            placeholder="What would you like to build? e.g. add JWT auth to the API…"
            className="w-full h-9 px-4 pr-10 rounded-lg bg-gray-100 dark:bg-gray-800 border border-transparent
                       focus:border-brand-500 focus:bg-white dark:focus:bg-gray-900 text-sm
                       placeholder-gray-400 dark:placeholder-gray-500 transition-all duration-150"
          />
          <div className="absolute right-1 top-1/2 -translate-y-1/2 flex items-center gap-1">
            {running ? (
              <button onClick={handleStop} className="btn-ghost p-1.5 rounded-md text-red-500" title="Stop run">
                <Square size={16} fill="currentColor" />
              </button>
            ) : (
              <button
                onClick={handleRun}
                disabled={!query.trim()}
                className="btn-ghost p-1.5 rounded-md text-brand-500 disabled:text-gray-400"
                title="Run"
              >
                <Play size={16} fill="currentColor" />
              </button>
            )}
          </div>
        </div>
      </div>

      {/* Specialist selector */}
      {agents.length > 0 && (
        <div className="relative hidden lg:block">
          <button
            onClick={() => setShowModelMenu(false)}
            className={clsx(
              'btn-secondary text-xs gap-1 px-2.5 py-1.5',
              specialist && 'bg-brand-100 dark:bg-brand-900/30 text-brand-700 dark:text-brand-300',
            )}
          >
            {specialist || 'Any agent'}
            <ChevronDown size={12} />
          </button>
          <div className="absolute top-full right-0 mt-1 w-48 card p-1 shadow-lg z-20 hidden group-focus-within:block">
            {agents.map((a) => (
              <button
                key={a.id}
                onClick={() => handleAgentSelect(a.id)}
                className={clsx(
                  'w-full text-left px-3 py-2 rounded-lg text-sm transition-colors',
                  specialist === a.id
                    ? 'bg-brand-50 dark:bg-brand-900/20 text-brand-700 dark:text-brand-300'
                    : 'hover:bg-gray-50 dark:hover:bg-gray-800',
                )}
              >
                <div className="font-medium">{a.title}</div>
                <div className="text-xs text-gray-500 truncate">{a.id}</div>
              </button>
            ))}
          </div>
        </div>
      )}

      {/* Model selector */}
      <div className="relative hidden sm:block" ref={modelRef}>
        <button
          onClick={() => { fetchModels(); setShowModelMenu(!showModelMenu); }}
          className="btn-secondary text-xs gap-2 px-3 py-1.5"
        >
          <span className="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse" />
          <span className="truncate max-w-[120px]">{currentModel || 'model'}</span>
          <ChevronDown size={12} />
        </button>

        {showModelMenu && (
          <div className="absolute top-full right-0 mt-1 w-56 card p-1 shadow-xl z-20 animate-fade-in max-h-64 overflow-auto">
            {models.map((m) => (
              <button
                key={m}
                onClick={() => handleModelSelect(m)}
                className={clsx(
                  'w-full text-left px-3 py-2 rounded-lg text-xs font-mono transition-colors',
                  m === currentModel
                    ? 'bg-brand-50 dark:bg-brand-900/20 text-brand-600 dark:text-brand-400'
                    : 'hover:bg-gray-50 dark:hover:bg-gray-800',
                )}
              >
                {m}
              </button>
            ))}
            {models.length === 0 && (
              <div className="px-3 py-2 text-xs text-gray-400">No models found</div>
            )}
          </div>
        )}
      </div>

      {/* Theme toggle */}
      <button
        onClick={ctx?.toggleDark}
        className="btn-ghost p-2 rounded-lg"
        title="Toggle theme"
      >
        {ctx?.dark ? <Sun size={18} /> : <Moon size={18} />}
      </button>

      {/* Settings button */}
      <button
        onClick={() => navigate('/settings')}
        className="btn-ghost p-2 rounded-lg"
        title="Settings"
      >
        <Settings size={18} />
      </button>

      {/* Provider badge */}
      {ctx?.config && (
        <span className="hidden md:inline-flex items-center gap-1.5 px-2 py-1 rounded-full text-[10px] font-semibold uppercase
                         bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400 tracking-wider">
          <span className="w-1.5 h-1.5 rounded-full bg-emerald-500" />
          {ctx.config.provider}
        </span>
      )}
    </header>
  );
}
