import { useState, useEffect, useCallback, useMemo } from 'react';
import { getAgents, getAgent, createAgent, updateAgent, deleteAgent, getSkills } from '@/api/client';
import type { AgentSpec, Skill } from '@/types';
import {
  ArrowRight,
  Bot,
  Plus,
  Trash2,
  Edit3,
  Check,
  Cpu,
  Wrench,
  Thermometer,
  BrainCircuit,
} from 'lucide-react';
import clsx from 'clsx';
import { useConfirm } from '@/components/ui/Modal';
import { useToast } from '@/components/ui/Toast';
import ErrorState from '@/components/ui/ErrorState';

const PREFERRED_CATEGORY_ORDER = [
  'Core',
  'Specialists',
  'HVE RPI',
  'HVE Discovery & UX',
  'HVE Product & Architecture',
  'HVE Review & Quality',
  'HVE Security, RAI & A11y',
  'HVE Data Science & Meta',
];

const CATEGORY_ALIASES: Record<string, string> = {
  core: 'Core',
  specialist: 'Specialists',
  specialists: 'Specialists',
  'hve-rpi': 'HVE RPI',
  'hve rpi': 'HVE RPI',
  'hve-discovery-ux': 'HVE Discovery & UX',
  'hve discovery & ux': 'HVE Discovery & UX',
  'hve-product-architecture': 'HVE Product & Architecture',
  'hve product & architecture': 'HVE Product & Architecture',
  'hve-review-quality': 'HVE Review & Quality',
  'hve review & quality': 'HVE Review & Quality',
  'hve-security-rai-a11y': 'HVE Security, RAI & A11y',
  'hve security, rai & a11y': 'HVE Security, RAI & A11y',
  'hve-data-science-meta': 'HVE Data Science & Meta',
  'hve data science & meta': 'HVE Data Science & Meta',
};

const CORE_ROLE_IDS = new Set([
  'planner',
  'scoper',
  'architect',
  'worker',
  'coder',
  'reviewer',
  'corrector',
  'verifier',
  'tester',
  'qa',
  'researcher',
  'dispatcher',
  'project-manager',
  'manager',
  'lead',
  'synthesizer',
  'evolver',
]);

interface NormalizedHandoff {
  target: string;
  label: string;
  prompt?: string;
}

function getRawCategory(agent: AgentSpec): string {
  const rec = agent as unknown as { category?: unknown };
  if (typeof rec.category !== 'string') return '';
  const trimmed = rec.category.trim();
  if (!trimmed) return '';
  return CATEGORY_ALIASES[trimmed.toLowerCase()] ?? trimmed;
}

function resolveAgentCategory(agent: AgentSpec): string {
  const explicit = getRawCategory(agent);
  if (explicit) return explicit;
  if (CORE_ROLE_IDS.has(agent.id)) return 'Core';
  return 'Specialists';
}

function getAgentHandoffs(agent: AgentSpec): NormalizedHandoff[] {
  const rec = agent as unknown as { handoffs?: unknown };
  if (!Array.isArray(rec.handoffs)) return [];
  const out: NormalizedHandoff[] = [];
  const seen = new Set<string>();
  for (const item of rec.handoffs) {
    if (typeof item === 'string' && item.trim()) {
      const target = item.trim();
      if (!seen.has(target)) {
        seen.add(target);
        out.push({ target, label: target });
      }
    } else if (item && typeof item === 'object') {
      const obj = item as { agent?: unknown; to?: unknown; id?: unknown; label?: unknown; prompt?: unknown };
      const target =
        typeof obj.agent === 'string'
          ? obj.agent.trim()
          : typeof obj.to === 'string'
            ? obj.to.trim()
            : typeof obj.id === 'string'
              ? obj.id.trim()
              : '';
      if (target && !seen.has(`${target}:${typeof obj.label === 'string' ? obj.label : ''}`)) {
        seen.add(`${target}:${typeof obj.label === 'string' ? obj.label : ''}`);
        const label = typeof obj.label === 'string' && obj.label.trim() ? obj.label.trim() : target;
        const prompt = typeof obj.prompt === 'string' && obj.prompt.trim() ? obj.prompt.trim() : undefined;
        out.push({ target, label, prompt });
      }
    }
  }
  return out;
}

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
  const [loadError, setLoadError] = useState<unknown>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [editing, setEditing] = useState<AgentSpec | null>(null);
  const [creating, setCreating] = useState(false);
  const [form, setForm] = useState<AgentSpec>(EMPTY_AGENT);
  const [allSkills, setAllSkills] = useState<Skill[]>([]);
  const [skillsLoaded, setSkillsLoaded] = useState(false);
  const [skillSearch, setSkillSearch] = useState('');
  const [selectedCategory, setSelectedCategory] = useState<string>('All');
  const [focusedAgentId, setFocusedAgentId] = useState<string | null>(null);

  const categoryCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const a of agents) {
      const cat = resolveAgentCategory(a);
      counts.set(cat, (counts.get(cat) ?? 0) + 1);
    }
    return counts;
  }, [agents]);

  const categoryTabs = useMemo(() => {
    const present = new Set<string>(['Core', 'Specialists', ...categoryCounts.keys()]);
    const ordered = [
      ...PREFERRED_CATEGORY_ORDER.filter((c) => present.has(c)),
      ...[...present]
        .filter((c) => !PREFERRED_CATEGORY_ORDER.includes(c))
        .sort((a, b) => a.localeCompare(b)),
    ].filter((c) => c === 'Core' || c === 'Specialists' || (categoryCounts.get(c) ?? 0) > 0);
    return [
      { name: 'All', count: agents.length },
      ...ordered.map((name) => ({ name, count: categoryCounts.get(name) ?? 0 })),
    ];
  }, [agents.length, categoryCounts]);

  const visibleAgents = useMemo(() => {
    if (selectedCategory === 'All') return agents;
    return agents.filter((a) => resolveAgentCategory(a) === selectedCategory);
  }, [agents, selectedCategory]);

  const handleFocusHandoff = useCallback(
    (targetId: string) => {
      const target = agents.find((a) => a.id === targetId);
      if (target && selectedCategory !== 'All' && resolveAgentCategory(target) !== selectedCategory) {
        setSelectedCategory('All');
      }
      setFocusedAgentId(targetId);
      if (editing || creating) {
        setCreating(false);
        setEditing(null);
      }
    },
    [agents, creating, editing, selectedCategory],
  );

  useEffect(() => {
    if (!focusedAgentId) return undefined;
    const id = window.requestAnimationFrame(() => {
      const el = document.getElementById(`agent-card-${focusedAgentId}`);
      if (el) {
        el.scrollIntoView?.({ behavior: 'smooth', block: 'center' });
        el.querySelector<HTMLElement>('button')?.focus({ preventScroll: true });
      }
    });
    return () => window.cancelAnimationFrame(id);
  }, [focusedAgentId, selectedCategory]);

  const fetch = useCallback(async () => {
    try {
      const list = await getAgents();
      setAgents(list);
      setLoadError(null);
    } catch (e) {
      // Shown in place of the list with a Retry; an empty list over a dead
      // backend used to read as "no custom agents defined yet".
      setLoadError(e);
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
        toast.reportError(e, 'Could not load the skill list for the picker');
        setAllSkills([]);
      })
      .finally(() => setSkillsLoaded(true));
  }, [toast]);

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
      // Fall back to list data (prompts may be missing) — and say so, or a
      // save from this form silently blanks the system prompt.
      toast.push({
        tone: 'warning',
        title: `Editing ${agent.title || agent.id} from the summary only`,
        detail: `The full spec could not be loaded (${e instanceof Error ? e.message : String(e)}); prompts may be missing.`,
      });
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
      <div className="mx-auto w-full max-w-[120rem] space-y-6 p-4 2xl:p-8">
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
                  {editing && getRawCategory(editing) && (
                    <span className="badge-brand ml-2 text-xs align-middle">{getRawCategory(editing)}</span>
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
                    {getAgentHandoffs(form).length > 0 && (
                      <div className="md:col-span-2 rounded-lg border border-brand-200/70 bg-brand-50/40 p-3 dark:border-brand-900/50 dark:bg-brand-950/20">
                        <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-brand-700 dark:text-brand-300">
                          Declarative Handoffs
                        </div>
                        <div className="flex flex-wrap gap-1.5">
                          {getAgentHandoffs(form).map((h) => (
                            <button
                              key={`${h.target}-${h.label}`}
                              type="button"
                              onClick={() => handleFocusHandoff(h.target)}
                              title={h.prompt ? `${h.label} → ${h.target}\n${h.prompt}` : `Jump to ${h.target}`}
                              className="inline-flex items-center gap-1 rounded-full border border-brand-300/80 bg-white px-2.5 py-0.5 font-mono text-[11px] font-medium text-brand-700 shadow-sm transition-all duration-150 hover:-translate-y-px hover:border-brand-500 hover:bg-brand-50 dark:border-brand-800 dark:bg-gray-900 dark:text-brand-300 dark:hover:bg-brand-900/40"
                            >
                              <span>{h.label}</span>
                              <ArrowRight size={11} className="shrink-0 opacity-75" />
                              <span className="opacity-75">{h.target}</span>
                            </button>
                          ))}
                        </div>
                      </div>
                    )}
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

        {/* Category Filter Bar */}
        {agents.length > 0 && (
          <div
            className="flex flex-wrap items-center gap-1.5 rounded-xl border border-gray-200/80 bg-white/80 p-1.5 shadow-sm backdrop-blur-sm dark:border-gray-800 dark:bg-gray-900/70"
            role="tablist"
            aria-label="Filter agents by category"
            data-testid="agent-category-bar"
          >
            {categoryTabs.map((tab) => {
              const active = selectedCategory === tab.name;
              return (
                <button
                  key={tab.name}
                  type="button"
                  role="tab"
                  aria-selected={active}
                  onClick={() => setSelectedCategory(tab.name)}
                  data-testid={`agent-category-${tab.name}`}
                  className={clsx(
                    'inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-all duration-200',
                    active
                      ? 'bg-brand-600 text-white shadow-sm dark:bg-brand-500'
                      : 'text-gray-600 hover:bg-gray-100 hover:text-gray-900 dark:text-gray-300 dark:hover:bg-gray-800 dark:hover:text-gray-100',
                  )}
                >
                  <span>{tab.name}</span>
                  <span
                    className={clsx(
                      'rounded-full px-1.5 py-0.2 text-[10px] font-semibold tabular-nums transition-colors duration-200',
                      active
                        ? 'bg-white/20 text-white'
                        : 'bg-gray-200/70 text-gray-600 dark:bg-gray-800 dark:text-gray-400',
                    )}
                  >
                    {tab.count}
                  </span>
                </button>
              );
            })}
          </div>
        )}

        {/* Agent list */}
        <div className="grid gap-3 [grid-template-columns:repeat(auto-fill,minmax(min(22rem,100%),1fr))]">
          {visibleAgents.map((agent) => {
            const rawCategory = getRawCategory(agent);
            const categoryLabel = rawCategory || resolveAgentCategory(agent);
            const handoffs = getAgentHandoffs(agent);
            const isFocused = focusedAgentId === agent.id;
            return (
              <div
                key={agent.id}
                id={`agent-card-${agent.id}`}
                data-testid={`agent-card-${agent.id}`}
                data-selected={isFocused ? 'true' : undefined}
                className={clsx(
                  'card-hover group flex min-h-[17rem] flex-col p-4 transition-all duration-200',
                  isFocused && 'ring-2 ring-brand-500 shadow-lg bg-brand-50/20 dark:bg-brand-950/20',
                )}
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
                      onClick={() => void handleEdit(agent)}
                      className="btn-ghost p-1.5 rounded-lg"
                      title="Edit"
                    >
                      <Edit3 size={14} />
                    </button>
                    <button
                      onClick={() => void handleDelete(agent)}
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

                {handoffs.length > 0 && (
                  <div className="mt-2.5 flex flex-wrap items-center gap-1" data-testid={`agent-handoffs-${agent.id}`}>
                    <span className="mr-0.5 text-[10px] font-semibold uppercase tracking-wider text-gray-400">
                      Handoffs:
                    </span>
                    {handoffs.map((h) => (
                      <button
                        key={`${h.target}-${h.label}`}
                        type="button"
                        onClick={() => handleFocusHandoff(h.target)}
                        title={h.prompt ? `${h.label} → ${h.target}\n${h.prompt}` : `Select ${h.target}`}
                        data-testid={`handoff-chip-${agent.id}-${h.target}`}
                        className="inline-flex items-center gap-1 rounded-full border border-brand-200/80 bg-brand-50/60 px-2 py-0.5 font-mono text-[10px] font-medium text-brand-700 transition-all duration-150 hover:-translate-y-px hover:border-brand-400 hover:bg-brand-100/80 hover:shadow-sm dark:border-brand-800/70 dark:bg-brand-950/40 dark:text-brand-300 dark:hover:bg-brand-900/50"
                      >
                        <ArrowRight size={9} className="shrink-0" />
                        <span>{h.label}</span>
                      </button>
                    ))}
                  </div>
                )}

                <div className="mt-auto flex items-center gap-2 pt-3 flex-wrap">
                  {categoryLabel && (
                    <span
                      className={clsx(
                        categoryLabel.startsWith('HVE') ? 'badge-brand' : 'badge-neutral',
                      )}
                      data-testid={`agent-category-badge-${agent.id}`}
                    >
                      {categoryLabel}
                    </span>
                  )}
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
            );
          })}
        </div>

        {loadError !== null && !showForm && <ErrorState error={loadError} what="agents" onRetry={fetch} />}

        {!loadError && agents.length === 0 && !showForm && (
          <div className="text-center py-12 text-gray-400">
            <Bot size={48} className="mx-auto mb-3 opacity-50" />
            <p className="text-sm">No agents yet.</p>
            <p className="text-xs mt-1">
              An agent is a role with its own prompt, model and skills — the pipeline seats one per phase. Built-in
              specialists are always available; add one here to override a builtin or introduce a new role.
            </p>
            <button type="button" onClick={handleCreate} className="btn-primary mx-auto mt-4 gap-2 text-sm">
              <Plus size={14} />
              Create your first agent
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
