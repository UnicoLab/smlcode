import { useCallback, useContext, useEffect, useRef, useState } from 'react';
import { Outlet, useLocation } from 'react-router-dom';
import { AlertTriangle, X } from 'lucide-react';
import TopBar from './TopBar';
import Sidebar from './Sidebar';
import HITLPopup from './Live/HITLPopup';
import ErrorBoundary from './ui/ErrorBoundary';
import ShortcutSheet from './ui/ShortcutSheet';
import CommandPalette from './ui/CommandPalette';
import Confetti from './ui/Confetti';
import { useToast } from './ui/Toast';
import { AppContext } from '@/App';
import { useKeyboardShortcuts } from '@/hooks/useKeyboard';
import type { RunEvent } from '@/types';

/** How long the tab title alternates after a run ends in a hidden tab. */
const TITLE_FLASH_MS = 12_000;

/** run_end with phase "done" whose message does not report failed tasks. */
export function runEndVerdict(ev: RunEvent): { ok: boolean; failedTasks: number } {
  const m = /(\d+)\s+failed/i.exec(ev.message || '');
  const failedTasks = m ? Number.parseInt(m[1], 10) : 0;
  return { ok: ev.phase !== 'error' && failedTasks === 0, failedTasks };
}

export default function Layout() {
  const ctx = useContext(AppContext);
  const location = useLocation();
  const toast = useToast();
  const { sheetOpen, setSheetOpen, paletteOpen, setPaletteOpen } = useKeyboardShortcuts();
  const [burst, setBurst] = useState(0);
  const endBurstDone = useCallback(() => setBurst(0), []);

  // ── Run end: a toast, a flashing tab title, a notification, a burst ──
  //
  // Studio is usually one tab among many while a local model grinds for ten
  // minutes. The run finishing has to reach the user wherever they are:
  // in the tab (toast + confetti), on the tab strip (title), or in another
  // window entirely (Notification, only when the tab is hidden and only when
  // permission was already granted — this never asks on its own).
  const events = ctx?.liveEvents ?? [];
  const last = events.length > 0 ? events[events.length - 1] : null;
  const sawRunning = useRef(false);
  const handledEnd = useRef<RunEvent | null>(null);
  const titleTimer = useRef<number | null>(null);
  const originalTitle = useRef<string>(typeof document !== 'undefined' ? document.title : 'Studio');

  useEffect(() => {
    if (ctx?.liveRunning) sawRunning.current = true;
  }, [ctx?.liveRunning]);

  const stopTitleFlash = useCallback(() => {
    if (titleTimer.current !== null) {
      window.clearInterval(titleTimer.current);
      titleTimer.current = null;
    }
    document.title = originalTitle.current;
  }, []);

  const flashTitle = useCallback(
    (text: string) => {
      stopTitleFlash();
      originalTitle.current = document.title;
      let on = false;
      titleTimer.current = window.setInterval(() => {
        on = !on;
        document.title = on ? text : originalTitle.current;
      }, 1000);
      window.setTimeout(stopTitleFlash, TITLE_FLASH_MS);
    },
    [stopTitleFlash],
  );

  // Coming back to the tab ends the title flash.
  useEffect(() => {
    const onVisible = () => {
      if (!document.hidden) stopTitleFlash();
    };
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      document.removeEventListener('visibilitychange', onVisible);
      stopTitleFlash();
    };
  }, [stopTitleFlash]);

  useEffect(() => {
    if (!last || last.kind !== 'run_end') return;
    if (handledEnd.current === last) return;
    handledEnd.current = last;
    // A run_end replayed from the server snapshot on page load is history, not news.
    if (!sawRunning.current) return;
    sawRunning.current = false;

    const { ok, failedTasks } = runEndVerdict(last);
    const summary = (last.message || '').slice(0, 200);
    if (ok) {
      toast.success('Run finished', summary);
      setBurst((n) => n + 1);
    } else {
      toast.push({
        tone: 'error',
        title: failedTasks > 0 ? `Run finished with ${failedTasks} failed task${failedTasks === 1 ? '' : 's'}` : 'Run failed',
        detail: summary,
      });
    }

    if (document.hidden) {
      flashTitle(ok ? '✓ Run finished' : '✗ Run needs you');
      try {
        if ('Notification' in window && Notification.permission === 'granted') {
          const n = new Notification(ok ? 'Studio: run finished' : 'Studio: run needs you', {
            body: summary || undefined,
            tag: 'slmcode-run-end',
          });
          n.onclick = () => {
            window.focus();
            n.close();
          };
        }
      } catch {
        /* notifications unavailable — the title flash still happened */
      }
    }
  }, [last, toast, flashTitle]);

  return (
    <div className="h-screen flex flex-col overflow-hidden">
      {/* First tab stop: jump past the chrome straight to the page. */}
      <a href="#main" className="skip-link">
        Skip to main content
      </a>
      <TopBar />

      {/* A gap means the server rolled events out of its buffer while we were
          away — say so rather than showing a silently incomplete log. */}
      {ctx?.streamGap && (
        <div
          role="status"
          className="flex items-center gap-2 border-b border-amber-500/30 bg-amber-50 px-4 py-1.5 text-[11px]
                     text-amber-800 dark:bg-amber-950/40 dark:text-amber-200"
        >
          <AlertTriangle size={13} aria-hidden="true" />
          <span className="flex-1">{ctx.streamGap}</span>
          <button
            type="button"
            onClick={ctx.clearStreamGap}
            className="focus-ring rounded p-0.5 hover:opacity-70"
            aria-label="Dismiss the dropped-events notice"
          >
            <X size={12} aria-hidden="true" />
          </button>
        </div>
      )}

      <div className="flex flex-1 overflow-hidden">
        {/* Sidebar — manages its own collapsed/expanded width */}
        <aside className="flex-shrink-0 border-r border-gray-200 dark:border-gray-800 bg-surface-alt">
          <Sidebar />
        </aside>

        {/* Main content. The boundary resets on navigation so a broken page
            does not poison the next one. */}
        <main id="main" className="min-w-0 flex-1 overflow-auto bg-surface">
          <ErrorBoundary resetKey={location.pathname}>
            <Outlet />
          </ErrorBoundary>
        </main>
      </div>

      {/* HITL gates are app-global, not route-scoped. Mounted only inside the
          Live route, navigating to Board/Files/Settings during a run meant the
          gate never rendered and the harness timed out into its default. */}
      <HITLPopup running={Boolean(ctx?.liveRunning)} askSignal={ctx?.askSignal ?? 0} />

      <ShortcutSheet open={sheetOpen} onClose={() => setSheetOpen(false)} />
      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />
      <Confetti burst={burst} onDone={endBurstDone} />
    </div>
  );
}
