import { useState, useEffect, useCallback } from 'react';
import { getSkills, createSkill, deleteSkill } from '@/api/client';
import type { Skill } from '@/types';
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
} from 'lucide-react';
import clsx from 'clsx';

export default function SkillManager() {
  const [skills, setSkills] = useState<Skill[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState('');
  const [newDesc, setNewDesc] = useState('');
  const [newAgents, setNewAgents] = useState('');
  const [expanded, setExpanded] = useState<string | null>(null);

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

  const handleCreate = async () => {
    const name = newName.trim();
    if (!name) return;
    try {
      await createSkill({
        name,
        description: newDesc.trim(),
        agents: newAgents.split(',').map((s) => s.trim()).filter(Boolean),
      });
      setNewName('');
      setNewDesc('');
      setNewAgents('');
      setCreating(false);
      await fetch();
    } catch (e) {
      console.error('Create failed:', e);
    }
  };

  const handleDelete = async (name: string) => {
    if (!confirm(`Delete skill "${name}"?`)) return;
    try {
      await deleteSkill(name);
      await fetch();
    } catch (e) {
      console.error('Delete failed:', e);
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
            <input
              type="text"
              value={newAgents}
              onChange={(e) => setNewAgents(e.target.value)}
              placeholder="worker, reviewer (comma-separated agent roles)"
              className="input"
            />
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
                    <span className="badge-neutral">
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
