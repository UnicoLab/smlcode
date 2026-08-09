import { useState, useMemo, useCallback, useRef, useEffect } from 'react';
import {
  FileCode,
  FilePlus,
  FileMinus,
  FilePenLine,
  MessageSquare,
  GitBranch,
  PlusCircle,
  Send,
  Clock,
  Flag,
  ExternalLink,
  Edit3,
  X,
  CornerDownRight,
  ChevronRight,
  Layers,
  Eye,
  AlertCircle,
} from 'lucide-react';
import { addTask, updateDoc, getDoc, getWorkspaceFile } from '@/api/client';
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

interface LineComment {
  id: string;
  line: number;
  text: string;
  timestamp: number;
}

interface Props {
  events: RunEvent[];
  running: boolean;
}

// ── Status metadata ──

const STATUS_META: Record<
  FileStatus,
  { icon: typeof FileCode; label: string; cls: string; dotCls: string }
> = {
  changed: {
    icon: FilePenLine,
    label: 'Modified',
    cls: 'text-amber-600 dark:text-amber-400',
    dotCls: 'bg-amber-400',
  },
  created: {
    icon: FilePlus,
    label: 'Created',
    cls: 'text-emerald-600 dark:text-emerald-400',
    dotCls: 'bg-emerald-400',
  },
  deleted: {
    icon: FileMinus,
    label: 'Deleted',
    cls: 'text-red-600 dark:text-red-400',
    dotCls: 'bg-red-400',
  },
  unknown: {
    icon: FileCode,
    label: 'Tracked',
    cls: 'text-gray-400 dark:text-gray-500',
    dotCls: 'bg-gray-400',
  },
};

// ── File path extraction patterns (from LiveFileInspector) ──

const FILE_PATTERNS: RegExp[] = [
  /\b[a-zA-Z_][\w.-]*\/\S*\.(?:go|mod|sum|work)\b/gi,
  /\b[a-zA-Z_][\w.-]*\/\S*\.(?:py|pyx|pyi)\b/gi,
  /\b[a-zA-Z_][\w.-]*\/\S*\.(?:tsx?|jsx?|mjs|cjs)\b/gi,
  /\b[a-zA-Z_][\w.-]*\/\S*\.(?:rs|java|rb|php|c|cc|cpp|cxx|h|hh|hpp|hxx|css|scss|less|html|vue|svelte|yaml|yml|json|toml|md|sql|sh|bash|dockerfile|makefile|cmake|xml|svg|graphql|proto|tf)\b/gi,
  /(?:\/[\w.-]+)+\.[a-z]{1,6}\b/gi,
  /(?:\.\.?\/[\w.-]+)+\.[a-z]{1,6}\b/gi,
];

// ── Helpers ──

function inferStatus(events: RunEvent[]): FileStatus {
  const allKinds = events.map((e) => e.kind).join(' ');
  const allMsgs = events
    .map((e) => e.message + ' ' + (e.output || ''))
    .join(' ')
    .toLowerCase();

  if (/\b(create|new|add|generate|init|scaffold|touch)\b/.test(allKinds))
    return 'created';
  if (/\b(created|added|generated|new file|wrote)\b/.test(allMsgs))
    return 'created';
  if (/\b(delete|remove|drop|rm)\b/.test(allKinds)) return 'deleted';
  if (/\b(deleted|removed|dropped)\b/.test(allMsgs)) return 'deleted';
  return 'changed';
}

function extractFiles(events: RunEvent[]): FileInfo[] {
  const seen = new Set<string>();
  const results: FileInfo[] = [];

  // 1. Parse files_changed from worker JSON outputs
  for (const event of events) {
    const output = event.output || '';
    try {
      const m = output.match(/"files_changed"\s*:\s*\[([\s\S]*?)\]/);
      if (m) {
        const arr = JSON.parse('[' + m[1] + ']');
        for (const f of arr) {
          const path = String(f).trim().replace(/^["'`]+|["'`]+$/g, '');
          if (
            path &&
            path.length > 1 &&
            !seen.has(path) &&
            !path.includes('node_modules') &&
            !path.includes('.git/')
          ) {
            seen.add(path);
            results.push({ path, status: 'changed', events: [], lastEvent: null });
          }
        }
      }
    } catch {}
    try {
      const obj = JSON.parse(output);
      if (obj.files_changed && Array.isArray(obj.files_changed)) {
        for (const f of obj.files_changed) {
          const path = String(f).trim();
          if (
            path &&
            path.length > 1 &&
            !seen.has(path) &&
            !path.includes('node_modules') &&
            !path.includes('.git/')
          ) {
            seen.add(path);
            results.push({ path, status: 'changed', events: [], lastEvent: null });
          }
        }
      }
    } catch {}
  }

  // 2. Extract from event messages/scopes using regex + standalone filenames
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
          if (path.length < 2) continue;
          if (path === '/' || path === './' || path === '../') continue;
          path = path.replace(/^\.\//, '');
          if (seen.has(path)) continue;
          seen.add(path);
          results.push({ path, status: 'unknown', events: [], lastEvent: null });
        }
      }
      const simpleRe = /\b([\w.-]+\.(?:go|py|tsx?|jsx?|rs|java|rb|css|html|md|yaml|yml|json|toml))\b/gi;
      let sm: RegExpExecArray | null;
      while ((sm = simpleRe.exec(text)) !== null) {
        const path = sm[1];
        if (seen.has(path)) continue;
        if (path.startsWith('.') && path.length < 4) continue;
        seen.add(path);
        results.push({ path, status: 'unknown', events: [], lastEvent: null });
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

// ── Simple syntax highlighter ──

interface Token {
  text: string;
  type: 'keyword' | 'string' | 'comment' | 'number' | 'function' | 'type' | 'plain';
}

const TOKEN_COLORS: Record<Token['type'], string> = {
  keyword: 'text-purple-600 dark:text-purple-400',
  string: 'text-emerald-600 dark:text-emerald-400',
  comment: 'text-gray-400 dark:text-gray-500 italic',
  number: 'text-amber-600 dark:text-amber-400',
  function: 'text-sky-600 dark:text-sky-400',
  type: 'text-teal-600 dark:text-teal-400',
  plain: 'text-gray-800 dark:text-gray-200',
};

const KEYWORDS = new Set([
  'function', 'const', 'let', 'var', 'if', 'else', 'for', 'while', 'do',
  'return', 'break', 'continue', 'switch', 'case', 'default', 'throw',
  'try', 'catch', 'finally', 'new', 'delete', 'typeof', 'instanceof',
  'in', 'of', 'class', 'extends', 'super', 'this', 'import', 'export',
  'from', 'as', 'async', 'await', 'yield', 'static', 'get', 'set',
  'interface', 'type', 'enum', 'implements', 'abstract', 'private',
  'protected', 'public', 'readonly', 'package', 'go', 'func', 'defer',
  'struct', 'map', 'chan', 'range', 'select', 'nil', 'true', 'false',
  'def', 'pass', 'raise', 'with', 'elif', 'except', 'lambda', 'None',
  'True', 'False', 'and', 'or', 'not', 'is', 'self', 'cls', '__init__',
]);

const TYPES = new Set([
  'string', 'number', 'boolean', 'void', 'null', 'undefined', 'never',
  'any', 'unknown', 'object', 'Array', 'Map', 'Set', 'Promise', 'Error',
  'int', 'float64', 'float32', 'int64', 'int32', 'bool', 'byte', 'rune',
  'uint', 'uint8', 'uint16', 'uint32', 'uint64', 'complex64', 'complex128',
  'str', 'int', 'float', 'list', 'dict', 'tuple', 'bool', 'set', 'frozenset',
]);

function tokenizeLine(line: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;

  while (i < line.length) {
    // Whitespace
    if (/\s/.test(line[i])) {
      let ws = '';
      while (i < line.length && /\s/.test(line[i])) ws += line[i++];
      tokens.push({ text: ws, type: 'plain' });
      continue;
    }

    // Single-line comment
    if (line[i] === '/' && line[i + 1] === '/') {
      tokens.push({ text: line.slice(i), type: 'comment' });
      break;
    }
    if (line[i] === '#') {
      tokens.push({ text: line.slice(i), type: 'comment' });
      break;
    }

    // Multi-line comment start
    if (line[i] === '/' && line[i + 1] === '*') {
      const end = line.indexOf('*/', i + 2);
      if (end !== -1) {
        tokens.push({ text: line.slice(i, end + 2), type: 'comment' });
        i = end + 2;
        continue;
      }
      tokens.push({ text: line.slice(i), type: 'comment' });
      break;
    }

    // Template string
    if (line[i] === '`') {
      const end = line.indexOf('`', i + 1);
      if (end !== -1) {
        tokens.push({ text: line.slice(i, end + 1), type: 'string' });
        i = end + 1;
        continue;
      }
      tokens.push({ text: line.slice(i), type: 'string' });
      break;
    }

    // Single/double-quoted string
    if (line[i] === '"' || line[i] === "'") {
      const quote = line[i];
      let str = quote;
      i++;
      while (i < line.length && line[i] !== quote) {
        if (line[i] === '\\' && i + 1 < line.length) str += line[i++];
        str += line[i++];
      }
      if (i < line.length) str += line[i++];
      tokens.push({ text: str, type: 'string' });
      continue;
    }

    // Number
    if (/[\d.]/.test(line[i]) && !/[a-zA-Z_]/.test(line[i - 1] || '')) {
      let num = '';
      while (i < line.length && /[\d.a-fA-FxXoObB_n]/.test(line[i])) {
        num += line[i++];
      }
      // Only treat as number if starts with digit
      if (/^\d/.test(num)) {
        tokens.push({ text: num, type: 'number' });
        continue;
      } else {
        // Put back and fall through to identifier
        i -= num.length;
      }
    }

    // Identifier / keyword
    let ident = '';
    while (i < line.length && /[\w$]/.test(line[i])) ident += line[i++];

    if (ident) {
      if (KEYWORDS.has(ident)) {
        tokens.push({ text: ident, type: 'keyword' });
      } else if (TYPES.has(ident)) {
        tokens.push({ text: ident, type: 'type' });
      } else if (
        i < line.length &&
        line[i] === '(' &&
        /^[A-Z]/.test(ident)
      ) {
        tokens.push({ text: ident, type: 'type' });
      } else if (
        i < line.length &&
        line[i] === '('
      ) {
        tokens.push({ text: ident, type: 'function' });
      } else {
        tokens.push({ text: ident, type: 'plain' });
      }
      continue;
    }

    // Punctuation / operators
    tokens.push({ text: line[i++], type: 'plain' });
  }

  return tokens;
}

function highlightLine(line: string, idx: number): React.ReactNode {
  const tokens = tokenizeLine(line);
  return (
    <span key={idx}>
      {tokens.map((t, ti) => (
        <span key={ti} className={TOKEN_COLORS[t.type]}>
          {t.text}
        </span>
      ))}
    </span>
  );
}

// ── Component ──

export default function FileInspector({ events, running }: Props) {
  // ── State ──
  const [selectedFile, setSelectedFile] = useState<string | null>(null);
  const [lineComments, setLineComments] = useState<Record<string, Record<number, LineComment[]>>>({});
  const [activeLine, setActiveLine] = useState<{ file: string; line: number } | null>(null);
  const [draftComment, setDraftComment] = useState('');
  const [pending, setPending] = useState<string | null>(null);
  const [toast, setToast] = useState<{ ok: boolean; msg: string } | null>(null);
  const [showCommentPanel, setShowCommentPanel] = useState(true);

  const draftInputRef = useRef<HTMLTextAreaElement>(null);

  const [fetchedContent, setFetchedContent] = useState<string | null>(null);
  const [validFiles, setValidFiles] = useState<Set<string>>(new Set());

  // Validate extracted files against workspace (only show files that exist)
  useEffect(() => {
    const extracted = extractFiles(events);
    const filesToCheck = extracted.map(f => f.path);
    if (filesToCheck.length === 0) return;
    let cancelled = false;
    // Check first 20 files (batch)
    const check = filesToCheck.slice(0, 20);
    Promise.allSettled(check.map(p => getWorkspaceFile(p))).then(results => {
      if (cancelled) return;
      const valid = new Set<string>();
      results.forEach((r, i) => {
        if (r.status === 'fulfilled') valid.add(check[i]);
      });
      setValidFiles(valid);
    });
    return () => { cancelled = true; };
  }, [events]);

  // Fetch actual file content from workspace when a file is selected
  useEffect(() => {
    if (!selectedFile) { setFetchedContent(null); return; }
    let cancelled = false;
    getWorkspaceFile(selectedFile).then((r) => {
      if (!cancelled) setFetchedContent(r.content);
    }).catch(() => {
      if (!cancelled) setFetchedContent(null);
    });
    return () => { cancelled = true; };
  }, [selectedFile]);

  // ── Derived data ──
  const allFiles = useMemo(() => extractFiles(events), [events]);
  // Only show files that exist in workspace, or all files if none validated yet
  const files = useMemo(() => {
    if (validFiles.size > 0) return allFiles.filter(f => validFiles.has(f.path));
    // Also filter out common false positives (files mentioned in agent instructions)
    return allFiles.filter(f => !f.path.startsWith('pkg/') && !f.path.startsWith('cmd/') && f.path !== 'AGENTS.md');
  }, [allFiles, validFiles]);

  const selectedFileInfo = useMemo(
    () => files.find((f) => f.path === selectedFile) ?? null,
    [files, selectedFile],
  );

  const fileContent = useMemo((): string => {
    // Prefer actual file content from workspace API
    if (fetchedContent) return fetchedContent;
    if (!selectedFileInfo) return '';
    for (const event of [...selectedFileInfo.events].reverse()) {
      if (event.output) {
        // If the output looks like code content (not just a brief message), use it
        const out = event.output.trim();
        if (out.length > 40 || out.includes('\n') || out.includes('{') || out.includes('(')) {
          return out;
        }
      }
    }
    // Fallback: concatenate relevant event messages
    return selectedFileInfo.events
      .map((e) => `// [${e.phase}/${e.kind}] ${e.message}`)
      .join('\n');
  }, [selectedFileInfo]);

  const contentLines = useMemo(() => {
    const lines = fileContent.split('\n');
    return lines.length === 1 && !lines[0] ? [] : lines;
  }, [fileContent]);

  const selectedFileComments = useMemo((): LineComment[] => {
    if (!selectedFile) return [];
    const rec = lineComments[selectedFile];
    if (!rec) return [];
    const flat: LineComment[] = [];
    for (const lineNum of Object.keys(rec)) {
      flat.push(...rec[Number(lineNum)]);
    }
    return flat.sort((a, b) => b.timestamp - a.timestamp);
  }, [lineComments, selectedFile]);

  // All comments across all files (for feed)
  const allCommentsFlat = useMemo(() => {
    const flat: (LineComment & { file: string })[] = [];
    for (const [file, rec] of Object.entries(lineComments)) {
      for (const lineNum of Object.keys(rec)) {
        for (const c of rec[Number(lineNum)]) {
          flat.push({ ...c, file });
        }
      }
    }
    return flat.sort((a, b) => b.timestamp - a.timestamp);
  }, [lineComments]);

  // ── Toast helper ──
  const flash = useCallback((ok: boolean, msg: string) => {
    setToast({ ok, msg });
    setTimeout(() => setToast(null), 4000);
  }, []);

  // ── Auto-select first file ──
  useEffect(() => {
    if (files.length > 0 && !selectedFile) {
      setSelectedFile(files[0].path);
    }
    if (files.length === 0) {
      setSelectedFile(null);
    }
  }, [files, selectedFile]);

  // ── Focus draft input when activeLine changes ──
  useEffect(() => {
    if (activeLine) {
      setTimeout(() => draftInputRef.current?.focus(), 50);
    }
  }, [activeLine]);

  // ── Actions ──

  const handleSelectFile = useCallback((path: string) => {
    setSelectedFile((prev) => (prev === path ? null : path));
    setActiveLine(null);
    setDraftComment('');
  }, []);

  const handleLineClick = useCallback(
    (file: string, line: number) => {
      if (activeLine?.file === file && activeLine?.line === line) {
        setActiveLine(null);
        setDraftComment('');
      } else {
        setActiveLine({ file, line });
        setDraftComment('');
      }
    },
    [activeLine],
  );

  const handleAddLineComment = useCallback(
    (file: string, line: number) => {
      const text = draftComment.trim();
      if (!text) return;
      const comment: LineComment = { id: genId(), line, text, timestamp: Date.now() };
      setLineComments((prev) => {
        const fileRec = { ...(prev[file] || {}) };
        const lineRec = { ...(fileRec[line] || {}) };
        return {
          ...prev,
          [file]: {
            ...fileRec,
            [line]: [...(prev[file]?.[line] || []), comment],
          },
        };
      });
      setActiveLine(null);
      setDraftComment('');
      flash(true, `Comment added on line ${line}`);
    },
    [draftComment, flash],
  );

  const handleSendAsTask = useCallback(
    async (filePath: string, comment: LineComment) => {
      setPending(comment.id);
      try {
        await addTask({
          title: `Review: ${filePath}:L${comment.line}`,
          description: comment.text,
          role: 'worker',
          files: [filePath],
        });
        flash(true, `Task created for ${filePath}:L${comment.line}`);
      } catch (err: any) {
        flash(false, err?.message || 'Failed to create task');
      } finally {
        setPending(null);
      }
    },
    [flash],
  );

  const handleAddToContext = useCallback(
    async (filePath: string, comment?: LineComment) => {
      const key = comment?.id ?? `ctx-${filePath}`;
      setPending(key);
      try {
        const doc = await getDoc('CONTEXT.md');
        const note = comment
          ? `\n- **Review note (${filePath}:L${comment.line})**: ${comment.text}\n`
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

  // ── Empty state ──

  if (files.length === 0) {
    return (
      <div className="h-full flex flex-col">
        {/* Header */}
        <div className="shrink-0 px-5 py-4 border-b border-gray-200 dark:border-gray-800">
          <div className="flex items-center justify-between">
            <div>
              <h1 className="text-lg font-bold flex items-center gap-2">
                <FileCode size={20} className="text-brand-500" />
                Code Review
              </h1>
              <p className="text-xs text-gray-500 mt-0.5">
                Review modified files and leave inline comments for agents
              </p>
            </div>
            {running && (
              <div className="flex items-center gap-2 px-3 py-1.5 rounded-full bg-brand-50 dark:bg-brand-900/30 border border-brand-200 dark:border-brand-800">
                <span className="relative flex h-2 w-2">
                  <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-brand-400 opacity-75" />
                  <span className="relative inline-flex rounded-full h-2 w-2 bg-brand-500" />
                </span>
                <span className="text-[11px] font-medium text-brand-600 dark:text-brand-400">
                  Live
                </span>
              </div>
            )}
          </div>
        </div>

        {/* Empty body */}
        <div className="flex-1 flex flex-col items-center justify-center text-center px-5">
          <div className="w-16 h-16 rounded-2xl bg-gray-100 dark:bg-gray-800 flex items-center justify-center mb-4">
            <FileCode size={28} className="text-gray-300 dark:text-gray-600" />
          </div>
          <p className="text-sm font-semibold text-gray-500 dark:text-gray-400">
            No files detected
          </p>
          <p className="text-xs text-gray-400 dark:text-gray-500 mt-1.5 max-w-[260px] leading-relaxed">
            Run a pipeline to see file changes. Files will appear here as agents
            explore and modify your codebase.
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
      </div>
    );
  }

  // ── Main render ──

  return (
    <div className="h-full flex flex-col overflow-hidden">
      {/* ═══ Toast ═══ */}
      {toast && (
        <div
          className={clsx(
            'mx-4 mt-3 px-4 py-2.5 rounded-lg text-xs font-medium animate-slide-up border shadow-sm',
            toast.ok
              ? 'bg-emerald-50 dark:bg-emerald-900/30 text-emerald-700 dark:text-emerald-300 border-emerald-200 dark:border-emerald-800'
              : 'bg-red-50 dark:bg-red-900/30 text-red-700 dark:text-red-300 border-red-200 dark:border-red-800',
          )}
        >
          {toast.msg}
        </div>
      )}

      {/* ═══ Header ═══ */}
      <div className="shrink-0 px-5 py-3 border-b border-gray-200 dark:border-gray-800">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-lg bg-brand-100 dark:bg-brand-900/40 flex items-center justify-center">
              <FileCode size={16} className="text-brand-600 dark:text-brand-400" />
            </div>
            <div>
              <h1 className="text-sm font-bold">Code Review</h1>
              <p className="text-[10px] text-gray-500">
                {files.length} file{files.length !== 1 ? 's' : ''} detected
              </p>
            </div>
          </div>
          <div className="flex items-center gap-3">
            {/* Comment feed toggle */}
            {allCommentsFlat.length > 0 && (
              <button
                onClick={() => setShowCommentPanel((p) => !p)}
                className={clsx(
                  'flex items-center gap-1.5 text-[11px] font-medium px-2.5 py-1.5 rounded-lg transition-colors',
                  showCommentPanel
                    ? 'bg-amber-50 dark:bg-amber-900/20 text-amber-700 dark:text-amber-300'
                    : 'text-gray-500 hover:bg-gray-100 dark:hover:bg-gray-800',
                )}
              >
                <MessageSquare size={13} />
                {allCommentsFlat.length}
              </button>
            )}
            {running && (
              <div className="flex items-center gap-1.5 px-2.5 py-1.5 rounded-full bg-brand-50 dark:bg-brand-900/30 border border-brand-200 dark:border-brand-800">
                <span className="relative flex h-1.5 w-1.5">
                  <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-brand-400 opacity-75" />
                  <span className="relative inline-flex rounded-full h-1.5 w-1.5 bg-brand-500" />
                </span>
                <span className="text-[10px] font-medium text-brand-600 dark:text-brand-400">
                  Live
                </span>
              </div>
            )}
          </div>
        </div>
      </div>

      {/* ═══ Body: File List + Code View ═══ */}
      <div className="flex-1 flex min-h-0 overflow-hidden">
        {/* ── Left: File List Sidebar (280px) ── */}
        <div className="w-[280px] shrink-0 border-r border-gray-200 dark:border-gray-800 flex flex-col bg-gray-50/50 dark:bg-gray-900/30">
          <div className="shrink-0 px-3 py-2.5">
            <h3 className="flex items-center gap-2 text-[10px] font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wider">
              <GitBranch size={11} />
              Files
              <span className="ml-auto text-gray-300 dark:text-gray-600 font-normal normal-case">
                {files.length}
              </span>
            </h3>
          </div>

          <div className="flex-1 overflow-y-auto px-2 pb-2 space-y-px">
            {files.map((file) => {
              const isSelected = selectedFile === file.path;
              const meta = STATUS_META[file.status];
              const Icon = meta.icon;
              const commentCount =
                lineComments[file.path] &&
                Object.values(lineComments[file.path]).reduce((s, arr) => s + arr.length, 0);

              return (
                <button
                  key={file.path}
                  type="button"
                  onClick={() => handleSelectFile(file.path)}
                  className={clsx(
                    'w-full flex items-center gap-2.5 px-3 py-2 rounded-lg text-left transition-all group',
                    isSelected
                      ? 'bg-brand-50 dark:bg-brand-900/30 border border-brand-200 dark:border-brand-700 shadow-sm'
                      : 'hover:bg-white dark:hover:bg-gray-800/50 border border-transparent',
                  )}
                >
                  <Icon size={14} className={clsx(meta.cls, 'shrink-0')} />
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-1.5">
                      <span
                        className={clsx(
                          'text-[12px] font-mono truncate font-medium',
                          isSelected
                            ? 'text-brand-800 dark:text-brand-200'
                            : 'text-gray-700 dark:text-gray-300',
                        )}
                      >
                        {file.path.split('/').pop()}
                      </span>
                      <span
                        className={clsx(
                          'shrink-0 w-1.5 h-1.5 rounded-full',
                          meta.dotCls,
                        )}
                      />
                    </div>
                    <div className="flex items-center gap-2 mt-0.5">
                      <span className="text-[10px] text-gray-400 dark:text-gray-500 truncate">
                        {file.path}
                      </span>
                    </div>
                  </div>
                  <div className="flex items-center gap-1.5 shrink-0">
                    {commentCount != null && commentCount > 0 && (
                      <span className="flex items-center gap-0.5 text-[9px] font-semibold text-amber-600 dark:text-amber-400 bg-amber-100 dark:bg-amber-900/30 px-1.5 py-0.5 rounded-full">
                        <MessageSquare size={9} />
                        {commentCount}
                      </span>
                    )}
                    <ChevronRight
                      size={12}
                      className={clsx(
                        'transition-transform',
                        isSelected ? 'rotate-90 text-brand-400' : 'text-gray-300 dark:text-gray-600',
                      )}
                    />
                  </div>
                </button>
              );
            })}
          </div>

          {/* Comment feed (bottom of sidebar) */}
          {allCommentsFlat.length > 0 && (
            <div className="shrink-0 border-t border-gray-200 dark:border-gray-800 px-3 py-2.5 max-h-44 overflow-y-auto">
              <h3 className="flex items-center gap-2 text-[10px] font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wider mb-2">
                <MessageSquare size={11} />
                Recent
                <span className="text-gray-300 dark:text-gray-600 font-normal normal-case">
                  {allCommentsFlat.length}
                </span>
              </h3>
              <div className="space-y-1.5">
                {allCommentsFlat.slice(0, 10).map((c) => (
                  <div
                    key={c.id}
                    className="flex items-start gap-1.5 text-[10px] group cursor-pointer hover:bg-amber-50 dark:hover:bg-amber-900/10 px-1.5 py-1 rounded transition-colors"
                    onClick={() => setSelectedFile(c.file)}
                  >
                    <MessageSquare size={10} className="text-amber-400 shrink-0 mt-0.5" />
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-1.5">
                        <span className="font-mono text-[9px] text-brand-500 dark:text-brand-400 truncate">
                          {c.file}:L{c.line}
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
                {allCommentsFlat.length > 10 && (
                  <p className="text-[10px] text-gray-400 text-center pt-0.5">
                    +{allCommentsFlat.length - 10} more
                  </p>
                )}
              </div>
            </div>
          )}
        </div>

        {/* ── Right: Code View + Comment Panel ── */}
        <div className="flex-1 flex flex-col min-w-0 overflow-hidden bg-white dark:bg-gray-900">
          {selectedFile && selectedFileInfo ? (
            <>
              {/* File header */}
              <div className="shrink-0 px-4 py-2.5 border-b border-gray-200 dark:border-gray-800 flex items-center justify-between bg-gray-50/80 dark:bg-gray-900/80">
                <div className="flex items-center gap-2.5 min-w-0">
                  {(() => {
                    const MetaIcon = STATUS_META[selectedFileInfo.status].icon;
                    return (
                      <MetaIcon
                        size={14}
                        className={clsx(STATUS_META[selectedFileInfo.status].cls, 'shrink-0')}
                      />
                    );
                  })()}
                  <span className="text-[12px] font-mono font-semibold text-gray-700 dark:text-gray-300 truncate">
                    {selectedFile}
                  </span>
                  <span
                    className={clsx(
                      'text-[9px] font-semibold uppercase tracking-wider px-1.5 py-0.5 rounded',
                      STATUS_META[selectedFileInfo.status].dotCls.replace('bg-', 'bg-').replace('400', '100 dark:bg-') +
                        '/30',
                      STATUS_META[selectedFileInfo.status].cls,
                    )}
                  >
                    {STATUS_META[selectedFileInfo.status].label}
                  </span>
                </div>
                <div className="flex items-center gap-1.5">
                  <button
                    onClick={() => handleFlagForReview(selectedFile)}
                    disabled={pending === `flag-${selectedFile}`}
                    className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded text-[10px] font-medium bg-red-50 dark:bg-red-900/20 text-red-600 dark:text-red-400 hover:bg-red-100 dark:hover:bg-red-900/40 transition-colors disabled:opacity-40"
                  >
                    <Flag size={10} />
                    Flag
                  </button>
                  <button
                    onClick={() => handleAddToContext(selectedFile)}
                    disabled={pending === `ctx-${selectedFile}`}
                    className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded text-[10px] font-medium bg-sky-50 dark:bg-sky-900/20 text-sky-600 dark:text-sky-400 hover:bg-sky-100 dark:hover:bg-sky-900/40 transition-colors disabled:opacity-40"
                  >
                    <PlusCircle size={10} />
                    Context
                  </button>
                </div>
              </div>

              {/* Code content */}
              <div className="flex-1 overflow-auto">
                {contentLines.length === 0 ? (
                  <div className="flex flex-col items-center justify-center h-full text-center px-5 gap-3">
                    <AlertCircle size={28} className="text-gray-300 dark:text-gray-600" />
                    <div>
                      <p className="text-sm font-medium text-gray-500 dark:text-gray-400">
                        No code content available
                      </p>
                      <p className="text-xs text-gray-400 dark:text-gray-500 mt-1 max-w-[320px]">
                        {selectedFileInfo.lastEvent
                          ? `Last event: ${selectedFileInfo.lastEvent.message}`
                          : 'No events reference this file with code output.'}
                      </p>
                    </div>
                  </div>
                ) : (
                  <div className="font-mono text-[13px] leading-6">
                    {contentLines.map((line, idx) => {
                      const lineNum = idx + 1;
                      const lineFileComments = lineComments[selectedFile]?.[lineNum] || [];
                      const isActiveLine =
                        activeLine?.file === selectedFile && activeLine?.line === lineNum;
                      const hasComments = lineFileComments.length > 0;

                      return (
                        <div key={idx} className="relative">
                          {/* ── Line row ── */}
                          <div
                            className={clsx(
                              'flex group transition-colors',
                              isActiveLine
                                ? 'bg-amber-50 dark:bg-amber-900/10'
                                : hasComments
                                  ? 'bg-amber-50/50 dark:bg-amber-900/5'
                                  : 'hover:bg-gray-50 dark:hover:bg-gray-800/30',
                            )}
                          >
                            {/* Line number */}
                            <button
                              type="button"
                              onClick={() => handleLineClick(selectedFile, lineNum)}
                              className={clsx(
                                'shrink-0 w-14 text-right pr-3 select-none transition-colors cursor-pointer',
                                'text-[11px] text-gray-400 dark:text-gray-600 leading-6',
                                'hover:text-gray-600 dark:hover:text-gray-400',
                                hasComments &&
                                  'text-amber-500 dark:text-amber-400 font-semibold',
                                isActiveLine && 'text-amber-600 dark:text-amber-400 font-bold',
                              )}
                              title={`Click to comment on line ${lineNum}`}
                            >
                              {hasComments && !isActiveLine && (
                                <MessageSquare
                                  size={10}
                                  className="inline-block mr-1 -mt-px text-amber-400"
                                />
                              )}
                              {lineNum}
                            </button>

                            {/* Code */}
                            <div className="flex-1 min-w-0 pr-4">
                              <span className="whitespace-pre">
                                {highlightLine(line, idx)}
                              </span>
                            </div>

                            {/* Comment bubbles (right side) */}
                            <div className="shrink-0 pr-3 flex items-center gap-1">
                              {lineFileComments.map((c) => (
                                <div
                                  key={c.id}
                                  className="relative group/bubble"
                                  title={c.text}
                                >
                                  <div className="w-5 h-5 rounded-full bg-amber-100 dark:bg-amber-900/40 border border-amber-300 dark:border-amber-700 flex items-center justify-center cursor-pointer hover:bg-amber-200 dark:hover:bg-amber-900/60 transition-colors">
                                    <MessageSquare
                                      size={10}
                                      className="text-amber-600 dark:text-amber-400"
                                    />
                                  </div>
                                  {/* Tooltip on hover */}
                                  <div className="absolute right-0 top-full mt-1 z-20 w-56 p-2 rounded-lg bg-gray-900 dark:bg-gray-100 text-white dark:text-gray-900 text-[10px] shadow-xl opacity-0 pointer-events-none group-hover/bubble:opacity-100 transition-opacity animate-fade-in">
                                    <p className="leading-relaxed">{c.text}</p>
                                  </div>
                                </div>
                              ))}
                              {!hasComments && (
                                <div className="w-5 h-5 rounded-full flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity cursor-pointer">
                                  <PlusCircle
                                    size={12}
                                    className="text-gray-300 dark:text-gray-600 hover:text-brand-500"
                                    onClick={() => handleLineClick(selectedFile, lineNum)}
                                  />
                                </div>
                              )}
                            </div>
                          </div>

                          {/* ── Inline comment form ── */}
                          {isActiveLine && (
                            <div className="ml-14 mr-3 my-1 animate-slide-up">
                              <div className="flex gap-2 p-2.5 rounded-lg bg-amber-50 dark:bg-amber-900/15 border border-amber-200 dark:border-amber-800">
                                <CornerDownRight
                                  size={14}
                                  className="text-amber-400 shrink-0 mt-1"
                                />
                                <div className="flex-1 space-y-2">
                                  <div className="flex items-center gap-2">
                                    <span className="text-[10px] font-semibold text-amber-700 dark:text-amber-300">
                                      Review comment on line {lineNum}
                                    </span>
                                    <button
                                      onClick={() => {
                                        setActiveLine(null);
                                        setDraftComment('');
                                      }}
                                      className="ml-auto text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
                                    >
                                      <X size={12} />
                                    </button>
                                  </div>
                                  <textarea
                                    ref={draftInputRef}
                                    value={draftComment}
                                    onChange={(e) => setDraftComment(e.target.value)}
                                    onKeyDown={(e) => {
                                      if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                                        e.preventDefault();
                                        handleAddLineComment(selectedFile, lineNum);
                                      }
                                    }}
                                    placeholder="Leave a review comment… (Cmd+Enter to submit)"
                                    rows={3}
                                    className="w-full px-2.5 py-2 rounded text-[12px] bg-white dark:bg-gray-900 border border-amber-200 dark:border-amber-800 text-gray-700 dark:text-gray-300 placeholder-gray-400 dark:placeholder-gray-500 focus:outline-none focus:ring-1 focus:ring-amber-500/40 resize-none font-sans"
                                  />
                                  <div className="flex items-center gap-2">
                                    <button
                                      onClick={() =>
                                        handleAddLineComment(selectedFile, lineNum)
                                      }
                                      disabled={!draftComment.trim()}
                                      className="inline-flex items-center gap-1 px-3 py-1.5 rounded text-[11px] font-medium bg-amber-500 text-white hover:bg-amber-600 transition-colors disabled:opacity-40"
                                    >
                                      <Send size={10} />
                                      Add comment
                                    </button>
                                    <span className="text-[10px] text-gray-400">
                                      Cmd+Enter to submit
                                    </span>
                                  </div>
                                </div>
                              </div>
                            </div>
                          )}
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>

              {/* ── Comment Panel (bottom) ── */}
              {showCommentPanel && selectedFileComments.length > 0 && (
                <div className="shrink-0 border-t border-gray-200 dark:border-gray-800 max-h-60 overflow-y-auto">
                  <div className="px-4 py-2.5 border-b border-gray-100 dark:border-gray-800 bg-gray-50/50 dark:bg-gray-900/50 flex items-center justify-between">
                    <h3 className="flex items-center gap-2 text-[11px] font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wider">
                      <MessageSquare size={12} className="text-amber-400" />
                      Comments
                      <span className="text-gray-300 dark:text-gray-600 font-normal normal-case">
                        ({selectedFileComments.length})
                      </span>
                    </h3>
                    <button
                      onClick={() => setShowCommentPanel(false)}
                      className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-300"
                    >
                      <X size={14} />
                    </button>
                  </div>
                  <div className="p-3 space-y-2">
                    {selectedFileComments.map((c) => (
                      <div
                        key={c.id}
                        className="p-3 rounded-lg bg-amber-50 dark:bg-amber-900/15 border border-amber-100 dark:border-amber-800/40 hover:border-amber-200 dark:hover:border-amber-700/60 transition-colors"
                      >
                        <div className="flex items-center gap-2 mb-1.5">
                          <span className="text-[10px] font-semibold text-amber-600 dark:text-amber-400 bg-amber-100 dark:bg-amber-900/30 px-1.5 py-0.5 rounded">
                            Line {c.line}
                          </span>
                          <span className="text-[10px] text-gray-400 flex items-center gap-1">
                            <Clock size={9} />
                            {new Date(c.timestamp).toLocaleTimeString([], {
                              hour: '2-digit',
                              minute: '2-digit',
                            })}
                          </span>
                        </div>
                        <p className="text-[12px] text-gray-700 dark:text-gray-300 leading-relaxed">
                          {c.text}
                        </p>
                        <div className="flex items-center gap-3 mt-2 pt-2 border-t border-amber-100 dark:border-amber-800/30">
                          <button
                            onClick={() => handleSendAsTask(selectedFile, c)}
                            disabled={pending === c.id}
                            className="inline-flex items-center gap-1 text-[10px] font-medium text-brand-600 dark:text-brand-400 hover:text-brand-700 dark:hover:text-brand-300 transition-colors disabled:opacity-40"
                          >
                            <PlusCircle size={10} />
                            Create task
                          </button>
                          <button
                            onClick={() => handleAddToContext(selectedFile, c)}
                            disabled={pending === c.id}
                            className="inline-flex items-center gap-1 text-[10px] font-medium text-sky-600 dark:text-sky-400 hover:text-sky-700 dark:hover:text-sky-300 transition-colors disabled:opacity-40"
                          >
                            <ExternalLink size={10} />
                            Add to context
                          </button>
                          <button
                            onClick={() =>
                              handleLineClick(selectedFile, c.line)
                            }
                            className="inline-flex items-center gap-1 text-[10px] font-medium text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-300 transition-colors ml-auto"
                          >
                            <Eye size={10} />
                            View line
                          </button>
                        </div>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </>
          ) : (
            /* No file selected */
            <div className="flex-1 flex flex-col items-center justify-center text-center px-5 gap-3">
              <div className="w-14 h-14 rounded-2xl bg-gray-100 dark:bg-gray-800 flex items-center justify-center">
                <Layers size={24} className="text-gray-300 dark:text-gray-600" />
              </div>
              <div>
                <p className="text-sm font-semibold text-gray-500 dark:text-gray-400">
                  Select a file to review
                </p>
                <p className="text-xs text-gray-400 dark:text-gray-500 mt-1 max-w-[280px] leading-relaxed">
                  Choose a file from the sidebar to view its content and leave
                  inline review comments for agents.
                </p>
              </div>
              <button
                onClick={() => files[0] && setSelectedFile(files[0].path)}
                className="btn-secondary text-xs"
              >
                <Eye size={13} />
                View first file
              </button>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
