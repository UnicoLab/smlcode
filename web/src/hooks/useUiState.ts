import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { RefObject } from 'react';

// ── Small UI-state primitives shared by the Live page ──
//
// All three exist because the Live page has to fit an unknown viewport while a
// stream mutates it dozens of times a second. Layout preferences therefore have
// to survive a reload, and nothing here may schedule a render per event.

const STORAGE_PREFIX = 'slmcode:ui:';

function readStored<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(STORAGE_PREFIX + key);
    if (raw === null) return fallback;
    return JSON.parse(raw) as T;
  } catch {
    // Private mode, disabled storage, or a value written by an older build.
    return fallback;
  }
}

/**
 * `useState` that persists to localStorage.
 *
 * `fallback` is only consulted on first mount — a later change to it must not
 * clobber a choice the user has already made, which is exactly what would
 * happen if the stored value were re-read on every render.
 */
export function usePersistentState<T>(key: string, fallback: T): [T, (next: T | ((prev: T) => T)) => void] {
  const [value, setValue] = useState<T>(() => readStored(key, fallback));

  const set = useCallback(
    (next: T | ((prev: T) => T)) => {
      setValue((prev) => {
        const resolved = typeof next === 'function' ? (next as (p: T) => T)(prev) : next;
        try {
          localStorage.setItem(STORAGE_PREFIX + key, JSON.stringify(resolved));
        } catch {
          /* the in-memory value is still correct */
        }
        return resolved;
      });
    },
    [key],
  );

  return [value, set];
}

/**
 * Live media-query match.
 *
 * Used to pick *defaults* for a short or narrow viewport, never to override a
 * choice the user made — a laptop that docks to an external monitor should not
 * silently re-expand panels the user collapsed.
 */
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return false;
    return window.matchMedia(query).matches;
  });

  useEffect(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return undefined;
    const mql = window.matchMedia(query);
    const onChange = (e: MediaQueryListEvent) => setMatches(e.matches);
    setMatches(mql.matches);
    // Safari < 14 only has the deprecated addListener form.
    if (typeof mql.addEventListener === 'function') {
      mql.addEventListener('change', onChange);
      return () => mql.removeEventListener('change', onChange);
    }
    mql.addListener(onChange);
    return () => mql.removeListener(onChange);
  }, [query]);

  return matches;
}

/**
 * Keep a scroll container pinned to its bottom as content arrives — but only
 * while the user is already there.
 *
 * Replaces `logEnd.current?.scrollIntoView({ behavior: 'smooth' })`, which was
 * wrong in three separate ways on a live stream:
 *
 *   1. `scrollIntoView` scrolls EVERY ancestor scroll container, so each event
 *      also yanked the page's <main> element — the visible "page jumping".
 *   2. `behavior: 'smooth'` restarts its animation on every call. Events arrive
 *      faster than the animation finishes, so the scroll position never settled
 *      and the log appeared to shiver continuously.
 *   3. It fired unconditionally, so scrolling up to read something older was
 *      undone by the next event.
 *
 * Writing `scrollTop` in a layout effect is instant, touches only this element,
 * and lands before paint — so there is no frame showing the pre-scroll offset.
 */
export function useStickToBottom<T extends HTMLElement>(
  dep: unknown,
  enabled = true,
  thresholdPx = 72,
): RefObject<T> {
  // `useRef<T>(null)`, not `useRef<T | null>(null)`: only the former resolves to
  // the RefObject<T> overload that a JSX `ref=` attribute accepts.
  const ref = useRef<T>(null);
  const stuckRef = useRef(true);

  useEffect(() => {
    const el = ref.current;
    if (!el) return undefined;
    const onScroll = () => {
      const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
      stuckRef.current = distanceFromBottom <= thresholdPx;
    };
    el.addEventListener('scroll', onScroll, { passive: true });
    return () => el.removeEventListener('scroll', onScroll);
  }, [thresholdPx]);

  // Layout effect, not effect: this must land in the same frame the new content
  // paints, or the user sees one frame of the old offset — a visible flicker.
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || !enabled || !stuckRef.current) return;
    el.scrollTop = el.scrollHeight;
  }, [dep, enabled]);

  return ref;
}
