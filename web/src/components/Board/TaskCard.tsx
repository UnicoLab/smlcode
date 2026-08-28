import { useEffect, useRef, useState } from 'react';
import type { MouseEvent, ReactNode } from 'react';
import { useSortable } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { ticketInfo } from './ticketInfo';
import {
  AlertTriangle,
  ArrowRight,
  CheckCircle,
  ChevronDown,
  ChevronRight,
  Edit3,
  FileText,
  GripVertical,
  Save,
  Trash2,
  User,
  Wrench,
  X,
} from 'lucide-react';
import { patchTask, deleteTask } from '@/api/client';
import type { Task } from '@/types';
import clsx from 'clsx';
import { useConfirm } from '@/components/ui/Modal';
import { useToast } from '@/components/ui/Toast';

interface TaskCardProps {
  task: Task;
  columns: string[];
  columnLabels: Record<string, string>;
  onUpdate: () => void;
  isDragOverlay?: boolean;
}

interface TaskDraft {
  title: string;
  description: string;
  role: string;
  column: string;
  status: string;
  priority: string;
  files: string;
  depends_on: string;
  acceptance: string;
  notes: string;
}

const STATUS_ICON: Record<string, ReactNode> = {
  done: <CheckCircle size={12} className="text-emerald-500" />,
  failed: <AlertTriangle size={12} className="text-red-500" />,
};

const STATUS_OPTIONS = ['todo', 'scoped', 'ready', 'running', 'review', 'correcting', 'blocked', 'failed', 'done'];

export default function TaskCard({ task, columns, columnLabels, onUpdate, isDragOverlay }: TaskCardProps) {
  const confirm = useConfirm();
  const toast = useToast();
  const [expanded, setExpanded] = useState(false);
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [draft, setDraft] = useState<TaskDraft>(() => taskToDraft(task));
  const titleFieldRef = useRef<HTMLTextAreaElement>(null);
  const ticket = ticketInfo(task);

  useEffect(() => {
    setDraft(taskToDraft(task));
  }, [task]);

  useEffect(() => {
    if (editing) {
      titleFieldRef.current?.focus();
    }
  }, [editing]);

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

  const handleSave = async () => {
    const title = draft.title.trim();
    if (!title) return;
    setSaving(true);
    try {
      const patch: Partial<Task> = {};
      if (title !== task.title) patch.title = title;
      if (draft.description !== (task.description || '')) patch.description = draft.description;
      if (draft.role.trim() !== (task.role || '')) patch.role = draft.role.trim();
      if (draft.column !== (task.column || 'to_scope')) patch.column = draft.column;
      if (draft.status !== (task.status || '')) patch.status = draft.status;

      const priority = Number.parseInt(draft.priority, 10) || 0;
      if (priority !== (task.priority || 0)) patch.priority = priority;

      const files = splitList(draft.files);
      if (files.join('\n') !== (task.files || []).join('\n')) patch.files = files;

      const deps = splitList(draft.depends_on);
      if (deps.join('\n') !== (task.depends_on || []).join('\n')) patch.depends_on = deps;

      if (draft.acceptance !== (task.acceptance || '')) patch.acceptance = draft.acceptance;
      if (draft.notes !== (task.notes || '')) patch.notes = draft.notes;

      if (Object.keys(patch).length > 0) {
        await patchTask(task.id, patch);
        onUpdate();
      }
      setEditing(false);
      setExpanded(true);
    } finally {
      setSaving(false);
    }
  };

  const handleCancel = () => {
    setDraft(taskToDraft(task));
    setEditing(false);
  };

  const handleDelete = async (e: MouseEvent) => {
    e.stopPropagation();
    const ok = await confirm({
      title: `Delete task "${task.title}"?`,
      description: 'The task is removed from the board.',
      confirmLabel: 'Delete task',
    });
    if (!ok) return;
    try {
      await deleteTask(task.id);
      onUpdate();
    } catch (err) {
      toast.reportError(err, 'Could not delete the task');
    }
  };

  return (
    <div
      ref={setNodeRef}
      style={style}
      className={clsx(
        'card group cursor-default select-none p-3 transition-shadow',
        isDragOverlay && 'shadow-xl',
        task.status === 'failed' && 'ring-1 ring-red-300 dark:ring-red-800',
        task.status === 'done' && 'ring-1 ring-emerald-300 dark:ring-emerald-800',
      )}
      role="button"
      tabIndex={0}
      onClick={() => !editing && setExpanded((v) => !v)}
      onKeyDown={(e) => {
        if (!editing && (e.key === 'Enter' || e.key === ' ')) {
          e.preventDefault();
          setExpanded((v) => !v);
        }
      }}
    >
      <div className="flex items-start gap-2">
        <button
          {...attributes}
          {...listeners}
          className="mt-0.5 shrink-0 cursor-grab rounded p-0.5 text-gray-400 hover:bg-gray-100 active:cursor-grabbing dark:hover:bg-gray-800"
          onClick={(e) => e.stopPropagation()}
          title="Drag task"
        >
          <GripVertical size={14} />
        </button>

        <div className="min-w-0 flex-1">
          <div className="flex items-start gap-1.5">
            <button
              onClick={(e) => {
                e.stopPropagation();
                setExpanded((v) => !v);
              }}
              className="mt-0.5 shrink-0 rounded text-gray-400 hover:text-gray-700 dark:hover:text-gray-200"
              title={expanded ? 'Collapse task' : 'Show full task details'}
            >
              {expanded ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
            </button>
            <div
              className="min-w-0 flex-1 text-xs font-semibold leading-snug text-gray-800 hover:text-brand-600 dark:text-gray-100 dark:hover:text-brand-400"
              title={task.title}
            >
              <span className={clsx(!expanded && 'line-clamp-2')}>{task.title}</span>
            </div>
          </div>

          <div className="mt-2 flex flex-wrap items-center gap-1.5">
            {task.role && (
              <span className="flex max-w-full items-center gap-1 rounded-md bg-gray-50 px-1.5 py-0.5 text-[10px] text-gray-500 dark:bg-gray-800/70" title={task.role}>
                <User size={10} />
                <span className="truncate">{task.role}</span>
              </span>
            )}
            {task.files && task.files.length > 0 && (
              <span className="flex items-center gap-1 rounded-md bg-gray-50 px-1.5 py-0.5 text-[10px] text-gray-500 dark:bg-gray-800/70" title={task.files.join('\n')}>
                <FileText size={10} />
                {task.files.length} file{task.files.length > 1 ? 's' : ''}
              </span>
            )}
            <span className="badge-neutral text-[9px]" title={`Column: ${task.column || 'to_scope'}`}>
              {columnLabels[task.column] || task.column || 'To Scope'}
            </span>
            {STATUS_ICON[task.status]}
            {task.priority > 0 && <span className="badge-warn text-[9px]">P{task.priority}</span>}
            {task.depends_on?.length > 0 && <span className="badge-neutral text-[9px]">{task.depends_on.length} dep</span>}
            {task.retries > 0 && <span className="badge-warn text-[9px]">{task.retries} retry</span>}
            {/* A correction ticket looks exactly like planned work — same
                shape, same badges — but it is a defect a gate found, with a
                reproduction and an owner, possibly on its second attempt and
                possibly moved here by the project manager. Reading the whole
                description to work that out is what makes a board feel like a
                log rather than a plan. */}
            {ticket.isTicket && (
              <span
                className="badge-warn flex items-center gap-1 text-[9px]"
                title="A defect a gate found, not planned work"
              >
                <Wrench size={9} />
                {ticket.attempt > 1 ? `fix · attempt ${ticket.attempt}` : 'fix'}
              </span>
            )}
            {ticket.reassignedTo && (
              <span
                className="badge-brand flex items-center gap-1 text-[9px]"
                title={`The project manager moved this to ${ticket.reassignedTo}`}
              >
                <ArrowRight size={9} />
                {ticket.reassignedTo}
              </span>
            )}
          </div>
        </div>

        <div className="flex shrink-0 items-center gap-1">
          <button
            onClick={(e) => {
              e.stopPropagation();
              setEditing(true);
              setExpanded(true);
            }}
            className="rounded p-1 text-gray-400 transition-colors hover:bg-brand-50 hover:text-brand-600 dark:hover:bg-brand-900/20"
            title="Edit task"
          >
            <Edit3 size={12} />
          </button>
          <button
            onClick={handleDelete}
            className="rounded p-1 text-gray-400 transition-colors hover:bg-red-50 hover:text-red-500 dark:hover:bg-red-900/20"
            title="Delete task"
          >
            <Trash2 size={12} />
          </button>
        </div>
      </div>

      {expanded && (
        <div
          className="mt-3 space-y-2 border-t border-gray-100 pt-3 dark:border-gray-800"
          role="button"
          tabIndex={0}
          onClick={(e) => e.stopPropagation()}
          onKeyDown={(e) => e.stopPropagation()}
        >
          {editing ? (
            <div className="space-y-3">
              <label>
                <span className="label">Title</span>
                <textarea
                  ref={titleFieldRef}
                  value={draft.title}
                  onChange={(e) => setDraft((d) => ({ ...d, title: e.target.value }))}
                  rows={2}
                  className="input resize-y text-xs"
                />
              </label>
              <div className="grid grid-cols-2 gap-2">
                <label>
                  <span className="label">Column</span>
                  <select
                    value={draft.column}
                    onChange={(e) => setDraft((d) => ({ ...d, column: e.target.value }))}
                    className="input text-xs"
                  >
                    {columns.map((col) => (
                      <option key={col} value={col}>{columnLabels[col] || col}</option>
                    ))}
                  </select>
                </label>
                <label>
                  <span className="label">Status</span>
                  <select
                    value={draft.status}
                    onChange={(e) => setDraft((d) => ({ ...d, status: e.target.value }))}
                    className="input text-xs"
                  >
                    <option value="">inherit</option>
                    {STATUS_OPTIONS.map((status) => (
                      <option key={status} value={status}>{status}</option>
                    ))}
                  </select>
                </label>
                <label>
                  <span className="label">Role</span>
                  <input
                    value={draft.role}
                    onChange={(e) => setDraft((d) => ({ ...d, role: e.target.value }))}
                    className="input-mono text-xs"
                    placeholder="worker"
                  />
                </label>
                <label>
                  <span className="label">Priority</span>
                  <input
                    type="number"
                    value={draft.priority}
                    onChange={(e) => setDraft((d) => ({ ...d, priority: e.target.value }))}
                    className="input text-xs"
                    min={0}
                  />
                </label>
              </div>
              <label>
                <span className="label">Description</span>
                <textarea
                  value={draft.description}
                  onChange={(e) => setDraft((d) => ({ ...d, description: e.target.value }))}
                  rows={4}
                  className="input resize-y text-xs"
                  placeholder="No description"
                />
              </label>
              <label>
                <span className="label">Acceptance</span>
                <textarea
                  value={draft.acceptance}
                  onChange={(e) => setDraft((d) => ({ ...d, acceptance: e.target.value }))}
                  rows={3}
                  className="input resize-y text-xs"
                  placeholder="No acceptance criteria"
                />
              </label>
              <label>
                <span className="label">Files</span>
                <textarea
                  value={draft.files}
                  onChange={(e) => setDraft((d) => ({ ...d, files: e.target.value }))}
                  rows={2}
                  className="input-mono resize-y text-xs"
                  placeholder="One file per line, or comma separated"
                />
              </label>
              <label>
                <span className="label">Dependencies</span>
                <input
                  value={draft.depends_on}
                  onChange={(e) => setDraft((d) => ({ ...d, depends_on: e.target.value }))}
                  className="input-mono text-xs"
                  placeholder="T1, T2"
                />
              </label>
              <label>
                <span className="label">Notes</span>
                <textarea
                  value={draft.notes}
                  onChange={(e) => setDraft((d) => ({ ...d, notes: e.target.value }))}
                  rows={3}
                  className="input resize-y text-xs"
                  placeholder="Manual notes"
                />
              </label>
              <div className="flex items-center gap-2">
                <button onClick={handleSave} disabled={saving || !draft.title.trim()} className="btn-primary h-8 gap-1.5 px-3 text-xs">
                  <Save size={13} />
                  {saving ? 'Saving' : 'Save'}
                </button>
                <button onClick={handleCancel} disabled={saving} className="btn-ghost h-8 gap-1.5 px-3 text-xs">
                  <X size={13} />
                  Cancel
                </button>
              </div>
            </div>
          ) : (
            <div className="space-y-2">
              <TaskText label="Description" value={task.description} />
              <TaskText label="Acceptance" value={task.acceptance} />
              {task.files && task.files.length > 0 && <TaskText label="Files" value={task.files.join('\n')} mono />}
              {task.depends_on && task.depends_on.length > 0 && <TaskText label="Dependencies" value={task.depends_on.join(', ')} mono />}
              {task.checklist && task.checklist.length > 0 && (
                <div>
                  <div className="mb-1 text-[10px] font-semibold uppercase text-gray-400">Checklist</div>
                  <ul className="space-y-1">
                    {task.checklist.map((item, i) => (
                      <li key={item.id || i} className="flex items-start gap-1 text-xs text-gray-600 dark:text-gray-400">
                        <span className={item.done ? 'text-green-500' : 'text-gray-300'}>-</span>
                        <span className="break-words">{typeof item === 'string' ? item : item.text}</span>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {task.error && (
                <div className="whitespace-pre-wrap break-words rounded-lg bg-red-50 p-2 text-xs text-red-600 dark:bg-red-900/20 dark:text-red-400">
                  {task.error}
                </div>
              )}
              {task.output && (
                <div>
                  <div className="mb-1 text-[10px] font-semibold uppercase text-gray-400">Output</div>
                  <div className="max-h-56 overflow-auto rounded-lg bg-gray-50 p-2 font-mono text-xs text-gray-600 whitespace-pre-wrap break-words dark:bg-gray-800 dark:text-gray-400">
                    {task.output}
                  </div>
                </div>
              )}
              <TaskText label="Notes" value={task.notes} />
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function TaskText({ label, value, mono = false }: { label: string; value?: string; mono?: boolean }) {
  if (!value) return null;
  return (
    <div>
      <div className="mb-1 text-[10px] font-semibold uppercase text-gray-400">{label}</div>
      <div
        className={clsx(
          'whitespace-pre-wrap break-words rounded-md bg-gray-50 px-2 py-1.5 text-xs text-gray-600 dark:bg-gray-800/60 dark:text-gray-400',
          mono && 'font-mono',
        )}
      >
        {value}
      </div>
    </div>
  );
}

function taskToDraft(task: Task): TaskDraft {
  return {
    title: task.title || '',
    description: task.description || '',
    role: task.role || '',
    column: task.column || 'to_scope',
    status: task.status || '',
    priority: String(task.priority || 0),
    files: (task.files || []).join('\n'),
    depends_on: (task.depends_on || []).join(', '),
    acceptance: task.acceptance || '',
    notes: task.notes || '',
  };
}

function splitList(value: string): string[] {
  return value
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter(Boolean);
}
