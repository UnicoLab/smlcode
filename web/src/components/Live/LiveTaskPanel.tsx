// ── LiveTaskPanel ──
// Compact sidebar panel for task management + context injection in LiveView.
// Self-contained: fetches its own data from the API.
import { useState, useEffect, useCallback, useContext, useMemo, useRef } from 'react';
import { AppContext } from '@/App';
import {
  getTasks,
  addTask,
  patchTask,
  deleteTask,
  getAgents,
  getDoc,
  updateDoc,
} from '@/api/client';
import type { Task, AgentSpec, Board } from '@/types';
import clsx from 'clsx';
import {
  Plus,
  Edit3,
  Trash2,
  Save,
  X,
  Target,
  Sliders,
  Thermometer,
  FileText,
  MessageSquare,
  Settings2,
  ChevronDown,
  ChevronRight,
  Check,
  ListChecks,
  Tag,
  AlertCircle,
  Loader2,
  RefreshCw,
  Clock,
  Lightbulb,
} from 'lucide-react';

// ── Constants ──
const POLL_INTERVAL = 5000;
const STATUS_BADGE: Record<string, string> = {
  done: 'badge-success',
  failed: 'badge-error',
  in_progress: 'badge-warn',
  in_review: 'badge-warn',
  blocked: 'badge-error',
  scoped: 'badge-neutral',
};

const COLUMN_COLORS: Record<string, { text: string; badge: string }> = {
  done:         { text: 'text-emerald-500', badge: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-400' },
  failed:       { text: 'text-red-500',     badge: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-400' },
  in_progress:  { text: 'text-amber-500',   badge: 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-400' },
  in_review:    { text: 'text-blue-500',    badge: 'bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-400' },
  blocked:      { text: 'text-red-500',     badge: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-400' },
  scoped:       { text: 'text-gray-500',    badge: 'bg-gray-100 text-gray-700 dark:bg-gray-900/40 dark:text-gray-400' },
  to_scope:     { text: 'text-purple-500',  badge: 'bg-purple-100 text-purple-700 dark:bg-purple-900/40 dark:text-purple-400' },
  ready_to_dev: { text: 'text-cyan-500',    badge: 'bg-cyan-100 text-cyan-700 dark:bg-cyan-900/40 dark:text-cyan-400' },
  todo:         { text: 'text-gray-500',    badge: 'bg-gray-100 text-gray-700 dark:bg-gray-900/40 dark:text-gray-400' },
};

const ROLE_ICONS: Record<string, string> = {
  worker:        '🔧',
  deep:          '🧠',
  tester:        '🧪',
  reviewer:      '👁️',
  corrector:     '✏️',
  coordinator:   '🎯',
  orchestrator:  '🎼',
  planner:       '📋',
  architect:     '🏗️',
  explorer:      '🔍',
  splitter:      '✂️',
  docs:          '📖',
  placeholder:   '⚡',
  escalate:      '🚨',
  memory:        '💾',
  context:       '📝',
};

const COMMON_ROLES = ['worker', 'tester', 'reviewer', 'corrector', 'explorer', 'planner', 'architect', 'splitter'];

// ── Types ──
interface NewTaskForm {
  title: string;
  description: string;
  role: string;
  acceptance: string;
  files: string;
}

const EMPTY_FORM: NewTaskForm = {
  title: '',
  description: '',
  role: '',
  acceptance: '',
  files: '',
};

function timeAgo(iso: string): string {
  if (!iso) return '';
  const diff = Date.now() - new Date(iso).getTime();
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const d = Math.floor(hr / 24);
  return `${d}d ago`;
}

// ── Component ──
export default function LiveTaskPanel() {
  // ── App context ──
  const ctx = useContext(AppContext);
  const config = ctx?.config;

  // ── State: tasks ──
  const [board, setBoard] = useState<Board | null>(null);
  const [tasksLoading, setTasksLoading] = useState(true);
  const [locallyAddedIds, setLocallyAddedIds] = useState<Set<string>>(new Set());
  const [optimisticTasks, setOptimisticTasks] = useState<Task[]>([]);
  const [lastPollTime, setLastPollTime] = useState<number>(Date.now());
  const [refreshing, setRefreshing] = useState(false);

  // ── State: agents ──
  const [agents, setAgents] = useState<AgentSpec[]>([]);

  // ── State: add task form ──
  const [showAddForm, setShowAddForm] = useState(false);
  const [newTask, setNewTask] = useState<NewTaskForm>(EMPTY_FORM);
  const [addError, setAddError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const newTaskTitleRef = useRef<HTMLInputElement>(null);

  // Focus the title field when the add-task form is opened (replaces autoFocus,
  // which jsx-a11y flags; the form only mounts on open, so this runs once per open).
  useEffect(() => {
    if (showAddForm) newTaskTitleRef.current?.focus();
  }, [showAddForm]);

  // ── State: edit ──
  const [editingId, setEditingId] = useState<string | null>(null);
  const editTitleRef = useRef<HTMLInputElement>(null);
  const [editTitle, setEditTitle] = useState('');
  const [editDescription, setEditDescription] = useState('');
  const [editAcceptance, setEditAcceptance] = useState('');
  const [editNotes, setEditNotes] = useState('');
  const [saving, setSaving] = useState(false);

  // Focus the inline title edit input when editing starts (replaces autoFocus,
  // which jsx-a11y flags; the input only mounts while editingId matches).
  useEffect(() => {
    if (editingId) editTitleRef.current?.focus();
  }, [editingId]);

  // ── State: delete confirmation ──
  const [deleteId, setDeleteId] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);

  // ── State: expanded tasks ──
  const [expandedTasks, setExpandedTasks] = useState<Set<string>>(new Set());

  // ── State: context injection ──
  const [contextContent, setContextContent] = useState('');
  const [contextLoading, setContextLoading] = useState(true);
  const [contextSaving, setContextSaving] = useState(false);
  const [contextSaved, setContextSaved] = useState(false);
  const [extraContext, setExtraContext] = useState('');

  // ── State: temperature override ──
  const [tempOverride, setTempOverride] = useState<boolean>(false);
  const [tempValue, setTempValue] = useState(config?.temperature ?? 0.7);

  // ── State: agent hints (sessionStorage-backed) ──
  const [agentHints, setAgentHints] = useState<string>(() => {
    try { return sessionStorage.getItem('slmcode:agentHints') || ''; }
    catch { return ''; }
  });
  const [hintsSaved, setHintsSaved] = useState(false);

  // ── Data fetching ──
  const fetchTasks = useCallback(async () => {
    try {
      const b = await getTasks();
      if (!b || !b.tasks) { setBoard(null); return; }

      // ── Optimistic merge: preserve locally-added tasks not yet on server ──
      setOptimisticTasks((prev) => {
        if (!prev || !Array.isArray(prev)) return [];
        const tasks = b.tasks || [];
        const cols = b.columns || [];
        const byCol = b.by_column || {};
        const serverIds = new Set(tasks.map((t) => t.id));
        // Keep only optimistic tasks whose IDs are not yet in the server response
        const stillMissing = prev.filter((t) => !serverIds.has(t.id));
        // Remove confirmed IDs from the tracking set
        setLocallyAddedIds((prevIds) => {
          const next = new Set(prevIds);
          for (const id of next) {
            if (serverIds.has(id)) next.delete(id);
          }
          return next;
        });
        return stillMissing;
      });

      // Merge optimistic tasks into the board
      setOptimisticTasks((prev) => {
        if (!prev || !Array.isArray(prev)) return [];
        if (prev.length === 0) {
          setBoard(b);
          return prev;
        }
        const tList = b.tasks || [];
        const cList = b.columns || [];
        const mergedTasks = [...prev, ...tList];
        const mergedByColumn: Record<string, Task[]> = {};
        for (const col of cList) {
          mergedByColumn[col] = mergedTasks.filter((t) => t.column === col);
        }
        for (const t of prev) {
          if (!mergedByColumn[t.column]) mergedByColumn[t.column] = [];
          if (!mergedByColumn[t.column].some((ex) => ex.id === t.id)) {
            mergedByColumn[t.column].push(t);
          }
        }
        const allColumns = [...new Set([...cList, ...Object.keys(mergedByColumn)])];
        setBoard({
          ...b,
          tasks: mergedTasks,
          columns: allColumns,
          by_column: mergedByColumn,
        });
        return prev;
      });

      setLastPollTime(Date.now());
    } catch (err) {
      // silently ignore polling errors
    } finally {
      setTasksLoading(false);
      setRefreshing(false);
    }
  }, []);

  const fetchAgents = useCallback(async () => {
    try {
      const list = await getAgents();
      setAgents(list);
    } catch {
      // ignore
    }
  }, []);

  const fetchContext = useCallback(async () => {
    try {
      const doc = await getDoc('CONTEXT.md');
      setContextContent(doc.content || '');
    } catch {
      setContextContent('');
    } finally {
      setContextLoading(false);
    }
  }, []);

  // Poll tasks every POLL_INTERVAL ms
  useEffect(() => {
    fetchTasks();
    fetchAgents();
    fetchContext();
    const interval = setInterval(fetchTasks, POLL_INTERVAL);
    return () => clearInterval(interval);
  }, [fetchTasks, fetchAgents, fetchContext]);

  // Sync temp value when config loads
  useEffect(() => {
    if (config && !tempOverride) {
      setTempValue(config.temperature);
    }
  }, [config, tempOverride]);

  // ── Task actions ──
  const handleAddTask = async () => {
    if (!newTask.title.trim()) {
      setAddError('Title is required');
      return;
    }
    setAdding(true);
    setAddError(null);
    try {
      const created = await addTask({
        title: newTask.title.trim(),
        description: newTask.description.trim(),
        role: newTask.role || undefined,
        acceptance: newTask.acceptance.trim(),
        files: newTask.files
          .split(/[\n,]/)
          .map((f) => f.trim())
          .filter(Boolean),
      });
      // ── Optimistic: store the returned task and track its ID ──
      setOptimisticTasks((prev) => [...prev, created]);
      setLocallyAddedIds((prev) => new Set(prev).add(created.id));
      setNewTask(EMPTY_FORM);
      setShowAddForm(false);
      // Still refetch to get the board updated, but optimistic task preserves it
      await fetchTasks();
    } catch (err: any) {
      setAddError(err?.message || 'Failed to add task');
    } finally {
      setAdding(false);
    }
  };

  const handleRefresh = async () => {
    setRefreshing(true);
    // Clear optimistic state on manual refresh to force a clean slate
    setOptimisticTasks([]);
    setLocallyAddedIds(new Set());
    await fetchTasks();
  };

  const handleEditTitle = async (task: Task) => {
    if (editTitle.trim() && editTitle.trim() !== task.title) {
      setSaving(true);
      try {
        await patchTask(task.id, { title: editTitle.trim() });
        await fetchTasks();
      } catch {
        // ignore
      } finally {
        setSaving(false);
      }
    }
    setEditingId(null);
  };

  const handleSaveExpanded = async (task: Task) => {
    setSaving(true);
    try {
      const patch: Partial<Task> = {};
      if (editDescription !== task.description) patch.description = editDescription;
      if (editAcceptance !== task.acceptance) patch.acceptance = editAcceptance;
      if (editNotes !== task.notes) patch.notes = editNotes;
      if (Object.keys(patch).length > 0) {
        await patchTask(task.id, patch);
        await fetchTasks();
      }
    } catch {
      // ignore
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (id: string) => {
    setDeleting(true);
    try {
      await deleteTask(id);
      setDeleteId(null);
      // Remove from optimistic tracking as well
      setOptimisticTasks((prev) => prev.filter((t) => t.id !== id));
      setLocallyAddedIds((prev) => {
        const next = new Set(prev);
        next.delete(id);
        return next;
      });
      await fetchTasks();
    } catch {
      // ignore
    } finally {
      setDeleting(false);
    }
  };

  const toggleExpand = (id: string) => {
    setExpandedTasks((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  };

  // ── Context injection ──
  const handleSaveContext = async () => {
    setContextSaving(true);
    setContextSaved(false);
    try {
      let content = contextContent;
      if (extraContext.trim()) {
        content +=
          (content.endsWith('\n') ? '' : '\n') +
          `\n<!-- Extra context (injected from LiveTaskPanel) -->\n${extraContext.trim()}\n`;
      }
      await updateDoc('CONTEXT.md', content);
      setContextContent(content);
      setContextSaved(true);
      setTimeout(() => setContextSaved(false), 2000);
    } catch {
      // ignore
    } finally {
      setContextSaving(false);
    }
  };

  // ── Agent hints ──
  const handleSaveHints = () => {
    try { sessionStorage.setItem('slmcode:agentHints', agentHints); } catch { /* ignore */ }
    setHintsSaved(true);
    setTimeout(() => setHintsSaved(false), 2000);
  };

  const handleQuickFillRole = (role: string) => {
    setNewTask((p) => ({ ...p, role: p.role === role ? '' : role }));
  };

  // ── Derived: tasks grouped by column ──
  const columns = board?.columns || [];
  const byColumn = board?.by_column || {};
  const allTasks = useMemo(() => board?.tasks || [], [board?.tasks]);
  const taskCount = allTasks.length;
  const taskHealth = useMemo(() => summarizeTaskHealth(allTasks), [allTasks]);

  // ── Render ──
  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden text-xs">
      {/* ═══════════════════════════════════════════════════ */}
      {/* ── SECTION 5: Context Injection ── */}
      {/* ═══════════════════════════════════════════════════ */}
      <div className="shrink-0 border-b border-gray-200 p-3 dark:border-gray-800 glass-alt">
        <div className="flex items-center gap-2 mb-2">
          <Target size={13} className="text-brand-500 shrink-0" />
          <span className="text-[10px] font-semibold uppercase tracking-wider text-gray-400">
            Context Injection
          </span>
        </div>

        {/* Pinned skills */}
        {config?.pinned_skills && config.pinned_skills.length > 0 && (
          <div className="flex flex-wrap gap-1 mb-2">
            {config.pinned_skills.map((s) => (
              <span
                key={s}
                className="badge-brand text-[10px]"
              >
                {s}
              </span>
            ))}
          </div>
        )}

        {/* Current query/context preview */}
        <div className="text-[10px] text-gray-500 dark:text-gray-400 mb-2 line-clamp-2">
          {contextLoading ? (
            <span className="italic">Loading context…</span>
          ) : contextContent ? (
            contextContent.slice(0, 140) + (contextContent.length > 140 ? '…' : '')
          ) : (
            <span className="italic">No CONTEXT.md yet</span>
          )}
        </div>

        {/* Extra context input */}
        <textarea
          value={extraContext}
          onChange={(e) => setExtraContext(e.target.value)}
          placeholder="Extra context notes for workers…"
          rows={2}
          className="input text-[10px] resize-none mb-2"
        />

        <button
          onClick={handleSaveContext}
          disabled={contextSaving || !extraContext.trim()}
          className="btn-primary text-[10px] py-1 px-2.5 gap-1 w-full"
        >
          {contextSaving ? (
            <Loader2 size={11} className="animate-spin" />
          ) : contextSaved ? (
            <Check size={11} />
          ) : (
            <Save size={11} />
          )}
          {contextSaved ? 'Saved' : 'Save to CONTEXT.md'}
        </button>
      </div>

      {/* ═══════════════════════════════════════════════════ */}
      {/* ── SECTION 6: Agent Hints ── */}
      {/* ═══════════════════════════════════════════════════ */}
      <div className="p-3 border-b border-gray-200 dark:border-gray-800 glass-alt">
        <div className="flex items-center gap-2 mb-2">
          <Lightbulb size={13} className="text-yellow-500 shrink-0" />
          <span className="text-[10px] font-semibold uppercase tracking-wider text-gray-400">
            Agent Hints
          </span>
        </div>
        <p className="text-[9px] text-gray-400 mb-2">
          Extra notes for agents. Not saved to CONTEXT.md — stored per-session.
        </p>
        <textarea
          value={agentHints}
          onChange={(e) => setAgentHints(e.target.value)}
          placeholder="e.g. 'Prefer functional components', 'Use async/await', 'Follow existing error-handling patterns'…"
          rows={3}
          className="input text-[10px] resize-none mb-2"
        />
        <button
          onClick={handleSaveHints}
          disabled={!agentHints.trim()}
          className="btn-primary text-[10px] py-1 px-2.5 gap-1 w-full"
        >
          {hintsSaved ? (
            <Check size={11} />
          ) : (
            <Save size={11} />
          )}
          {hintsSaved ? 'Hints saved' : 'Save hints'}
        </button>
      </div>

      {/* ═══════════════════════════════════════════════════ */}
      {/* ── SECTION 7: Worker Precision ── */}
      {/* ═══════════════════════════════════════════════════ */}
      <div className="p-3 border-b border-gray-200 dark:border-gray-800 glass-alt">
        <div className="flex items-center gap-2 mb-2">
          <Thermometer size={13} className="text-amber-500 shrink-0" />
          <span className="text-[10px] font-semibold uppercase tracking-wider text-gray-400">
            Worker Precision
          </span>
        </div>

        {/* Current config temperature display */}
        <div className="flex items-center justify-between mb-2 px-2 py-1 rounded bg-white/50 dark:bg-gray-900/50">
          <span className="text-[10px] text-gray-400">Config temp</span>
          <span className="text-[10px] font-mono font-bold text-gray-700 dark:text-gray-300">
            {config?.temperature?.toFixed(2) ?? '0.70'}
          </span>
        </div>

        {/* Override toggle */}
        <label className="flex items-center gap-2 mb-2 cursor-pointer">
          <input
            type="checkbox"
            checked={tempOverride}
            onChange={(e) => {
              setTempOverride(e.target.checked);
              if (!e.target.checked) setTempValue(config?.temperature ?? 0.7);
            }}
            className="rounded border-gray-300 dark:border-gray-600 text-brand-600 focus:ring-brand-500"
          />
          <span className="text-[10px] text-gray-500 dark:text-gray-400">
            Override for next run
          </span>
        </label>

        {/* Temperature slider */}
        {tempOverride && (
          <div className="space-y-1.5">
            <div className="flex items-center gap-2">
              <Sliders size={11} className="text-gray-400 shrink-0" />
              <input
                type="range"
                min="0"
                max="2"
                step="0.05"
                value={tempValue}
                onChange={(e) => setTempValue(parseFloat(e.target.value))}
                className="flex-1 h-1 accent-brand-500"
              />
              <span className="text-[10px] font-mono font-bold text-brand-600 dark:text-brand-400 w-9 text-right tabular-nums">
                {tempValue.toFixed(2)}
              </span>
            </div>
            <div className="flex justify-between text-[9px] text-gray-400 px-1">
              <span>Precise</span>
              <span>Balanced</span>
              <span>Creative</span>
            </div>
          </div>
        )}
      </div>

      {/* ═══════════════════════════════════════════════════ */}
      {/* ── SECTION 1+2: Task Header + Add Button ── */}
      {/* ═══════════════════════════════════════════════════ */}
      <div className="p-3 border-b border-gray-200 dark:border-gray-800 flex items-center justify-between">
        <div className="flex items-center gap-2 min-w-0">
          <Settings2 size={13} className="text-gray-400 shrink-0" />
          <span className="text-[10px] font-semibold uppercase tracking-wider text-gray-400">
            Tasks
          </span>
          {taskCount > 0 && (
            <span className="badge-neutral text-[9px]">{taskCount}</span>
          )}
          {locallyAddedIds.size > 0 && (
            <span className="text-[9px] text-amber-500" title={`${locallyAddedIds.size} task(s) pending sync`}>
              (+{locallyAddedIds.size})
            </span>
          )}
        </div>
        <div className="flex items-center gap-1 shrink-0">
          <button
            onClick={handleRefresh}
            disabled={refreshing}
            className="btn-ghost p-1 rounded-md"
            title="Refresh tasks"
          >
            <RefreshCw size={13} className={clsx(refreshing && 'animate-spin')} />
          </button>
          <button
            onClick={() => setShowAddForm(!showAddForm)}
            className="btn-ghost p-1 rounded-md"
            title="Add task"
          >
            <Plus size={14} className={clsx(showAddForm && 'rotate-45 transition-transform')} />
          </button>
        </div>
      </div>

      {taskCount > 0 && (
        <div className="p-3 border-b border-gray-200 dark:border-gray-800 glass-alt">
          <div className="mb-2 flex items-center justify-between gap-2">
            <div className="flex items-center gap-2 min-w-0">
              <ListChecks size={13} className="text-emerald-500 shrink-0" />
              <span className="text-[10px] font-semibold uppercase tracking-wider text-gray-400">
                Task Health
              </span>
            </div>
            <span className="text-[9px] text-gray-400 tabular-nums">
              {taskHealth.withFiles}/{taskCount} scoped
            </span>
          </div>

          <div className="grid grid-cols-4 gap-1.5">
            <TaskMetric label="Active" value={taskHealth.active} tone={taskHealth.active > 0 ? 'info' : 'neutral'} />
            <TaskMetric label="Blocked" value={taskHealth.blocked} tone={taskHealth.blocked > 0 ? 'error' : 'neutral'} />
            <TaskMetric label="Failed" value={taskHealth.failed} tone={taskHealth.failed > 0 ? 'error' : 'neutral'} />
            <TaskMetric label="Retries" value={taskHealth.retries} tone={taskHealth.retries > 0 ? 'warning' : 'neutral'} />
          </div>

          {taskHealth.attention.length > 0 ? (
            <div className="mt-2 space-y-1">
              {taskHealth.attention.map((task) => (
                <div
                  key={task.id}
                  className="flex items-center gap-2 rounded-md border border-amber-200 bg-amber-50 px-2 py-1.5 text-[10px] text-amber-800 dark:border-amber-900/70 dark:bg-amber-950/30 dark:text-amber-300"
                  title={task.title}
                >
                  <AlertCircle size={11} className="shrink-0" />
                  <span className="truncate font-medium">{task.title}</span>
                  <span className="ml-auto shrink-0 font-mono opacity-70">{taskStateLabel(task)}</span>
                </div>
              ))}
            </div>
          ) : (
            <div className="mt-2 rounded-md border border-emerald-200 bg-emerald-50 px-2 py-1.5 text-[10px] text-emerald-700 dark:border-emerald-900/70 dark:bg-emerald-950/30 dark:text-emerald-300">
              No blocked, failed, or retrying tasks.
            </div>
          )}
        </div>
      )}

      {/* ── Add Task Form ── */}
      {showAddForm && (
        <div className="p-3 border-b border-gray-200 dark:border-gray-800 glass-alt space-y-2 animate-slide-up">
          {/* Quick-fill role buttons */}
          <div>
            <div className="text-[9px] text-gray-400 mb-1.5">Quick role:</div>
            <div className="flex flex-wrap gap-1">
              {COMMON_ROLES.map((role) => (
                <button
                  key={role}
                  onClick={() => handleQuickFillRole(role)}
                  className={clsx(
                    'text-[9px] py-0.5 px-1.5 rounded-full border transition-colors',
                    newTask.role === role
                      ? 'bg-brand-100 border-brand-300 text-brand-700 dark:bg-brand-900/40 dark:border-brand-700 dark:text-brand-300'
                      : 'border-gray-200 dark:border-gray-700 text-gray-500 dark:text-gray-400 hover:border-brand-300 dark:hover:border-brand-600',
                  )}
                >
                  {ROLE_ICONS[role] || ''} {role}
                </button>
              ))}
            </div>
          </div>

          <input
            ref={newTaskTitleRef}
            type="text"
            value={newTask.title}
            onChange={(e) => setNewTask((p) => ({ ...p, title: e.target.value }))}
            placeholder="Task title *"
            className="input text-xs h-8"
          />
          <textarea
            value={newTask.description}
            onChange={(e) => setNewTask((p) => ({ ...p, description: e.target.value }))}
            placeholder="Description"
            rows={2}
            className="input text-xs resize-none"
          />
          <select
            value={newTask.role}
            onChange={(e) => setNewTask((p) => ({ ...p, role: e.target.value }))}
            className="input text-xs h-8"
          >
            <option value="">Role (any)</option>
            {agents.map((a) => (
              <option key={a.id} value={a.id}>
                {ROLE_ICONS[a.id] || ''} {a.title || a.id}
              </option>
            ))}
          </select>
          <input
            type="text"
            value={newTask.acceptance}
            onChange={(e) => setNewTask((p) => ({ ...p, acceptance: e.target.value }))}
            placeholder="Acceptance criteria"
            className="input text-xs h-8"
          />
          <input
            type="text"
            value={newTask.files}
            onChange={(e) => setNewTask((p) => ({ ...p, files: e.target.value }))}
            placeholder="Files (comma or newline separated)"
            className="input text-xs h-8"
          />
          {addError && (
            <div className="flex items-center gap-1 text-[10px] text-red-500">
              <AlertCircle size={10} />
              {addError}
            </div>
          )}
          <div className="flex items-center gap-2">
            <button
              onClick={handleAddTask}
              disabled={adding || !newTask.title.trim()}
              className="btn-primary text-[10px] py-1 px-3 gap-1"
            >
              {adding ? (
                <Loader2 size={10} className="animate-spin" />
              ) : (
                <Plus size={10} />
              )}
              Add
            </button>
            <button
              onClick={() => {
                setShowAddForm(false);
                setNewTask(EMPTY_FORM);
                setAddError(null);
              }}
              className="btn-ghost text-[10px] py-1 px-3"
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {/* ═══════════════════════════════════════════════════ */}
      {/* ── SECTION 1: Task List ── */}
      {/* ═══════════════════════════════════════════════════ */}
      <div className="min-h-0 flex-1 overflow-y-auto">
        {tasksLoading && taskCount === 0 && optimisticTasks.length === 0 ? (
          <div className="flex items-center justify-center py-8 text-[10px] text-gray-400">
            <Loader2 size={12} className="animate-spin mr-2" />
            Loading tasks…
          </div>
        ) : taskCount === 0 && optimisticTasks.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-8 text-[10px] text-gray-400 gap-1">
            <MessageSquare size={16} className="mb-1 opacity-50" />
            No tasks yet
            <button
              onClick={() => setShowAddForm(true)}
              className="text-brand-500 hover:text-brand-600 mt-1"
            >
              + Add your first task
            </button>
          </div>
        ) : (
          <div className="divide-y divide-gray-100 dark:divide-gray-800/50">
            {(columns || []).map((col) => {
              const tasks = byColumn[col] || [];
              if (tasks.length === 0) return null;

              const colColors = COLUMN_COLORS[col] || COLUMN_COLORS.scoped;

              return (
                <div key={col}>
                  {/* Column header with colored badge */}
                  <div className="px-3 py-1.5 flex items-center gap-2 glass-alt">
                    <span
                      className={clsx(
                        'text-[10px] font-semibold uppercase tracking-wider',
                        colColors.text,
                      )}
                    >
                      {col.replace(/_/g, ' ')}
                    </span>
                    <span className={clsx('text-[9px] px-1.5 py-0.5 rounded-full font-medium', colColors.badge)}>
                      {tasks.length}
                    </span>
                  </div>

                  {/* Tasks in column */}
                  {(tasks || []).map((task) => {
                    const isExpanded = expandedTasks.has(task.id);
                    const isEditing = editingId === task.id;
                    const isConfirmingDelete = deleteId === task.id;
                    const isPending = locallyAddedIds.has(task.id);

                    return (
                      <div
                        key={task.id}
                        className={clsx(
                          'px-3 py-2 hover:bg-gray-50 dark:hover:bg-gray-800/40 transition-colors border-b border-gray-50 dark:border-gray-800/30',
                          isPending && 'bg-amber-50/50 dark:bg-amber-900/10',
                        )}
                      >
                        {/* Compact row */}
                        <div className="flex items-start gap-2">
                          {/* Expand chevron */}
                          <button
                            onClick={() => toggleExpand(task.id)}
                            className="shrink-0 mt-0.5 text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
                          >
                            {isExpanded ? (
                              <ChevronDown size={12} />
                            ) : (
                              <ChevronRight size={12} />
                            )}
                          </button>

                          {/* Role icon */}
                          {task.role && ROLE_ICONS[task.role] && (
                            <span className="shrink-0 mt-0.5 text-[11px]" title={task.role}>
                              {ROLE_ICONS[task.role]}
                            </span>
                          )}

                          {/* Title (inline-edit on double-click) */}
                          <div
                            className="flex-1 min-w-0"
                            onDoubleClick={() => {
                              setEditingId(task.id);
                              setEditTitle(task.title);
                            }}
                          >
                            {isEditing ? (
                              <div className="flex items-center gap-1">
                                <input
                                  ref={editTitleRef}
                                  type="text"
                                  value={editTitle}
                                  onChange={(e) => setEditTitle(e.target.value)}
                                  onKeyDown={(e) => {
                                    if (e.key === 'Enter') handleEditTitle(task);
                                    if (e.key === 'Escape') setEditingId(null);
                                  }}
                                  onBlur={() => handleEditTitle(task)}
                                  className="input text-xs h-7 flex-1"
                                />
                                <button
                                  onMouseDown={(e) => {
                                    e.preventDefault();
                                    handleEditTitle(task);
                                  }}
                                  className="text-brand-500 hover:text-brand-600"
                                >
                                  <Save size={12} />
                                </button>
                                <button
                                  onMouseDown={(e) => {
                                    e.preventDefault();
                                    setEditingId(null);
                                  }}
                                  className="text-gray-400 hover:text-gray-600"
                                >
                                  <X size={12} />
                                </button>
                              </div>
                            ) : (
                              <div>
                                <div className="text-xs font-medium text-gray-800 dark:text-gray-200 truncate">
                                  {isPending && (
                                    <span className="text-amber-500 mr-1" title="Pending sync">⏳</span>
                                  )}
                                  {task.title}
                                </div>
                                {/* Acceptance criteria preview */}
                                {task.acceptance && (
                                  <div className="text-[9px] text-gray-400 dark:text-gray-500 truncate mt-0.5 flex items-center gap-1">
                                    <Check size={9} className="shrink-0" />
                                    {task.acceptance.slice(0, 60)}{task.acceptance.length > 60 ? '…' : ''}
                                  </div>
                                )}
                              </div>
                            )}
                          </div>

                          {/* Badges */}
                          <div className="flex items-center gap-1 shrink-0">
                            {/* Files count */}
                            {task.files?.length > 0 && (
                              <span
                                className="inline-flex items-center gap-0.5 text-[9px] text-gray-400"
                                title={task.files.join(', ')}
                              >
                                <FileText size={9} />
                                {task.files.length}
                              </span>
                            )}

                            {/* Status */}
                            <span
                              className={clsx(
                                'text-[9px]',
                                STATUS_BADGE[task.status] || 'badge-neutral',
                              )}
                            >
                              {task.status}
                            </span>

                            {/* Delete button */}
                            {isConfirmingDelete ? (
                              <div className="flex items-center gap-1">
                                <button
                                  onClick={() => handleDelete(task.id)}
                                  disabled={deleting}
                                  className="text-red-500 hover:text-red-700"
                                  title="Confirm delete"
                                >
                                  {deleting ? (
                                    <Loader2 size={11} className="animate-spin" />
                                  ) : (
                                    <Check size={12} />
                                  )}
                                </button>
                                <button
                                  onClick={() => setDeleteId(null)}
                                  className="text-gray-400 hover:text-gray-600"
                                  title="Cancel"
                                >
                                  <X size={12} />
                                </button>
                              </div>
                            ) : (
                              <button
                                onClick={() => setDeleteId(task.id)}
                                className="text-gray-400 hover:text-red-500 transition-colors opacity-0 group-hover:opacity-100 hover:opacity-100"
                                title="Delete task"
                              >
                                <Trash2 size={11} />
                              </button>
                            )}
                          </div>
                        </div>

                        {/* Expanded details */}
                        {isExpanded && (
                          <div className="ml-5 mt-2 space-y-2 pl-2 border-l-2 border-gray-100 dark:border-gray-800">
                            {/* Task age */}
                            {task.updated_at && (
                              <div className="flex items-center gap-1 text-[9px] text-gray-400">
                                <Clock size={9} />
                                Created {timeAgo(task.updated_at)}
                                {isPending && (
                                  <span className="text-amber-500 ml-1">(pending sync)</span>
                                )}
                              </div>
                            )}

                            {/* Description */}
                            <div>
                              <div className="text-[9px] font-semibold text-gray-400 uppercase mb-0.5 flex items-center gap-1">
                                <FileText size={9} /> Description
                              </div>
                              <textarea
                                value={editDescription}
                                onChange={(e) => setEditDescription(e.target.value)}
                                rows={2}
                                className="input text-[10px] resize-none"
                                placeholder="No description"
                                onFocus={() => {
                                  if (editingId !== task.id) {
                                    setEditingId(task.id);
                                    setEditDescription(task.description || '');
                                    setEditAcceptance(task.acceptance || '');
                                    setEditNotes(task.notes || '');
                                  }
                                }}
                              />
                            </div>

                            {/* Acceptance criteria */}
                            <div>
                              <div className="text-[9px] font-semibold text-gray-400 uppercase mb-0.5 flex items-center gap-1">
                                <Check size={9} /> Acceptance
                              </div>
                              <textarea
                                value={editAcceptance}
                                onChange={(e) => setEditAcceptance(e.target.value)}
                                rows={2}
                                className="input text-[10px] resize-none"
                                placeholder="No acceptance criteria"
                              />
                            </div>

                            {/* Checklist */}
                            {task.checklist && task.checklist.length > 0 && (
                              <div>
                                <div className="text-[9px] font-semibold text-gray-400 uppercase mb-0.5 flex items-center gap-1">
                                  <ListChecks size={9} /> Checklist
                                </div>
                                <div className="space-y-0.5">
                                  {(task.checklist || []).map((item) => (
                                    <div
                                      key={item.id}
                                      className="flex items-start gap-1.5 text-[10px]"
                                    >
                                      <span
                                        className={clsx(
                                          'shrink-0 mt-0.5',
                                          item.done
                                            ? 'text-emerald-500'
                                            : 'text-gray-300 dark:text-gray-600',
                                        )}
                                      >
                                        {item.done ? (
                                          <Check size={10} />
                                        ) : (
                                          <div className="w-2.5 h-2.5 rounded-sm border border-gray-300 dark:border-gray-600" />
                                        )}
                                      </span>
                                      <span
                                        className={clsx(
                                          item.done &&
                                            'line-through text-gray-400 dark:text-gray-500',
                                        )}
                                      >
                                        {item.text}
                                      </span>
                                    </div>
                                  ))}
                                </div>
                              </div>
                            )}

                            {/* Output */}
                            {task.output && (
                              <div>
                                <div className="text-[9px] font-semibold text-gray-400 uppercase mb-0.5 flex items-center gap-1">
                                  <MessageSquare size={9} /> Output
                                </div>
                                <div className="text-[10px] text-gray-600 dark:text-gray-400 bg-gray-50 dark:bg-gray-800/50 rounded p-1.5 max-h-24 overflow-y-auto whitespace-pre-wrap font-mono">
                                  {task.output.slice(0, 500)}
                                  {task.output.length > 500 && '…'}
                                </div>
                              </div>
                            )}

                            {/* Notes */}
                            <div>
                              <div className="text-[9px] font-semibold text-gray-400 uppercase mb-0.5 flex items-center gap-1">
                                <Tag size={9} /> Notes
                              </div>
                              <textarea
                                value={editNotes}
                                onChange={(e) => setEditNotes(e.target.value)}
                                rows={2}
                                className="input text-[10px] resize-none"
                                placeholder="No notes"
                              />
                            </div>

                            {/* Save button for expanded edits */}
                            <button
                              onClick={() => handleSaveExpanded(task)}
                              disabled={saving}
                              className="btn-primary text-[10px] py-1 px-2.5 gap-1"
                            >
                              {saving ? (
                                <Loader2 size={10} className="animate-spin" />
                              ) : (
                                <Save size={10} />
                              )}
                              Save changes
                            </button>
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              );
            })}
          </div>
        )}
      </div>

      {/* ── Footer stats ── */}
      <div className="px-3 py-1.5 border-t border-gray-200 dark:border-gray-800 glass-alt flex items-center justify-between text-[9px] text-gray-400">
        <span>
          {taskCount} task{taskCount !== 1 ? 's' : ''}
          {locallyAddedIds.size > 0 && ` (${locallyAddedIds.size} pending)`}
        </span>
        <div className="flex items-center gap-2">
          <span className="tabular-nums">
            {columns.length} column{columns.length !== 1 ? 's' : ''}
          </span>
          <span className="tabular-nums text-gray-300 dark:text-gray-600">·</span>
          <span className="tabular-nums">
            Updated {timeAgo(new Date(lastPollTime).toISOString()) || 'just now'}
          </span>
        </div>
      </div>
    </div>
  );
}

function TaskMetric({
  label,
  value,
  tone,
}: {
  label: string;
  value: number;
  tone: 'neutral' | 'info' | 'warning' | 'error';
}) {
  return (
    <div className="rounded-md bg-white/70 px-2 py-1.5 dark:bg-gray-900/50">
      <div className="text-[9px] uppercase text-gray-400">{label}</div>
      <div
        className={clsx(
          'mt-0.5 font-mono text-sm font-bold tabular-nums',
          tone === 'info' && 'text-blue-600 dark:text-blue-300',
          tone === 'warning' && 'text-amber-600 dark:text-amber-300',
          tone === 'error' && 'text-red-600 dark:text-red-300',
          tone === 'neutral' && 'text-gray-700 dark:text-gray-300',
        )}
      >
        {value}
      </div>
    </div>
  );
}

function summarizeTaskHealth(tasks: Task[]) {
  let active = 0;
  let blocked = 0;
  let failed = 0;
  let retries = 0;
  let withFiles = 0;
  const attention: Task[] = [];

  for (const task of tasks) {
    if (task.files?.length > 0) withFiles += 1;
    if (isBlockedTask(task)) blocked += 1;
    if (isFailedTask(task)) failed += 1;
    if (isActiveTask(task)) active += 1;
    if ((task.retries || 0) > 0) retries += 1;
    if (
      attention.length < 3 &&
      (isBlockedTask(task) || isFailedTask(task) || (task.retries || 0) > 0)
    ) {
      attention.push(task);
    }
  }

  return { active, blocked, failed, retries, withFiles, attention };
}

function taskStateLabel(task: Task): string {
  return task.status || task.column || 'task';
}

function isActiveTask(task: Task): boolean {
  return task.status === 'running' ||
    task.status === 'review' ||
    task.status === 'correcting' ||
    task.column === 'in_progress' ||
    task.column === 'in_review';
}

function isBlockedTask(task: Task): boolean {
  return task.status === 'blocked' || task.column === 'blocked';
}

function isFailedTask(task: Task): boolean {
  return task.status === 'failed' || task.column === 'failed';
}
