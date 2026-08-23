import { RefreshCw, Wifi, WifiOff } from 'lucide-react';
import clsx from 'clsx';
import type { ConnectionState } from '@/types';

// ── Connection status ──
//
// Studio used to run `refresh()` exactly once at mount and swallow the failure
// with `catch {}`. Kill the server and the SPA looked perfectly idle and
// healthy. This surfaces the truth, derived from EventSource.readyState plus a
// 10s /api/health poll.

const LABEL: Record<ConnectionState, string> = {
  connecting: 'Connecting',
  live: 'Live',
  reconnecting: 'Reconnecting',
  down: 'API disconnected',
};

const TITLE: Record<ConnectionState, string> = {
  connecting: 'Opening the event stream…',
  live: 'Connected to the Studio API',
  reconnecting: 'Lost the event stream — retrying with backoff',
  down: 'The Studio API is not answering. Is `slmcode studio` still running?',
};

const DOT: Record<ConnectionState, string> = {
  connecting: 'bg-amber-500 animate-pulse',
  live: 'bg-emerald-500 animate-pulse',
  reconnecting: 'bg-amber-500 animate-pulse',
  down: 'bg-red-500',
};

const CHROME: Record<ConnectionState, string> = {
  connecting: 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300',
  live: 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300',
  reconnecting: 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-200',
  down: 'bg-red-100 text-red-800 dark:bg-red-900/40 dark:text-red-200',
};

interface Props {
  state: ConnectionState;
  onRetry?: () => void;
}

export default function ConnectionBadge({ state, onRetry }: Props) {
  const degraded = state === 'down' || state === 'reconnecting';
  return (
    <div
      className={clsx(
        'inline-flex items-center gap-1.5 rounded-full px-2 py-1 text-[10px] font-semibold uppercase tracking-wider',
        CHROME[state],
      )}
      title={TITLE[state]}
    >
      <span className={clsx('h-1.5 w-1.5 rounded-full', DOT[state])} aria-hidden="true" />
      {/* The live region is what a screen reader announces on state change. */}
      <span role="status" aria-live="polite" className="hidden sm:inline">
        {LABEL[state]}
      </span>
      <span className="sm:hidden" aria-hidden="true">
        {state === 'down' ? <WifiOff size={12} /> : <Wifi size={12} />}
      </span>
      {degraded && onRetry && (
        <button
          type="button"
          onClick={onRetry}
          className="focus-ring ml-0.5 rounded p-0.5 hover:opacity-70"
          aria-label="Reconnect to the Studio API now"
          title="Reconnect now"
        >
          <RefreshCw size={11} aria-hidden="true" />
        </button>
      )}
    </div>
  );
}
