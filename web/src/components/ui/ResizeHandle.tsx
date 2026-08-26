import { useCallback, useEffect, useRef } from 'react';
import clsx from 'clsx';

// ── Drag-to-resize divider ──
//
// The Live page's side panel used to be a hard `lg:w-[27rem]`. At 1280px that
// is a third of the window spent on a panel the user may not be reading, and
// there was no way to give the space back to the log. A fixed width cannot be
// right for both a 13" laptop and an ultrawide, so let the user decide — and
// remember it.
//
// Keyboard resizing is not an afterthought: this is a real `separator` widget,
// so arrow keys move it and Home/End jump to the clamped extremes.

interface Props {
  /** Current size in px (width for a vertical divider, height for horizontal). */
  size: number;
  onResize: (next: number) => void;
  min: number;
  max: number;
  /** 'vertical' = a vertical bar the user drags horizontally. */
  orientation?: 'vertical' | 'horizontal';
  /** Dragging left/up grows the panel when the panel sits after the handle. */
  invert?: boolean;
  label: string;
}

const KEY_STEP = 24;

export default function ResizeHandle({
  size,
  onResize,
  min,
  max,
  orientation = 'vertical',
  invert = false,
  label,
}: Props) {
  const draggingRef = useRef(false);
  const startRef = useRef({ pos: 0, size: 0 });
  // Read in a listener attached once per drag; a ref keeps the listener stable
  // without re-subscribing on every pixel of movement.
  const onResizeRef = useRef(onResize);
  onResizeRef.current = onResize;
  const boundsRef = useRef({ min, max });
  boundsRef.current = { min, max };

  const clamp = (n: number) => Math.min(boundsRef.current.max, Math.max(boundsRef.current.min, n));

  const onPointerDown = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      draggingRef.current = true;
      startRef.current = {
        pos: orientation === 'vertical' ? e.clientX : e.clientY,
        size,
      };
      e.currentTarget.setPointerCapture(e.pointerId);
      // Without this the drag selects text across the whole page, and the
      // cursor flickers between resize and text as it crosses elements.
      document.body.style.userSelect = 'none';
      document.body.style.cursor = orientation === 'vertical' ? 'col-resize' : 'row-resize';
    },
    [orientation, size],
  );

  const onPointerMove = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      if (!draggingRef.current) return;
      const current = orientation === 'vertical' ? e.clientX : e.clientY;
      const delta = current - startRef.current.pos;
      const next = startRef.current.size + (invert ? -delta : delta);
      onResizeRef.current(clamp(next));
    },
    [invert, orientation],
  );

  const endDrag = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (!draggingRef.current) return;
    draggingRef.current = false;
    try {
      e.currentTarget.releasePointerCapture(e.pointerId);
    } catch {
      /* the capture is already gone if the pointer left the window */
    }
    document.body.style.userSelect = '';
    document.body.style.cursor = '';
  }, []);

  // A drag interrupted by an alt-tab or a crashed render must not leave the
  // whole document unselectable.
  useEffect(
    () => () => {
      document.body.style.userSelect = '';
      document.body.style.cursor = '';
    },
    [],
  );

  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const grow = invert ? -1 : 1;
    let next: number | null = null;
    if (orientation === 'vertical' && e.key === 'ArrowLeft') next = size - KEY_STEP * grow;
    if (orientation === 'vertical' && e.key === 'ArrowRight') next = size + KEY_STEP * grow;
    if (orientation === 'horizontal' && e.key === 'ArrowUp') next = size - KEY_STEP * grow;
    if (orientation === 'horizontal' && e.key === 'ArrowDown') next = size + KEY_STEP * grow;
    if (e.key === 'Home') next = min;
    if (e.key === 'End') next = max;
    if (next === null) return;
    e.preventDefault();
    onResize(clamp(next));
  };

  return (
    // WAI-ARIA 1.2: a FOCUSABLE `separator` is a window-splitter widget, not the
    // static rule this role usually plays, and the authoring pattern for it is
    // exactly this — tabindex plus aria-valuenow/min/max and arrow-key handling.
    // jsx-a11y classifies `separator` as non-interactive unconditionally and so
    // cannot express that distinction. Using a <button> instead would announce
    // the wrong role to a screen reader, which is a real regression in service
    // of a false positive.
    // eslint-disable-next-line jsx-a11y/no-noninteractive-element-interactions -- focusable separator = splitter widget; see above
    <div
      role="separator"
      aria-orientation={orientation}
      aria-label={label}
      aria-valuenow={Math.round(size)}
      aria-valuemin={min}
      aria-valuemax={max}
      // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- same: the splitter must be reachable by keyboard to be resizable without a pointer
      tabIndex={0}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={endDrag}
      onPointerCancel={endDrag}
      onKeyDown={onKeyDown}
      className={clsx(
        'group relative shrink-0 bg-gray-200 transition-colors hover:bg-brand-400 dark:bg-gray-800 dark:hover:bg-brand-600',
        'focus-visible:bg-brand-500 focus-visible:outline-none',
        orientation === 'vertical' ? 'w-px cursor-col-resize' : 'h-px cursor-row-resize',
      )}
    >
      {/* The visible divider is 1px, which is far too small a pointer target.
          This invisible overlay widens the grab area to 9px without moving
          anything in the layout. */}
      <span
        aria-hidden="true"
        className={clsx(
          'absolute',
          orientation === 'vertical'
            ? '-left-1 top-0 h-full w-[9px] cursor-col-resize'
            : 'left-0 -top-1 h-[9px] w-full cursor-row-resize',
        )}
      />
    </div>
  );
}
