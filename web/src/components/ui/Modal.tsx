import React, { createContext, useCallback, useContext, useEffect, useId, useMemo, useRef, useState } from 'react';
import { AlertTriangle } from 'lucide-react';
import clsx from 'clsx';

// ── Accessible modal + confirm ──
//
// Nine destructive actions used `window.confirm()` / `alert()`: unstylable,
// unlabelled, and impossible to test. HITLPopup already did this properly
// (role="dialog", aria-modal, labelled controls, focus restore); this lifts that
// pattern into a primitive everything else can use.

export interface ModalProps {
  open: boolean;
  title: string;
  description?: string;
  onClose: () => void;
  children?: React.ReactNode;
  footer?: React.ReactNode;
  /** Applied to the dialog panel — use for wide content such as diffs. */
  className?: string;
  /** Element focused when the dialog opens. Falls back to the first control. */
  initialFocusRef?: React.RefObject<HTMLElement>;
}

const FOCUSABLE =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

export function Modal({
  open,
  title,
  description,
  onClose,
  children,
  footer,
  className,
  initialFocusRef,
}: ModalProps) {
  const panelRef = useRef<HTMLDivElement>(null);
  const titleId = useId();
  const descId = useId();

  // Focus management: move focus in on open, restore it on close.
  useEffect(() => {
    if (!open) return undefined;
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const timer = window.setTimeout(() => {
      const target =
        initialFocusRef?.current ??
        panelRef.current?.querySelector<HTMLElement>(FOCUSABLE) ??
        panelRef.current;
      target?.focus();
    }, 0);
    return () => {
      window.clearTimeout(timer);
      previous?.focus();
    };
  }, [open, initialFocusRef]);

  // Esc closes; Tab is trapped inside the dialog.
  useEffect(() => {
    if (!open) return undefined;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        onClose();
        return;
      }
      if (e.key !== 'Tab') return;
      const panel = panelRef.current;
      if (!panel) return;
      const items = Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
        (el) => el.offsetParent !== null || el === document.activeElement,
      );
      if (items.length === 0) return;
      const first = items[0];
      const last = items[items.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    };
    document.addEventListener('keydown', onKeyDown, true);
    return () => document.removeEventListener('keydown', onKeyDown, true);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div
        className="absolute inset-0 bg-black/50 backdrop-blur-sm"
        onClick={onClose}
        aria-hidden="true"
      />
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={description ? descId : undefined}
        tabIndex={-1}
        className={clsx(
          'relative z-10 w-full max-w-lg rounded-xl border border-gray-200 bg-white shadow-2xl',
          'dark:border-gray-800 dark:bg-gray-900',
          className,
        )}
      >
        <div className="border-b border-gray-100 px-5 py-3 dark:border-gray-800">
          <h2 id={titleId} className="text-sm font-semibold">
            {title}
          </h2>
          {description && (
            <p id={descId} className="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {description}
            </p>
          )}
        </div>
        {children && <div className="px-5 py-4 text-sm">{children}</div>}
        {footer && (
          <div className="flex justify-end gap-2 border-t border-gray-100 px-5 py-3 dark:border-gray-800">
            {footer}
          </div>
        )}
      </div>
    </div>
  );
}

// ── confirm() replacement ──

interface ConfirmOptions {
  title: string;
  description?: string;
  confirmLabel?: string;
  cancelLabel?: string;
  /** Styles the confirm button as destructive. Defaults to true. */
  destructive?: boolean;
}

type Resolver = (ok: boolean) => void;

interface ConfirmContextValue {
  confirm: (opts: ConfirmOptions) => Promise<boolean>;
}

const ConfirmContext = createContext<ConfirmContextValue | null>(null);

export function ConfirmProvider({ children }: { children: React.ReactNode }) {
  const [pending, setPending] = useState<(ConfirmOptions & { resolve: Resolver }) | null>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);

  const confirm = useCallback(
    (opts: ConfirmOptions) =>
      new Promise<boolean>((resolve) => {
        setPending({ ...opts, resolve });
      }),
    [],
  );

  const settle = useCallback(
    (ok: boolean) => {
      setPending((p) => {
        p?.resolve(ok);
        return null;
      });
    },
    [],
  );

  const value = useMemo<ConfirmContextValue>(() => ({ confirm }), [confirm]);

  return (
    <ConfirmContext.Provider value={value}>
      {children}
      <Modal
        open={pending !== null}
        title={pending?.title ?? ''}
        description={pending?.description}
        onClose={() => settle(false)}
        initialFocusRef={confirmRef}
        footer={
          <>
            <button type="button" className="btn-secondary focus-ring text-xs" onClick={() => settle(false)}>
              {pending?.cancelLabel ?? 'Cancel'}
            </button>
            <button
              ref={confirmRef}
              type="button"
              className={clsx(
                'focus-ring rounded-lg px-3 py-1.5 text-xs font-semibold text-white',
                pending?.destructive === false
                  ? 'bg-brand-600 hover:bg-brand-700'
                  : 'bg-red-600 hover:bg-red-700',
              )}
              onClick={() => settle(true)}
            >
              {pending?.confirmLabel ?? 'Delete'}
            </button>
          </>
        }
      >
        {pending?.destructive !== false && (
          <div className="flex items-start gap-2 text-xs text-gray-600 dark:text-gray-400">
            <AlertTriangle size={16} className="mt-0.5 shrink-0 text-amber-500" aria-hidden="true" />
            <span>This cannot be undone.</span>
          </div>
        )}
      </Modal>
    </ConfirmContext.Provider>
  );
}

/**
 * useConfirm returns an async confirm(). Without a provider it degrades to the
 * native dialog rather than breaking the caller.
 */
export function useConfirm(): (opts: ConfirmOptions) => Promise<boolean> {
  const ctx = useContext(ConfirmContext);
  return useMemo(() => {
    if (ctx) return ctx.confirm;
    return async (opts: ConfirmOptions) =>
      typeof window !== 'undefined'
        ? // eslint-disable-next-line no-alert -- intentional fallback when no ConfirmProvider is mounted; the provider itself never reaches this branch
          window.confirm(`${opts.title}${opts.description ? `\n\n${opts.description}` : ''}`)
        : false;
  }, [ctx]);
}
