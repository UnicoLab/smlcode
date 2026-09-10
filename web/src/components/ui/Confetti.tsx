import { useEffect, useMemo } from 'react';

// ── A short burst for a green run ──
//
// Pure DOM and CSS: forty coloured rectangles falling for about two seconds,
// then gone. It renders nothing when the user asked for reduced motion, and it
// is `aria-hidden` and pointer-transparent so it cannot get in the way of the
// result the user is about to read.

const COLORS = ['#10b981', '#3b82f6', '#f59e0b', '#ec4899', '#8b5cf6', '#14b8a6'];
const PIECES = 40;
export const CONFETTI_MS = 2000;

interface ConfettiProps {
  /** Bumps to trigger a new burst; 0 renders nothing. */
  burst: number;
  onDone?: () => void;
}

export function prefersReducedMotion(): boolean {
  try {
    return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  } catch {
    return false;
  }
}

export default function Confetti({ burst, onDone }: ConfettiProps) {
  const reduced = prefersReducedMotion();
  const pieces = useMemo(
    () =>
      Array.from({ length: PIECES }, (_, i) => ({
        left: `${(i * 37 + burst * 11) % 100}%`,
        delay: `${((i * 53) % 40) * 10}ms`,
        duration: `${1400 + ((i * 29) % 6) * 100}ms`,
        color: COLORS[i % COLORS.length],
        rotate: `${(i * 97) % 360}deg`,
        size: 6 + ((i * 13) % 5),
      })),
    [burst],
  );

  useEffect(() => {
    if (!burst || reduced) return undefined;
    const t = window.setTimeout(() => onDone?.(), CONFETTI_MS);
    return () => window.clearTimeout(t);
  }, [burst, reduced, onDone]);

  if (!burst || reduced) return null;
  return (
    <div
      aria-hidden="true"
      data-testid="confetti"
      className="pointer-events-none fixed inset-0 z-[55] overflow-hidden"
    >
      {pieces.map((p, i) => (
        <span
          key={`${burst}-${i}`}
          className="confetti-piece absolute -top-3 block rounded-sm"
          style={{
            left: p.left,
            width: p.size,
            height: p.size * 1.6,
            background: p.color,
            animationDelay: p.delay,
            animationDuration: p.duration,
            transform: `rotate(${p.rotate})`,
          }}
        />
      ))}
    </div>
  );
}
