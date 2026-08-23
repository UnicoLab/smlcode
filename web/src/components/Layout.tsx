import { useContext } from 'react';
import { Outlet, useLocation } from 'react-router-dom';
import { AlertTriangle, X } from 'lucide-react';
import TopBar from './TopBar';
import Sidebar from './Sidebar';
import HITLPopup from './Live/HITLPopup';
import ErrorBoundary from './ui/ErrorBoundary';
import ShortcutSheet from './ui/ShortcutSheet';
import { AppContext } from '@/App';
import { useKeyboardShortcuts } from '@/hooks/useKeyboard';

export default function Layout() {
  const ctx = useContext(AppContext);
  const location = useLocation();
  const { sheetOpen, setSheetOpen } = useKeyboardShortcuts();

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
    </div>
  );
}
