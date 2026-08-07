import { useEffect, useState } from 'react';
import type { StackPreset } from '@/types';
import { applyStack, getStacks } from '@/api/client';
import clsx from 'clsx';

interface StackSelectorProps {
  current: { provider: string; model: string; endpoint: string; active_stack?: string };
  onApplied: () => void;
}

export default function StackSelector({ current, onApplied }: StackSelectorProps) {
  const [stacks, setStacks] = useState<StackPreset[]>([]);
  const [loading, setLoading] = useState(true);
  const [applying, setApplying] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [clearAgentLLM, setClearAgentLLM] = useState(false);
  const [applyAgentDefaults, setApplyAgentDefaults] = useState(false);
  const [forceAgents, setForceAgents] = useState(false);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await getStacks();
        if (!cancelled) setStacks(res.stacks || []);
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : 'Failed to load stacks');
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [current.provider, current.model, current.active_stack]);

  const isActive = (stack: StackPreset) =>
    (current.active_stack && current.active_stack === stack.id) ||
    (!current.active_stack && current.provider === stack.provider && current.model === stack.model);

  const handleSelect = async (stack: StackPreset) => {
    setApplying(stack.id);
    setError(null);
    setNotice(null);
    try {
      const res = await applyStack(stack.id, {
        clear_agent_llm: clearAgentLLM,
        apply_agent_defaults: applyAgentDefaults,
        force_agents: forceAgents,
      });
      const conflicts = res.result?.conflicting_agents || [];
      if (conflicts.length > 0 && !clearAgentLLM) {
        setNotice(
          `Stack applied. Agents still pinning LLM: ${conflicts.join(', ')} — enable “Clear agent LLM pins” to inherit.`,
        );
      } else {
        setNotice(`Applied ${stack.label} → ${res.result?.provider}/${res.result?.model}`);
      }
      onApplied();
      const refreshed = await getStacks();
      setStacks(refreshed.stacks || []);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to apply stack');
    } finally {
      setApplying(null);
    }
  };

  if (loading) {
    return <div className="text-sm text-gray-400 py-6 text-center">Loading stacks…</div>;
  }

  if (error && stacks.length === 0) {
    return <div className="text-sm text-red-500 py-4">{error}</div>;
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-4 text-xs text-gray-500">
        <label className="flex items-center gap-2 cursor-pointer">
          <input
            type="checkbox"
            checked={clearAgentLLM}
            onChange={(e) => setClearAgentLLM(e.target.checked)}
            className="w-3.5 h-3.5 rounded border-gray-300 text-brand-600 focus:ring-brand-500"
          />
          Clear agent LLM pins (inherit stack)
        </label>
        <label className="flex items-center gap-2 cursor-pointer">
          <input
            type="checkbox"
            checked={applyAgentDefaults}
            onChange={(e) => setApplyAgentDefaults(e.target.checked)}
            className="w-3.5 h-3.5 rounded border-gray-300 text-brand-600 focus:ring-brand-500"
          />
          Apply stack agent defaults
        </label>
        <label className={clsx(
          'flex items-center gap-2 cursor-pointer',
          !applyAgentDefaults && 'opacity-40 pointer-events-none',
        )}>
          <input
            type="checkbox"
            checked={forceAgents}
            disabled={!applyAgentDefaults}
            onChange={(e) => setForceAgents(e.target.checked)}
            className="w-3.5 h-3.5 rounded border-gray-300 text-brand-600 focus:ring-brand-500"
          />
          Force overwrite existing agent pins
        </label>
      </div>

      <p className="text-[11px] text-gray-400">
        Stack sets global provider/model. Each agent may override — empty override means inherit.
      </p>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        {stacks.map((stack) => {
          const active = isActive(stack);
          const busy = applying === stack.id;
          const agentCount = stack.agents ? Object.keys(stack.agents).length : 0;
          return (
            <button
              key={stack.id}
              disabled={!!applying}
              onClick={() => handleSelect(stack)}
              className={clsx(
                'relative group flex flex-col items-center gap-3 p-4 rounded-xl border-2 transition-all duration-200',
                'hover:shadow-lg hover:-translate-y-0.5 disabled:opacity-60',
                busy && 'scale-95',
                active
                  ? 'border-brand-500 bg-brand-50 dark:bg-brand-900/20 shadow-md'
                  : 'border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-900 hover:border-brand-300 dark:hover:border-brand-700',
              )}
            >
              {active && (
                <div className="absolute top-2 right-2 w-2 h-2 rounded-full bg-emerald-500 ring-2 ring-white dark:ring-gray-900 animate-pulse" />
              )}

              <div
                className={clsx(
                  'w-12 h-12 rounded-xl bg-gradient-to-br flex items-center justify-center text-2xl shadow-sm',
                  stack.color || 'from-gray-500 to-gray-700',
                )}
              >
                <span className="drop-shadow-sm">{stack.icon || '📦'}</span>
              </div>

              <div className="text-center">
                <div className={clsx(
                  'text-sm font-bold',
                  active ? 'text-brand-700 dark:text-brand-300' : 'text-gray-700 dark:text-gray-300',
                )}>
                  {stack.label}
                </div>
                <div className="text-[10px] text-gray-400 mt-0.5 line-clamp-2">{stack.description}</div>
              </div>

              <div className="text-[9px] font-mono text-gray-400 dark:text-gray-600 truncate w-full text-center px-1">
                {busy ? 'Applying…' : stack.model}
              </div>

              <div className="flex items-center gap-1 flex-wrap justify-center">
                <span className={clsx('badge text-[9px]', active ? 'badge-brand' : 'badge-neutral')}>
                  {stack.provider}
                </span>
                {agentCount > 0 && (
                  <span className="badge-neutral text-[9px]">{agentCount} roles</span>
                )}
              </div>
            </button>
          );
        })}
      </div>

      {notice && (
        <div className="text-xs text-emerald-700 dark:text-emerald-300 bg-emerald-50 dark:bg-emerald-900/20 rounded-lg px-3 py-2">
          {notice}
        </div>
      )}
      {error && (
        <div className="text-xs text-red-600 bg-red-50 dark:bg-red-900/20 rounded-lg px-3 py-2">
          {error}
        </div>
      )}
    </div>
  );
}
