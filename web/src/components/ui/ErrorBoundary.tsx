import React from 'react';
import { AlertOctagon, RefreshCw } from 'lucide-react';

// ── Error boundary ──
//
// Without one, a single render throw inside a lazily-loaded page unmounts the
// whole SPA and leaves a blank white screen with no way back short of a manual
// reload. This keeps the chrome alive and offers a real recovery action.

interface Props {
  children: React.ReactNode;
  /** Changing this value resets the boundary — pass the route key. */
  resetKey?: string;
  label?: string;
}

interface State {
  error: Error | null;
}

export default class ErrorBoundary extends React.Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidUpdate(prev: Props) {
    if (prev.resetKey !== this.props.resetKey && this.state.error) {
      this.setState({ error: null });
    }
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    console.error('Studio render error:', error, info.componentStack);
  }

  private retry = () => this.setState({ error: null });

  private reload = () => window.location.reload();

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;

    return (
      <div role="alert" className="flex h-full items-center justify-center p-8">
        <div className="w-full max-w-lg rounded-xl border border-red-500/40 bg-red-50 p-6 dark:bg-red-950/40">
          <div className="flex items-center gap-2 text-red-700 dark:text-red-300">
            <AlertOctagon size={18} aria-hidden="true" />
            <h2 className="text-sm font-semibold">
              {this.props.label ? `${this.props.label} failed to render` : 'This page failed to render'}
            </h2>
          </div>
          <p className="mt-2 text-xs text-red-800/80 dark:text-red-200/70">
            The rest of Studio is still running — the agent run, if any, is unaffected.
          </p>
          <pre className="mt-3 max-h-40 overflow-auto rounded-lg bg-white/70 p-3 font-mono text-[11px] text-red-900 dark:bg-black/30 dark:text-red-200">
            {error.message}
          </pre>
          <div className="mt-4 flex gap-2">
            <button type="button" onClick={this.retry} className="btn-secondary focus-ring text-xs">
              Try again
            </button>
            <button
              type="button"
              onClick={this.reload}
              className="focus-ring inline-flex items-center gap-1.5 rounded-lg bg-red-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-red-700"
            >
              <RefreshCw size={13} aria-hidden="true" />
              Reload Studio
            </button>
          </div>
        </div>
      </div>
    );
  }
}
