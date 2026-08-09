import { useState, useMemo, useCallback } from 'react';
import {
  FileCode,
  MessageSquare,
  GitBranch,
  PlusCircle,
  Flag,
  Eye,
  ChevronDown,
  ChevronRight,
  Send,
  Clock,
  FilePlus,
  FileMinus,
  FilePenLine,
} from 'lucide-react';
import { addTask, updateDoc, getDoc } from '@/api/client';
import type { RunEvent } from '@/types';
import clsx from 'clsx';

// ── Types ──

type FileStatus = 'changed' | 'created' | 'deleted' | 'unknown';

interface FileInfo {
  path: string;
  status: FileStatus;
  events: RunEvent[];
  lastEvent: RunEvent | null;
}

interface FileComment {
  id: string;
  text: string;
  timestamp: number;
}

interface LiveFileInspectorProps {
  events: RunEvent[];
  running: boolean;
}

// ── Status helpers ──

const STATUS_META: Record<FileStatus, {
  icon: typeof FileCode;
  label: string;
  cls: string;
}> = {
  changed:   { icon: FilePenLine, label: 'Modified', cls: 'text-amber-500' },
  created:   { icon: FilePlus,    label: 'Created',  cls: 'text-emerald-500' },
  deleted:   { icon: FileMinus,   label: 'Deleted',  cls: 'text-red-500' },
  unknown:   { icon: FileCode,    label: 'Tracked',  cls: 'text-gray-400 dark:text-gray-500' },
};

// ── File path extraction patterns ──

const FILE_PATTERNS: RegExp[] = [
  // go-style: pkg/server/server.go, cmd/slmcode/main.go
  /\b[a-zA-Z_][\w.-]*\/\S*\.(?:go|mod|sum|work)\b/gi,
  // python: src/module/file.py, tests/test_app.py
  /\b[a-zA-Z_][\w.-]*\/\S*\.(?:py|pyx|pyi)\b/gi,
  // typescript / javascript / react: src/components/App.tsx, lib/utils.ts
  /\b[a-zA-Z_][\w.-]*\/\S*\.(?:tsx?|jsx?|mjs|cjs)\b/gi,
  // rust / java / c-family / web / config / data
  /\b[a-zA-Z_][\w.-]*\/\S*\.(?:rs|java|rb|php|c|cc|cpp|cxx|h|hh|hpp|hxx|css|scss|less|html|vue|svelte|yaml|yml|json|toml|md|sql|sh|bash|dockerfile|makefile|cmake|xml|svg|graphql|proto|tf)\b/gi,
  // absolute paths: /some/path/to/file.go
  /(?:\/[\w.-]+)+\.[a-z]{1,6}\b/gi,
  // relative paths: ./path/file.go or ../path/file.go
  /(?:\.\.?\/[\w.-]+)+\.[a-z]{1,6}\b/gi,
];

// ── Helpers ──

function inferStatus(events: RunEvent[]): FileStatus {
  const allKinds = events.map((e) => e.kind).join(' ');
  const allMsgs = events
    .map((e) => e.message + ' ' + (e.output || ''))
    .join(' ')
    .toLowerCase();

  if (/\b(create|new|add|generate|init|scaffold|touch)\b/.test(allKinds)) return 'created';
  if (/\b(created|added|generated|new file|wrote)\b/.test(allMsgs)) return 'created';
  if (/\b(delete|remove|drop|rm)\b/.test(allKinds)) return 'deleted';
  if (/\b(deleted|removed|dropped)\b/.test(allMsgs)) return 'deleted';
  return 'changed';
}

function extractFiles(events: RunEvent[]): FileInfo[] {
  const seen = new Set<string>();
  const results: FileInfo[] = [];

  for (const event of events) {
    const texts = [event.scope, event.message, event.output].filter(Boolean) as string[];

    for (const text of texts) {
      for (const pattern of FILE_PATTERNS) {
        pattern.lastIndex = 0;
        let match: RegExpExecArray | null;
        while ((match = pattern.exec(text)) !== null) {
          let path = match[0]
            .trim()
            .replace(/^["'`([{]+/, '')
            .replace(/["'`)\]:;,!?]+$/, '');

          // skip obviously invalid
          if (path.length < 4) continue;
          if (path === '/' || path === './' || path === '../') continue;

          // normalize leading ./ or leading /
          path = path.replace(/^\.\//, '');

          if (seen.has(path)) continue;
          seen.add(path);
          results.push({
            path,
            status: 'unknown',
            events: [],
            lastEvent: null,
          });
        }
      }
    }
  }

  // Populate events per file and infer status
  for (const file of results) {
    file.events = events.filter((e) => {
      const haystack = [e.scope, e.message, e.output].filter(Boolean).join(' ');
      return haystack.includes(file.path);
    });
    file.lastEvent = file.events[file.events.length - 1] ?? null;
    file.status = inferStatus(file.events);
  }

  return results.sort((a, b) => a.path.localeCompare(b.path));
}

function genId(): string {
  return `${Date.now()}-${Math.random().toString(36).slice(2, 9)}`;
}

// ── Component ──

export default function LiveFileInspector({ events, running }: LiveFileInspectorProps) {
  const [expandedFile, setExpandedFile] = useState<string | null>(null);
  const [comments, setComments] = useState<Record<string, FileComment[]>>({});
  const [inputs, setInputs] = useState<Record<string, string>>({});
  const [pending, setPending] = useState<string | null>(null);
  const [toast, setToast] = useState<{ ok: boolean; msg: string } | null>(null);

  // ── Derived data ──
  const files = useMemo(() => extractFiles(events), [events]);

  const allComments = useMemo(() => {
    const flat: (FileComment & { file: string })[] = [];
    for (const [file, list] of Object.entries(comments)) {
      for (const c of list) flat.push({ ...c, file });
    }
    return flat.sort((a, b) => b.timestamp - a.timestamp);
  }, [comments]);

  // ── Toast helper ──
  const flash = useCallback((ok: boolean, msg: string) => {
    setToast({ ok, msg });
    setTimeout(() => setToast(null), 4000);
  }, []);

  // ── Actions ──

  const handleAddComment = useCallback(
    (filePath: string) => {
      const text = (inputs[filePath] || '').trim();
      if (!text) return;
      const comment: FileComment = { id: genId(), text, timestamp: Date.now() };
      setComments((prev) => ({
        ...prev,
        [filePath]: [...(prev[filePath] || []), comment],
      }));
      setInputs((prev) => ({ ...prev, [filePath]: '' }));
    },
    [inputs],
  );

  const handleSendAsTask = useCallback(
    async (filePath: string, comment: FileComment) => {
      setPending(comment.id);
      try {
        await addTask({
          title: `Review: ${filePath}`,
          description: comment.text,
          role: 'worker',
          files: [filePath],
        });
        flash(true, `Task created for ${filePath}`);
      } catch (err: any) {
        flash(false, err?.message || 'Failed to create task');
      } finally {
        setPending(null);
      }
    },
    [flash],
  );

  const handleFlagForReview = useCallback(
    async (filePath: string) => {
      setPending(`flag-${filePath}`);
      try {
        const eventMessages = events
          .filter((e) => (e.scope || '').includes(filePath))
          .map((e) => e.message)
          .join('; ');
        await addTask({
          title: `[FLAG] Review required: ${filePath}`,
          description: `Manual review flagged.\n\nRecent activity:\n${eventMessages}`.slice(0, 2000),
          role: 'reviewer',
          files: [filePath],
        });
        flash(true, `Review flagged: ${filePath}`);
      } catch (err: any) {
        flash(false, err?.message || 'Failed to flag');
      } finally {
        setPending(null);
      }
    },
    [events, flash],
  );

  const handleAddToContext = useCallback(
    async (filePath: string, comment?: FileComment) => {
      const key = comment?.id ?? `ctx-${filePath}`;
      setPending(key);
      try {
        const doc = await getDoc('CONTEXT.md');
        const note = comment
          ? `\n- **Review note (${filePath})**: ${comment.text}\n`
          : `\n- **Tracked file**: \`${filePath}\`\n`;
        await updateDoc('CONTEXT.md', (doc.content || '') + note);
        flash(true, `Added to CONTEXT.md`);
      } catch (err: any) {
        flash(false, err?.message || 'Could not update context');
      } finally {
        setPending(null);
      }
    },
    [flash],
  );

  // ── Empty state ──

  if (files.length === 0 && allComments.length === 0) {
    return (
      <div className="h-full flex flex-col items-center justify-center text-center px-5">
        <div className="w-14 h-14 rounded-2xl bg-gray-100 dark:bg-gray-800 flex items-center justify-center mb-4">
          <FileCode size={24} className="text-gray-300 dark:text-gray-600" />
        </div>
        <p className="text-sm font-semibold text-gray-500 dark:text-gray-400">
          No files detected
        </p>
        <p className="text-xs text-gray-400 dark:text-gray-500 mt-1.5 max-w-[240px] leading-relaxed">
          Files will appear here as agents explore and modify your codebase during the
          pipeline run.
        </p>
        {running && (
          <div className="mt-4 flex items-center gap-2 px-3 py-1.5 rounded-full bg-brand-50 dark:bg-brand-900/30 border border-brand-200 dark:border-brand-800">
            <span className="relative flex h-2 w-2">
              <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-brand-400 opacity-75" />
              <span className="relative inline-flex rounded-full h-2 w-2 bg-brand-500" />
            </span>
            <span className="text-[11px] font-medium text-brand-600 dark:text-brand-400">
              Watching for changes…
            </span>
          </div>
        )}
      </div>
    );
  }

  // ── Render ──

  return (
    <div className="h-full flex flex-col overflow-hidden">
      {/* ── Toast ── */}
      {toast && (
        <div
          className={clsx(
            'mx-3 mt-2 px-3 py-2 rounded-lg text-xs font-medium animate-slide-up border',
            toast.ok
              ? 'bg-emerald-50 dark:bg-emerald-900/30 text-emerald-700 dark:text-emerald-300 border-emerald-200 dark:border-emerald-800'
              : 'bg-red-50 dark:bg-red-900/30 text-red-700 dark:text-red-300 border-red-200 dark:border-red-800',
          )}
        >
          {toast.msg}
        </div>
      )}

      {/* ═══ Tracked Files header ═══ */}
      <div className="shrink-0 px-3 pt-3 pb-1.5">
        <h3 className="flex items-center gap-2 text-[10px] font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wider">
          <GitBranch size={11} />
          Tracked Files
          {files.length > 0 && (
            <span className="text-gray-300 dark:text-gray-600 font-normal normal-case">
              ({files.length})
            </span>
          )}
        </h3>
      </div>

      {/* ═══ File list ═══ */}
      <div className="flex-1 overflow-y-auto px-2 py-1 space-y-px">
        {files.map((file) => {
          const isOpen = expandedFile === file.path;
          const meta = STATUS_META[file.status];
          const Icon = meta.icon;
          const fileComments = comments[file.path] || [];

          return (
            <div key={file.path}>
              {/* ── File row ── */}
              <button
                type="button"
                onClick={() => setExpandedFile(isOpen ? null : file.path)}
                className={clsx(
                  'w-full flex items-center gap-2 px-2 py-1.5 rounded-lg text-left transition-colors group',
                  isOpen
                    ? 'bg-brand-50 dark:bg-brand-900/20 border border-brand-200 dark:border-brand-800'
                    : 'hover:bg-gray-50 dark:hover:bg-gray-800/50 border border-transparent',
                )}
              >
                {isOpen ? (
                  <ChevronDown size={12} className="text-gray-400 shrink-0" />
                ) : (
                  <ChevronRight size={12} className="text-gray-400 shrink-0" />
                )}
                <Icon size={13} className={clsx(meta.cls, 'shrink-0')} />
                <span className="flex-1 text-[11px] font-mono text-gray-700 dark:text-gray-300 truncate">
                  {file.path}
                </span>
                {fileComments.length > 0 && (
                  <span className="shrink-0 flex items-center gap-0.5 text-[9px] font-semibold text-brand-600 dark:text-brand-400 bg-brand-100 dark:bg-brand-900/30 px-1.5 py-0.5 rounded-full">
                    <MessageSquare size={9} />
                    {fileComments.length}
                  </span>
                )}
                <span
                  className={clsx(
                    'shrink-0 text-[9px] uppercase tracking-wider font-semibold opacity-0 group-hover:opacity-100 transition-opacity',
                    meta.cls,
                  )}
                >
                  {meta.label}
                </span>
              </button>

              {/* ── Expanded detail ── */}
              {isOpen && (
                <div className="ml-6 mt-1 mb-2 border-l-2 border-brand-200 dark:border-brand-800 pl-3 animate-fade-in space-y-2">
                  {/* Snapshot card */}
                  <div className="p-2.5 rounded-lg bg-gray-50 dark:bg-gray-800/50 border border-gray-100 dark:border-gray-700/50">
                    <div className="flex items-center gap-1.5 mb-1.5">
                      <FileCode size={11} className="text-gray-400" />
                      <span className="text-[10px] font-semibold text-gray-400 uppercase tracking-wider">
                        Snapshot
                      </span>
                    </div>

                    <p className="text-[11px] font-mono text-gray-700 dark:text-gray-300 break-all leading-relaxed">
                      {file.path}
                    </p>

                    {file.lastEvent && (
                      <div className="mt-2 pt-2 border-t border-gray-100 dark:border-gray-700">
                        <p className="text-[10px] text-gray-400 font-medium mb-0.5">
                          Last event{' '}
                          <span className="text-gray-500">
                            ({file.lastEvent.phase}/{file.lastEvent.kind})
                          </span>
                        </p>
                        <p className="text-[11px] text-gray-600 dark:text-gray-400 line-clamp-3 leading-relaxed">
                          {file.lastEvent.message}
                        </p>
                        {file.lastEvent.output && (
                          <pre className="mt-1.5 p-2 rounded bg-gray-100 dark:bg-gray-900 font-mono text-[10px] text-gray-500 dark:text-gray-500 max-h-32 overflow-auto whitespace-pre-wrap leading-relaxed">
                            {file.lastEvent.output.slice(0, 600)}
                          </pre>
                        )}
                      </div>
                    )}

                    {file.events.length > 1 && (
                      <p className="mt-1.5 text-[10px] text-gray-400">
                        +{file.events.length - 1} more event
                        {file.events.length > 2 ? 's' : ''} referencing this file
                      </p>
                    )}
                  </div>

                  {/* ── Quick actions ── */}
                  <div className="flex flex-wrap gap-1.5">
                    <button
                      type="button"
                      onClick={() => handleFlagForReview(file.path)}
                      disabled={pending === `flag-${file.path}`}
                      className="inline-flex items-center gap-1 px-2 py-1 rounded text-[10px] font-medium bg-red-50 dark:bg-red-900/20 text-red-600 dark:text-red-400 hover:bg-red-100 dark:hover:bg-red-900/40 transition-colors disabled:opacity-40"
                    >
                      <Flag size={10} />
                      Flag for review
                    </button>
                    <button
                      type="button"
                      onClick={() => handleAddToContext(file.path)}
                      disabled={pending === `ctx-${file.path}`}
                      className="inline-flex items-center gap-1 px-2 py-1 rounded text-[10px] font-medium bg-sky-50 dark:bg-sky-900/20 text-sky-600 dark:text-sky-400 hover:bg-sky-100 dark:hover:bg-sky-900/40 transition-colors disabled:opacity-40"
                    >
                      <PlusCircle size={10} />
                      Add to context
                    </button>
                  </div>

                  {/* ── Inline comments ── */}
                  <div>
                    <div className="flex items-center gap-1.5 mb-1.5">
                      <MessageSquare size={11} className="text-gray-400" />
                      <span className="text-[10px] font-semibold text-gray-400 uppercase tracking-wider">
                        Comments
                      </span>
                      {fileComments.length > 0 && (
                        <span className="text-[9px] text-gray-300 dark:text-gray-600">
                          ({fileComments.length})
                        </span>
                      )}
                    </div>

                    {fileComments.map((c) => (
                      <div
                        key={c.id}
                        className="mb-1.5 p-2 rounded-lg bg-amber-50 dark:bg-amber-900/15 border border-amber-100 dark:border-amber-800/40"
                      >
                        <p className="text-[11px] text-gray-700 dark:text-gray-300 leading-relaxed">
                          {c.text}
                        </p>
                        <div className="flex items-center justify-between mt-1.5">
                          <span className="text-[9px] text-gray-400 flex items-center gap-1">
                            <Clock size={9} />
                            {new Date(c.timestamp).toLocaleTimeString([], {
                              hour: '2-digit',
                              minute: '2-digit',
                            })}
                          </span>
                          <div className="flex items-center gap-2">
                            <button
                              type="button"
                              onClick={() => handleSendAsTask(file.path, c)}
                              disabled={pending === c.id}
                              className="text-[9px] font-medium text-brand-500 hover:text-brand-600 dark:text-brand-400 dark:hover:text-brand-300 transition-colors disabled:opacity-40"
                            >
                              Send to agents
                            </button>
                            <button
                              type="button"
                              onClick={() => handleAddToContext(file.path, c)}
                              disabled={pending === c.id}
                              className="text-[9px] font-medium text-sky-500 hover:text-sky-600 dark:text-sky-400 dark:hover:text-sky-300 transition-colors disabled:opacity-40"
                            >
                              Add to context
                            </button>
                          </div>
                        </div>
                      </div>
                    ))}

                    {/* Comment input */}
                    <div className="flex gap-1.5">
                      <input
                        type="text"
                        value={inputs[file.path] || ''}
                        onChange={(e) =>
                          setInputs((prev) => ({ ...prev, [file.path]: e.target.value }))
                        }
                        onKeyDown={(e) => {
                          if (e.key === 'Enter' && !e.shiftKey) {
                            e.preventDefault();
                            handleAddComment(file.path);
                          }
                        }}
                        placeholder="Add a comment…"
                        className="flex-1 px-2 py-1 rounded text-[11px] bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 text-gray-700 dark:text-gray-300 placeholder-gray-400 dark:placeholder-gray-500 focus:outline-none focus:ring-1 focus:ring-brand-500/40 transition-shadow"
                      />
                      <button
                        type="button"
                        onClick={() => handleAddComment(file.path)}
                        disabled={!inputs[file.path]?.trim()}
                        className="px-2.5 py-1 rounded bg-brand-100 dark:bg-brand-900/30 text-brand-600 dark:text-brand-400 hover:bg-brand-200 dark:hover:bg-brand-900/50 transition-colors disabled:opacity-30"
                        title="Add comment"
                      >
                        <Send size={11} />
                      </button>
                    </div>
                  </div>
                </div>
              )}
            </div>
          );
        })}
      </div>

      {/* ═══ Comment feed ═══ */}
      {allComments.length > 0 && (
        <div className="shrink-0 border-t border-gray-200 dark:border-gray-800 px-3 py-2.5">
          <h3 className="flex items-center gap-2 text-[10px] font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wider mb-2">
            <MessageSquare size={11} />
            Comment Feed
            <span className="text-gray-300 dark:text-gray-600 font-normal normal-case">
              ({allComments.length})
            </span>
          </h3>

          <div className="max-h-36 overflow-y-auto space-y-1.5">
            {allComments.slice(0, 12).map((c) => (
              <div
                key={c.id}
                className="flex items-start gap-2 text-[10px] group hover:bg-gray-50 dark:hover:bg-gray-800/30 px-1 py-0.5 rounded transition-colors"
              >
                <MessageSquare size={10} className="text-amber-400 shrink-0 mt-0.5" />
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-1.5">
                    <span className="font-mono text-[9px] text-brand-500 dark:text-brand-400 truncate font-medium">
                      {c.file}
                    </span>
                    <span className="text-[9px] text-gray-400 shrink-0">
                      {new Date(c.timestamp).toLocaleTimeString([], {
                        hour: '2-digit',
                        minute: '2-digit',
                      })}
                    </span>
                  </div>
                  <p className="text-gray-600 dark:text-gray-400 truncate mt-0.5">
                    {c.text}
                  </p>
                </div>
              </div>
            ))}
            {allComments.length > 12 && (
              <p className="text-[10px] text-gray-400 text-center pt-0.5">
                +{allComments.length - 12} more comments
              </p>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
