import { useState } from 'react';
import { useSortable } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { GripVertical, User, FileText, Trash2, CheckCircle, AlertTriangle } from 'lucide-react';
import { patchTask, deleteTask } from '@/api/client';
import type { Task } from '@/types';
import clsx from 'clsx';

interface TaskCardProps {
  task: Task;
  onUpdate: () => void;
  isDragOverlay?: boolean;
}

const STATUS_ICON: Record<string, React.ReactNode> = {
  done: <CheckCircle size={12} className="text-emerald-500" />,
  failed: <AlertTriangle size={12} className="text-red-500" />,
};

export default function TaskCard({ task, onUpdate, isDragOverlay }: TaskCardProps) {
  const [expanded, setExpanded] = useState(false);
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(task.title);

  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: task.id });

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.3 : undefined,
  };

  const handleSaveTitle = async () => {
    const trimmed = title.trim();
    if (trimmed && trimmed !== task.title) {
      try {
        await patchTask(task.id, { title: trimmed });
        onUpdate();
      } catch { /* revert */ }
    }
    setEditing(false);
  };

  const handleDelete = async (e: React.MouseEvent) => {
    e.stopPropagation();
    if (!confirm(`Delete task "${task.title}"?`)) return;
    try {
      await deleteTask(task.id);
      onUpdate();
    } catch { /* ignore */ }
  };

  return (
    <div
      ref={setNodeRef}
      style={style}
      className={clsx(
        'card p-3 cursor-default group transition-shadow select-none',
        isDragOverlay && 'shadow-xl',
        task.status === 'failed' && 'ring-1 ring-red-300 dark:ring-red-800',
        task.status === 'done' && 'ring-1 ring-emerald-300 dark:ring-emerald-800',
      )}
      onClick={() => setExpanded(!expanded)}
    >
      {/* Header */}
      <div className="flex items-start gap-2">
        <button
          {...attributes}
          {...listeners}
          className="mt-0.5 p-0.5 rounded hover:bg-gray-100 dark:hover:bg-gray-800 text-gray-400 cursor-grab active:cursor-grabbing shrink-0"
          onClick={(e) => e.stopPropagation()}
        >
          <GripVertical size={14} />
        </button>

        <div className="flex-1 min-w-0">
          {/* Title */}
          {editing ? (
            <input
              autoFocus
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              onBlur={handleSaveTitle}
              onKeyDown={(e) => {
                if (e.key === 'Enter') handleSaveTitle();
                if (e.key === 'Escape') { setTitle(task.title); setEditing(false); }
              }}
              onClick={(e) => e.stopPropagation()}
              className="input text-xs py-1 px-2"
            />
          ) : (
            <div
              className="text-xs font-medium truncate cursor-text hover:text-brand-600 dark:hover:text-brand-400"
              onDoubleClick={(e) => { e.stopPropagation(); setEditing(true); setTitle(task.title); }}
            >
              {task.title}
            </div>
          )}

          {/* Meta */}
          <div className="flex items-center gap-2 mt-1 flex-wrap">
            {task.role && (
              <span className="flex items-center gap-1 text-[10px] text-gray-500">
                <User size={10} />
                {task.role}
              </span>
            )}
            {task.files && task.files.length > 0 && (
              <span className="flex items-center gap-1 text-[10px] text-gray-500">
                <FileText size={10} />
                {task.files.length} file{task.files.length > 1 ? 's' : ''}
              </span>
            )}
            {STATUS_ICON[task.status]}
            {task.priority > 0 && (
              <span className="badge-warn text-[9px]">P{task.priority}</span>
            )}
          </div>

          {/* Badge */}
          <div className="mt-2 flex items-center gap-1">
            {task.depends_on?.length > 0 && (
              <span className="badge-neutral text-[9px]">
                {task.depends_on.length} dep
              </span>
            )}
            {task.retries > 0 && (
              <span className="badge-warn text-[9px]">
                {task.retries} retry
              </span>
            )}
          </div>
        </div>

        {/* Delete (hover) */}
        <button
          onClick={handleDelete}
          className="opacity-0 group-hover:opacity-100 p-1 rounded hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 hover:text-red-500 transition-all shrink-0"
          title="Delete task"
        >
          <Trash2 size={12} />
        </button>
      </div>

      {/* Expanded details */}
      {expanded && (
        <div className="mt-3 pt-3 border-t border-gray-100 dark:border-gray-800 space-y-2 animate-fade-in" onClick={(e) => e.stopPropagation()}>
          {task.description && (
            <div>
              <div className="text-[10px] font-semibold text-gray-400 uppercase mb-1">Description</div>
              <div className="text-xs text-gray-600 dark:text-gray-400 whitespace-pre-wrap line-clamp-4">
                {task.description}
              </div>
            </div>
          )}
          {task.acceptance && (
            <div>
              <div className="text-[10px] font-semibold text-gray-400 uppercase mb-1">Acceptance</div>
              <div className="text-xs text-gray-600 dark:text-gray-400 whitespace-pre-wrap line-clamp-3">
                {task.acceptance}
              </div>
            </div>
          )}
          {task.checklist && task.checklist.length > 0 && (
            <div>
              <div className="text-[10px] font-semibold text-gray-400 uppercase mb-1">Checklist</div>
              <ul className="space-y-1">
                {task.checklist.map((item, i) => (
                  <li key={item.id || i} className="text-xs text-gray-600 dark:text-gray-400 flex items-start gap-1">
                    <span className={item.done ? 'text-green-500' : 'text-gray-300'}>-</span>
                    <span className="truncate">{typeof item === 'string' ? item : item.text}</span>
                  </li>
                ))}
              </ul>
            </div>
          )}
          {task.error && (
            <div className="bg-red-50 dark:bg-red-900/20 rounded-lg p-2 text-xs text-red-600 dark:text-red-400">
              {task.error}
            </div>
          )}
          {task.output && (
            <div>
              <div className="text-[10px] font-semibold text-gray-400 uppercase mb-1">Output</div>
              <div className="text-xs font-mono text-gray-600 dark:text-gray-400 bg-gray-50 dark:bg-gray-800 rounded-lg p-2 whitespace-pre-wrap line-clamp-6">
                {task.output}
              </div>
            </div>
          )}
          {task.notes && (
            <div>
              <div className="text-[10px] font-semibold text-gray-400 uppercase mb-1">Notes</div>
              <div className="text-xs text-gray-500 italic">{task.notes}</div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
