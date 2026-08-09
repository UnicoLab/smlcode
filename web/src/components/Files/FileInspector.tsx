import { useState, useMemo } from 'react';
import { addTask, getDoc, updateDoc } from '@/api/client';
import type { RunEvent } from '@/types';
import { FileCode, MessageSquare, PlusCircle, Eye, GitBranch, Clock, Send, ExternalLink } from 'lucide-react';
import clsx from 'clsx';

interface Props { events: RunEvent[]; running: boolean; }
interface FileComment { id: string; text: string; time: string; file: string; }

const FILE_RE = /([\w.\-/]+\.(go|py|tsx?|jsx?|rs|java|rb|php|c|cpp|h|hpp|css|scss|html|md|yaml|yml|json|toml))/gi;
const S: Record<string, string> = { modified: 'bg-amber-100 text-amber-700', created: 'bg-emerald-100 text-emerald-700', deleted: 'bg-red-100 text-red-700', tracked: 'bg-gray-100 text-gray-600' };

export default function FileInspector({ events, running }: Props) {
  const [sel, setSel] = useState<string | null>(null);
  const [comments, setComments] = useState<FileComment[]>([]);
  const [input, setInput] = useState<Record<string, string>>({});
  const [toast, setToast] = useState<string | null>(null);

  const files = useMemo(() => {
    const seen = new Set<string>();
    const result: { path: string; status: string; lastEvent?: RunEvent }[] = [];
    for (const e of events) {
      const text = (e.message || '') + ' ' + (e.output || '') + ' ' + (e.scope || '');
      for (const m of text.matchAll(FILE_RE)) {
        const path = m[1];
        if (!seen.has(path) && !path.includes('node_modules') && !path.includes('.git/')) {
          seen.add(path);
          const s = text.toLowerCase().includes('create') || text.toLowerCase().includes('new file') ? 'created' : text.toLowerCase().includes('delete') ? 'deleted' : 'modified';
          result.push({ path, status: s, lastEvent: e });
        }
      }
    }
    return result;
  }, [events]);

  const addComment = (file: string) => {
    const text = input[file]?.trim(); if (!text) return;
    setComments(p => [{ id: Date.now().toString(), text, time: new Date().toLocaleTimeString(), file }, ...p]);
    setInput(p => ({ ...p, [file]: '' }));
  };

  const sendToAgents = async (c: FileComment) => {
    try { await addTask({ title: `Review: ${c.file}`, description: c.text, role: 'worker' }); setToast(`Task created`); setTimeout(() => setToast(null), 3000); } catch { setToast('Failed'); }
  };

  const addToContext = async (c: FileComment) => {
    try { const d = await getDoc('CONTEXT.md'); const ct = (d?.content || '') + `\n\n### ${c.file}\n${c.text}\n`; await updateDoc('CONTEXT.md', ct); setToast('Added to context'); setTimeout(() => setToast(null), 3000); } catch { setToast('Failed'); }
  };

  return (
    <div className="h-full overflow-auto p-6">
      <div className="max-w-5xl mx-auto space-y-6">
        <div className="flex items-center justify-between">
          <div><h1 className="text-2xl font-bold">File Inspector</h1><p className="text-sm text-gray-500 mt-1">Review modified files and leave inline comments for agents</p></div>
          {running && <span className="badge-warn animate-pulse">Live</span>}
        </div>
        {toast && <div className="animate-slide-up px-4 py-2 rounded-lg bg-emerald-50 dark:bg-emerald-900/20 text-emerald-700 text-sm">{toast}</div>}
        {files.length === 0 && (
          <div className="flex flex-col items-center justify-center py-20 text-gray-400 gap-3">
            <FileCode size={48} className="opacity-50" /><p className="text-lg font-medium">No files detected yet</p><p className="text-sm">{running ? 'Watching…' : 'Run a pipeline'}</p>
          </div>
        )}
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {files.map(f => (
            <div key={f.path} className={clsx('card p-4 cursor-pointer transition-all hover:shadow-md', sel === f.path && 'ring-2 ring-brand-500')} onClick={() => setSel(sel === f.path ? null : f.path)}>
              <div className="flex items-start justify-between mb-2"><div className="flex items-center gap-2"><GitBranch size={16} className="text-gray-400" /><span className="text-sm font-mono font-bold truncate">{f.path}</span></div><span className={clsx('badge text-[10px]', S[f.status] || 'badge-neutral')}>{f.status}</span></div>
              {f.lastEvent && <div className="text-[10px] text-gray-400 mt-2"><span className="font-semibold uppercase">{f.lastEvent.phase}</span>{f.lastEvent.agent && <span className="ml-1">@{f.lastEvent.agent}</span>}</div>}
              {sel === f.path && (
                <div className="mt-3 pt-3 border-t border-gray-100 dark:border-gray-800 animate-fade-in" onClick={e => e.stopPropagation()}>
                  {f.lastEvent?.output && <div className="mb-3"><div className="text-[10px] font-semibold text-gray-400 uppercase mb-1">Snapshot</div><pre className="text-[10px] font-mono bg-gray-50 dark:bg-gray-800 rounded-lg p-2 max-h-48 overflow-auto whitespace-pre-wrap">{f.lastEvent.output.slice(0, 600)}</pre></div>}
                  <div className="flex gap-2"><input value={input[f.path] || ''} onChange={e => setInput(p => ({ ...p, [f.path]: e.target.value }))} onKeyDown={e => { if (e.key === 'Enter') addComment(f.path); }} placeholder="Add comment…" className="input text-xs flex-1 py-1.5" /><button onClick={() => addComment(f.path)} className="btn-primary text-xs px-2.5 py-1.5"><Send size={12} /></button></div>
                  {comments.filter(c => c.file === f.path).map(c => (
                    <div key={c.id} className="mt-2 p-2 rounded-lg bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800">
                      <div className="flex items-center gap-2 text-[10px] text-gray-400 mb-1"><Clock size={10} /> {c.time}</div><p className="text-xs">{c.text}</p>
                      <div className="flex gap-2 mt-2"><button onClick={() => sendToAgents(c)} className="text-[10px] text-brand-600 hover:underline flex items-center gap-1"><PlusCircle size={10} />Task</button><button onClick={() => addToContext(c)} className="text-[10px] text-gray-500 hover:underline flex items-center gap-1"><ExternalLink size={10} />Context</button></div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
        {comments.length > 0 && (
          <div className="card p-4"><h2 className="text-sm font-bold mb-3 flex gap-2"><MessageSquare size={16} />Feed ({comments.length})</h2>
            <div className="space-y-2">{comments.slice(0, 12).map(c => (
              <div key={c.id} className="flex items-start gap-3 p-2 rounded-lg bg-gray-50 dark:bg-gray-800/50"><MessageSquare size={12} className="text-amber-500 shrink-0 mt-0.5" /><div><div className="flex gap-2 text-[10px] text-gray-400"><span className="font-mono">{c.file}</span><Clock size={10} />{c.time}</div><p className="text-xs mt-0.5">{c.text}</p></div></div>
            ))}</div>
          </div>
        )}
      </div>
    </div>
  );
}
