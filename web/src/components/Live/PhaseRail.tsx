import { useEffect, useMemo, useRef } from 'react';
import { Check } from 'lucide-react';
import clsx from 'clsx';

/**
 * The pipeline as ONE journey.
 *
 * A single track from the first phase to the last, drawn as a path the run
 * walks: phases are stops, the five groups are the colored stretches between
 * them, the stretch already walked is lit and the stop the run is at pulses.
 * Position along the track IS the progress, so the two can never disagree.
 *
 * Color carries the GROUP (five of them, and groups are a real concept in
 * pipeline.yaml) and state is carried by fill, weight and motion — nobody can
 * hold fifteen hues to fifteen phase names.
 */

export type PhaseState = 'pending' | 'active' | 'completed';

export interface RailGroup {
  id: string;
  label: string;
  phases: string[];
}

/** Group accents. Five, matching the pipeline's own grouping. */
const GROUP_TONE: Record<string, { hex: string; text: string; soft: string }> = {
  prepare: { hex: '#0ea5e9', text: 'text-sky-600 dark:text-sky-400', soft: 'bg-sky-500/10' },
  design: { hex: '#8b5cf6', text: 'text-violet-600 dark:text-violet-400', soft: 'bg-violet-500/10' },
  build: { hex: '#f59e0b', text: 'text-amber-600 dark:text-amber-400', soft: 'bg-amber-500/10' },
  verify: { hex: '#10b981', text: 'text-emerald-600 dark:text-emerald-400', soft: 'bg-emerald-500/10' },
  finish: { hex: '#64748b', text: 'text-slate-500 dark:text-slate-400', soft: 'bg-slate-500/10' },
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

  // Keep the active phase in view as the run walks the track.
  useEffect(() => {
    activeRef.current?.scrollIntoView({ behavior: 'smooth', block: 'nearest', inline: 'center' });
  }, [activePhase]);

  const shown = useMemo(() => groups.filter((g) => g.phases.length > 0), [groups]);
  const all = useMemo(() => shown.flatMap((g) => g.phases), [shown]);
  const total = all.length;
  const done = Object.values(phaseState).filter((s) => s === 'completed').length;
  // How far along the track the run is: the active stop, else the last
  // completed one, else the start.
  const activeIndex = activePhase ? all.indexOf(activePhase) : -1;
  const reached = activeIndex >= 0 ? activeIndex : Math.max(-1, done - 1);
  const progress = total <= 1 ? (reached >= 0 ? 100 : 0) : Math.max(0, Math.min(100, (reached / (total - 1)) * 100));

  if (total === 0) return null;

  return (
    <div className="shrink-0 border-b border-gray-200 bg-white/90 dark:border-gray-800 dark:bg-gray-950/90">
      <div className="flex items-center gap-3 px-3 py-2 sm:px-4">
        <div className="relative min-w-0 flex-1 overflow-x-auto pb-1 [mask-image:linear-gradient(to_right,black_calc(100%-1.5rem),transparent)]">
          <div className="relative flex min-w-max items-stretch" role="list" aria-label="Pipeline phases">
            {/* The track: a base line and the lit portion the run has walked. */}
            <div className="pointer-events-none absolute left-4 right-4 top-[15px] h-1 rounded-full bg-gray-200 dark:bg-gray-800" aria-hidden="true" />
            <div
              className="journey-fill pointer-events-none absolute left-4 top-[15px] h-1 rounded-full bg-gradient-to-r from-sky-500 via-violet-500 to-emerald-500"
              style={{ width: `calc((100% - 2rem) * ${progress / 100})` }}
              aria-hidden="true"
            />
            {shown.map((group) => {
              const tone = toneFor(group.id);
              return (
                <div key={group.id} className="relative flex shrink-0 flex-col">
                  <div className="flex items-start">
                    {group.phases.map((phase) => {
                      const state = phaseState[phase] ?? 'pending';
                      const isActive = state === 'active';
                      return (
                        <div
                          key={phase}
                          ref={isActive ? activeRef : undefined}
                          role="listitem"
                          title={`${group.label} · ${phase} · ${state}`}
                          className="relative flex w-[4.6rem] shrink-0 flex-col items-center px-1"
                        >
                          <span className="relative flex h-8 w-8 items-center justify-center">
                            {isActive && running && (
                              <span className="journey-active-ring absolute inset-0 rounded-full" style={{ background: tone.hex, opacity: 0.35 }} aria-hidden="true" />
                            )}
                            <span
                              className={clsx(
                                'relative flex items-center justify-center rounded-full border-2 transition-all',
                                isActive ? 'h-7 w-7 shadow-md' : 'h-5 w-5',
                                state === 'pending' && 'border-dashed border-gray-300 bg-white dark:border-gray-700 dark:bg-gray-950',
                              )}
                              style={
                                state === 'pending'
                                  ? undefined
                                  : { borderColor: tone.hex, background: state === 'completed' ? tone.hex : '#fff' }
                              }
                            >
                              {state === 'completed' && <Check size={11} className="text-white" aria-hidden="true" strokeWidth={3} />}
                              {isActive && <span className={clsx('h-2.5 w-2.5 rounded-full', running && 'animate-pulse')} style={{ background: tone.hex }} aria-hidden="true" />}
                            </span>
                          </span>
                          <span
                            className={clsx(
                              'mt-1 max-w-full truncate text-[10.5px] leading-tight',
                              isActive ? 'font-bold text-gray-900 dark:text-gray-50' : state === 'completed' ? 'font-medium text-gray-600 dark:text-gray-300' : 'text-gray-400 dark:text-gray-600',
                            )}
                          >
                            {phase}
                          </span>
                        </div>
                      );
                    })}
                  </div>
                  <span className={clsx('mt-0.5 self-center rounded px-1.5 text-[9px] font-bold uppercase tracking-[0.14em]', tone.text, tone.soft)}>
                    {group.label}
                  </span>
                </div>
              );
            })}
          </div>
        </div>

        <div className="flex shrink-0 flex-col items-end gap-0.5 border-l border-gray-200 pl-3 dark:border-gray-800">
          <span className="font-mono text-[11px] font-semibold tabular-nums text-gray-600 dark:text-gray-300">
            {done}/{total}
          </span>
          <span className="text-[9px] uppercase tracking-wider text-gray-400">
            {activePhase && running ? `now: ${activePhase}` : done === total && total > 0 ? 'complete' : 'phases'}
          </span>
        </div>
      </div>
    </div>
  );
}
