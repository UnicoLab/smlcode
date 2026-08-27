import { useEffect, useRef } from 'react';
import clsx from 'clsx';

/**
 * The pipeline as ONE horizontal track.
 *
 * It replaces a stack of four separate header panels — a metrics grid, a
 * percentage bar, a wrapped grid of phase chips, and a "current stage" card —
 * that between them consumed roughly a third of the viewport to answer a single
 * question: where in the run are we? On a laptop that pushed the live event log,
 * which is the entire point of this page, below the fold.
 *
 * One track answers it in ~44px, and answers it better: position along the rail
 * IS the progress bar, so the two can never disagree.
 *
 * Colour carries meaning and nothing else. Phases used to be tinted by a
 * fifteen-entry per-phase palette, which reads as decoration because it is —
 * nobody can hold fifteen hues to fifteen phase names. Here the tint is the
 * phase's GROUP (five of them, and groups are a real concept in pipeline.yaml),
 * and STATE is carried by fill and weight rather than by hue.
 */

export type PhaseState = 'pending' | 'active' | 'completed';

export interface RailGroup {
  id: string;
  label: string;
  phases: string[];
}

/** Group accents. Five, matching the pipeline's own grouping. */
const GROUP_TONE: Record<string, { dot: string; text: string }> = {
  prepare: { dot: 'bg-sky-500', text: 'text-sky-600 dark:text-sky-400' },
  design: { dot: 'bg-violet-500', text: 'text-violet-600 dark:text-violet-400' },
  build: { dot: 'bg-amber-500', text: 'text-amber-600 dark:text-amber-400' },
  verify: { dot: 'bg-emerald-500', text: 'text-emerald-600 dark:text-emerald-400' },
  finish: { dot: 'bg-slate-400', text: 'text-slate-500 dark:text-slate-400' },
};

function toneFor(groupID: string) {
  return GROUP_TONE[groupID] ?? GROUP_TONE.finish;
}

export default function PhaseRail({
  groups,
  phaseState,
  activePhase,
  running,
}: {
  groups: RailGroup[];
  phaseState: Record<string, PhaseState>;
  activePhase: string | null;
  running: boolean;
}) {
  const activeRef = useRef<HTMLDivElement>(null);

  // Keep the active phase in view as the run walks the rail. Without this the
  // rail is only useful until the run scrolls past the fold of its own track.
  useEffect(() => {
    activeRef.current?.scrollIntoView({ behavior: 'smooth', block: 'nearest', inline: 'center' });
  }, [activePhase]);

  const total = groups.reduce((n, g) => n + g.phases.length, 0);
  const done = Object.values(phaseState).filter((s) => s === 'completed').length;

  if (total === 0) return null;

  return (
    <div className="shrink-0 border-b border-gray-200 bg-white dark:border-gray-800 dark:bg-gray-950">
      <div className="flex items-center gap-3 px-3 py-2 sm:px-4">
        {/* The fade is the only affordance that this track scrolls: a clipped
            chip at the right edge otherwise reads as a rendering bug rather
            than as "there is more pipeline over here". */}
        <div
          className="flex min-w-0 flex-1 items-stretch gap-1 overflow-x-auto pb-0.5 [mask-image:linear-gradient(to_right,black_calc(100%-1.5rem),transparent)]"
          role="list"
          aria-label="Pipeline phases"
        >
          {groups.map((group) => {
            // A group whose phases were all filtered out by a budget class is
            // not a group. Callers filter too, but a lone dangling group label
            // is a confusing enough artifact to guard against here as well.
            if (group.phases.length === 0) return null;
            const tone = toneFor(group.id);
            return (
              <div key={group.id} className="flex shrink-0 items-stretch gap-1">
                <span
                  className={clsx(
                    'hidden select-none self-center pl-1 pr-0.5 text-[9px] font-bold uppercase tracking-[0.14em] md:inline',
                    tone.text,
                  )}
                >
                  {group.label}
                </span>
                {group.phases.map((phase) => {
                  const state = phaseState[phase] ?? 'pending';
                  const isActive = state === 'active';
                  return (
                    <div
                      key={phase}
                      ref={isActive ? activeRef : undefined}
                      role="listitem"
                      title={`${group.label} · ${phase} · ${state}`}
                      className={clsx(
                        'flex shrink-0 items-center gap-1.5 rounded-md border px-2 py-1 text-[11px] transition-colors',
                        isActive &&
                          'border-brand-400 bg-brand-50 font-bold text-brand-700 shadow-sm dark:border-brand-500 dark:bg-brand-950/50 dark:text-brand-200',
                        state === 'completed' &&
                          'border-transparent bg-gray-100 font-medium text-gray-600 dark:bg-gray-800/70 dark:text-gray-300',
                        state === 'pending' &&
                          'border-dashed border-gray-200 bg-transparent font-medium text-gray-400 dark:border-gray-800 dark:text-gray-600',
                      )}
                    >
                      <span
                        className={clsx(
                          'h-1.5 w-1.5 shrink-0 rounded-full',
                          isActive && running && 'animate-pulse',
                          state === 'pending' ? 'bg-gray-300 dark:bg-gray-700' : tone.dot,
                        )}
                      />
                      {phase}
                    </div>
                  );
                })}
              </div>
            );
          })}
        </div>

        <div className="flex shrink-0 items-center gap-2 border-l border-gray-200 pl-3 dark:border-gray-800">
          <div className="hidden h-1.5 w-20 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-800 lg:block">
            <div
              className="h-full rounded-full bg-brand-500 transition-[width] duration-700"
              style={{ width: `${total > 0 ? Math.round((done / total) * 100) : 0}%` }}
            />
          </div>
          <span className="font-mono text-[11px] font-semibold tabular-nums text-gray-500 dark:text-gray-400">
            {done}/{total}
          </span>
        </div>
      </div>
    </div>
  );
}
