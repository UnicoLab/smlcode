import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { AlertTriangle, CheckCircle2, Info, X, XCircle } from 'lucide-react';
import clsx from 'clsx';
import { errorText } from '@/api/client';

// ── Toasts ──
//
// 37 bare `catch {}` blocks used to swallow every API failure. The worst was
// TopBar's model switcher: the server answers `409 cannot update configuration
// while a run is active` and the UI showed nothing at all. This is the shared
// surface those handlers now report to.

export type ToastTone = 'info' | 'success' | 'warning' | 'error';

export interface Toast {
  id: number;
  tone: ToastTone;
  title: string;
  detail?: string;
  /** Auto-dismiss delay; 0 keeps it until dismissed. */
  ttl: number;
}

interface ToastContextValue {
  toasts: Toast[];
  push: (toast: Omit<Toast, 'id' | 'ttl'> & { ttl?: number }) => number;
  dismiss: (id: number) => void;
  /** Convenience for `catch` blocks: surfaces any thrown value. */
  reportError: (err: unknown, title?: string) => void;
  success: (title: string, detail?: string) => void;
  info: (title: string, detail?: string) => void;
}

const ToastContext = createContext<ToastContextValue | null>(null);

const DEFAULT_TTL: Record<ToastTone, number> = {
  info: 4000,
  success: 3500,
  warning: 7000,
  error: 0, // errors stay until acknowledged
};

let nextId = 1;

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const timers = useRef<Map<number, number>>(new Map());

  const dismiss = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
    const handle = timers.current.get(id);
    if (handle !== undefined) {
      window.clearTimeout(handle);
      timers.current.delete(id);
    }
  }, []);

  const push = useCallback<ToastContextValue['push']>(
    (toast) => {
      const id = nextId++;
      const ttl = toast.ttl ?? DEFAULT_TTL[toast.tone];
      setToasts((prev) => [...prev.slice(-4), { ...toast, id, ttl }]);
      if (ttl > 0) {
        timers.current.set(
          id,
          window.setTimeout(() => dismiss(id), ttl),
        );
      }
      return id;
    },
    [dismiss],
  );

  const reportError = useCallback<ToastContextValue['reportError']>(
    (err, title = 'Something went wrong') => {
      push({ tone: 'error', title, detail: errorText(err) });
    },
    [push],
  );

  const success = useCallback(
    (title: string, detail?: string) => {
      push({ tone: 'success', title, detail });
    },
    [push],
  );

  const info = useCallback(
    (title: string, detail?: string) => {
      push({ tone: 'info', title, detail });
    },
    [push],
  );

  useEffect(() => {
    const handles = timers.current;
    return () => {
      handles.forEach((h) => window.clearTimeout(h));
      handles.clear();
    };
  }, []);

  const value = useMemo<ToastContextValue>(
    () => ({ toasts, push, dismiss, reportError, success, info }),
    [toasts, push, dismiss, reportError, success, info],
  );

  return (
    <ToastContext.Provider value={value}>
      {children}
      <ToastViewport toasts={toasts} onDismiss={dismiss} />
    </ToastContext.Provider>
  );
}

/**
 * useToast never throws when the provider is absent — components rendered in
 * isolation (tests, error-boundary fallbacks) still work, they just do not show
 * toasts.
 */
export function useToast(): ToastContextValue {
  const ctx = useContext(ToastContext);
  const fallback = useMemo<ToastContextValue>(
    () => ({
      toasts: [],
      push: () => 0,
      dismiss: () => {},
      reportError: (err) => console.error(err),
      success: () => {},
      info: () => {},
    }),
    [],
  );
  return ctx ?? fallback;
}

const TONE_ICON: Record<ToastTone, React.ReactNode> = {
  info: <Info size={16} aria-hidden="true" />,
  success: <CheckCircle2 size={16} aria-hidden="true" />,
  warning: <AlertTriangle size={16} aria-hidden="true" />,
  error: <XCircle size={16} aria-hidden="true" />,
};

const TONE_CLASS: Record<ToastTone, string> = {
  info: 'border-sky-500/40 bg-sky-50 dark:bg-sky-950/50 text-sky-900 dark:text-sky-100',
  success: 'border-emerald-500/40 bg-emerald-50 dark:bg-emerald-950/50 text-emerald-900 dark:text-emerald-100',
  warning: 'border-amber-500/40 bg-amber-50 dark:bg-amber-950/50 text-amber-900 dark:text-amber-100',
  error: 'border-red-500/40 bg-red-50 dark:bg-red-950/50 text-red-900 dark:text-red-100',
};

function ToastViewport({ toasts, onDismiss }: { toasts: Toast[]; onDismiss: (id: number) => void }) {
  return (
    <div
      className="fixed bottom-4 right-4 z-[60] flex w-[min(24rem,calc(100vw-2rem))] flex-col gap-2"
      role="region"
      aria-label="Notifications"
    >
      {toasts.map((t) => (
        <div
          key={t.id}
          role={t.tone === 'error' ? 'alert' : 'status'}
          aria-live={t.tone === 'error' ? 'assertive' : 'polite'}
          className={clsx(
            'flex items-start gap-2 rounded-lg border px-3 py-2 shadow-lg animate-fade-in',
            TONE_CLASS[t.tone],
          )}
        >
          <span className="mt-0.5 shrink-0">{TONE_ICON[t.tone]}</span>
          <div className="min-w-0 flex-1">
            <div className="text-xs font-semibold">{t.title}</div>
            {t.detail && <div className="mt-0.5 break-words text-[11px] opacity-80">{t.detail}</div>}
          </div>
          <button
            type="button"
            onClick={() => onDismiss(t.id)}
            aria-label={`Dismiss notification: ${t.title}`}
            className="focus-ring shrink-0 rounded p-0.5 opacity-60 hover:opacity-100"
          >
            <X size={14} aria-hidden="true" />
          </button>
        </div>
      ))}
    </div>
  );
}
