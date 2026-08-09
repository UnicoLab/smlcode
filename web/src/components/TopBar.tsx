import { useState, useContext, useRef, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Play,
  Square,
  Moon,
  Sun,
  Zap,
  Settings,
  ChevronDown,
} from 'lucide-react';
import { AppContext } from '@/App';
import { startRun, stopRun, getModels, getAgents, updateConfig } from '@/api/client';
import type { AgentSpec, AuthStatus, ModelCost } from '@/types';
import clsx from 'clsx';

export default function TopBar() {
  const ctx = useContext(AppContext);
  const navigate = useNavigate();

  const [query, setQuery] = useState('');
  const [running, setRunning] = useState(false);
  const [models, setModels] = useState<string[]>([]);
  const [modelCosts, setModelCosts] = useState<Record<string, ModelCost>>({});
  const [enabledModels, setEnabledModels] = useState<string[]>([]);
  const [modelFilter, setModelFilter] = useState('');
  const [currentModel, setCurrentModel] = useState('');
  const [auth, setAuth] = useState<AuthStatus | null>(null);
  const [modelError, setModelError] = useState<string | null>(null);
  const [agents, setAgents] = useState<AgentSpec[]>([]);
  const [specialist, setSpecialist] = useState('');
  const [showModelMenu, setShowModelMenu] = useState(false);
  const [showAgentMenu, setShowAgentMenu] = useState(false);
  const modelRef = useRef<HTMLDivElement>(null);
  const agentRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function close(e: MouseEvent) {
      if (modelRef.current && !modelRef.current.contains(e.target as Node)) {
        setShowModelMenu(false);
      }
      if (agentRef.current && !agentRef.current.contains(e.target as Node)) {
        setShowAgentMenu(false);
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

  const fetchModels = async (q = '') => {
    try {
      const res = await getModels(q ? { q, limit: 40 } : { limit: 40 });
      setModels(res.models || []);
      setCurrentModel(res.current);
      setAuth(res.auth || null);
      setModelError(res.error || null);
      setEnabledModels(res.enabled_models || []);
      const costMap: Record<string, ModelCost> = {};
      (res.costs || []).forEach((c) => {
        if (c?.model) costMap[c.model] = c;
      });
      setModelCosts(costMap);
    } catch {
      /* ignore */
    }
  };

  const cycleEnabledModel = async () => {
    const list = enabledModels.length > 0 ? enabledModels : models;
    if (list.length < 2) return;
    const idx = Math.max(0, list.indexOf(currentModel));
    const next = list[(idx + 1) % list.length];
    await handleModelSelect(next);
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
      // Manual model switch clears active_stack (backend ApplyPatch).
      const nc = await updateConfig({ model });
      setCurrentModel(nc.model);
      ctx?.refresh();
    } catch { /* ignore */ }
  };

  const handleAgentSelect = (id: string) => {
    setSpecialist(id === specialist ? '' : id);
    setShowAgentMenu(false);
  };

  useEffect(() => {
    getAgents().then(setAgents).catch(() => {});
  }, [ctx?.config]);

  const authOk = auth ? auth.configured : true;
  const stackLabel = ctx?.config?.active_stack;

  return (
    <header className="h-14 flex items-center gap-3 px-4 border-b border-gray-200 dark:border-gray-800 glass shrink-0 z-10">
      <button onClick={() => navigate('/')} className="flex items-center gap-2 shrink-0">
        <div className="w-7 h-7 rounded-md bg-brand-600 flex items-center justify-center">
          <Zap size={14} className="text-white" />
        </div>
        <span className="font-bold text-sm tracking-tight logo-glow hidden sm:inline">
          SLMCode
        </span>
        <span className="text-xs text-gray-400 hidden md:inline">Studio</span>
      </button>

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
        <div className="relative hidden lg:block" ref={agentRef}>
          <button
            onClick={() => { setShowModelMenu(false); setShowAgentMenu(!showAgentMenu); }}
            className={clsx(
              'btn-secondary text-xs gap-1 px-2.5 py-1.5',
              specialist && 'bg-brand-100 dark:bg-brand-900/30 text-brand-700 dark:text-brand-300',
            )}
          >
            {specialist || 'Any agent'}
            <ChevronDown size={12} />
          </button>
          {showAgentMenu && (
            <div className="absolute top-full right-0 mt-1 w-64 card p-1 shadow-lg z-20 max-h-72 overflow-auto">
              <button
                onClick={() => handleAgentSelect('')}
                className={clsx(
                  'w-full text-left px-3 py-2 rounded-lg text-sm',
                  !specialist ? 'bg-brand-50 dark:bg-brand-900/20 text-brand-700' : 'hover:bg-gray-50 dark:hover:bg-gray-800',
                )}
              >
                Full pipeline (any agent)
              </button>
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
                  <div className="font-medium">{a.title || a.id}</div>
                  <div className="text-[10px] text-gray-500 font-mono truncate">
                    {a.effective_provider || a.provider || 'stack'}/
                    {a.effective_model || a.model || 'inherit'}
                    {a.inherits_model ? ' · inherits' : ''}
                  </div>
                </button>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Model selector */}
      <div className="relative hidden sm:block" ref={modelRef}>
        <button
          onClick={() => {
            setShowAgentMenu(false);
            const next = !showModelMenu;
            setShowModelMenu(next);
            if (next) {
              setModelFilter('');
              fetchModels();
            }
          }}
          className="btn-secondary text-xs gap-2 px-3 py-1.5"
          title={stackLabel ? `Stack: ${stackLabel}` : 'Global model (clears active stack on change)'}
        >
          <span className={clsx(
            'w-1.5 h-1.5 rounded-full',
            authOk ? 'bg-emerald-500 animate-pulse' : 'bg-amber-500',
          )} />
          <span className="truncate max-w-[120px]">{currentModel || 'model'}</span>
          <ChevronDown size={12} />
        </button>

        {showModelMenu && (
          <div className="absolute top-full right-0 mt-1 w-72 card p-1 shadow-xl z-20 animate-fade-in max-h-80 overflow-auto">
            <div className="px-2 py-1.5 sticky top-0 bg-white dark:bg-gray-900 border-b border-gray-100 dark:border-gray-800">
              <input
                autoFocus
                value={modelFilter}
                onChange={(e) => {
                  setModelFilter(e.target.value);
                  fetchModels(e.target.value);
                }}
                placeholder="Search models…"
                className="input-mono text-xs h-8 w-full"
              />
              {stackLabel && (
                <div className="text-[10px] text-gray-400 mt-1">
                  Active stack: {stackLabel} — picking a model clears it
                </div>
              )}
              {modelError && (
                <div className="text-[10px] text-amber-600 mt-1">{modelError}</div>
              )}
              {enabledModels.length > 0 && (
                <div className="text-[10px] text-gray-400 mt-1 flex items-center justify-between gap-2">
                  <span>enabled_models: {enabledModels.length}</span>
                  <button
                    type="button"
                    className="text-brand-600 hover:underline"
                    onClick={(e) => {
                      e.preventDefault();
                      cycleEnabledModel();
                    }}
                  >
                    Cycle
                  </button>
                </div>
              )}
            </div>
            {models.map((m) => {
              const cost = modelCosts[m];
              return (
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
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate">{m}</span>
                    {cost?.known && (
                      <span className="text-[10px] text-gray-400 shrink-0">
                        ${cost.prompt_per_mtok}/{cost.completion_per_mtok}
                      </span>
                    )}
                  </div>
                </button>
              );
            })}
            {models.length === 0 && (
              <div className="px-3 py-2 text-xs text-gray-400">No models found</div>
            )}
            <button
              onClick={() => { setShowModelMenu(false); navigate('/settings'); }}
              className="w-full text-left px-3 py-2 rounded-lg text-xs text-brand-600 hover:bg-brand-50 dark:hover:bg-brand-900/20 border-t border-gray-100 dark:border-gray-800 mt-1"
            >
              Open stack settings…
            </button>
          </div>
        )}
      </div>

      <button onClick={ctx?.toggleDark} className="btn-ghost p-2 rounded-lg" title="Toggle theme">
        {ctx?.dark ? <Sun size={18} /> : <Moon size={18} />}
      </button>

      <button onClick={() => navigate('/settings')} className="btn-ghost p-2 rounded-lg" title="Settings">
        <Settings size={18} />
      </button>

      {ctx?.config && (
        <span
          className="hidden md:inline-flex items-center gap-1.5 px-2 py-1 rounded-full text-[10px] font-semibold uppercase
                     bg-gray-100 dark:bg-gray-800 text-gray-500 dark:text-gray-400 tracking-wider"
          title={auth?.message || (stackLabel ? `stack:${stackLabel}` : '')}
        >
          <span className={clsx('w-1.5 h-1.5 rounded-full', authOk ? 'bg-emerald-500' : 'bg-amber-500')} />
          {ctx.config.provider}
          {stackLabel ? ` · ${stackLabel}` : ''}
        </span>
      )}
    </header>
  );
}
