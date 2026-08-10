import { useState, useEffect } from 'react';
import { MessageSquareText, Send, Trash2, Loader2 } from 'lucide-react';
import { getFeedback, postFeedback, clearFeedback } from '@/api/client';
import type { FeedbackState } from '@/types';
import clsx from 'clsx';

interface LiveFeedbackProps {
  /** Called with the newly active feedback text (or '') after set/clear. */
  onChanged?: (text: string) => void;
}

interface Notice {
  type: 'ok' | 'error';
  msg: string;
}

// ── Live Feedback ──
// Compact card for steering running agents in real time. The text is injected
// into the next agent prompt ("LIVE FEEDBACK FROM USER") by the backend, and
// set/cleared events also surface in the SSE event log as kind "intervention".
export default function LiveFeedback({ onChanged }: LiveFeedbackProps) {
  const [text, setText] = useState('');
  const [current, setCurrent] = useState('');
  const [setAt, setSetAt] = useState<string | undefined>(undefined);
  const [sending, setSending] = useState(false);
  const [clearing, setClearing] = useState(false);
  const [notice, setNotice] = useState<Notice | null>(null);

  // Show any existing active feedback so it survives page navigation.
  useEffect(() => {
    getFeedback()
      .then((f: FeedbackState) => {
        setCurrent(f.text || '');
        setSetAt(f.set_at || undefined);
      })
      .catch(() => { /* backend may not expose feedback yet — treat as none */ });
  }, []);

  const refreshFromServer = async () => {
    const f = await getFeedback();
    setCurrent(f.text || '');
    setSetAt(f.set_at || undefined);
  };

  const handleSend = async () => {
    const t = text.trim();
    if (!t || sending) return;
    setSending(true);
    setNotice(null);
    try {
      const res = await postFeedback(t);
      if (!res.ok) throw new Error(res.text ? `Backend rejected: ${res.text}` : 'Backend rejected');
      const confirmed = res.text || t;
      setText('');
      try {
        await refreshFromServer();
      } catch {
        setCurrent(confirmed);
        setSetAt(new Date().toISOString());
      }
      setNotice({ type: 'ok', msg: 'Injected — the next agent call will see it' });
      onChanged?.(confirmed);
    } catch (e) {
      setNotice({ type: 'error', msg: e instanceof Error ? e.message : String(e) });
    } finally {
      setSending(false);
    }
  };

  const handleClear = async () => {
    if (!current || clearing) return;
    if (!window.confirm('Clear the active live feedback? Agents will no longer see it.')) return;
    setClearing(true);
    setNotice(null);
    try {
      await clearFeedback();
      setCurrent('');
      setSetAt(undefined);
      setNotice({ type: 'ok', msg: 'Cleared — agents no longer see feedback' });
      onChanged?.('');
    } catch (e) {
      setNotice({ type: 'error', msg: e instanceof Error ? e.message : String(e) });
    } finally {
      setClearing(false);
    }
  };

  return (
    <div className="card p-3 flex flex-col gap-2">
      {/* Header */}
      <div className="flex items-center gap-2">
        <MessageSquareText size={14} className="text-brand-500 shrink-0" />
        <span className="text-[11px] font-bold text-gray-700 dark:text-gray-200 uppercase tracking-wider">
          Live Feedback
        </span>
        <span className="text-[10px] text-gray-400 hidden sm:inline">
          steers the next agent call
        </span>
      </div>

      {/* Active feedback */}
      {current && (
        <div className="rounded-lg bg-amber-50 dark:bg-amber-950/30 border border-amber-200 dark:border-amber-800 px-3 py-2">
          <div className="text-[10px] font-semibold text-amber-700 dark:text-amber-400 uppercase tracking-wider mb-0.5">
            Active{setAt ? ` · set at ${formatTime(setAt)}` : ''}
          </div>
          <p className="text-xs text-amber-900 dark:text-amber-200 leading-relaxed break-words whitespace-pre-wrap">
            {current}
          </p>
        </div>
      )}

      {/* Composer */}
      <div className="flex gap-2 items-end">
        <textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') handleSend();
          }}
          placeholder="Steer the agents… e.g. 'focus on tests' or 'stop refactoring, ship it'"
          rows={2}
          className="input flex-1 resize-none"
        />
        <div className="flex flex-col gap-1.5 shrink-0">
          <button
            onClick={handleSend}
            disabled={!text.trim() || sending}
            className="btn-primary h-8 px-3 text-xs"
          >
            {sending ? <Loader2 size={13} className="animate-spin" /> : <Send size={13} />}
            Send
          </button>
          {current && (
            <button
              onClick={handleClear}
              disabled={clearing}
              className="btn-ghost h-8 px-3 text-xs"
            >
              {clearing ? <Loader2 size={13} className="animate-spin" /> : <Trash2 size={13} />}
              Clear
            </button>
          )}
        </div>
      </div>

      {/* Notice */}
      {notice && (
        <div
          className={clsx(
            'text-[11px] leading-snug',
            notice.type === 'ok'
              ? 'text-emerald-600 dark:text-emerald-400'
              : 'text-red-600 dark:text-red-400',
          )}
        >
          {notice.msg}
        </div>
      )}
    </div>
  );
}

function formatTime(iso: string): string {
  try {
    return new Date(iso).toLocaleTimeString('en-US', {
      hour12: false,
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    });
  } catch {
    return '--:--:--';
  }
}
