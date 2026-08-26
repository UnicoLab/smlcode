import { ChevronRight } from 'lucide-react';
import type { ReactNode } from 'react';
import clsx from 'clsx';

// ── A header panel that can get out of the way ──
//
// The Live page stacks status, pipeline progress, stage detail, agent activity
// and composition above the event log. Every one of them was `shrink-0`, so on
// a laptop viewport they consumed the whole screen and the log — the thing the
// page exists to show — was squeezed to a few pixels or clipped entirely.
//
// Collapsing is a per-section user choice that survives a reload (see
// usePersistentState in hooks/useUiState).

interface Props {
  /** Left-hand title. */
  title: string;
  /** Rendered next to the title — counts, badges, current phase. */
  meta?: ReactNode;
  open: boolean;
  onToggle: () => void;
  children: ReactNode;
  /** Extra classes for the outer <section>. */
  className?: string;
  /** Body padding; set false when the child manages its own. */
  padded?: boolean;
}

export default function CollapsibleSection({
  title,
  meta,
  open,
  onToggle,
  children,
  className,
  padded = true,
}: Props) {
  const bodyId = `section-${title.replace(/\s+/g, '-').toLowerCase()}`;

  return (
    <section
      className={clsx(
        'overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-900',
        className,
      )}
    >
      <h3 className="m-0">
        <button
          type="button"
          onClick={onToggle}
          aria-expanded={open}
          aria-controls={bodyId}
          className="focus-ring flex w-full items-center gap-2 px-3 py-2 text-left transition-colors
                     hover:bg-gray-50 dark:hover:bg-gray-800/60"
        >
          <ChevronRight
            size={14}
            aria-hidden="true"
            className={clsx(
              'shrink-0 text-gray-400 transition-transform duration-150',
              open && 'rotate-90',
            )}
          />
          <span className="shrink-0 text-xs font-bold text-gray-800 dark:text-gray-100">{title}</span>
          {/* min-w-0 + truncate: metadata is the part that may be long (a model
              id, a phase name), and it must shrink rather than push the chevron
              and title off the edge on a narrow panel. */}
          <span className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5 overflow-hidden">{meta}</span>
        </button>
      </h3>
      {open && (
        <div id={bodyId} className={clsx('border-t border-gray-200 dark:border-gray-800', padded && 'p-3')}>
          {children}
        </div>
      )}
    </section>
  );
}
