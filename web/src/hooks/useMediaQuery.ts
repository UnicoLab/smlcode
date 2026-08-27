import { useEffect, useState } from 'react';

/**
 * Subscribe to a CSS media query from React.
 *
 * Tailwind handles almost all of this app's responsiveness in CSS, which is the
 * right default — a breakpoint that only exists in a stylesheet cannot
 * disagree with the layout it describes. This hook is for the cases CSS cannot
 * express: when a breakpoint has to change *behaviour* rather than appearance.
 *
 * There are two, and both are about panels that are a column on a wide screen
 * and a full-height overlay on a narrow one. An overlay that defaults to open
 * covers the page the user came for, so the default itself has to know the
 * viewport — and "open" is component state, not a class name.
 */
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return false;
    return window.matchMedia(query).matches;
  });

  useEffect(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return;
    const mql = window.matchMedia(query);
    const onChange = (e: MediaQueryListEvent) => setMatches(e.matches);
    // Re-read on subscribe: the query can have changed between the initial
    // state and this effect, and on a resize that straddles the breakpoint the
    // first change event is easy to miss.
    setMatches(mql.matches);
    mql.addEventListener('change', onChange);
    return () => mql.removeEventListener('change', onChange);
  }, [query]);

  return matches;
}

/** Tailwind's `lg` breakpoint — where side panels become columns. */
export const LG_QUERY = '(min-width: 1024px)';

/** True at Tailwind's `lg` breakpoint and above. */
export function useIsDesktop(): boolean {
  return useMediaQuery(LG_QUERY);
}
