import { useState, useEffect, useCallback, useMemo } from 'react';
import {
  DndContext,
  DragOverlay,
  closestCorners,
  PointerSensor,
  useSensor,
  useSensors,
  type DragStartEvent,
  type DragEndEvent,
} from '@dnd-kit/core';
import { SortableContext, verticalListSortingStrategy } from '@dnd-kit/sortable';
import { getBoard, patchTask, addTask } from '@/api/client';
import type { Board as BoardType, Task } from '@/types';
import TaskCard from './TaskCard';
import clsx from 'clsx';
import { Loader2, Plus, RefreshCw, X } from 'lucide-react';

const COLUMN_LABELS: Record<string, string> = {
  to_scope: 'To Scope',
  scoped: 'Scoped',
  ready_to_dev: 'Ready',
  in_progress: 'In Progress',
  in_review: 'In Review',
  blocked: 'Blocked',
  done: 'Done',
};

const COLUMN_ORDER = ['to_scope', 'scoped', 'ready_to_dev', 'in_progress', 'in_review', 'blocked', 'done'];

interface NewTaskDraft {
  title: string;
  description: string;
  role: string;
  column: string;
  priority: string;
  files: string;
  acceptance: string;
}

const EMPTY_DRAFT: NewTaskDraft = {
  title: '',
  description: '',
  role: 'worker',
  column: 'to_scope',
  priority: '0',
  files: '',
  acceptance: '',
};

export default function KanbanBoard() {
  const [board, setBoard] = useState<BoardType | null>(null);
  const [activeTask, setActiveTask] = useState<Task | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [showAdd, setShowAdd] = useState(false);
  const [draft, setDraft] = useState<NewTaskDraft>(EMPTY_DRAFT);
  const [savingAdd, setSavingAdd] = useState(false);
  const [addError, setAddError] = useState('');

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
  );

  const fetchBoard = useCallback(async () => {
    try {
      setRefreshing(true);
      const b = await getBoard();
      setBoard(b);
    } catch (e) {
      console.error('Failed to load board:', e);
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, []);

  useEffect(() => {
    fetchBoard();
    const interval = setInterval(fetchBoard, 3000);
    return () => clearInterval(interval);
  }, [fetchBoard]);

  const columns = useMemo(() => {
    const fromBoard = board?.columns?.length ? board.columns : [];
    return [...new Set([...COLUMN_ORDER, ...fromBoard])];
  }, [board?.columns]);

  const byColumn = useMemo(() => groupTasks(board?.tasks || [], columns), [board?.tasks, columns]);
  const totalTasks = board?.tasks?.length || 0;

  const handleDragStart = (event: DragStartEvent) => {
    const taskId = event.active.id as string;
    if (board) {
      const task = board.tasks.find((t) => t.id === taskId);
      setActiveTask(task || null);
    }
  };

  const handleDragEnd = async (event: DragEndEvent) => {
    setActiveTask(null);
    const { active, over } = event;
    if (!over || !board) return;

    const taskId = active.id as string;
    const overId = over.id as string;

    // Determine target column
    let targetCol: string;
    if (columns.includes(overId)) {
      targetCol = overId;
    } else {
      const overTask = board.tasks.find((t) => t.id === overId);
      if (!overTask) return;
      targetCol = overTask.column;
    }

    const task = board.tasks.find((t) => t.id === taskId);
    if (!task || task.column === targetCol) return;

    // Optimistic update
    const newTasks = board.tasks.map((t) =>
      t.id === taskId ? { ...t, column: targetCol } : t,
    );
    setBoard({ ...board, tasks: newTasks, by_column: groupTasks(newTasks, columns) });

    try {
      await patchTask(taskId, { column: targetCol });
    } catch {
      fetchBoard(); // revert
    }
  };

  const handleOpenAdd = (column = 'to_scope') => {
    setDraft({ ...EMPTY_DRAFT, column });
    setAddError('');
    setShowAdd(true);
  };

  const handleAddTask = async () => {
    const title = draft.title.trim();
    if (!title) {
      setAddError('Title is required');
      return;
    }
    setSavingAdd(true);
    setAddError('');
    try {
      const created = await addTask({
        title,
        description: draft.description.trim(),
        role: draft.role.trim() || undefined,
        column: draft.column || 'to_scope',
        status: draft.column || 'to_scope',
        priority: Number.parseInt(draft.priority, 10) || 0,
        acceptance: draft.acceptance.trim(),
        files: splitList(draft.files),
      });
      if (board) {
        const tasks = [created, ...(board.tasks || [])];
        setBoard({ ...board, tasks, by_column: groupTasks(tasks, columns) });
      }
      setShowAdd(false);
      setDraft(EMPTY_DRAFT);
      await fetchBoard();
    } catch (err: any) {
      setAddError(err?.message || 'Failed to add task');
    } finally {
      setSavingAdd(false);
    }
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="flex items-center gap-3 text-gray-400">
          <div className="w-5 h-5 border-2 border-brand-500 border-t-transparent rounded-full animate-spin" />
          Loading board…
        </div>
      </div>
    );
  }

  if (!board) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-center space-y-3">
          <p className="text-gray-400">No board data yet.</p>
          <p className="text-xs text-gray-500">Run a query to populate the kanban board.</p>
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-full flex-col bg-gray-50/70 p-4 dark:bg-gray-950">
      <div className="mb-4 flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-lg font-bold">Kanban Board</h1>
            <span className="badge-neutral text-[10px]">{totalTasks} task{totalTasks === 1 ? '' : 's'}</span>
          </div>
          {board.plan?.summary && (
            <p className="mt-1 max-w-4xl text-sm text-gray-500 dark:text-gray-400">
              {board.plan.summary}
            </p>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <button
            onClick={fetchBoard}
            disabled={refreshing}
            className="btn-secondary h-9 gap-2 px-3 text-xs"
            title="Refresh board"
          >
            <RefreshCw size={14} className={clsx(refreshing && 'animate-spin')} />
            Refresh
          </button>
          <button
            onClick={() => handleOpenAdd()}
            className="btn-primary h-9 gap-2 px-3 text-xs"
          >
            <Plus size={14} />
            Add task
          </button>
        </div>
      </div>

      {showAdd && (
        <div className="mb-4 rounded-lg border border-brand-200 bg-white p-4 shadow-sm dark:border-brand-900 dark:bg-gray-900">
          <div className="mb-3 flex items-center justify-between gap-3">
            <div>
              <h2 className="text-sm font-bold">Add Task</h2>
              <p className="text-xs text-gray-500 dark:text-gray-400">Create a task directly on any board column.</p>
            </div>
            <button
              onClick={() => {
                setShowAdd(false);
                setDraft(EMPTY_DRAFT);
                setAddError('');
              }}
              className="btn-ghost rounded-lg p-1.5"
              title="Close"
            >
              <X size={16} />
            </button>
          </div>

          <div className="grid gap-3 lg:grid-cols-4">
            <label className="lg:col-span-2">
              <span className="label">Title</span>
              <input
                autoFocus
                value={draft.title}
                onChange={(e) => setDraft((d) => ({ ...d, title: e.target.value }))}
                className="input"
                placeholder="Small, testable task title"
              />
            </label>
            <label>
              <span className="label">Column</span>
              <select
                value={draft.column}
                onChange={(e) => setDraft((d) => ({ ...d, column: e.target.value }))}
                className="input"
              >
                {columns.map((col) => (
                  <option key={col} value={col}>{COLUMN_LABELS[col] || col}</option>
                ))}
              </select>
            </label>
            <label>
              <span className="label">Role</span>
              <input
                value={draft.role}
                onChange={(e) => setDraft((d) => ({ ...d, role: e.target.value }))}
                className="input-mono"
                placeholder="worker"
              />
            </label>
            <label className="lg:col-span-2">
              <span className="label">Description</span>
              <textarea
                value={draft.description}
                onChange={(e) => setDraft((d) => ({ ...d, description: e.target.value }))}
                className="input min-h-24 resize-y"
                placeholder="Context, constraints, and expected implementation notes"
              />
            </label>
            <label className="lg:col-span-2">
              <span className="label">Acceptance</span>
              <textarea
                value={draft.acceptance}
                onChange={(e) => setDraft((d) => ({ ...d, acceptance: e.target.value }))}
                className="input min-h-24 resize-y"
                placeholder="How the task should be verified"
              />
            </label>
            <label>
              <span className="label">Priority</span>
              <input
                type="number"
                value={draft.priority}
                onChange={(e) => setDraft((d) => ({ ...d, priority: e.target.value }))}
                className="input"
                min={0}
              />
            </label>
            <label className="lg:col-span-3">
              <span className="label">Files</span>
              <input
                value={draft.files}
                onChange={(e) => setDraft((d) => ({ ...d, files: e.target.value }))}
                className="input-mono"
                placeholder="web/src/components/Board/TaskCard.tsx, pkg/server/server.go"
              />
            </label>
          </div>

          {addError && <div className="mt-3 text-xs text-red-500">{addError}</div>}
          <div className="mt-4 flex items-center gap-2">
            <button onClick={handleAddTask} disabled={savingAdd || !draft.title.trim()} className="btn-primary h-9 gap-2 text-xs">
              {savingAdd ? <Loader2 size={14} className="animate-spin" /> : <Plus size={14} />}
              Add task
            </button>
            <button
              onClick={() => {
                setShowAdd(false);
                setDraft(EMPTY_DRAFT);
                setAddError('');
              }}
              className="btn-ghost h-9 text-xs"
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      <DndContext
        sensors={sensors}
        collisionDetection={closestCorners}
        onDragStart={handleDragStart}
        onDragEnd={handleDragEnd}
      >
        <div className="flex min-h-0 flex-1 gap-3 overflow-x-auto pb-4">
          {columns.map((col) => {
            const tasks = byColumn[col] || [];
            return (
              <div
                key={col}
                className={clsx(
                  'flex w-72 flex-shrink-0 flex-col rounded-xl border p-3',
                  `kanban-col-${col}`,
                )}
              >
                <div className="mb-2 flex items-center justify-between gap-2 px-1">
                  <h3 className={clsx('text-xs font-bold uppercase tracking-wider', `col-${col}`)}>
                    {COLUMN_LABELS[col] || col}
                  </h3>
                  <div className="flex items-center gap-1">
                    <span className="badge-neutral text-[10px]">{tasks.length}</span>
                    <button
                      onClick={() => handleOpenAdd(col)}
                      className="btn-ghost rounded-md p-1"
                      title={`Add task to ${COLUMN_LABELS[col] || col}`}
                    >
                      <Plus size={12} />
                    </button>
                  </div>
                </div>

                <SortableContext
                  items={tasks.map((t) => t.id)}
                  strategy={verticalListSortingStrategy}
                >
                  <div className="flex-1 space-y-2 overflow-y-auto min-h-[60px]">
                    {tasks.map((task) => (
                      <TaskCard key={task.id} task={task} columns={columns} columnLabels={COLUMN_LABELS} onUpdate={fetchBoard} />
                    ))}
                    {tasks.length === 0 && (
                      <div className="flex items-center justify-center h-16 text-[10px] text-gray-400 italic border border-dashed rounded-lg border-gray-300 dark:border-gray-700">
                        Drop tasks here
                      </div>
                    )}
                  </div>
                </SortableContext>
              </div>
            );
          })}
        </div>

        <DragOverlay>
          {activeTask && (
            <div className="opacity-90 rotate-2">
              <TaskCard task={activeTask} columns={columns} columnLabels={COLUMN_LABELS} onUpdate={fetchBoard} isDragOverlay />
            </div>
          )}
        </DragOverlay>
      </DndContext>
    </div>
  );
}

function groupTasks(tasks: Task[], columns: string[]): Record<string, Task[]> {
  const grouped: Record<string, Task[]> = {};
  for (const col of columns) grouped[col] = [];
  for (const task of tasks || []) {
    const col = task.column || task.status || 'to_scope';
    if (!grouped[col]) grouped[col] = [];
    grouped[col].push(task);
  }
  return grouped;
}

function splitList(value: string): string[] {
  return value
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter(Boolean);
}
