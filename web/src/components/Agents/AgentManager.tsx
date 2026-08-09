import { useState, useEffect, useCallback } from 'react';
import { getAgents, getAgent, createAgent, updateAgent, deleteAgent } from '@/api/client';
import type { AgentSpec } from '@/types';
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
  const [agents, setAgents] = useState<AgentSpec[]>([]);
  const [loading, setLoading] = useState(true);
  const [detailLoading, setDetailLoading] = useState(false);
  const [editing, setEditing] = useState<AgentSpec | null>(null);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState<AgentSpec>(EMPTY_AGENT);

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
      console.error('Save failed:', e);
    }
  };

  const handleDelete = async (agent: AgentSpec) => {
    if (!confirm(`Delete agent "${agent.title}"?`)) return;
    try {
      await deleteAgent(agent.id);
      await fetch();
    } catch (e) {
      console.error('Delete failed:', e);
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

        {/* Agent Form */}
        {showForm && (
          <div className="card p-6 space-y-4 border-brand-300 dark:border-brand-700 animate-slide-up">
            <div className="flex items-center justify-between">
              <h2 className="font-bold text-lg">
                {creating ? 'Create Agent' : `Edit ${editing?.title}`}
                {editing?.builtin && (
                  <span className="badge-neutral ml-2 text-xs align-middle">built-in</span>
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

            {detailLoading && (
              <div className="flex items-center justify-center py-12 text-gray-400">
                <div className="w-5 h-5 border-2 border-brand-500 border-t-transparent rounded-full animate-spin mr-3" />
                Loading agent detail…
              </div>
            )}

            {!detailLoading && (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div>
                <label className="label">Agent ID</label>
                <input
                  type="text"
                  value={form.id}
                  onChange={(e) => setForm({ ...form, id: e.target.value })}
                  className="input-mono"
                  placeholder="my-agent"
                  disabled={!!editing}
                />
              </div>
              <div>
                <label className="label">Title</label>
                <input
                  type="text"
                  value={form.title}
                  onChange={(e) => setForm({ ...form, title: e.target.value })}
                  className="input"
                  placeholder="My Agent"
                />
              </div>
              <div className="md:col-span-2">
                <label className="label">Description</label>
                <input
                  type="text"
                  value={form.description}
                  onChange={(e) => setForm({ ...form, description: e.target.value })}
                  className="input"
                  placeholder="What this agent does…"
                />
              </div>
              <div className="md:col-span-2">
                <label className="label">System Prompt</label>
                <textarea
                  value={form.system_prompt || ''}
                  onChange={(e) => setForm({ ...form, system_prompt: e.target.value })}
                  className="input font-mono text-xs h-32 resize-none"
                  placeholder="You are a specialist agent…"
                />
                <p className="text-[10px] text-gray-400 mt-1">
                  Base instructions for this agent. At runtime, the orchestrator automatically injects: project context (PROJECT.md, CONTEXT.md), matched skills, current plan, task details, and tool guidance. Keep this prompt focused on the agent's core role and output format.
                </p>
              </div>
              <div>
                <label className="label">Skills (comma separated)</label>
                <input
                  type="text"
                  value={form.skills?.join(', ') || ''}
                  onChange={(e) =>
                    setForm({ ...form, skills: e.target.value.split(',').map((s) => s.trim()).filter(Boolean) })
                  }
                  className="input"
                  placeholder="react, typescript, testing"
                />
              </div>
              <div>
                <label className="label">Temperature</label>
                <input
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
                <label className="label">Max Tokens</label>
                <input
                  type="number"
                  value={form.max_tokens}
                  onChange={(e) => setForm({ ...form, max_tokens: parseInt(e.target.value) })}
                  className="input"
                />
              </div>
              <div>
                <label className="label">Max Iterations</label>
                <input
                  type="number"
                  value={form.max_iter || 8}
                  onChange={(e) => setForm({ ...form, max_iter: parseInt(e.target.value) })}
                  className="input"
                />
              </div>
              <div>
                <label className="label">Model (override)</label>
                <input
                  type="text"
                  value={form.model || ''}
                  onChange={(e) => setForm({ ...form, model: e.target.value })}
                  className="input-mono"
                  placeholder="empty = inherit stack/global"
                />
              </div>
              <div>
                <label className="label">Provider (override)</label>
                <input
                  type="text"
                  value={form.provider || ''}
                  onChange={(e) => setForm({ ...form, provider: e.target.value })}
                  className="input-mono"
                  placeholder="empty = inherit stack/global"
                />
              </div>
              <div className="md:col-span-2">
                <label className="label">Endpoint (override)</label>
                <input
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
        )}

        {/* Agent list */}
        <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
          {agents.map((agent) => (
            <div
              key={agent.id}
              className="card-hover p-4 group"
            >
              <div className="flex items-start justify-between">
                <div className="flex items-center gap-3">
                  <div className="w-10 h-10 rounded-xl bg-brand-100 dark:bg-brand-900/30 flex items-center justify-center">
                    <Bot size={20} className="text-brand-600" />
                  </div>
                  <div>
                    <h3 className="font-bold text-sm">{agent.title}</h3>
                    <p className="text-[10px] font-mono text-gray-400">{agent.id}</p>
                  </div>
                </div>
                <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
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
                <p className="text-xs text-gray-500 mt-3 line-clamp-2">{agent.description}</p>
              )}

              <div className="flex items-center gap-2 mt-3 flex-wrap">
                {agent.custom && <span className="badge-brand">custom</span>}
                {!agent.custom && <span className="badge-neutral">built-in</span>}
                {agent.override && <span className="badge-brand">override</span>}
                {agent.skills && agent.skills.length > 0 && (
                  <span className="badge-brand">{agent.skills.length} skills</span>
                )}
                <span className="badge-neutral flex items-center gap-1" title="Effective LLM after stack inheritance">
                  <Cpu size={10} />
                  {agent.effective_provider || agent.provider || 'stack'}/
                  {agent.effective_model || agent.model || 'inherit'}
                </span>
                {agent.inherits_model && agent.inherits_provider && (
                  <span className="badge-neutral">inherits stack</span>
                )}
              </div>

              <div className="grid grid-cols-3 gap-2 mt-3 text-[10px] text-gray-400">
                <div className="flex items-center gap-1">
                  <Thermometer size={10} />
                  {agent.temperature}
                </div>
                <div className="flex items-center gap-1">
                  <BrainCircuit size={10} />
                  {agent.max_tokens} tok
                </div>
                <div className="flex items-center gap-1">
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
