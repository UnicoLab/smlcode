import { useState, useEffect, useCallback, useMemo } from 'react';
import { getSkills, getSkill, createSkill, updateSkill, deleteSkill, getAgents } from '@/api/client';
import type { Skill, AgentSpec } from '@/types';
import {
  Puzzle,
  Plus,
  Trash2,
  Eye,
  EyeOff,
  Tag,
  User,
  FileText,
  X,
  Check,
  Edit3,
} from 'lucide-react';
import clsx from 'clsx';

export default function SkillManager() {
  const [skills, setSkills] = useState<Skill[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState('');
  const [newDesc, setNewDesc] = useState('');
  const [newAgentsList, setNewAgentsList] = useState<string[]>([]);
  const [expanded, setExpanded] = useState<string | null>(null);
  // Edit state: skill name being edited + form fields
  const [editingName, setEditingName] = useState<string | null>(null);
  const [editForm, setEditForm] = useState<{ name: string; description: string; agents: string[]; triggers: string[]; user_invocable: boolean; body: string }>({
    name: '', description: '', agents: [], triggers: [], user_invocable: false, body: '',
  });
  const [editLoading, setEditLoading] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  // All known agents (deduped by id) for the toggle-chip picker
  const [allAgents, setAllAgents] = useState<AgentSpec[]>([]);
  const [agentsLoaded, setAgentsLoaded] = useState(false);
  const [agentSearch, setAgentSearch] = useState('');

  const fetch = useCallback(async () => {
    try {
      const list = await getSkills();
      setSkills(list);
    } catch (e) {
      console.error('Failed to load skills:', e);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetch();
  }, [fetch]);

  // Fetch all agents once for the toggle-chip picker (dedupe by id)
  useEffect(() => {
    getAgents()
      .then((list) => {
        const seen = new Set<string>();
        const deduped: AgentSpec[] = [];
        for (const a of list || []) {
          if (!a.id || seen.has(a.id)) continue;
          seen.add(a.id);
          deduped.push(a);
        }
        setAllAgents(deduped);
      })
      .catch((e) => {
        console.error('Failed to load agents:', e);
        setAllAgents([]);
      })
      .finally(() => setAgentsLoaded(true));
  }, []);

  const toggleAgent = (selected: string[], id: string): string[] =>
    selected.includes(id) ? selected.filter((a) => a !== id) : [...selected, id];

  // Alphabetically sorted union of known agents + still-selected ids (marked missing if gone)
  const agentChips = useMemo(
    () => (selected: string[]) => {
      const union = new Set([...allAgents.map((a) => a.id), ...selected]);
      const term = agentSearch.trim().toLowerCase();
      return [...union]
        .sort((a, b) => a.localeCompare(b))
        .filter((id) => !term || id.toLowerCase().includes(term));
    },
    [allAgents, agentSearch],
  );

  const renderAgentPicker = (
    selected: string[],
    onToggle: (id: string) => void,
    onReplace: (ids: string[]) => void,
  ) => {
    if (!agentsLoaded) {
      return <p className="text-xs text-gray-400">Loading agents…</p>;
    }
    if (allAgents.length === 0) {
      // Fallback: no agents available — keep the form functional with a text input
      return (
        <input
          type="text"
          value={selected.join(', ')}
          onChange={(e) => onReplace(e.target.value.split(',').map((s) => s.trim()).filter(Boolean))}
          className="input"
          placeholder="worker, reviewer (comma-separated agent roles)"
        />
      );
    }
    return (
      <>
        <div className="flex items-center justify-between mb-2">
          <span className="text-xs text-gray-400">{selected.length} selected</span>
          {allAgents.length >= 10 && (
            <input
              type="text"
              value={agentSearch}
              onChange={(e) => setAgentSearch(e.target.value)}
              className="input text-xs py-1 w-48"
              placeholder="Filter agents…"
            />
          )}
        </div>
        <div className="flex flex-wrap gap-1.5">
          {agentChips(selected).map((id) => {
            const agent = allAgents.find((a) => a.id === id);
            const isSelected = selected.includes(id);
            return (
              <button
                key={id}
                type="button"
                onClick={() => onToggle(id)}
                title={agent?.title ? `${id} — ${agent.title}` : `${id} — not found in agent registry`}
                className={clsx(
                  'px-2 py-1 rounded-full text-[11px] border transition-colors',
                  isSelected
                    ? 'bg-brand-500 text-white border-brand-500'
                    : 'bg-gray-100 dark:bg-gray-800 border-gray-200 dark:border-gray-700 text-gray-600 dark:text-gray-300 hover:border-brand-400',
                )}
              >
                {id}
                {!agent && <span className="ml-1 text-amber-200">missing</span>}
              </button>
            );
          })}
          {agentChips(selected).length === 0 && (
            <span className="text-xs text-gray-400">No agents match &quot;{agentSearch}&quot;.</span>
          )}
        </div>
        <p className="text-[10px] text-gray-400 mt-1.5">Agents with this skill load it automatically when matched.</p>
      </>
    );
  };

  const handleCreate = async () => {
    const name = newName.trim();
    if (!name) return;
    try {
      await createSkill({
        name,
        description: newDesc.trim(),
        agents: newAgentsList,
      });
      setNewName('');
      setNewDesc('');
      setNewAgentsList([]);
      setCreating(false);
      setNotice(`Skill "${name}" created`);
      await fetch();
    } catch (e) {
      setNotice(`Create failed: ${e instanceof Error ? e.message : 'unknown error'}`);
    }
  };

  const handleEdit = async (skill: Skill) => {
    setEditingName(skill.name);
    setNotice(null);
    setEditLoading(true);
    // Close the expanded preview if it's the skill being edited
    if (expanded === skill.name) setExpanded(null);
    try {
      const full = await getSkill(skill.name);
      setEditForm({
        name: full.name || skill.name,
        description: full.description || skill.description || '',
        agents: full.agents || skill.agents || [],
        triggers: full.triggers || skill.triggers || [],
        user_invocable: !!full.user_invocable,
        body: full.body || '',
      });
    } catch (e) {
      console.error('Failed to load skill detail:', e);
      setEditForm({
        name: skill.name,
        description: skill.description || '',
        agents: skill.agents || [],
        triggers: skill.triggers || [],
        user_invocable: !!skill.user_invocable,
        body: skill.body || '',
      });
    } finally {
      setEditLoading(false);
    }
  };

  const handleSaveEdit = async () => {
    const name = editForm.name.trim();
    if (!editingName || !name) return;
    try {
      await updateSkill(editingName, {
        name,
        description: editForm.description.trim(),
        agents: editForm.agents,
        triggers: editForm.triggers,
        user_invocable: editForm.user_invocable,
        body: editForm.body,
      });
      setEditingName(null);
      setNotice(`Skill "${name}" saved`);
      await fetch();
    } catch (e) {
      setNotice(`Save failed: ${e instanceof Error ? e.message : 'unknown error'}`);
    }
  };

  const handleCancelEdit = () => {
    setEditingName(null);
  };

  const handleDelete = async (name: string) => {
    if (!confirm(`Delete skill "${name}"?`)) return;
    try {
      await deleteSkill(name);
      setNotice(`Skill "${name}" deleted`);
      await fetch();
    } catch (e) {
      setNotice(`Delete failed: ${e instanceof Error ? e.message : 'unknown error'}`);
    }
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full text-gray-400">
        <div className="w-5 h-5 border-2 border-brand-500 border-t-transparent rounded-full animate-spin mr-3" />
        Loading skills…
      </div>
    );
  }

  return (
    <div className="h-full overflow-auto">
      <div className="max-w-4xl mx-auto p-6 space-y-6">
        {/* Header */}
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold">Skill Packs</h1>
            <p className="text-sm text-gray-500 mt-1">Reusable skill packs loaded by agents during runs</p>
          </div>
          <button
            onClick={() => setCreating(!creating)}
            className="btn-primary text-sm gap-2"
          >
            <Plus size={16} />
            New Skill
          </button>
        </div>

        {/* Notice */}
        {notice && (
          <div className="flex items-center justify-between gap-2 px-4 py-3 rounded-lg bg-emerald-50 dark:bg-emerald-900/20 text-emerald-700 dark:text-emerald-300 text-sm">
            <span>{notice}</span>
            <button onClick={() => setNotice(null)} className="opacity-60 hover:opacity-100"><X size={14} /></button>
          </div>
        )}

        {/* Create form */}
        {creating && (
          <div className="card p-4 space-y-3 animate-slide-up border-brand-300 dark:border-brand-700">
            <div className="flex items-center gap-3">
              <input
                type="text"
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                placeholder="skill-name"
                className="input font-mono flex-1"
                autoFocus
              />
              <button onClick={handleCreate} className="btn-primary text-sm gap-1.5" disabled={!newName.trim()}>
                <Check size={14} /> Create
              </button>
              <button onClick={() => setCreating(false)} className="btn-ghost text-sm">
                <X size={14} /> Cancel
              </button>
            </div>
            <input
              type="text"
              value={newDesc}
              onChange={(e) => setNewDesc(e.target.value)}
              placeholder="Description"
              className="input"
            />
            <div>
              <label className="label">Agents</label>
              {renderAgentPicker(
                newAgentsList,
                (id) => setNewAgentsList((l) => toggleAgent(l, id)),
                setNewAgentsList,
              )}
            </div>
          </div>
        )}

        {/* Edit modal */}
        {editingName && (
          <div
            className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/50 p-4 backdrop-blur-sm sm:p-6"
            onMouseDown={handleCancelEdit}
          >
            <div
              className="card my-4 flex max-h-[88vh] w-full max-w-2xl flex-col overflow-hidden"
              onMouseDown={(e) => e.stopPropagation()}
            >
              <div className="flex items-center justify-between border-b border-gray-200 px-6 py-4 dark:border-gray-800">
                <h2 className="font-bold">Edit Skill <span className="font-mono text-brand-500">{editingName}</span></h2>
                <div className="flex items-center gap-2">
                  <button onClick={handleCancelEdit} className="btn-ghost text-sm"><X size={14} /> Cancel</button>
                  <button onClick={handleSaveEdit} className="btn-primary text-sm gap-1.5" disabled={!editForm.name.trim()}>
                    <Check size={14} /> Save
                  </button>
                </div>
              </div>
              <div className="flex-1 space-y-4 overflow-y-auto px-6 py-4">
                {editLoading ? (
                  <div className="flex items-center justify-center py-10 text-gray-400">
                    <div className="w-5 h-5 border-2 border-brand-500 border-t-transparent rounded-full animate-spin mr-3" />
                    Loading skill…
                  </div>
                ) : (
                  <div className="space-y-3">
                    <input
                      type="text"
                      value={editForm.name}
                      onChange={(e) => setEditForm({ ...editForm, name: e.target.value })}
                      placeholder="skill-name"
                      className="input font-mono"
                    />
                    <input
                      type="text"
                      value={editForm.description}
                      onChange={(e) => setEditForm({ ...editForm, description: e.target.value })}
                      placeholder="Description"
                      className="input"
                    />
                    <div>
                      <label className="label">Agents</label>
                      {renderAgentPicker(
                        editForm.agents,
                        (id) => setEditForm((f) => ({ ...f, agents: toggleAgent(f.agents, id) })),
                        (ids) => setEditForm((f) => ({ ...f, agents: ids })),
                      )}
                    </div>
                    <input
                      type="text"
                      value={editForm.triggers.join(', ')}
                      onChange={(e) =>
                        setEditForm({ ...editForm, triggers: e.target.value.split(',').map((s) => s.trim()).filter(Boolean) })
                      }
                      placeholder="triggers (comma-separated keywords)"
                      className="input"
                    />
                    <label className="flex items-center gap-3">
                      <input
                        type="checkbox"
                        checked={editForm.user_invocable}
                        onChange={(e) => setEditForm({ ...editForm, user_invocable: e.target.checked })}
                        className="w-4 h-4 rounded border-gray-300 text-brand-600 focus:ring-brand-500"
                      />
                      <span className="text-sm">User-invocable (@skill:name)</span>
                    </label>
                    <textarea
                      value={editForm.body}
                      onChange={(e) => setEditForm({ ...editForm, body: e.target.value })}
                      placeholder="# Skill instructions…"
                      className="input font-mono text-xs h-48 resize-y"
                    />
                  </div>
                )}
              </div>
            </div>
          </div>
        )}

        {/* Skills grid */}
        <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
          {skills.map((skill) => {
            const isExpanded = expanded === skill.name;
            return (
              <div
                key={skill.name}
                className="card-hover p-4 group"
              >
                <div className="flex items-start justify-between">
                  <div className="flex items-center gap-3">
                    <div className={clsx(
                      'w-10 h-10 rounded-xl flex items-center justify-center',
                      skill.user_invocable
                        ? 'bg-emerald-100 dark:bg-emerald-900/30'
                        : 'bg-gray-100 dark:bg-gray-800',
                    )}>
                      <Puzzle
                        size={20}
                        className={clsx(
                          skill.user_invocable ? 'text-emerald-600' : 'text-gray-400',
                        )}
                      />
                    </div>
                    <div>
                      <h3 className="font-bold text-sm">{skill.name}</h3>
                      {skill.description && (
                        <p className="text-xs text-gray-500">{skill.description}</p>
                      )}
                    </div>
                  </div>

                  <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                    <button
                      onClick={() => handleEdit(skill)}
                      className="btn-ghost p-1.5 rounded-lg"
                      title="Edit"
                    >
                      <Edit3 size={14} />
                    </button>
                    <button
                      onClick={() => setExpanded(isExpanded ? null : skill.name)}
                      className="btn-ghost p-1.5 rounded-lg"
                      title={isExpanded ? 'Collapse' : 'Expand'}
                    >
                      {isExpanded ? <EyeOff size={14} /> : <Eye size={14} />}
                    </button>
                    <button
                      onClick={() => handleDelete(skill.name)}
                      className="btn-ghost p-1.5 rounded-lg text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20"
                      title="Delete"
                    >
                      <Trash2 size={14} />
                    </button>
                  </div>
                </div>

                {/* Meta badges */}
                <div className="flex items-center gap-2 mt-3 flex-wrap">
                  {skill.user_invocable && (
                    <span className="badge-success">@invocable</span>
                  )}
                  {skill.triggers && skill.triggers.length > 0 && (
                    <span className="badge-brand">
                      <Tag size={10} className="mr-1" />
                      {skill.triggers.length} triggers
                    </span>
                  )}
                  {skill.agents && skill.agents.length > 0 && (
                    <span className="badge-neutral" title={skill.agents.join(', ')}>
                      <User size={10} className="mr-1" />
                      {skill.agents.join(', ')}
                    </span>
                  )}
                </div>

                {/* Expanded details */}
                {isExpanded && skill.body && (
                  <div className="mt-3 pt-3 border-t border-gray-100 dark:border-gray-800 animate-fade-in">
                    <div className="text-[10px] font-semibold text-gray-400 uppercase mb-1">Skill Body</div>
                    <pre className="text-xs font-mono text-gray-600 dark:text-gray-400 whitespace-pre-wrap bg-gray-50 dark:bg-gray-800 rounded-lg p-3 max-h-48 overflow-auto">
                      {skill.body.slice(0, 1000)}
                      {skill.body.length > 1000 && '\n\n… (truncated)'}
                    </pre>
                  </div>
                )}

                {/* Path */}
                <div className="mt-2 text-[10px] text-gray-400 font-mono truncate">
                  {skill.path}
                </div>
              </div>
            );
          })}
        </div>

        {skills.length === 0 && (
          <div className="text-center py-12 text-gray-400">
            <Puzzle size={48} className="mx-auto mb-3 opacity-50" />
            <p className="text-sm">No skill packs defined.</p>
            <p className="text-xs mt-1">Create custom skills that agents can load with @skill:name.</p>
          </div>
        )}
      </div>
    </div>
  );
}
