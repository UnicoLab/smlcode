import { useCallback, useContext, useEffect, useMemo, useState } from 'react';
import {
  Check,
  CheckCheck,
  Columns2,
  FileDiff,
  FilePlus2,
  Loader2,
  RefreshCw,
  Rows3,
  X,
} from 'lucide-react';
import clsx from 'clsx';
import { AppContext } from '@/App';
import { applyPendingChanges, getPendingReview, rejectPendingChanges } from '@/api/client';
import type { PendingChange, ReviewQueue } from '@/types';
import { useToast } from '@/components/ui/Toast';
import { useConfirm } from '@/components/ui/Modal';
import DiffView, { type DiffMode } from './DiffView';

// ── Review queue ──
//
// With `permission: review` the harness writes every proposed change to
// .slmcode/pending/*.patch.json and waits for a human. Until now the only way
// to act on that queue was `slmcode apply`, which applies *everything* blind.
// This is the per-file diff review that was missing.

export default function ReviewView() {
  const ctx = useContext(AppContext);
  const toast = useToast();
  const confirm = useConfirm();

  const [queue, setQueue] = useState<ReviewQueue | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [mode, setMode] = useState<DiffMode>('unified');
  const [busy, setBusy] = useState<string | null>(null);

  const load = useCallback(
    async (opts?: { quiet?: boolean }) => {
      if (!opts?.quiet) setLoading(true);
      try {
        const q = await getPendingReview({ hunks: true, context: 3 });
        setQueue(q);
        setError(null);
        setSelectedId((prev) => (prev && q.items.some((i) => i.id === prev) ? prev : q.items[0]?.id ?? null));
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Could not load the review queue');
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  useEffect(() => {
    load();
  }, [load]);

  // A run that writes new pending changes should refresh the queue.
  const runEnded = ctx?.liveRunning === false;
  useEffect(() => {
    if (runEnded) load({ quiet: true });
  }, [runEnded, load]);

  const selected = useMemo(
    () => queue?.items.find((i) => i.id === selectedId) ?? null,
    [queue, selectedId],
  );

  const act = useCallback(
    async (kind: 'apply' | 'reject', target: { id?: string; all?: boolean }, label: string) => {
      setBusy(target.id ?? 'all');
      try {
        const res =
          kind === 'apply' ? await applyPendingChanges(target) : await rejectPendingChanges(target);
        const touched = kind === 'apply' ? (res as { applied: string[] }).applied : (res as { rejected: string[] }).rejected;
        if (res.failed.length > 0) {
          toast.push({
            tone: 'warning',
            title: `${label} partially failed`,
            detail: res.failed.map((f) => `${f.path || f.id}: ${f.error}`).join('; '),
          });
        } else {
          toast.success(`${label} — ${touched.length} file${touched.length === 1 ? '' : 's'}`);
        }
        await load({ quiet: true });
      } catch (err) {
        toast.reportError(err, `${label} failed`);
      } finally {
        setBusy(null);
      }
    },
    [load, toast],
  );

  const applyAll = useCallback(async () => {
    const n = queue?.count ?? 0;
    const ok = await confirm({
      title: `Apply all ${n} pending change${n === 1 ? '' : 's'}?`,
      description: 'Every queued file will be written to the workspace.',
      confirmLabel: 'Apply all',
      destructive: false,
    });
    if (ok) act('apply', { all: true }, 'Applied all');
  }, [act, confirm, queue?.count]);

  const rejectAll = useCallback(async () => {
    const n = queue?.count ?? 0;
    const ok = await confirm({
      title: `Reject all ${n} pending change${n === 1 ? '' : 's'}?`,
      description: 'The queued patches are deleted. The workspace is left untouched.',
      confirmLabel: 'Reject all',
    });
    if (ok) act('reject', { all: true }, 'Rejected all');
  }, [act, confirm, queue?.count]);

  const isReviewMode = queue?.permission === 'review';

  return (
    <div className="flex h-full flex-col">
      <header className="flex flex-wrap items-center gap-3 border-b border-gray-200 px-4 py-3 dark:border-gray-800">
        <div className="flex items-center gap-2">
          <FileDiff size={16} className="text-brand-600" aria-hidden="true" />
          <h1 className="text-sm font-semibold">Review queue</h1>
          <span className="rounded-full bg-gray-100 px-2 py-0.5 text-[10px] font-semibold dark:bg-gray-800">
            {queue?.count ?? 0} pending
          </span>
          {queue && (queue.stat.added > 0 || queue.stat.removed > 0) && (
            <span className="font-mono text-[10px] text-gray-500">
              <span className="text-emerald-600">+{queue.stat.added}</span>{' '}
              <span className="text-red-600">−{queue.stat.removed}</span>
            </span>
          )}
        </div>

        <div className="flex-1" />

        <div className="flex items-center gap-1 rounded-lg border border-gray-200 p-0.5 dark:border-gray-700" role="group" aria-label="Diff layout">
          <button
            type="button"
            onClick={() => setMode('unified')}
            aria-pressed={mode === 'unified'}
            className={clsx('focus-ring flex items-center gap-1 rounded px-2 py-1 text-[11px]', mode === 'unified' && 'bg-gray-100 dark:bg-gray-800')}
          >
            <Rows3 size={12} aria-hidden="true" /> Unified
          </button>
          <button
            type="button"
            onClick={() => setMode('split')}
            aria-pressed={mode === 'split'}
            className={clsx('focus-ring flex items-center gap-1 rounded px-2 py-1 text-[11px]', mode === 'split' && 'bg-gray-100 dark:bg-gray-800')}
          >
            <Columns2 size={12} aria-hidden="true" /> Split
          </button>
        </div>

        <button type="button" onClick={() => load()} className="btn-secondary focus-ring text-xs" aria-label="Refresh the review queue">
          <RefreshCw size={13} aria-hidden="true" />
        </button>
        <button
          type="button"
          onClick={rejectAll}
          disabled={!queue?.count || busy !== null}
          className="btn-secondary focus-ring text-xs disabled:opacity-40"
        >
          Reject all
        </button>
        <button
          type="button"
          onClick={applyAll}
          disabled={!queue?.count || busy !== null}
          className="focus-ring inline-flex items-center gap-1.5 rounded-lg bg-brand-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-brand-700 disabled:opacity-40"
        >
          {busy === 'all' ? <Loader2 size={13} className="animate-spin" aria-hidden="true" /> : <CheckCheck size={13} aria-hidden="true" />}
          Apply all
        </button>
      </header>

      {!isReviewMode && queue && queue.count === 0 && (
        <p className="border-b border-amber-500/30 bg-amber-50 px-4 py-2 text-xs text-amber-800 dark:bg-amber-950/40 dark:text-amber-200">
          Permission mode is <code className="font-mono">{queue.permission || 'auto'}</code>, so agents write
          directly and nothing is queued. Set <code className="font-mono">permission: review</code> in Settings to
          route every write through this page.
        </p>
      )}

      {error && (
        <p role="alert" className="border-b border-red-500/30 bg-red-50 px-4 py-2 text-xs text-red-700 dark:bg-red-950/40 dark:text-red-200">
          {error}
        </p>
      )}

      <div className="flex min-h-0 flex-1">
        {/* File list */}
        <nav aria-label="Pending changes" className="w-72 shrink-0 overflow-y-auto border-r border-gray-200 dark:border-gray-800">
          {loading && (
            <div className="flex items-center gap-2 px-4 py-6 text-xs text-gray-400">
              <Loader2 size={14} className="animate-spin" aria-hidden="true" /> Loading queue…
            </div>
          )}
          {!loading && queue?.count === 0 && (
            <p className="px-4 py-6 text-xs text-gray-400">Nothing waiting for review.</p>
          )}
          <ul>
            {queue?.items.map((item) => (
              <li key={item.id}>
                <button
                  type="button"
                  onClick={() => setSelectedId(item.id)}
                  aria-current={item.id === selectedId ? 'true' : undefined}
                  className={clsx(
                    'focus-ring flex w-full items-start gap-2 border-b border-gray-100 px-3 py-2 text-left dark:border-gray-800',
                    item.id === selectedId
                      ? 'bg-brand-50 dark:bg-brand-900/20'
                      : 'hover:bg-gray-50 dark:hover:bg-gray-800/50',
                  )}
                >
                  {item.is_new ? (
                    <FilePlus2 size={13} className="mt-0.5 shrink-0 text-emerald-500" aria-hidden="true" />
                  ) : (
                    <FileDiff size={13} className="mt-0.5 shrink-0 text-amber-500" aria-hidden="true" />
                  )}
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-mono text-[11px]">{item.path}</span>
                    <span className="mt-0.5 block font-mono text-[10px] text-gray-500">
                      {item.is_new && <span className="mr-1 text-emerald-600">new</span>}
                      <span className="text-emerald-600">+{item.stat.added}</span>{' '}
                      <span className="text-red-600">−{item.stat.removed}</span>
                      {item.error && <span className="ml-1 text-red-600">· {item.error}</span>}
                    </span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </nav>

        {/* Diff panel */}
        <section className="min-w-0 flex-1 overflow-y-auto" aria-label="Change detail">
          {!selected && !loading && (
            <p className="px-6 py-10 text-center text-xs text-gray-400">
              Select a pending change to review its diff.
            </p>
          )}
          {selected && (
            <>
              <div className="sticky top-0 z-10 flex flex-wrap items-center gap-2 border-b border-gray-200 bg-surface px-4 py-2 dark:border-gray-800">
                <code className="min-w-0 flex-1 truncate font-mono text-xs">{selected.path}</code>
                <span className="font-mono text-[10px] text-gray-500">{selected.bytes} B</span>
                <button
                  type="button"
                  disabled={busy !== null}
                  onClick={() => act('reject', { id: selected.id }, `Rejected ${selected.path}`)}
                  className="btn-secondary focus-ring inline-flex items-center gap-1 text-xs disabled:opacity-40"
                >
                  <X size={12} aria-hidden="true" /> Reject
                </button>
                <button
                  type="button"
                  disabled={busy !== null || Boolean(selected.error)}
                  onClick={() => act('apply', { id: selected.id }, `Applied ${selected.path}`)}
                  className="focus-ring inline-flex items-center gap-1 rounded-lg bg-emerald-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-emerald-700 disabled:opacity-40"
                >
                  {busy === selected.id ? (
                    <Loader2 size={12} className="animate-spin" aria-hidden="true" />
                  ) : (
                    <Check size={12} aria-hidden="true" />
                  )}
                  Apply
                </button>
              </div>
              <DiffView
                hunks={selected.hunks ?? []}
                mode={mode}
                truncated={selected.truncated}
                emptyLabel={
                  selected.stat.binary
                    ? 'Binary file — no textual diff available.'
                    : selected.is_new
                      ? 'New empty file.'
                      : 'The proposed content is identical to the file on disk.'
                }
              />
            </>
          )}
        </section>
      </div>
    </div>
  );
}
