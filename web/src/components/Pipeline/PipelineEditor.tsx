import { useState, useEffect, useCallback, useContext } from 'react';
import { getPipeline, updatePipeline, resetPipeline, getBlocks, applyPipelinePreset, getAgents, deleteBlock } from '@/api/client';
import { AppContext } from '@/App';
import type { PipelineConfig, PipelineView, PhaseSpec, GroupMeta, Slot, ExecuteLoop, BlockCatalogEntry } from '@/types';
import BlockEditor from '@/components/Blocks/BlockEditor';
import {
  RotateCcw,
  Save,
  GripVertical,
  Check,
  X,
  Layers,
  Zap,
  Plus,
  Trash2,
  ChevronUp,
  ChevronDown,
  ListPlus,
  AlertCircle,
  Workflow,
  Edit3,
  CheckCircle2,
  Info,
  Archive,
} from 'lucide-react';
import clsx from 'clsx';
import {
  WHEN_OPTIONS,
  PERSIST_OPTIONS,
  FAIL_MODE_OPTIONS,
  SLOT_WHEN_PREFIX,
  mergeAgents,
  normalizeConfig,
  phaseOrDefault,
  orphanIds,
  archivedIds,
  restorePhase,
  groupOf,
  reorderGroups,
  movePhaseInGroup,
  addPhase,
  removePhase,
  movePhaseToGroup,
  updatePhase,
  updateGroup,
  deleteGroup,
  addSlot,
  updateSlot,
  removeSlot,
} from './pipelineUtils';
import type { AgentOption } from './pipelineUtils';

const WHEN_COLORS: Record<string, string> = {
  always: 'text-emerald-500',
  auto: 'text-amber-500',
  never: 'text-gray-400 line-through',
};

const GROUP_COLORS: Record<string, string> = {
  prepare: 'bg-sky-100 dark:bg-sky-900/30 text-sky-700 dark:text-sky-300',
  design: 'bg-violet-100 dark:bg-violet-900/30 text-violet-700 dark:text-violet-300',
  build: 'bg-amber-100 dark:bg-amber-900/30 text-amber-700 dark:text-amber-300',
  verify: 'bg-emerald-100 dark:bg-emerald-900/30 text-emerald-700 dark:text-emerald-300',
  finish: 'bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-400',
};

const EMPTY_EXECUTE: ExecuteLoop = { default_role: '', reviewer: '', corrector: '', max_waves: 0 };

const iconBtn =
  'p-1 rounded-md opacity-70 hover:opacity-100 hover:bg-black/10 dark:hover:bg-white/10 disabled:opacity-25 disabled:hover:bg-transparent disabled:cursor-not-allowed transition-colors';
const iconBtnDanger =
  'p-1 rounded-md opacity-70 hover:opacity-100 hover:text-red-600 hover:bg-red-500/10 disabled:opacity-25 disabled:cursor-not-allowed transition-colors';

interface Notice {
  type: 'success' | 'error';
  text: string;
}

export default function PipelineEditor() {
  const ctx = useContext(AppContext);
  const [pipeline, setPipeline] = useState<PipelineView | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [presets, setPresets] = useState<any[]>([]);
  const [applyingPreset, setApplyingPreset] = useState(false);
  const [presetNotice, setPresetNotice] = useState<string | null>(null);
  const [agents, setAgents] = useState<AgentOption[]>([]);
  const [notice, setNotice] = useState<Notice | null>(null);
  const [newPhaseInputs, setNewPhaseInputs] = useState<Record<string, string>>({});
  const [phaseAddErrors, setPhaseAddErrors] = useState<Record<string, string>>({});
  // Pipeline block editor (create/edit custom pipelines via the shared BlockEditor modal)
  const [editorOpen, setEditorOpen] = useState(false);
  const [editorMode, setEditorMode] = useState<'create' | 'edit'>('create');
  const [editorBlock, setEditorBlock] = useState<BlockCatalogEntry | null>(null);

  const fetch = useCallback(async () => {
    try {
      const p = await getPipeline();
      setPipeline(p);
    } catch (e) {
      console.error('Failed to load pipeline:', e);
    } finally {
      setLoading(false);
    }
  }, []);

  const fetchPresets = useCallback(async () => {
    try {
      const data = await getBlocks('pipeline');
      setPresets(data.blocks?.filter((b: any) => b.kind === 'pipeline') || []);
    } catch {}
  }, []);

  // Merge /api/agents (builtin + custom, runtime-effective) with agent blocks
  // (e.g. go-tester from a pack) into one deduped list for all agent selects.
  const fetchAgents = useCallback(async () => {
    try {
      const [list, view] = await Promise.all([getAgents(), getBlocks()]);
      setAgents(mergeAgents(list, view.agents || []));
    } catch {
      // Agent list is best-effort — selects fall back to free text.
    }
  }, []);

  useEffect(() => { fetch(); fetchPresets(); fetchAgents(); }, [fetch, fetchPresets, fetchAgents]);

  // Auto-dismiss success notices; errors stay until the next save.
  useEffect(() => {
    if (notice?.type !== 'success') return;
    const t = window.setTimeout(() => setNotice((n) => (n?.type === 'success' ? null : n)), 3000);
    return () => window.clearTimeout(t);
  }, [notice]);

  const setCfg = (next: PipelineConfig) => {
    setPipeline((p) => (p ? { ...p, config: next } : p));
  };

  const setExec = (patch: Partial<ExecuteLoop>) => {
    setPipeline((p) =>
      p
        ? { ...p, config: { ...p.config, execute: { ...(p.config.execute || EMPTY_EXECUTE), ...patch } } }
        : p,
    );
  };

  const handleApplyPreset = async (id: string) => {
    setApplyingPreset(true);
    setPresetNotice(null);
    try {
      await applyPipelinePreset(id);
      setPresetNotice(`Pipeline "${id}" selected`);
      const p = await getPipeline();
      setPipeline(p);
      ctx?.refresh();
    } catch (e) {
      setPresetNotice(`Failed: ${e instanceof Error ? e.message : 'Unknown error'}`);
    } finally {
      setApplyingPreset(false);
    }
  };

  const openCreateEditor = () => {
    setEditorMode('create');
    setEditorBlock(null);
    setEditorOpen(true);
  };

  const openEditEditor = (p: BlockCatalogEntry) => {
    setEditorMode('edit');
    setEditorBlock(p);
    setEditorOpen(true);
  };

  const handleEditorSaved = () => {
    setEditorOpen(false);
    fetchPresets();
    ctx?.refresh();
  };

  const handleDeletePipeline = async (p: BlockCatalogEntry) => {
    if (!confirm(`Delete pipeline "${p.name}" (${p.id})?`)) return;
    try {
      await deleteBlock('pipeline', p.id);
      setPresetNotice(`Pipeline "${p.id}" deleted`);
      fetchPresets();
      ctx?.refresh();
    } catch (e) {
      setPresetNotice(`Delete failed: ${e instanceof Error ? e.message : 'Unknown error'}`);
    }
  };

  const handleAddPhase = (groupId: string) => {
    if (!pipeline) return;
    const id = (newPhaseInputs[groupId] || '').trim();
    if (!id) {
      setPhaseAddErrors((prev) => ({ ...prev, [groupId]: 'Enter a phase id' }));
      return;
    }
    const next = addPhase(pipeline.config, groupId, id);
    if (!next) {
      setPhaseAddErrors((prev) => ({ ...prev, [groupId]: `Phase "${id}" already exists` }));
      return;
    }
    setCfg(next);
    setNewPhaseInputs((prev) => ({ ...prev, [groupId]: '' }));
    setPhaseAddErrors((prev) => ({ ...prev, [groupId]: '' }));
  };

  const handleSave = async () => {
    if (!pipeline) return;
    setSaving(true);
    setNotice(null);
    try {
      const updated = await updatePipeline(normalizeConfig(pipeline.config));
      setPipeline(updated);
      setNotice({ type: 'success', text: 'Pipeline saved' });
      ctx?.refresh();
    } catch (e) {
      setNotice({
        type: 'error',
        text: e instanceof Error ? e.message : 'Save failed — unknown error',
      });
    } finally {
      setSaving(false);
    }
  };

  const handleReset = async () => {
    try {
      const p = await resetPipeline();
      setPipeline(p);
      setNotice(null);
    } catch (e) {
      console.error(e);
    }
  };

  if (loading) return (
    <div className="flex items-center justify-center h-full"><div className="flex items-center gap-3 text-gray-400"><div className="w-5 h-5 border-2 border-brand-500 border-t-transparent rounded-full animate-spin" />Loading pipeline…</div></div>
  );
  if (!pipeline) return (
    <div className="flex items-center justify-center h-full text-gray-400">No pipeline data.</div>
  );

  const { config } = pipeline;
  const groups = config.groups || [];
  const execute = config.execute || EMPTY_EXECUTE;
  const slots = config.slots || [];
  const orphans = orphanIds(config);
  const archived = archivedIds(config);
  const activePipe = ctx?.config?.active_pipeline;

  return (
    <div className="h-full overflow-auto">
      <div className="max-w-4xl mx-auto p-6 space-y-6">
        {/* Header */}
        <div className="flex items-center justify-between flex-wrap gap-3">
          <div>
            <h1 className="text-2xl font-bold">Pipeline</h1>
            <p className="text-sm text-gray-500 mt-1">
              Configure execution phases{activePipe && <span className="ml-2 badge-brand">active: {activePipe}</span>}
            </p>
          </div>
          <div className="flex items-center gap-2">
            <button onClick={handleReset} className="btn-ghost text-sm gap-2"><RotateCcw size={16} />Reset</button>
            <button onClick={handleSave} disabled={saving} className="btn-primary text-sm gap-2"><Save size={16} />{saving ? 'Saving…' : 'Save'}</button>
          </div>
        </div>

        {/* Save / error notice */}
        {notice && (
          <div
            className={clsx(
              'flex items-center justify-between gap-3 px-4 py-3 rounded-xl border text-sm',
              notice.type === 'error'
                ? 'bg-red-50 dark:bg-red-950/30 border-red-200 dark:border-red-900 text-red-700 dark:text-red-300'
                : 'bg-emerald-50 dark:bg-emerald-950/30 border-emerald-200 dark:border-emerald-900 text-emerald-700 dark:text-emerald-300',
            )}
          >
            <div className="flex items-center gap-2 min-w-0">
              {notice.type === 'error' ? <AlertCircle size={16} className="shrink-0" /> : <CheckCircle2 size={16} className="shrink-0" />}
              <span className="font-mono text-xs break-all">{notice.text}</span>
            </div>
            <button onClick={() => setNotice(null)} className="shrink-0 p-1 rounded-md hover:bg-black/10 dark:hover:bg-white/10 transition-colors" title="Dismiss">
              <X size={14} />
            </button>
          </div>
        )}

        {/* Pipeline Library — all pipeline blocks (builtin + custom) */}
        <div className="card p-4 space-y-3">
          <div className="flex items-center justify-between flex-wrap gap-2">
            <div className="flex items-center gap-2 text-sm font-bold">
              <Layers size={16} className="text-brand-500" />Pipeline Library
              <span className="badge-neutral">{presets.length}</span>
            </div>
            <button onClick={openCreateEditor} className="btn-primary text-xs py-1.5 gap-1">
              <Plus size={14} />New Pipeline
            </button>
          </div>
          <p className="text-[11px] text-gray-400 dark:text-gray-500">
            Select a pipeline to make it active, or edit / delete custom pipelines. The editor below always shows the
            active pipeline's config.
          </p>
          {presets.length === 0 ? (
            <p className="text-xs text-gray-400 italic">No pipeline blocks found — create one above.</p>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
              {presets.map((p: any) => {
                const isActive = activePipe === p.id;
                const deletable = p.custom || p.path;
                return (
                  <div
                    key={p.id}
                    className={clsx(
                      'rounded-xl border p-3 transition-all hover:shadow-md',
                      isActive
                        ? 'border-brand-300 bg-brand-50/60 dark:border-brand-700 dark:bg-brand-900/15'
                        : 'border-gray-200 dark:border-gray-700',
                    )}
                  >
                    <div className="flex items-start gap-2.5">
                      <div className="mt-0.5 text-xl leading-none">{p.icon || <Workflow size={18} className="text-sky-500" />}</div>
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-1.5 flex-wrap">
                          <span className="text-sm font-bold truncate">{p.name}</span>
                          {isActive && <span className="badge-brand">active</span>}
                          {p.builtin && !p.custom && <span className="badge-neutral">builtin</span>}
                          {p.custom && <span className="badge-brand">custom</span>}
                        </div>
                        <div className="text-[10px] font-mono text-gray-400 truncate">
                          {p.id}
                          {p.language && ` · ${p.language}`}
                          {p.version && ` · v${p.version}`}
                        </div>
                        {p.description && (
                          <p className="text-xs text-gray-500 dark:text-gray-400 mt-1 line-clamp-2">{p.description}</p>
                        )}
                        <div className="flex items-center gap-1.5 mt-2.5 flex-wrap">
                          <button
                            onClick={() => handleApplyPreset(p.id)}
                            disabled={applyingPreset || isActive}
                            className={clsx(
                              'flex items-center gap-1 px-2.5 py-1 rounded-lg text-[11px] font-medium transition-colors',
                              isActive
                                ? 'bg-gray-100 dark:bg-gray-800 text-gray-400 cursor-default'
                                : 'bg-brand-500 hover:bg-brand-600 text-white',
                              applyingPreset && 'opacity-50 cursor-not-allowed',
                            )}
                          >
                            <Check size={11} />
                            {isActive ? 'Active' : 'Select'}
                          </button>
                          <button
                            onClick={() => openEditEditor(p)}
                            className="btn-ghost flex items-center gap-1 px-2.5 py-1 rounded-lg text-[11px]"
                            title="Edit pipeline block"
                          >
                            <Edit3 size={11} />Edit
                          </button>
                          <button
                            onClick={() => handleDeletePipeline(p)}
                            disabled={!deletable}
                            title={
                              deletable
                                ? 'Delete pipeline block'
                                : 'Builtin pipeline — edit it to create an override instead'
                            }
                            className="btn-ghost flex items-center gap-1 px-2.5 py-1 rounded-lg text-[11px] text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20 disabled:opacity-30 disabled:cursor-not-allowed"
                          >
                            <Trash2 size={11} />Delete
                          </button>
                        </div>
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
          {presetNotice && (
            <div className={clsx('text-xs px-2 py-1 rounded', presetNotice.includes('Failed') ? 'text-red-600 bg-red-50' : 'text-emerald-600 bg-emerald-50')}>
              {presetNotice}
            </div>
          )}
        </div>

        {/* BlockEditor modal for creating/editing pipeline blocks */}
        <BlockEditor
          open={editorOpen}
          kind="pipeline"
          mode={editorMode}
          block={editorBlock}
          onClose={() => setEditorOpen(false)}
          onSaved={handleEditorSaved}
        />

        {/* Execute loop config */}
        <div className="card p-4 space-y-3">
          <div className="flex items-center gap-2 text-sm font-bold"><Zap size={16} className="text-brand-500" />Execute Loop</div>
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
            <div>
              <div className="label">Default Role</div>
              <AgentSelect
                value={execute.default_role || ''}
                options={agents}
                onChange={(v) => setExec({ default_role: v })}
                emptyLabel="worker (engine default)"
              />
            </div>
            <div>
              <div className="label">Reviewer</div>
              <AgentSelect
                value={execute.reviewer || ''}
                options={agents}
                onChange={(v) => setExec({ reviewer: v })}
                emptyLabel="— inherit —"
              />
              {!execute.reviewer && <p className="text-[10px] text-gray-400 mt-1">fallback: reviewer</p>}
            </div>
            <div>
              <div className="label">Corrector</div>
              <AgentSelect
                value={execute.corrector || ''}
                options={agents}
                onChange={(v) => setExec({ corrector: v })}
                emptyLabel="— inherit —"
              />
              {!execute.corrector && <p className="text-[10px] text-gray-400 mt-1">fallback: corrector</p>}
            </div>
            <div>
              <div className="label">Max Waves</div>
              <input
                type="number"
                min={0}
                value={execute.max_waves ?? 2}
                onChange={(e) => setExec({ max_waves: Math.max(0, parseInt(e.target.value || '0', 10) || 0) })}
                className="input text-xs py-1.5"
              />
              <p className="text-[10px] text-gray-400 mt-1">0 = engine default</p>
            </div>
          </div>
          <p className="text-xs text-gray-500 dark:text-gray-400 flex items-center gap-1.5">
            <Info size={13} className="text-brand-500 shrink-0" />
            <span>default_role is used for worker tasks when a task has no explicit role.</span>
          </p>
        </div>

        {/* Phase groups */}
        {groups.map((group, gi) => {
          const groupPhases = group.steps.filter((id) => config.phases[id]);
          return (
            <div key={group.id} className="card overflow-hidden">
              {/* Group header */}
              <div className={clsx('px-3 py-2 flex items-center gap-2', GROUP_COLORS[group.id] || 'bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-300')}>
                <input
                  value={group.label}
                  onChange={(e) => setCfg(updateGroup(config, gi, { label: e.target.value }))}
                  placeholder={group.id}
                  title={`Group label (id: ${group.id})`}
                  className="flex-1 min-w-0 bg-transparent text-xs font-bold focus:outline-none border-b border-transparent focus:border-current placeholder:opacity-40"
                />
                <span className="text-[10px] font-mono opacity-60 shrink-0">{group.id}</span>
                <div className="flex items-center gap-0.5 shrink-0">
                  <button
                    disabled={gi === 0}
                    onClick={() => setCfg(reorderGroups(config, gi, -1))}
                    className={iconBtn}
                    title="Move group up"
                  >
                    <ChevronUp size={14} />
                  </button>
                  <button
                    disabled={gi === groups.length - 1}
                    onClick={() => setCfg(reorderGroups(config, gi, 1))}
                    className={iconBtn}
                    title="Move group down"
                  >
                    <ChevronDown size={14} />
                  </button>
                  <button
                    onClick={() => {
                      if (confirm(`Remove group "${group.label}"?\nIts phases will stay and move to Unassigned.`)) {
                        setCfg(deleteGroup(config, gi));
                      }
                    }}
                    className={iconBtnDanger}
                    title="Delete group"
                  >
                    <Trash2 size={14} />
                  </button>
                </div>
              </div>

              {/* Phase rows */}
              <div className="divide-y divide-gray-100 dark:divide-gray-800">
                {groupPhases.length === 0 && (
                  <div className="px-4 py-3 text-xs text-gray-400 italic">No phases in this group</div>
                )}
                {groupPhases.map((pid, pi) => (
                  <PhaseRow
                    key={pid}
                    phase={phaseOrDefault(config, pid)}
                    phaseId={pid}
                    agents={agents}
                    groups={groups}
                    defaultAgent={pipeline.defaults?.[pid]}
                    index={pi}
                    groupLen={groupPhases.length}
                    onPatch={(patch) => setCfg(updatePhase(config, pid, patch))}
                    onMoveTo={(gid) => setCfg(movePhaseToGroup(config, pid, gid))}
                    onRemove={() => {
                      if (confirm(`Remove phase "${pid}"?\nIt will be deleted from the pipeline and all groups.`)) {
                        setCfg(removePhase(config, pid));
                      }
                    }}
                    onReorder={(dir) => setCfg(movePhaseInGroup(config, group.id, pi, dir))}
                  />
                ))}

                {/* Add phase */}
                <div className="px-3 py-2.5 flex items-center gap-2 flex-wrap">
                  <Plus size={14} className="text-gray-400 shrink-0" />
                  <input
                    value={newPhaseInputs[group.id] || ''}
                    onChange={(e) => {
                      setNewPhaseInputs((prev) => ({ ...prev, [group.id]: e.target.value }));
                      setPhaseAddErrors((prev) => ({ ...prev, [group.id]: '' }));
                    }}
                    onKeyDown={(e) => { if (e.key === 'Enter') handleAddPhase(group.id); }}
                    placeholder={`new phase id e.g. ${group.id}-lint`}
                    className="input input-mono text-xs py-1.5 w-44"
                  />
                  <button onClick={() => handleAddPhase(group.id)} className="btn-secondary text-xs py-1.5 gap-1">
                    <Plus size={13} />Add phase
                  </button>
                  {phaseAddErrors[group.id] && (
                    <span className="text-[11px] text-red-500">{phaseAddErrors[group.id]}</span>
                  )}
                </div>
              </div>
            </div>
          );
        })}

        {/* Unassigned phases */}
        {orphans.length > 0 && (
          <div className="card overflow-hidden">
            <div className="px-3 py-2 flex items-center gap-2 bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-300">
              <Layers size={14} className="shrink-0" />
              <span className="text-xs font-bold">Unassigned Phases</span>
              <span className="badge-neutral">{orphans.length}</span>
              <span className="text-[10px] opacity-60 ml-auto hidden sm:inline">Not in any group — appended to order</span>
            </div>
            <div className="divide-y divide-gray-100 dark:divide-gray-800">
              {orphans.map((pid) => (
                <PhaseRow
                  key={pid}
                  phase={phaseOrDefault(config, pid)}
                  phaseId={pid}
                  agents={agents}
                  groups={groups}
                  defaultAgent={pipeline.defaults?.[pid]}
                  onPatch={(patch) => setCfg(updatePhase(config, pid, patch))}
                  onMoveTo={(gid) => setCfg(movePhaseToGroup(config, pid, gid))}
                  onRemove={() => {
                    if (confirm(`Remove phase "${pid}"?\nIt will be deleted from the pipeline.`)) {
                      setCfg(removePhase(config, pid));
                    }
                  }}
                />
              ))}
            </div>
          </div>
        )}

        {/* Archived (deleted) phases */}
        {archived.length > 0 && (
          <div className="card overflow-hidden border-dashed">
            <div className="px-3 py-2 flex items-center gap-2 bg-gray-50 dark:bg-gray-900 text-gray-500 dark:text-gray-400">
              <Archive size={14} className="shrink-0" />
              <span className="text-xs font-bold">Archived Phases</span>
              <span className="badge-neutral">{archived.length}</span>
              <span className="text-[10px] opacity-60 ml-auto hidden sm:inline">Deleted phases — restore to re-enable</span>
            </div>
            <div className="divide-y divide-gray-100 dark:divide-gray-800">
              {archived.map((pid) => {
                const ph = phaseOrDefault(config, pid);
                return (
                  <div key={pid} className="flex items-center gap-3 px-4 py-2.5 opacity-70">
                    <div className="w-28 shrink-0">
                      <div className="text-sm font-bold line-through decoration-gray-400">{ph.label || pid}</div>
                      <div className="text-[10px] text-gray-400 font-mono">{pid}</div>
                    </div>
                    <div className="text-[10px] text-gray-400 font-mono truncate">
                      {ph.agent ? `agent: ${ph.agent}` : 'no agent'} · when: never
                    </div>
                    <button
                      onClick={() => setCfg(restorePhase(config, pid))}
                      className="btn-ghost text-xs ml-auto gap-1 px-2 py-1 rounded-lg text-emerald-600 dark:text-emerald-400 hover:bg-emerald-50 dark:hover:bg-emerald-900/20"
                      title="Restore phase"
                    >
                      <RotateCcw size={12} />Restore
                    </button>
                  </div>
                );
              })}
            </div>
          </div>
        )}

        {/* Slots */}
        <div className="card p-4 space-y-3">
          <div className="flex items-center justify-between flex-wrap gap-2">
            <div className="flex items-center gap-2 text-sm font-bold">
              <ListPlus size={16} className="text-brand-500" />Slots
              <span className="badge-neutral">{slots.length}</span>
            </div>
            <button onClick={() => setCfg(addSlot(config))} className="btn-secondary text-xs py-1.5 gap-1">
              <Plus size={14} />Add slot
            </button>
          </div>
          {slots.length === 0 && (
            <p className="text-xs text-gray-500">
              No slots yet. Slots inject agent calls around phase anchors — <span className="font-mono">before</span>,{' '}
              <span className="font-mono">after</span>, or <span className="font-mono">replace</span>.
            </p>
          )}
          <div className="space-y-3">
            {slots.map((slot, i) => (
              <SlotCard
                key={i}
                slot={slot}
                agents={agents}
                anchors={pipeline.anchors || []}
                onPatch={(patch) => setCfg(updateSlot(config, i, patch))}
                onRemove={() => setCfg(removeSlot(config, i))}
              />
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

// ── Shared sub-components ──

function AgentSelect({ value, options, onChange, emptyLabel, className }: {
  value: string;
  options: AgentOption[];
  onChange: (v: string) => void;
  emptyLabel: string;
  className?: string;
}) {
  const known = options.some((o) => o.id === value);
  return (
    <select value={value} onChange={(e) => onChange(e.target.value)} className={clsx('input text-xs py-1.5 font-mono', className)}>
      <option value="">{emptyLabel}</option>
      {!known && value && <option value={value}>{value} (unknown)</option>}
      {options.map((o) => (
        <option key={o.id} value={o.id}>
          {o.id}
          {o.title && o.title !== o.id ? ` — ${o.title}` : ''}
        </option>
      ))}
    </select>
  );
}

function EnabledToggle({ checked, onChange, title }: {
  checked: boolean;
  onChange: (v: boolean) => void;
  title: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      onClick={() => onChange(!checked)}
      title={title}
      className={clsx(
        'relative w-9 h-5 rounded-full transition-colors shrink-0',
        checked ? 'bg-emerald-500' : 'bg-gray-300 dark:bg-gray-600',
      )}
    >
      <span
        className={clsx(
          'absolute top-0.5 w-4 h-4 rounded-full bg-white shadow flex items-center justify-center transition-all',
          checked ? 'left-[18px]' : 'left-0.5',
        )}
      >
        {checked && <Check size={10} className="text-emerald-600" strokeWidth={3.5} />}
      </span>
    </button>
  );
}

interface PhaseRowProps {
  phase: PhaseSpec;
  phaseId: string;
  agents: AgentOption[];
  groups: GroupMeta[];
  defaultAgent?: string;
  index?: number;
  groupLen?: number;
  onPatch: (patch: Partial<PhaseSpec>) => void;
  onMoveTo: (groupId: string | null) => void;
  onRemove: () => void;
  onReorder?: (dir: -1 | 1) => void;
}

function PhaseRow({ phase, phaseId, agents, groups, defaultAgent, index, groupLen, onPatch, onMoveTo, onRemove, onReorder }: PhaseRowProps) {
  const isEnabled = phase.enabled !== false;
  const currentGroup = groupOf(groups, phaseId);
  const canReorder = onReorder !== undefined && index !== undefined && groupLen !== undefined;
  return (
    <div className={clsx('flex items-center gap-2 px-3 py-2.5 flex-wrap lg:flex-nowrap transition-opacity', !isEnabled && 'opacity-60')}>
      {/* Grip + per-phase reorder */}
      <div className="flex flex-col items-center gap-px w-4 shrink-0 self-start lg:self-center pt-1.5 lg:pt-0">
        <GripVertical size={14} className="text-gray-300 dark:text-gray-600" />
        {onReorder && canReorder && (
          <>
            <button
              disabled={index === 0}
              onClick={() => onReorder(-1)}
              className="p-0.5 rounded text-gray-400 hover:text-gray-700 dark:hover:text-gray-200 hover:bg-gray-100 dark:hover:bg-gray-800 disabled:opacity-20 disabled:cursor-not-allowed transition-colors"
              title="Move phase up"
            >
              <ChevronUp size={12} />
            </button>
            <button
              disabled={groupLen === index + 1}
              onClick={() => onReorder(1)}
              className="p-0.5 rounded text-gray-400 hover:text-gray-700 dark:hover:text-gray-200 hover:bg-gray-100 dark:hover:bg-gray-800 disabled:opacity-20 disabled:cursor-not-allowed transition-colors"
              title="Move phase down"
            >
              <ChevronDown size={12} />
            </button>
          </>
        )}
      </div>

      {/* Phase id (readonly) */}
      <div className="w-20 shrink-0 self-start lg:self-center pt-1.5 lg:pt-0">
        <div className="text-xs font-bold font-mono truncate" title={phaseId}>{phaseId}</div>
      </div>

      {/* Label + tip */}
      <input
        value={phase.label || phaseId}
        onChange={(e) => onPatch({ label: e.target.value })}
        placeholder={phaseId}
        title="Phase label"
        className="input text-xs py-1.5 w-32 shrink-0"
      />
      <input
        value={phase.tip || ''}
        onChange={(e) => onPatch({ tip: e.target.value })}
        placeholder="tip / hint"
        title="Phase tip"
        className="input text-xs py-1.5 flex-1 min-w-32"
      />

      {/* Agent */}
      <AgentSelect
        value={phase.agent || ''}
        options={agents}
        onChange={(v) => onPatch({ agent: v })}
        emptyLabel={defaultAgent ? `inherit (${defaultAgent})` : 'inherit / auto'}
        className="w-44 shrink-0"
      />

      {/* When */}
      <select
        value={phase.when || 'auto'}
        onChange={(e) => onPatch({ when: e.target.value })}
        title="When to run"
        className={clsx('input text-xs py-1.5 w-24 shrink-0', WHEN_COLORS[phase.when || 'auto'])}
      >
        {WHEN_OPTIONS.map((w) => (
          <option key={w} value={w}>{w[0].toUpperCase() + w.slice(1)}</option>
        ))}
      </select>

      {/* Enabled */}
      <EnabledToggle checked={isEnabled} onChange={(v) => onPatch({ enabled: v })} title={isEnabled ? 'Disable phase' : 'Enable phase'} />

      {/* Move to group */}
      <select
        value={currentGroup || ''}
        onChange={(e) => onMoveTo(e.target.value || null)}
        title="Assign phase to group"
        className="input text-xs py-1.5 w-28 shrink-0"
      >
        <option value="">— unassigned</option>
        {groups.map((g) => (
          <option key={g.id} value={g.id}>{g.label || g.id}</option>
        ))}
      </select>

      {/* Remove */}
      <button
        onClick={onRemove}
        className="p-1.5 rounded-lg text-gray-400 hover:text-red-600 hover:bg-red-50 dark:hover:bg-red-950/40 transition-colors shrink-0"
        title="Remove phase"
      >
        <Trash2 size={15} />
      </button>
    </div>
  );
}

function SlotCard({ slot, agents, anchors, onPatch, onRemove }: {
  slot: Slot;
  agents: AgentOption[];
  anchors: string[];
  onPatch: (patch: Partial<Slot>) => void;
  onRemove: () => void;
}) {
  const enabled = slot.enabled !== false;
  const isQuery = (slot.when || '').startsWith(SLOT_WHEN_PREFIX);
  const queryPattern = isQuery ? slot.when.slice(SLOT_WHEN_PREFIX.length) : '';
  const datalistId = `slot-anchors-${slot.id.replace(/[^a-zA-Z0-9_-]/g, '')}`;
  return (
    <div className={clsx('rounded-xl border border-gray-200 dark:border-gray-800 bg-gray-50/60 dark:bg-gray-800/40 p-3 space-y-3 transition-opacity', !enabled && 'opacity-60')}>
      {/* Header */}
      <div className="flex items-center gap-2 flex-wrap">
        <Workflow size={14} className="text-brand-500 shrink-0" />
        <input
          value={slot.id}
          onChange={(e) => onPatch({ id: e.target.value })}
          placeholder="slot id"
          title="Slot id (editable on create)"
          className="input input-mono text-xs py-1 w-40 shrink-0"
        />
        <input
          value={slot.title || ''}
          onChange={(e) => onPatch({ title: e.target.value })}
          placeholder="Title (optional)"
          className="input text-xs py-1 flex-1 min-w-40"
        />
        <EnabledToggle checked={enabled} onChange={(v) => onPatch({ enabled: v })} title={enabled ? 'Disable slot' : 'Enable slot'} />
        <button
          onClick={onRemove}
          className="p-1.5 rounded-lg text-gray-400 hover:text-red-600 hover:bg-red-50 dark:hover:bg-red-950/40 transition-colors shrink-0"
          title="Delete slot"
        >
          <Trash2 size={15} />
        </button>
      </div>

      {/* Agent / when / persist / fail */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-2">
        <div>
          <div className="label">Agent</div>
          <AgentSelect
            value={slot.agent}
            options={agents}
            onChange={(v) => onPatch({ agent: v })}
            emptyLabel="choose agent…"
          />
        </div>
        <div>
          <div className="label">When</div>
          <select
            value={isQuery ? SLOT_WHEN_PREFIX : (slot.when || 'always')}
            onChange={(e) => {
              const v = e.target.value;
              if (v === SLOT_WHEN_PREFIX) {
                onPatch({ when: SLOT_WHEN_PREFIX + queryPattern });
              } else {
                onPatch({ when: v });
              }
            }}
            className="input text-xs py-1"
          >
            <option value="always">always</option>
            <option value="never">never</option>
            <option value={SLOT_WHEN_PREFIX}>query_matches:&lt;re&gt;</option>
          </select>
          {isQuery && (
            <input
              value={queryPattern}
              onChange={(e) => onPatch({ when: SLOT_WHEN_PREFIX + e.target.value })}
              placeholder="regex e.g. .*(refactor|fix).*"
              className="input input-mono text-xs py-1 mt-1"
            />
          )}
        </div>
        <div>
          <div className="label">Persist To</div>
          <select
            value={slot.persist_to || 'none'}
            onChange={(e) => onPatch({ persist_to: e.target.value })}
            className="input text-xs py-1"
          >
            {PERSIST_OPTIONS.map((o) => (
              <option key={o} value={o}>{o}</option>
            ))}
          </select>
        </div>
        <div>
          <div className="label">Fail Mode</div>
          <select
            value={slot.fail_mode || 'continue'}
            onChange={(e) => onPatch({ fail_mode: e.target.value })}
            className="input text-xs py-1"
          >
            {FAIL_MODE_OPTIONS.map((o) => (
              <option key={o} value={o}>{o}</option>
            ))}
          </select>
        </div>
      </div>

      {/* Anchors */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-2">
        {(['before', 'after', 'replace'] as const).map((anchor) => (
          <div key={anchor}>
            <div className="label">{anchor}</div>
            <input
              value={slot[anchor] || ''}
              onChange={(e) => onPatch({ [anchor]: e.target.value })}
              list={datalistId}
              placeholder="phase anchor"
              className="input input-mono text-xs py-1"
            />
          </div>
        ))}
      </div>
      <datalist id={datalistId}>
        {anchors.map((a) => (
          <option key={a} value={a} />
        ))}
      </datalist>

      {/* Input template */}
      <div>
        <div className="label">Input Template</div>
        <textarea
          value={slot.input || ''}
          onChange={(e) => onPatch({ input: e.target.value })}
          rows={2}
          placeholder="Prompt template — {{query}}, {{exploration}}, {{plan}}, {{phase}} available"
          className="input input-mono text-xs py-1.5 resize-y"
        />
      </div>
    </div>
  );
}
