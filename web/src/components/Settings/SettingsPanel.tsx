import { useState, useContext, useEffect } from 'react';
import { AppContext } from '@/App';
import { getAuthStatus, getMCPStatus, putAuthKey, updateConfig } from '@/api/client';
import type { AuthStatus, ConfigPatch, MCPStatus } from '@/types';
import StackSelector from './StackSelector';
import PackSelector from './PackSelector';
import {
  Cpu,
  Key,
  Sliders,
  ShieldCheck,
  Gauge,
  Wrench,
  Zap,
  Save,
  RotateCcw,
  Package,
} from 'lucide-react';
import clsx from 'clsx';

export default function SettingsPanel() {
  const ctx = useContext(AppContext);
  const [saving, setSaving] = useState(false);
  const [local, setLocal] = useState<ConfigPatch>({});
  const [auth, setAuth] = useState<AuthStatus | null>(null);
  const [mcp, setMcp] = useState<MCPStatus | null>(null);

  const config = ctx?.config;

  useEffect(() => {
    getAuthStatus().then(setAuth).catch(() => setAuth(null));
    getMCPStatus().then(setMcp).catch(() => setMcp(null));
  }, [config?.provider, config?.api_key, config?.active_stack]);

  if (!config) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-gray-400">Loading config…</div>
      </div>
    );
  }

  const cfg = { ...config, ...local };

  const handleChange = (key: keyof ConfigPatch, value: unknown) => {
    setLocal((prev) => ({ ...prev, [key]: value }));
  };

  const handleSave = async () => {
    setSaving(true);
    try {
      await updateConfig(local);
      setLocal({});
      ctx?.refresh();
      getAuthStatus().then(setAuth).catch(() => {});
    } catch (e) {
      console.error('Save failed:', e);
    } finally {
      setSaving(false);
    }
  };

  const hasChanges = Object.keys(local).length > 0;

  return (
    <div className="h-full overflow-auto">
      <div className="max-w-4xl mx-auto p-6 space-y-8">
        {/* Header */}
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold">Settings</h1>
            <p className="text-sm text-gray-500 mt-1">Provider, model, and stack configuration</p>
          </div>
          {hasChanges && (
            <div className="flex items-center gap-2">
              <button
                onClick={() => setLocal({})}
                className="btn-ghost text-sm gap-2"
              >
                <RotateCcw size={16} />
                Reset
              </button>
              <button
                onClick={handleSave}
                disabled={saving}
                className="btn-primary text-sm gap-2"
              >
                <Save size={16} />
                {saving ? 'Saving…' : 'Save Changes'}
              </button>
            </div>
          )}
        </div>

        {/* Stack Selector (prominent) */}
        <section className="card p-6 space-y-4">
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 rounded-xl bg-brand-100 dark:bg-brand-900/30 flex items-center justify-center">
              <Zap size={20} className="text-brand-600" />
            </div>
            <div>
              <h2 className="text-lg font-bold">Model Stack</h2>
              <p className="text-sm text-gray-500">Switch between model providers and stacks in one click</p>
            </div>
          </div>
          <StackSelector
            current={{
              provider: cfg.provider,
              model: cfg.model,
              endpoint: cfg.endpoint,
              active_stack: cfg.active_stack,
            }}
            onApplied={() => {
              setLocal({});
              ctx?.refresh();
            }}
          />
        </section>

        {/* Language Pack Selector */}
        <section className="card p-6 space-y-4">
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 rounded-xl bg-amber-100 dark:bg-amber-900/30 flex items-center justify-center">
              <Package size={20} className="text-amber-600" />
            </div>
            <div>
              <h2 className="text-lg font-bold">Language Pack</h2>
              <p className="text-sm text-gray-500">Switch between Go, Python, React, Web, Rust, Java, and C/C++ pipelines in one click</p>
            </div>
          </div>
          <PackSelector
            currentPack={cfg.active_pack || ''}
            currentPipeline={cfg.active_pipeline || ''}
            onApplied={() => {
              setLocal({});
              ctx?.refresh();
            }}
          />
        </section>

        {/* Provider & Model */}
        <section className="card p-6 space-y-4">
          <div className="flex items-center gap-3 mb-2">
            <Cpu size={20} className="text-gray-400" />
            <h2 className="font-bold">Provider & Model</h2>
          </div>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <label className="label">Provider</label>
              <input
                type="text"
                value={cfg.provider}
                onChange={(e) => handleChange('provider', e.target.value)}
                className="input"
                placeholder="omlx, openai, ollama, openrouter…"
              />
            </div>
            <div>
              <label className="label">Model</label>
              <input
                type="text"
                value={cfg.model}
                onChange={(e) => handleChange('model', e.target.value)}
                className="input-mono"
                placeholder="gpt-4o, deepseek-chat…"
              />
            </div>
            <div className="md:col-span-2">
              <label className="label">Endpoint</label>
              <input
                type="text"
                value={cfg.endpoint}
                onChange={(e) => handleChange('endpoint', e.target.value)}
                className="input-mono"
                placeholder="https://api.openai.com/v1"
              />
            </div>
            <div className="md:col-span-2">
              <label className="label flex items-center gap-2">
                <Key size={14} className="text-gray-400" />
                API Key
              </label>
              <input
                type="password"
                value={local.api_key ?? ''}
                onChange={(e) => handleChange('api_key', e.target.value)}
                className="input-mono"
                placeholder={cfg.api_key === '***' ? '•••• saved (enter to replace)' : 'optional — env vars preferred'}
                autoComplete="off"
              />
              <div className="flex items-center gap-2 mt-2 text-[11px]">
                <span className={clsx(
                  'w-1.5 h-1.5 rounded-full',
                  auth?.configured ? 'bg-emerald-500' : auth?.required ? 'bg-amber-500' : 'bg-gray-400',
                )} />
                <span className="text-gray-500">
                  {auth?.message
                    || (auth?.configured
                      ? `Auth OK (${auth.source}${auth.env_key ? ` · ${auth.env_key}` : ''})`
                      : 'Auth status unknown')}
                </span>
                {cfg.active_stack && (
                  <span className="badge-neutral text-[10px]">stack:{cfg.active_stack}</span>
                )}
                {local.api_key && local.api_key !== '***' && (
                  <button
                    type="button"
                    className="text-brand-600 hover:underline ml-2"
                    onClick={async () => {
                      try {
                        await putAuthKey(String(local.api_key), cfg.provider);
                        setLocal((p) => {
                          const next = { ...p };
                          delete next.api_key;
                          return next;
                        });
                        getAuthStatus().then(setAuth).catch(() => {});
                      } catch (e) {
                        console.error(e);
                      }
                    }}
                  >
                    Save to auth.json
                  </button>
                )}
              </div>
            </div>
            <div>
              <label className="label">Backend</label>
              <select
                value={cfg.backend}
                onChange={(e) => handleChange('backend', e.target.value)}
                className="input"
              >
                <option value="slmcode">SLMCode</option>
                <option value="claude-code">Claude Code</option>
              </select>
            </div>
            <div>
              <label className="label">Mode</label>
              <select
                value={cfg.mode}
                onChange={(e) => handleChange('mode', e.target.value)}
                className="input"
              >
                <option value="full">Full Pipeline</option>
                <option value="specialist">Single Specialist</option>
              </select>
            </div>
          </div>
        </section>

        {/* MCP status */}
        <section className="card p-6 space-y-3">
          <div className="flex items-center gap-3 mb-1">
            <Wrench size={20} className="text-gray-400" />
            <h2 className="font-bold">MCP</h2>
          </div>
          <p className="text-sm text-gray-500">
            Single meta-tool <code className="text-xs">mcp_call</code> — servers stay skill-gated, not one tool per capability.
          </p>
          {!mcp?.enabled ? (
            <p className="text-sm text-gray-400">No mcp_servers configured in config.yaml</p>
          ) : (
            <ul className="space-y-2 text-sm">
              {mcp.servers.map((s) => (
                <li key={s.name} className="flex items-center justify-between gap-3 border border-gray-200 dark:border-gray-700 rounded-lg px-3 py-2">
                  <span className="font-medium">{s.name}</span>
                  <span className="text-gray-500">
                    {s.connected ? 'connected' : 'offline'} · {s.transport} · {s.tool_count} tools
                  </span>
                </li>
              ))}
            </ul>
          )}
        </section>

        {/* Performance */}
        <section className="card p-6 space-y-4">
          <div className="flex items-center gap-3 mb-2">
            <Gauge size={20} className="text-gray-400" />
            <h2 className="font-bold">Performance</h2>
          </div>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
            <div>
              <label className="label">Temperature</label>
              <input
                type="number"
                step="0.01"
                min="0"
                max="2"
                value={cfg.temperature}
                onChange={(e) => handleChange('temperature', parseFloat(e.target.value))}
                className="input"
              />
            </div>
            <div>
              <label className="label">Max Tokens</label>
              <input
                type="number"
                value={cfg.max_tokens}
                onChange={(e) => handleChange('max_tokens', parseInt(e.target.value))}
                className="input"
              />
            </div>
            <div>
              <label className="label">Max Parallel</label>
              <input
                type="number"
                value={cfg.max_parallel}
                onChange={(e) => handleChange('max_parallel', parseInt(e.target.value))}
                className="input"
              />
            </div>
            <div>
              <label className="label">Max Retries</label>
              <input
                type="number"
                value={cfg.max_retries}
                onChange={(e) => handleChange('max_retries', parseInt(e.target.value))}
                className="input"
              />
            </div>
            <div>
              <label className="label">Context KB</label>
              <input
                type="number"
                value={cfg.max_context_kb}
                onChange={(e) => handleChange('max_context_kb', parseInt(e.target.value))}
                className="input"
              />
            </div>
            <div>
              <label className="label">Think Passes</label>
              <input
                type="number"
                value={cfg.think_passes}
                onChange={(e) => handleChange('think_passes', parseInt(e.target.value))}
                className="input"
              />
            </div>
            <div>
              <label className="label">QA Gate Rounds</label>
              <input
                type="number"
                value={cfg.qa_gate_max_rounds}
                onChange={(e) => handleChange('qa_gate_max_rounds', parseInt(e.target.value))}
                className="input"
              />
            </div>
          </div>
        </section>

        {/* Quality Gates */}
        <section className="card p-6 space-y-4">
          <div className="flex items-center gap-3 mb-2">
            <ShieldCheck size={20} className="text-gray-400" />
            <h2 className="font-bold">Quality Gates</h2>
          </div>
          <div className="grid grid-cols-2 md:grid-cols-3 gap-3">
            {([
              { key: 'dynamic_pipeline' as keyof ConfigPatch, label: 'Dynamic Pipeline (composer)', hint: 'Assemble a task-specific pipeline per run' },
              { key: 'qa_gate' as keyof ConfigPatch, label: 'QA Gate' },
              { key: 'quality_monitor' as keyof ConfigPatch, label: 'Quality Monitor' },
              { key: 'static_quality' as keyof ConfigPatch, label: 'Static Quality' },
              { key: 'thinking_budget' as keyof ConfigPatch, label: 'Thinking Budget' },
              { key: 'worker_critique' as keyof ConfigPatch, label: 'Worker Critique' },
              { key: 'post_worker_smoke' as keyof ConfigPatch, label: 'Post-Worker Smoke' },
            ]).map(({ key, label, hint }) => (
              <label
                key={key}
                className="flex items-center gap-3 p-3 rounded-lg border border-gray-200 dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-800/50 cursor-pointer transition-colors"
                title={hint}
              >
                <input
                  type="checkbox"
                  checked={!!cfg[key]}
                  onChange={(e) => handleChange(key, e.target.checked)}
                  className="w-4 h-4 rounded border-gray-300 text-brand-600 focus:ring-brand-500"
                />
                <span className="text-sm font-medium">{label}</span>
              </label>
            ))}
          </div>
        </section>

        {/* Interactions */}
        <section className="card p-6 space-y-4">
          <div className="flex items-center gap-3 mb-2">
            <Wrench size={20} className="text-gray-400" />
            <h2 className="font-bold">Interactions & Permissions</h2>
          </div>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <label className="label">Permission</label>
              <select
                value={cfg.permission}
                onChange={(e) => handleChange('permission', e.target.value)}
                className="input"
              >
                <option value="auto">Auto</option>
                <option value="dry-run">Dry Run</option>
                <option value="review">Review</option>
              </select>
            </div>
            <div>
              <label className="label">Shell Permission</label>
              <select
                value={cfg.shell_permission || 'auto'}
                onChange={(e) => handleChange('shell_permission', e.target.value)}
                className="input"
              >
                <option value="auto">Auto</option>
                <option value="ask">Ask</option>
                <option value="deny">Deny</option>
              </select>
            </div>
            <div>
              <label className="label">Clarify Mode</label>
              <select
                value={cfg.clarify_mode}
                onChange={(e) => handleChange('clarify_mode', e.target.value)}
                className="input"
              >
                <option value="auto">Auto</option>
                <option value="ask">Ask</option>
                <option value="skip">Skip</option>
              </select>
            </div>
            <div>
              <label className="label">Plan Approve</label>
              <select
                value={cfg.plan_approve}
                onChange={(e) => handleChange('plan_approve', e.target.value)}
                className="input"
              >
                <option value="auto">Auto</option>
                <option value="ask">Ask</option>
                <option value="skip">Skip</option>
              </select>
            </div>
            <div>
              <label className="label">Continue Ask</label>
              <select
                value={cfg.continue_ask}
                onChange={(e) => handleChange('continue_ask', e.target.value)}
                className="input"
              >
                <option value="ask">Ask</option>
                <option value="auto">Auto</option>
              </select>
            </div>
            <div>
              <label className="label">Escalate Ask</label>
              <select
                value={cfg.escalate_ask}
                onChange={(e) => handleChange('escalate_ask', e.target.value)}
                className="input"
              >
                <option value="ask">Ask</option>
                <option value="auto">Auto</option>
              </select>
            </div>
            <label className="flex items-center gap-3">
              <input
                type="checkbox"
                checked={cfg.dry_run || false}
                onChange={(e) => handleChange('dry_run', e.target.checked)}
                className="w-4 h-4 rounded border-gray-300 text-brand-600 focus:ring-brand-500"
              />
              <span className="text-sm font-medium">Dry Run</span>
            </label>
            <label className="flex items-center gap-3">
              <input
                type="checkbox"
                checked={cfg.auto_approve || false}
                onChange={(e) => handleChange('auto_approve', e.target.checked)}
                className="w-4 h-4 rounded border-gray-300 text-brand-600 focus:ring-brand-500"
              />
              <span className="text-sm font-medium">Auto Approve</span>
            </label>
          </div>
        </section>

        {/* Harness invariants */}
        <section className="card p-6 space-y-4">
          <div className="flex items-center gap-3 mb-2">
            <ShieldCheck size={20} className="text-gray-400" />
            <h2 className="font-bold">Harness Invariants</h2>
          </div>
          <div className="grid grid-cols-2 md:grid-cols-3 gap-3">
            {[
              { key: 'write_guard' as const, label: 'Write Guard' },
              { key: 'read_before_edit' as const, label: 'Read Before Edit' },
              { key: 'tool_guidance' as const, label: 'Tool Guidance' },
              { key: 'knowledge_inject' as const, label: 'Knowledge Inject' },
              { key: 'context_compact' as const, label: 'Context Compact' },
              { key: 'react_compact' as const, label: 'ReAct Compact' },
              { key: 'session_event_log' as const, label: 'Session Event Log' },
              { key: 'auto_refine' as const, label: 'Auto Refine' },
              { key: 'wave_snapshots' as const, label: 'Wave Snapshots' },
              { key: 'hooks_enabled' as const, label: 'Hooks Enabled' },
            ].map(({ key, label }) => (
              <label
                key={key}
                className="flex items-center gap-3 p-3 rounded-lg border border-gray-200 dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-800/50 cursor-pointer transition-colors"
              >
                <input
                  type="checkbox"
                  checked={!!cfg[key]}
                  onChange={(e) => handleChange(key, e.target.checked)}
                  className="w-4 h-4 rounded border-gray-300 text-brand-600 focus:ring-brand-500"
                />
                <span className="text-sm font-medium">{label}</span>
              </label>
            ))}
          </div>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4 pt-2">
            <div>
              <label className="label">Context Compact Engine</label>
              <select
                value={cfg.context_compact_engine || 'heuristic'}
                onChange={(e) => handleChange('context_compact_engine', e.target.value)}
                className="input"
              >
                <option value="heuristic">heuristic</option>
                <option value="llm">llm</option>
                <option value="auto">auto</option>
              </select>
            </div>
            <div>
              <label className="label">Enabled Models (comma-separated)</label>
              <input
                type="text"
                value={(cfg.enabled_models || []).join(', ')}
                onChange={(e) =>
                  handleChange(
                    'enabled_models',
                    e.target.value
                      .split(',')
                      .map((s) => s.trim())
                      .filter(Boolean),
                  )
                }
                className="input"
                placeholder="empty = all models"
              />
            </div>
            <div>
              <label className="label">LLM Retry Count</label>
              <input
                type="number"
                value={cfg.llm_retry_count ?? 3}
                onChange={(e) => handleChange('llm_retry_count', parseInt(e.target.value) || 0)}
                className="input"
              />
            </div>
            <div>
              <label className="label">LLM Retry Delay (ms)</label>
              <input
                type="number"
                value={cfg.llm_retry_delay_ms ?? 1000}
                onChange={(e) => handleChange('llm_retry_delay_ms', parseInt(e.target.value) || 0)}
                className="input"
              />
            </div>
          </div>
        </section>
      </div>
    </div>
  );
}
