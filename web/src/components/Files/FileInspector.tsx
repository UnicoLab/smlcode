import { useState, useMemo, useCallback, useRef, useEffect } from 'react';
import {
  FileCode,
  FilePlus,
  FileMinus,
  FilePenLine,
  MessageSquare,
  PlusCircle,
  Send,
  Flag,
  X,
  CornerDownRight,
  ChevronRight,
  ChevronDown,
  FolderOpen,
  Folder,
  RefreshCw,
  AlertCircle,
  Eye,
  EyeOff,
} from 'lucide-react';
import { addTask, getWorkspaceFile, getWorkspaceTree } from '@/api/client';
import type { RunEvent } from '@/types';
import clsx from 'clsx';

// ── Types ──

type FileStatus = 'changed' | 'created' | 'deleted' | 'unchanged';

interface LineComment {
  id: string;
  line: number;
  text: string;
  timestamp: number;
}

interface TreeEntry {
  name: string;
  path: string;
  is_dir: boolean;
  size?: number;
}

interface Props {
  events: RunEvent[];
  running: boolean;
}

// ── Agent-modified file tracking ──

function extractModifiedPaths(events: RunEvent[]): Set<string> {
  const paths = new Set<string>();
  for (const e of events) {
    const haystack = [e.scope, e.message, e.output, e.phase].filter(Boolean).join(' ');
    try {
      const m = haystack.match(/"files_changed"\s*:\s*\[([\s\S]*?)\]/);
      if (m) {
        const arr = JSON.parse('[' + m[1] + ']');
        for (const f of arr) paths.add(String(f).trim().replace(/^["'`]+|["'`]+$/g, ''));
      }
    } catch {
      // Best-effort scrape of an agent's `files_changed` array; malformed or
      // absent JSON just means this event lists no files.
    }
    try {
      const obj = JSON.parse(e.output || '');
      if (obj.files_changed && Array.isArray(obj.files_changed)) {
        for (const f of obj.files_changed) paths.add(String(f).trim());
      }
    } catch {
      // As above — the event output is frequently plain prose.
    }
  }
  return paths;
}

// ── Simple syntax highlighter ──

interface Token { text: string; type: 'keyword' | 'string' | 'comment' | 'number' | 'function' | 'type' | 'plain'; }

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
  'str', 'float', 'list', 'dict', 'tuple', 'set', 'frozenset',
]);

function tokenizeLine(line: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;
  while (i < line.length) {
    if (/\s/.test(line[i])) {
      let ws = '';
      while (i < line.length && /\s/.test(line[i])) ws += line[i++];
      tokens.push({ text: ws, type: 'plain' });
      continue;
    }
    if (line[i] === '/' && line[i + 1] === '/') {
      tokens.push({ text: line.slice(i), type: 'comment' });
      break;
    }
    if (line[i] === '#') {
      tokens.push({ text: line.slice(i), type: 'comment' });
      break;
    }
    if (line[i] === '/' && line[i + 1] === '*') {
      const end = line.indexOf('*/', i + 2);
      if (end !== -1) { tokens.push({ text: line.slice(i, end + 2), type: 'comment' }); i = end + 2; continue; }
      tokens.push({ text: line.slice(i), type: 'comment' }); break;
    }
    if (line[i] === '`') {
      const end = line.indexOf('`', i + 1);
      if (end !== -1) { tokens.push({ text: line.slice(i, end + 1), type: 'string' }); i = end + 1; continue; }
      tokens.push({ text: line.slice(i), type: 'string' }); break;
    }
    if (line[i] === '"' || line[i] === "'") {
      const quote = line[i]; let str = quote; i++;
      while (i < line.length && line[i] !== quote) {
        if (line[i] === '\\' && i + 1 < line.length) str += line[i++];
        str += line[i++];
      }
      if (i < line.length) str += line[i++];
      tokens.push({ text: str, type: 'string' });
      continue;
    }
    if (/[\d.]/.test(line[i]) && !/[a-zA-Z_]/.test(line[i - 1] || '')) {
      let num = '';
      while (i < line.length && /[\d.a-fA-FxXoObB_n]/.test(line[i])) num += line[i++];
      if (/^\d/.test(num)) { tokens.push({ text: num, type: 'number' }); continue; }
      else i -= num.length;
    }
    let ident = '';
    while (i < line.length && /[\w$]/.test(line[i])) ident += line[i++];
    if (ident) {
      if (KEYWORDS.has(ident)) tokens.push({ text: ident, type: 'keyword' });
      else if (TYPES.has(ident)) tokens.push({ text: ident, type: 'type' });
      else if (i < line.length && line[i] === '(' && /^[A-Z]/.test(ident)) tokens.push({ text: ident, type: 'type' });
      else if (i < line.length && line[i] === '(') tokens.push({ text: ident, type: 'function' });
      else tokens.push({ text: ident, type: 'plain' });
      continue;
    }
    tokens.push({ text: line[i++], type: 'plain' });
  }
  return tokens;
}

function highlightLine(line: string, idx: number): React.ReactNode {
  const tokens = tokenizeLine(line);
  return <span key={idx}>{tokens.map((t, ti) => <span key={ti} className={TOKEN_COLORS[t.type]}>{t.text}</span>)}</span>;
}

function genId(): string { return `${Date.now()}-${Math.random().toString(36).slice(2, 9)}`; }

// ── Component ──

export default function FileInspector({ events, running }: Props) {
  // ── State ──
  const [selectedFile, setSelectedFile] = useState<string | null>(null);
  const [expandedDirs, setExpandedDirs] = useState<Set<string>>(new Set(['']));
  const [tree, setTree] = useState<Record<string, TreeEntry[]>>({});
  const [treeLoading, setTreeLoading] = useState(true);
  const [content, setContent] = useState<string | null>(null);
  const [contentLoading, setContentLoading] = useState(false);
  const [lineComments, setLineComments] = useState<Record<string, Record<number, LineComment[]>>>({});
  const [activeLine, setActiveLine] = useState<{ file: string; line: number } | null>(null);
  const [draftComment, setDraftComment] = useState('');
  const [toast, setToast] = useState<{ ok: boolean; msg: string } | null>(null);
  const [showOnlyModified, setShowOnlyModified] = useState(false);
  // Dot-entries are shown by default: `.slmcode/pending/` is the review queue
  // and `.github/` is real project content. `.git` stays hidden server-side.
  const [showHidden, setShowHidden] = useState(true);
  const draftInputRef = useRef<HTMLTextAreaElement>(null);

  // ── Track agent-modified files ──
  const modifiedPaths = useMemo(() => extractModifiedPaths(events), [events]);

  // ── Load directory tree ──
  const loadDir = useCallback(async (dirPath: string) => {
    try {
      const res = await getWorkspaceTree(dirPath || undefined, { hidden: showHidden });
      setTree(prev => ({ ...prev, [dirPath || '']: res.entries }));
    } catch {
      // The directory may not exist (a stale expanded path); the tree simply
      // shows nothing for it. Connection failures surface in the TopBar badge.
    } finally {
      if (!dirPath) setTreeLoading(false);
    }
  }, [showHidden]);

  useEffect(() => { setTree({}); loadDir(''); }, [loadDir]);

  // ── Load file content ──
  useEffect(() => {
    if (!selectedFile) { setContent(null); return; }
    let cancelled = false;
    setContentLoading(true);
    getWorkspaceFile(selectedFile).then(r => {
      if (!cancelled) { setContent(r.content); setContentLoading(false); }
    }).catch(() => {
      if (!cancelled) { setContent(null); setContentLoading(false); }
    });
    return () => { cancelled = true; };
  }, [selectedFile]);

  // Focus draft input
  useEffect(() => {
    if (activeLine) setTimeout(() => draftInputRef.current?.focus(), 50);
  }, [activeLine]);

  // ── Toast ──
  const flash = useCallback((ok: boolean, msg: string) => {
    setToast({ ok, msg });
    setTimeout(() => setToast(null), 4000);
  }, []);

  // ── Tree helpers ──
  const toggleDir = useCallback(async (dirPath: string) => {
    setExpandedDirs(prev => {
      const next = new Set(prev);
      if (next.has(dirPath)) { next.delete(dirPath); return next; }
      next.add(dirPath);
      return next;
    });
    if (!tree[dirPath]) await loadDir(dirPath);
  }, [tree, loadDir]);

  // ── Filter entries ──
  const getFilteredEntries = useCallback((dirPath: string): TreeEntry[] => {
    const entries = tree[dirPath] || [];
    if (!showOnlyModified) return entries;
    return entries.filter(e => {
      if (e.is_dir) return entries.some(child => modifiedPaths.has(child.path));
      return modifiedPaths.has(e.path);
    });
  }, [tree, showOnlyModified, modifiedPaths]);

  // ── Recursive tree render ──
  const renderTree = (dirPath: string, depth: number): React.ReactNode => {
    const entries = getFilteredEntries(dirPath);
    return entries.map(entry => {
      const isExpanded = expandedDirs.has(entry.path);
      const isModified = modifiedPaths.has(entry.path);
      const paddingLeft = depth * 16 + 8;

      if (entry.is_dir) {
        return (
          <div key={entry.path}>
            <button
              type="button"
              onClick={() => toggleDir(entry.path)}
              className="w-full flex items-center gap-1.5 py-1.5 hover:bg-gray-50 dark:hover:bg-gray-800/40 transition-colors text-left"
              style={{ paddingLeft }}
            >
              {isExpanded ? <ChevronDown size={12} className="text-gray-400 shrink-0" /> : <ChevronRight size={12} className="text-gray-400 shrink-0" />}
              {isExpanded ? <FolderOpen size={14} className="text-amber-500 shrink-0" /> : <Folder size={14} className="text-amber-500 shrink-0" />}
              <span className="text-[12px] font-medium text-gray-700 dark:text-gray-300 truncate">{entry.name}</span>
            </button>
            {isExpanded && renderTree(entry.path, depth + 1)}
          </div>
        );
      }

      return (
        <button
          key={entry.path}
          type="button"
          onClick={() => setSelectedFile(entry.path)}
          className={clsx(
            'w-full flex items-center gap-1.5 py-1.5 hover:bg-gray-50 dark:hover:bg-gray-800/40 transition-colors text-left',
            selectedFile === entry.path && 'bg-brand-50 dark:bg-brand-900/20 border-l-2 border-brand-500',
          )}
          style={{ paddingLeft }}
        >
          <span className="w-3 shrink-0" />
          <FileCode size={14} className={clsx(isModified ? 'text-amber-500' : 'text-gray-400', 'shrink-0')} />
          <span className={clsx('text-[12px] font-mono truncate', selectedFile === entry.path ? 'text-brand-700 dark:text-brand-300 font-semibold' : 'text-gray-600 dark:text-gray-400')}>
            {entry.name}
          </span>
          {isModified && <span className="w-1.5 h-1.5 rounded-full bg-amber-400 shrink-0" />}
        </button>
      );
    });
  };

  const allCommentsFlat = useMemo(() => {
    const flat: (LineComment & { file: string })[] = [];
    for (const [file, rec] of Object.entries(lineComments)) {
      for (const lineNum of Object.keys(rec)) {
        for (const c of rec[Number(lineNum)]) flat.push({ ...c, file });
      }
    }
    return flat.sort((a, b) => b.timestamp - a.timestamp);
  }, [lineComments]);

  // ── Actions ──
  const handleLineClick = useCallback((file: string, line: number) => {
    if (activeLine?.file === file && activeLine?.line === line) {
      setActiveLine(null);
      setDraftComment('');
    } else {
      setActiveLine({ file, line });
      setDraftComment('');
    }
  }, [activeLine]);

  const handleAddLineComment = useCallback((file: string, line: number) => {
    const text = draftComment.trim();
    if (!text) return;
    const comment: LineComment = { id: genId(), line, text, timestamp: Date.now() };
    setLineComments(prev => {
      const fileRec = { ...(prev[file] || {}) };
      return { ...prev, [file]: { ...fileRec, [line]: [...(prev[file]?.[line] || []), comment] } };
    });
    setActiveLine(null);
    setDraftComment('');
    flash(true, `Comment added on line ${line}`);
  }, [draftComment, flash]);

  const handleSendAsTask = useCallback(async (filePath: string, comment: LineComment) => {
    try {
      await addTask({ title: `Review: ${filePath}:L${comment.line}`, description: comment.text, role: 'worker', files: [filePath] });
      flash(true, `Task created for ${filePath}:L${comment.line}`);
    } catch { flash(false, 'Failed to create task'); }
  }, [flash]);

  // ── Content lines ──
  const contentLines = useMemo(() => {
    if (content === null) return [];
    return content.split('\n');
  }, [content]);

  // ── Render ──
  return (
    <div className="h-full flex flex-col overflow-hidden">
      {/* Toast */}
      {toast && (
        <div className={clsx('mx-4 mt-3 px-4 py-2.5 rounded-lg text-xs font-medium animate-slide-up border shadow-sm',
          toast.ok ? 'bg-emerald-50 dark:bg-emerald-900/30 text-emerald-700 dark:text-emerald-300 border-emerald-200' : 'bg-red-50 dark:bg-red-900/30 text-red-700 dark:text-red-300 border-red-200')}>
          {toast.msg}
        </div>
      )}

      {/* Header */}
      <div className="shrink-0 px-5 py-3 border-b border-gray-200 dark:border-gray-800 flex items-center justify-between">
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 rounded-lg bg-brand-100 dark:bg-brand-900/40 flex items-center justify-center">
            <FileCode size={16} className="text-brand-600 dark:text-brand-400" />
          </div>
          <div>
            <h1 className="text-sm font-bold">File Browser</h1>
            <p className="text-[10px] text-gray-500">Browse workspace files & leave review comments</p>
          </div>
        </div>
        <div className="flex items-center gap-3">
          {/* Toggle: show all vs modified only */}
          <button
            onClick={() => setShowOnlyModified(v => !v)}
            className={clsx('flex items-center gap-1.5 text-[11px] font-medium px-2.5 py-1.5 rounded-lg transition-colors',
              showOnlyModified ? 'bg-amber-50 dark:bg-amber-900/20 text-amber-700 dark:text-amber-300' : 'text-gray-500 hover:bg-gray-100 dark:hover:bg-gray-800')}
            title={showOnlyModified ? 'Show all files' : 'Show only agent-modified files'}
          >
            {showOnlyModified ? <Eye size={13} /> : <EyeOff size={13} />}
            {showOnlyModified ? 'Modified only' : 'All files'}
          </button>
          {allCommentsFlat.length > 0 && (
            <span className="flex items-center gap-1 text-[11px] text-amber-600 dark:text-amber-400 bg-amber-100 dark:bg-amber-900/30 px-2 py-1 rounded-full">
              <MessageSquare size={12} />{allCommentsFlat.length}
            </span>
          )}
          {running && (
            <div className="flex items-center gap-1.5 px-2.5 py-1.5 rounded-full bg-brand-50 dark:bg-brand-900/30 border border-brand-200">
              <span className="relative flex h-2 w-2">
                <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-brand-400 opacity-75" />
                <span className="relative inline-flex rounded-full h-2 w-2 bg-brand-500" />
              </span>
              <span className="text-[10px] font-medium text-brand-600 dark:text-brand-400">Live</span>
            </div>
          )}
        </div>
      </div>

      {/* Body */}
      <div className="flex-1 flex min-h-0 overflow-hidden">
        {/* Left: File Tree */}
        <div className="w-[280px] shrink-0 border-r border-gray-200 dark:border-gray-800 flex flex-col bg-gray-50/50 dark:bg-gray-900/30">
          <div className="shrink-0 px-3 py-2.5 flex items-center justify-between">
            <h3 className="flex items-center gap-2 text-[10px] font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wider">
              <FolderOpen size={12} />
              Workspace
            </h3>
            <div className="flex items-center gap-1">
              <button
                type="button"
                onClick={() => setShowHidden(v => !v)}
                aria-pressed={showHidden}
                className={clsx('focus-ring rounded px-1.5 py-0.5 font-mono text-[10px]',
                  showHidden ? 'bg-brand-100 text-brand-700 dark:bg-brand-900/40 dark:text-brand-300'
                             : 'text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800')}
                title={showHidden ? 'Hide dot-files (.slmcode, .github…)' : 'Show dot-files (.slmcode, .github…)'}
              >
                .*
              </button>
              <button onClick={() => { setTree({}); loadDir(''); }} className="btn-ghost focus-ring p-1 rounded" title="Refresh" aria-label="Refresh the file tree">
                <RefreshCw size={12} />
              </button>
            </div>
          </div>
          <div className="flex-1 overflow-y-auto px-1 pb-2">
            {treeLoading ? (
              <div className="flex items-center justify-center py-8 text-gray-400">
                <RefreshCw size={14} className="animate-spin mr-2" />Loading…
              </div>
            ) : (
              renderTree('', 0)
            )}
          </div>
          {/* Recent comments */}
          {allCommentsFlat.length > 0 && (
            <div className="shrink-0 border-t border-gray-200 dark:border-gray-800 px-3 py-2.5 max-h-36 overflow-y-auto">
              <h3 className="flex items-center gap-2 text-[10px] font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wider mb-1.5">
                <MessageSquare size={11} />Recent ({allCommentsFlat.length})
              </h3>
              {allCommentsFlat.slice(0, 8).map(c => (
                <div key={c.id} className="flex items-start gap-1.5 text-[10px] py-1 px-1.5 rounded hover:bg-amber-50 dark:hover:bg-amber-900/10 cursor-pointer"
                  role="button" tabIndex={0}
                  onClick={() => setSelectedFile(c.file)}
                  onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setSelectedFile(c.file); } }}>
                  <MessageSquare size={10} className="text-amber-400 shrink-0 mt-0.5" />
                  <div className="flex-1 min-w-0">
                    <span className="font-mono text-[9px] text-brand-500">{c.file}:L{c.line}</span>
                    <p className="text-gray-600 dark:text-gray-400 truncate">{c.text}</p>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Right: Code View */}
        <div className="flex-1 flex flex-col min-w-0 overflow-hidden bg-white dark:bg-gray-900">
          {selectedFile ? (
            <>
              {/* File header */}
              <div className="shrink-0 px-4 py-2.5 border-b border-gray-200 dark:border-gray-800 flex items-center justify-between bg-gray-50/80 dark:bg-gray-900/80">
                <div className="flex items-center gap-2.5 min-w-0">
                  {modifiedPaths.has(selectedFile) ? (
                    <FilePenLine size={14} className="text-amber-500 shrink-0" />
                  ) : (
                    <FileCode size={14} className="text-gray-400 shrink-0" />
                  )}
                  <span className="text-[12px] font-mono font-semibold text-gray-700 dark:text-gray-300 truncate">{selectedFile}</span>
                  {modifiedPaths.has(selectedFile) && (
                    <span className="text-[9px] font-semibold uppercase px-1.5 py-0.5 rounded bg-amber-100 dark:bg-amber-900/30 text-amber-600 dark:text-amber-400">Modified</span>
                  )}
                </div>
              </div>

              {/* Code */}
              <div className="flex-1 overflow-auto">
                {contentLoading ? (
                  <div className="flex items-center justify-center h-full text-gray-400"><RefreshCw size={20} className="animate-spin mr-2" />Loading…</div>
                ) : contentLines.length === 0 ? (
                  <div className="flex flex-col items-center justify-center h-full text-center px-5 gap-3">
                    <AlertCircle size={28} className="text-gray-300 dark:text-gray-600" />
                    <p className="text-sm font-medium text-gray-500 dark:text-gray-400">No content</p>
                    <p className="text-xs text-gray-400">Empty file or could not be read.</p>
                  </div>
                ) : (
                  <div className="font-mono text-[13px] leading-6">
                    {contentLines.map((line, idx) => {
                      const lineNum = idx + 1;
                      const lineFileComments = lineComments[selectedFile]?.[lineNum] || [];
                      const isActiveLine = activeLine?.file === selectedFile && activeLine?.line === lineNum;
                      const hasComments = lineFileComments.length > 0;

                      return (
                        <div key={idx} className="relative">
                          <div className={clsx('flex group transition-colors', isActiveLine ? 'bg-amber-50 dark:bg-amber-900/10' : hasComments ? 'bg-amber-50/50 dark:bg-amber-900/5' : 'hover:bg-gray-50 dark:hover:bg-gray-800/30')}>
                            <button type="button" onClick={() => handleLineClick(selectedFile, lineNum)}
                              className={clsx('shrink-0 w-14 text-right pr-3 select-none transition-colors cursor-pointer text-[11px] text-gray-400 dark:text-gray-600 leading-6 hover:text-gray-600 dark:hover:text-gray-400', hasComments && 'text-amber-500 dark:text-amber-400 font-semibold', isActiveLine && 'text-amber-600 dark:text-amber-400 font-bold')}
                              title={`Click to comment on line ${lineNum}`}>
                              {hasComments && !isActiveLine && <MessageSquare size={10} className="inline-block mr-1 -mt-px text-amber-400" />}
                              {lineNum}
                            </button>
                            <div className="flex-1 min-w-0 pr-4"><span className="whitespace-pre">{highlightLine(line, idx)}</span></div>
                            <div className="shrink-0 pr-3 flex items-center gap-1">
                              {lineFileComments.map(c => (
                                <div key={c.id} className="relative group/bubble" title={c.text}>
                                  <div className="w-5 h-5 rounded-full bg-amber-100 dark:bg-amber-900/40 border border-amber-300 dark:border-amber-700 flex items-center justify-center cursor-pointer hover:bg-amber-200 dark:hover:bg-amber-900/60 transition-colors">
                                    <MessageSquare size={10} className="text-amber-600 dark:text-amber-400" />
                                  </div>
                                  <div className="absolute right-0 top-full mt-1 z-20 w-56 p-2 rounded-lg bg-gray-900 dark:bg-gray-100 text-white dark:text-gray-900 text-[10px] shadow-xl opacity-0 pointer-events-none group-hover/bubble:opacity-100 transition-opacity"><p className="leading-relaxed">{c.text}</p></div>
                                </div>
                              ))}
                              {!hasComments && (
                                <div
                                  className="w-5 h-5 rounded-full flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity cursor-pointer"
                                  role="button"
                                  tabIndex={0}
                                  aria-label={`Add comment on line ${lineNum}`}
                                  onClick={() => handleLineClick(selectedFile, lineNum)}
                                  onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); handleLineClick(selectedFile, lineNum); } }}
                                >
                                  <PlusCircle size={12} className="text-gray-300 dark:text-gray-600 hover:text-brand-500" />
                                </div>
                              )}
                            </div>
                          </div>
                          {isActiveLine && (
                            <div className="ml-14 mr-3 my-1 animate-slide-up">
                              <div className="flex gap-2 p-2.5 rounded-lg bg-amber-50 dark:bg-amber-900/15 border border-amber-200 dark:border-amber-800">
                                <CornerDownRight size={14} className="text-amber-400 shrink-0 mt-1" />
                                <div className="flex-1 space-y-2">
                                  <div className="flex items-center gap-2">
                                    <span className="text-[10px] font-semibold text-amber-700 dark:text-amber-300">Review comment on line {lineNum}</span>
                                    <button onClick={() => { setActiveLine(null); setDraftComment(''); }} className="ml-auto text-gray-400 hover:text-gray-600"><X size={12} /></button>
                                  </div>
                                  <textarea ref={draftInputRef} value={draftComment} onChange={e => setDraftComment(e.target.value)}
                                    onKeyDown={e => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); handleAddLineComment(selectedFile, lineNum); } }}
                                    placeholder="Leave a review comment… (Cmd+Enter to submit)" rows={3}
                                    className="w-full px-2.5 py-2 rounded text-[12px] bg-white dark:bg-gray-900 border border-amber-200 dark:border-amber-800 text-gray-700 dark:text-gray-300 placeholder-gray-400 focus:outline-none focus:ring-1 focus:ring-amber-500/40 resize-none font-sans" />
                                  <div className="flex items-center gap-2">
                                    <button onClick={() => handleAddLineComment(selectedFile, lineNum)} disabled={!draftComment.trim()}
                                      className="inline-flex items-center gap-1 px-3 py-1.5 rounded text-[11px] font-medium bg-amber-500 text-white hover:bg-amber-600 transition-colors disabled:opacity-40"><Send size={10} />Add comment</button>
                                    <button onClick={() => { handleAddLineComment(selectedFile, lineNum); if (lineComments[selectedFile]?.[lineNum]?.[0]) handleSendAsTask(selectedFile, lineComments[selectedFile][lineNum][0]); }}
                                      disabled={!draftComment.trim()} className="inline-flex items-center gap-1 px-3 py-1.5 rounded text-[11px] font-medium bg-brand-50 dark:bg-brand-900/20 text-brand-600 dark:text-brand-400 hover:bg-brand-100 transition-colors disabled:opacity-40"><Flag size={10} />Send as task</button>
                                    <span className="text-[10px] text-gray-400">Cmd+Enter</span>
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
            </>
          ) : (
            <div className="flex-1 flex flex-col items-center justify-center text-center px-5">
              <div className="w-16 h-16 rounded-2xl bg-gray-100 dark:bg-gray-800 flex items-center justify-center mb-4">
                <FileCode size={28} className="text-gray-300 dark:text-gray-600" />
              </div>
              <p className="text-sm font-semibold text-gray-500 dark:text-gray-400">Select a file to view</p>
              <p className="text-xs text-gray-400 dark:text-gray-500 mt-1.5 max-w-[280px]">Browse the workspace tree on the left. Click any file to view its content and leave inline review comments.</p>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
