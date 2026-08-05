import { useState, useEffect, useCallback } from 'react';
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
import { getBoard, patchTask, updateTasks } from '@/api/client';
import type { Board as BoardType, Task } from '@/types';
import TaskCard from './TaskCard';
import clsx from 'clsx';

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

export default function KanbanBoard() {
  const [board, setBoard] = useState<BoardType | null>(null);
  const [activeTask, setActiveTask] = useState<Task | null>(null);
  const [loading, setLoading] = useState(true);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
  );

  const fetchBoard = useCallback(async () => {
    try {
      const b = await getBoard();
      setBoard(b);
    } catch (e) {
      console.error('Failed to load board:', e);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchBoard();
    const interval = setInterval(fetchBoard, 3000);
    return () => clearInterval(interval);
  }, [fetchBoard]);

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
    if (COLUMN_ORDER.includes(overId)) {
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
    setBoard({ ...board, tasks: newTasks });

    try {
      await patchTask(taskId, { column: targetCol });
    } catch {
      fetchBoard(); // revert
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

  const byColumn = board.by_column || {};
  COLUMN_ORDER.forEach((col) => {
    if (!byColumn[col]) byColumn[col] = [];
  });

  return (
    <div className="h-full p-4">
      <div className="mb-4">
        <h1 className="text-lg font-bold">Kanban Board</h1>
        {board.plan?.summary && (
          <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">{board.plan.summary}</p>
        )}
      </div>

      <DndContext
        sensors={sensors}
        collisionDetection={closestCorners}
        onDragStart={handleDragStart}
        onDragEnd={handleDragEnd}
      >
        <div className="flex gap-3 overflow-x-auto pb-4 h-[calc(100%-4rem)]">
          {COLUMN_ORDER.map((col) => {
            const tasks = byColumn[col] || [];
            return (
              <div
                key={col}
                className={clsx(
                  'flex-shrink-0 w-64 rounded-xl border p-3 flex flex-col',
                  `kanban-col-${col}`,
                )}
              >
                <div className="flex items-center justify-between mb-2 px-1">
                  <h3 className={clsx('text-xs font-bold uppercase tracking-wider', `col-${col}`)}>
                    {COLUMN_LABELS[col] || col}
                  </h3>
                  <span className="badge-neutral text-[10px]">{tasks.length}</span>
                </div>

                <SortableContext
                  items={tasks.map((t) => t.id)}
                  strategy={verticalListSortingStrategy}
                >
                  <div className="flex-1 space-y-2 overflow-y-auto min-h-[60px]">
                    {tasks.map((task) => (
                      <TaskCard key={task.id} task={task} onUpdate={fetchBoard} />
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
              <TaskCard task={activeTask} onUpdate={fetchBoard} isDragOverlay />
            </div>
          )}
        </DragOverlay>
      </DndContext>
    </div>
  );
}
