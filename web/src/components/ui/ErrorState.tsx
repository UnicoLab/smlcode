import { AlertTriangle, RefreshCw } from 'lucide-react';
import clsx from 'clsx';
import { errorText } from '@/api/client';

// ── Inline error with a way out ──
//
// A list page whose load failed used to log to the console and render its
// empty state — "No agents defined yet" over a dead backend. This says what
// went wrong where the list would have been, and offers the retry the user
// was about to attempt with a reload anyway.

interface ErrorStateProps {
  /** Anything thrown; normalised through errorText(). */
  error: unknown;
  /** What was being loaded, e.g. "agents". */
  what?: string;
  onRetry?: () => void;
  retrying?: boolean;
  className?: string;
}

export default function ErrorState({ error, what, onRetry, retrying, className }: ErrorStateProps) {
  return (
    <div
      role="alert"
      className={clsx(
        'flex flex-col items-center gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-6 text-center',
        'dark:border-red-900/60 dark:bg-red-950/30',
        className,
      )}
    >
      <AlertTriangle size={20} className="text-red-500" aria-hidden="true" />
      <div className="text-sm font-semibold text-red-800 dark:text-red-200">
        {what ? `Could not load ${what}` : 'Something went wrong'}
      </div>
      <div className="max-w-md break-words text-xs text-red-700/80 dark:text-red-300/80">{errorText(error)}</div>
      {onRetry && (
        <button
          type="button"
          onClick={onRetry}
          disabled={retrying}
          className="btn-secondary focus-ring mt-1 h-8 gap-1.5 px-3 text-xs"
        >
          <RefreshCw size={12} className={clsx(retrying && 'animate-spin')} aria-hidden="true" />
          {retrying ? 'Retrying…' : 'Retry'}
        </button>
      )}
    </div>
  );
}
