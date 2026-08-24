import { useState, useEffect, useCallback, useMemo } from 'react';
import { getAgents, getAgent, createAgent, updateAgent, deleteAgent, getSkills } from '@/api/client';
import type { AgentSpec, Skill } from '@/types';
import {
  Bot,
  Plus,
  Trash2,
  Edit3,
  Check,
  X,
  Cpu,
  Wrench,
  Thermometer,
  BrainCircuit,
} from 'lucide-react';
import clsx from 'clsx';
import { useConfirm } from '@/components/ui/Modal';
import { useToast } from '@/components/ui/Toast';

const EMPTY_AGENT: AgentSpec = {
  id: '',
  title: '',
  description: '',
  system_prompt: '',
  skills: [],
  model: '',
  provider: '',
  endpoint: '',
  tools: true,
  max_iter: 8,
  temperature: 0.15,
  max_tokens: 4096,
};

export default function AgentManager() {
  const confirm = useConfirm();
  const toast = useToast();
  const [agents, setAgents] = useState<AgentSpec[]>([]);
  const [loading, setLoading] = useState(true);
  const [detailLoading, setDetailLoading] = useState(false);
  const [editing, setEditing] = useState<AgentSpec | null>(null);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState<AgentSpec>(EMPTY_AGENT);
  const [allSkills, setAllSkills] = useState<Skill[]>([]);
  const [skillsLoaded, setSkillsLoaded] = useState(false);
  const [skillSearch, setSkillSearch] = useState('');

  const fetch = useCallback(async () => {
    try {
      const list = await getAgents();
      setAgents(list);
    } catch (e) {
      console.error('Failed to load agents:', e);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetch();
  }, [fetch]);

  // Fetch all skills once for the multi-select picker (fallback to text input on failure/empty)
  useEffect(() => {
    getSkills()
      .then((list) => setAllSkills(list || []))
      .catch((e) => {
        console.error('Failed to load skills:', e);
        setAllSkills([]);
      })
      .finally(() => setSkillsLoaded(true));
  }, []);

  const toggleSkill = useCallback((name: string) => {
    setForm((f) => {
      const current = f.skills || [];
      return {
        ...f,
        skills: current.includes(name) ? current.filter((s) => s !== name) : [...current, name],
      };
    });
  }, []);

  // Alphabetically sorted union of known skills + still-selected skills (marked missing if gone)
  const skillChips = useMemo(() => {
    const selected = form.skills || [];
    const union = new Set([...allSkills.map((s) => s.name), ...selected]);
    const term = skillSearch.trim().toLowerCase();
    return [...union]
      .sort((a, b) => a.localeCompare(b))
      .filter((name) => !term || name.toLowerCase().includes(term));
  }, [allSkills, form.skills, skillSearch]);

  const handleCreate = () => {
    setForm({ ...EMPTY_AGENT, id: `custom-${Date.now()}` });
    setCreating(true);
    setEditing(null);
  };

  const handleEdit = async (agent: AgentSpec) => {
    setEditing(agent);
    setCreating(false);
    setDetailLoading(true);
    try {
      const detail = await getAgent(agent.id);
      setForm({ ...detail });
    } catch (e) {
      console.error('Failed to load agent detail:', e);
      // Fall back to list data (prompts may be missing)
      setForm({ ...agent });
    } finally {
      setDetailLoading(false);
    }
  };

  const handleCancel = () => {
    setCreating(false);
    setEditing(null);
    setForm(EMPTY_AGENT);
  };

  const handleSave = async () => {
    if (!form.id.trim() || !(form.title || '').trim()) return;
    try {
      if (creating) {
        await createAgent(form);
      } else if (editing) {
        await updateAgent(editing.id, form);
      }
      await fetch();
      handleCancel();
    } catch (e) {
      toast.reportError(e, 'Could not save the agent');
    }
  };

  const handleDelete = async (agent: AgentSpec) => {
    const ok = await confirm({
      title: `Delete agent "${agent.title}"?`,
      description: 'Custom agent files and builtin overrides are removed.',
      confirmLabel: 'Delete agent',
    });
    if (!ok) return;
    try {
      await deleteAgent(agent.id);
      await fetch();
    } catch (e) {
      toast.reportError(e, 'Could not delete the agent');
    }
  };

  const showForm = creating || editing;

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full text-gray-400">
        <div className="w-5 h-5 border-2 border-brand-500 border-t-transparent rounded-full animate-spin mr-3" />
        Loading agents…
      </div>
    );
  }

  return (
    <div className="h-full overflow-auto">
      <div className="max-w-4xl mx-auto p-6 space-y-6">
        {/* Header */}
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold">Agent Specialists</h1>
            <p className="text-sm text-gray-500 mt-1">Custom agent roles for the orchestration pipeline</p>
          </div>
          <button onClick={handleCreate} className="btn-primary text-sm gap-2">
            <Plus size={16} />
            New Agent
          </button>
        </div>

        {/* Agent Form — modal overlay */}
        {showForm && (
          <div
            role="button"
            tabIndex={0}
            aria-label="Close dialog"
            className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/50 p-4 backdrop-blur-sm sm:p-6"
            onClick={(e) => {
              if (e.target === e.currentTarget) handleCancel();
            }}
            onKeyDown={(e) => {
              if (e.key === 'Escape') {
                handleCancel();
              } else if ((e.key === 'Enter' || e.key === ' ') && e.target === e.currentTarget) {
                e.preventDefault();
                handleCancel();
              }
            }}
          >
            <div
              className="card my-4 flex max-h-[88vh] w-full max-w-2xl flex-col overflow-hidden"
              role="dialog"
              aria-modal="true"
              aria-label={creating ? 'Create Agent' : `Edit ${editing?.title || ''}`}
            >
              <div className="flex items-center justify-between border-b border-gray-200 px-6 py-4 dark:border-gray-800">
                <h2 className="font-bold text-lg">
                  {creating ? 'Create Agent' : `Edit ${editing?.title}`}
                  {editing?.builtin && (
                    <span className="badge-neutral ml-2 text-xs align-middle">built-in</span>
                  )}
                  {editing?.override && (
                    <span className="badge-brand ml-2 text-xs align-middle">override</span>
                  )}
                </h2>
                <div className="flex items-center gap-2">
                  <button onClick={handleCancel} className="btn-ghost text-sm">Cancel</button>
                  <button onClick={handleSave} className="btn-primary text-sm gap-1.5">
                    <Check size={14} />
                    Save
                  </button>
                </div>
              </div>

              <div className="flex-1 space-y-4 overflow-y-auto px-6 py-4">
                {detailLoading && (
                  <div className="flex items-center justify-center py-12 text-gray-400">
                    <div className="w-5 h-5 border-2 border-brand-500 border-t-transparent rounded-full animate-spin mr-3" />
                    Loading agent detail…
                  </div>
                )}

                {!detailLoading && (
                  <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                    <div>
                      <label className="label" htmlFor="agent-id-input">Agent ID</label>
                      <input
                        id="agent-id-input"
                        type="text"
                        value={form.id}
                        onChange={(e) => setForm({ ...form, id: e.target.value })}
                        className="input-mono"
                        placeholder="my-agent"
                        disabled={!!editing}
                      />
                    </div>
                    <div>
                      <label className="label" htmlFor="agent-title-input">Title</label>
                      <input
                        id="agent-title-input"
                        type="text"
                        value={form.title}
                        onChange={(e) => setForm({ ...form, title: e.target.value })}
                        className="input"
                        placeholder="My Agent"
                      />
                    </div>
                    <div className="md:col-span-2">
                      <label className="label" htmlFor="agent-description-input">Description</label>
                      <input
                        id="agent-description-input"
                        type="text"
                        value={form.description}
                        onChange={(e) => setForm({ ...form, description: e.target.value })}
                        className="input"
                        placeholder="What this agent does…"
                      />
                    </div>
                    <div className="md:col-span-2">
                      <label className="label" htmlFor="agent-system-prompt-textarea">System Prompt</label>
                      <textarea
                        id="agent-system-prompt-textarea"
                        value={form.system_prompt || ''}
                        onChange={(e) => setForm({ ...form, system_prompt: e.target.value })}
                        className="input font-mono text-xs h-48 resize-y"
                        placeholder="You are a specialist agent…"
                      />
                      <p className="text-[10px] text-gray-400 mt-1">
                        Base instructions for this agent. At runtime, the orchestrator automatically injects: project context (PROJECT.md, CONTEXT.md), matched skills, current plan, task details, and tool guidance. Keep this prompt focused on the agent's core role and output format.
                      </p>
                    </div>
                    <fieldset className="md:col-span-2 m-0 border-0 p-0">
                      <legend className="label">Skills</legend>
                      {!skillsLoaded ? (
                        <p className="text-xs text-gray-400">Loading skills…</p>
                      ) : allSkills.length === 0 ? (
                        <input
                          type="text"
                          value={form.skills?.join(', ') || ''}
                          onChange={(e) =>
                            setForm({ ...form, skills: e.target.value.split(',').map((s) => s.trim()).filter(Boolean) })
                          }
                          className="input"
                          placeholder="react, typescript, testing"
                        />
                      ) : (
                        <>
                          <div className="flex items-center justify-between mb-2">
                            <span className="text-xs text-gray-400">
                              {(form.skills || []).length} selected
                            </span>
                            {allSkills.length >= 10 && (
                              <input
                                type="text"
                                value={skillSearch}
                                onChange={(e) => setSkillSearch(e.target.value)}
                                className="input text-xs py-1 w-48"
                                placeholder="Filter skills…"
                              />
                            )}
                          </div>
                          <div className="flex flex-wrap gap-1.5">
                            {skillChips.map((name) => {
                              const selected = (form.skills || []).includes(name);
                              const missing = !allSkills.some((s) => s.name === name);
                              return (
                                <button
                                  key={name}
                                  type="button"
                                  onClick={() => toggleSkill(name)}
                                  title={missing ? `${name} — not found in skill registry` : name}
                                  className={clsx(
                                    'px-2 py-1 rounded-full text-[11px] border transition-colors',
                                    selected
                                      ? 'bg-brand-500 text-white border-brand-500'
                                      : 'bg-gray-100 dark:bg-gray-800 border-gray-200 dark:border-gray-700 text-gray-600 dark:text-gray-300 hover:border-brand-400',
                                  )}
                                >
                                  {name}
                                  {missing && <span className="ml-1 text-amber-200">missing</span>}
                                </button>
                              );
                            })}
                            {skillChips.length === 0 && (
                              <span className="text-xs text-gray-400">No skills match &quot;{skillSearch}&quot;.</span>
                            )}
                          </div>
                        </>
                      )}
                    </fieldset>
                    <div>
                      <label className="label" htmlFor="agent-temperature-input">Temperature</label>
                      <input
                        id="agent-temperature-input"
                        type="number"
                        step="0.01"
                        min="0"
                        max="2"
                        value={form.temperature}
                        onChange={(e) => setForm({ ...form, temperature: parseFloat(e.target.value) })}
                        className="input"
                      />
                    </div>
                    <div>
                      <label className="label" htmlFor="agent-max-tokens-input">Max Tokens</label>
                      <input
                        id="agent-max-tokens-input"
                        type="number"
                        value={form.max_tokens}
                        onChange={(e) => setForm({ ...form, max_tokens: parseInt(e.target.value) })}
                        className="input"
                      />
                    </div>
                    <div>
                      <label className="label" htmlFor="agent-max-iter-input">Max Iterations</label>
                      <input
                        id="agent-max-iter-input"
                        type="number"
                        value={form.max_iter || 8}
                        onChange={(e) => setForm({ ...form, max_iter: parseInt(e.target.value) })}
                        className="input"
                      />
                    </div>
                    <div>
                      <label className="label" htmlFor="agent-model-input">Model (override)</label>
                      <input
                        id="agent-model-input"
                        type="text"
                        value={form.model || ''}
                        onChange={(e) => setForm({ ...form, model: e.target.value })}
                        className="input-mono"
                        placeholder="empty = inherit stack/global"
                      />
                    </div>
                    <div>
                      <label className="label" htmlFor="agent-provider-input">Provider (override)</label>
                      <input
                        id="agent-provider-input"
                        type="text"
                        value={form.provider || ''}
                        onChange={(e) => setForm({ ...form, provider: e.target.value })}
                        className="input-mono"
                        placeholder="empty = inherit stack/global"
                      />
                    </div>
                    <div className="md:col-span-2">
                      <label className="label" htmlFor="agent-endpoint-input">Endpoint (override)</label>
                      <input
                        id="agent-endpoint-input"
                        type="text"
                        value={form.endpoint || ''}
                        onChange={(e) => setForm({ ...form, endpoint: e.target.value })}
                        className="input-mono"
                        placeholder="empty = provider default / global endpoint"
                      />
                      <p className="text-[10px] text-gray-400 mt-1">
                        Resolution: agent override → active stack / global config. Leave blank to inherit.
                      </p>
                    </div>
                    <label className="flex items-center gap-3">
                      <input
                        type="checkbox"
                        checked={form.tools !== false}
                        onChange={(e) => setForm({ ...form, tools: e.target.checked })}
                        className="w-4 h-4 rounded border-gray-300 text-brand-600 focus:ring-brand-500"
                      />
                      <span className="text-sm">Enable built-in tools</span>
                    </label>
                  </div>
                )}
              </div>
            </div>
          </div>
        )}

        {/* Agent list */}
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
          {agents.map((agent) => (
            <div
              key={agent.id}
              className="card-hover group flex min-h-[17rem] flex-col p-4"
            >
              <div className="flex items-start justify-between gap-3">
                <div className="flex min-w-0 items-start gap-3">
                  <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-brand-100 dark:bg-brand-900/30">
                    <Bot size={20} className="text-brand-600" />
                  </div>
                  <div className="min-w-0">
                    <h3 className="text-sm font-bold leading-snug line-clamp-2" title={agent.title || agent.id}>
                      {agent.title || agent.id}
                    </h3>
                    <p className="font-mono text-[10px] text-gray-400 truncate" title={agent.id}>{agent.id}</p>
                  </div>
                </div>
                <div className="flex shrink-0 items-center gap-1 opacity-100 transition-opacity sm:opacity-0 sm:group-hover:opacity-100">
                  <button
                    onClick={() => handleEdit(agent)}
                    className="btn-ghost p-1.5 rounded-lg"
                    title="Edit"
                  >
                    <Edit3 size={14} />
                  </button>
                  <button
                    onClick={() => handleDelete(agent)}
                    className="btn-ghost p-1.5 rounded-lg text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20"
                    title="Delete"
                  >
                    <Trash2 size={14} />
                  </button>
                </div>
              </div>

              {agent.description && (
                <details className="mt-3 rounded-lg border border-gray-100 bg-gray-50/70 p-2 dark:border-gray-800 dark:bg-gray-800/40">
                  <summary className="cursor-pointer list-none text-xs font-medium text-gray-700 line-clamp-3 dark:text-gray-200" title={agent.description}>
                    {agent.description}
                  </summary>
                  <p className="mt-2 whitespace-pre-wrap break-words text-xs leading-relaxed text-gray-600 dark:text-gray-300">
                    {agent.description}
                  </p>
                </details>
              )}

              {agent.system_prompt && (
                <details className="mt-2 rounded-lg border border-gray-100 bg-gray-50/70 p-2 dark:border-gray-800 dark:bg-gray-800/40">
                  <summary className="cursor-pointer list-none font-mono text-[10px] text-gray-500 line-clamp-2 dark:text-gray-400" title={agent.system_prompt}>
                    {agent.system_prompt}
                  </summary>
                  <pre className="mt-2 max-h-44 overflow-auto whitespace-pre-wrap break-words font-mono text-[10px] leading-relaxed text-gray-600 dark:text-gray-300">
                    {agent.system_prompt}
                  </pre>
                </details>
              )}

              <div className="mt-auto flex items-center gap-2 pt-3 flex-wrap">
                {agent.custom && <span className="badge-brand">custom</span>}
                {!agent.custom && <span className="badge-neutral">built-in</span>}
                {agent.override && <span className="badge-brand">override</span>}
                {agent.skills && agent.skills.length > 0 && (
                  <span className="badge-brand" title={agent.skills.join(', ')}>
                    {agent.skills.length} skills
                  </span>
                )}
                <span
                  className="badge-neutral flex max-w-full items-center gap-1 whitespace-normal break-all text-left"
                  title={`Effective LLM after stack inheritance: ${agent.effective_provider || agent.provider || 'stack'}/${agent.effective_model || agent.model || 'inherit'}`}
                >
                  <Cpu size={10} className="shrink-0" />
                  <span className="min-w-0">
                    {agent.effective_provider || agent.provider || 'stack'}/
                    {agent.effective_model || agent.model || 'inherit'}
                  </span>
                </span>
                {agent.inherits_model && agent.inherits_provider && (
                  <span className="badge-neutral">inherits stack</span>
                )}
              </div>

              <div className="mt-3 grid grid-cols-3 gap-2 text-[10px] text-gray-400">
                <div className="flex min-w-0 items-center gap-1">
                  <Thermometer size={10} />
                  {agent.temperature}
                </div>
                <div className="flex min-w-0 items-center gap-1" title={`${agent.max_tokens} tokens`}>
                  <BrainCircuit size={10} />
                  {agent.max_tokens} tok
                </div>
                <div className="flex min-w-0 items-center gap-1">
                  <Wrench size={10} />
                  {agent.tools !== false ? 'tools' : 'no tools'}
                </div>
              </div>
            </div>
          ))}
        </div>

        {agents.length === 0 && !showForm && (
          <div className="text-center py-12 text-gray-400">
            <Bot size={48} className="mx-auto mb-3 opacity-50" />
            <p className="text-sm">No custom agents defined yet.</p>
            <p className="text-xs mt-1">Built-in specialists are always available.</p>
          </div>
        )}
      </div>
    </div>
  );
}
