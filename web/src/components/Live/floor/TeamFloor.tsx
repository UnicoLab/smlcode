import { Suspense, lazy, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { Box, LayoutTemplate } from 'lucide-react';
import clsx from 'clsx';
import { AppContext } from '@/App';
import { usePersistentState } from '@/hooks/useUiState';
import type { RunEvent } from '@/types';
import TeamFloorFlat from './TeamFloorFlat';
import FloorDossier from './FloorDossier';
import PulseFeed from './PulseFeed';
import { diffFloors, freshPulses, type FloorModel, type FloorPulse } from './floorModel';
import type { FloorSelection } from './floorShared';

// ── The team floor: 3D when the machine can, flat when it cannot ────────
//
// The scene is WebGL, and WebGL is not a given: a remote desktop, a locked
// down browser, a test runner. The flat SVG stage draws the same model, so
// the page always has a floor — the 3D one is loaded lazily on top of it,
// only where it can run, and the user can pin either.
//
// Whichever stage draws, this wrapper owns what is interactive about it:
// the selection (a clicked person or ticket, opened in the dossier), and
// the pulses (what changed since the last floor, flashed on the stage and
// listed in the feed).

export interface TeamFloorProps {
  floor: FloorModel;
  running: boolean;
  /** The run's log, for the dossier's trail. */
  events?: RunEvent[];
  onTicket?: (id: string) => void;
  /** Current time, injected for tests. */
  now?: number;
}

const TeamFloor3D = lazy(() => import('./TeamFloor3D'));

const NO_EVENTS: RunEvent[] = [];
const PULSE_KEEP = 40;

function webGLAvailable(): boolean {
  if (typeof window === 'undefined') return false;
  // jsdom defines neither constructor; asking it for a context logs an error.
  if (typeof (window as unknown as { WebGL2RenderingContext?: unknown }).WebGL2RenderingContext === 'undefined' &&
      typeof (window as unknown as { WebGLRenderingContext?: unknown }).WebGLRenderingContext === 'undefined') {
    return false;
  }
  try {
    const canvas = document.createElement('canvas');
    return !!(canvas.getContext('webgl2') || canvas.getContext('webgl'));
  } catch {
    return false;
  }
}

function useReducedMotion(): boolean {
  const [reduced, setReduced] = useState(() =>
    typeof window !== 'undefined' && typeof window.matchMedia === 'function'
      ? window.matchMedia('(prefers-reduced-motion: reduce)').matches
      : false,
  );
  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return undefined;
    const mq = window.matchMedia('(prefers-reduced-motion: reduce)');
    const onChange = () => setReduced(mq.matches);
    mq.addEventListener?.('change', onChange);
    return () => mq.removeEventListener?.('change', onChange);
  }, []);
  return reduced;
}

/** A one-second clock while the floor is alive, frozen to `fixed` in tests. */
function useClock(alive: boolean, fixed?: number): number {
  const [tick, setTick] = useState(() => fixed ?? Date.now());
  useEffect(() => {
    if (fixed !== undefined) {
      setTick(fixed);
      return undefined;
    }
    setTick(Date.now());
    if (!alive) return undefined;
    const id = window.setInterval(() => setTick(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [alive, fixed]);
  return tick;
}

/**
 * usePulses diffs each new floor against the last and keeps the changes for
 * PULSE_TTL_MS. Diffing happens in the effect, not in render, so a floor that
 * re-renders without changing produces nothing.
 */
function usePulses(floor: FloorModel, now: number, fixed?: number): FloorPulse[] {
  const prev = useRef<FloorModel | null>(null);
  const [pulses, setPulses] = useState<FloorPulse[]>([]);
  useEffect(() => {
    const at = fixed ?? Date.now();
    const found = diffFloors(prev.current, floor, at);
    prev.current = floor;
    if (found.length > 0) setPulses((list) => freshPulses([...list, ...found], at).slice(-PULSE_KEEP));
  }, [floor, fixed]);
  // Pruning: driven by the shared clock so the feed's "12s" labels and its
  // fading stay in step.
  useEffect(() => {
    setPulses((list) => {
      const kept = freshPulses(list, now);
      return kept.length === list.length ? list : kept;
    });
  }, [now]);
  return pulses;
}

export default function TeamFloor({ floor, running, events = NO_EVENTS, onTicket, now: fixedNow }: TeamFloorProps) {
  const ctx = useContext(AppContext);
  const dark = ctx?.dark ?? false;
  const reducedMotion = useReducedMotion();
  const canGL = useMemo(() => webGLAvailable(), []);
  // The user's pick outlives the session: someone on a weak GPU who chose the
  // flat floor once should not have to choose it on every visit.
  const [prefer, setPrefer] = usePersistentState<'3d' | 'flat'>('live.floor.mode', '3d');
  const use3D = canGL && prefer === '3d';

  const [selection, setSelection] = useState<FloorSelection>(null);
  const onSelect = useCallback((sel: FloorSelection) => setSelection(sel), []);
  const now = useClock(running || selection !== null, fixedNow);
  const pulses = usePulses(floor, now, fixedNow);

  if (floor.mode === 'idle') {
    return <TeamFloorFlat floor={floor} running={running} onTicket={onTicket} />;
  }

  const stageProps = { floor, running, selection, onSelect, pulses, onTicket };

  // The stage gets the width minus a fixed column at the right for the
  // 3D/map toggle and the feed: what slides in there never covers a label in
  // the scene, whatever the camera is doing.
  return (
    <div className="relative flex h-full w-full" data-testid="team-floor-shell">
      <div className="relative min-w-0 flex-1">
        {use3D ? (
          <Suspense fallback={<TeamFloorFlat {...stageProps} now={fixedNow} />}>
            <TeamFloor3D {...stageProps} dark={dark} reducedMotion={reducedMotion} />
          </Suspense>
        ) : (
          <TeamFloorFlat {...stageProps} now={fixedNow} />
        )}
        <FloorDossier floor={floor} events={events} selection={selection} running={running} now={now} onSelect={onSelect} onTicket={onTicket} />
      </div>
      <div className="relative hidden w-52 shrink-0 flex-col gap-2 border-l border-gray-200/70 bg-gray-50/60 p-2 dark:border-gray-800 dark:bg-gray-950/40 sm:flex" data-testid="floor-side">
        {canGL && (
          <div className="flex self-end overflow-hidden rounded-md border border-gray-200/80 bg-white/80 text-[10px] backdrop-blur dark:border-gray-700/80 dark:bg-gray-900/80">
          <button
            type="button"
            onClick={() => setPrefer('3d')}
            aria-pressed={prefer === '3d'}
            title="Walk the floor in 3D — drag to orbit, wheel to zoom"
            className={clsx('focus-ring inline-flex items-center gap-1 px-2 py-1', prefer === '3d' ? 'bg-brand-500 text-white' : 'text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800')}
          >
            <Box size={11} aria-hidden="true" /> 3D
          </button>
          <button
            type="button"
            onClick={() => setPrefer('flat')}
            aria-pressed={prefer === 'flat'}
            title="The flat map of the same floor — lighter on the GPU"
            className={clsx('focus-ring inline-flex items-center gap-1 px-2 py-1', prefer === 'flat' ? 'bg-brand-500 text-white' : 'text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800')}
          >
            <LayoutTemplate size={11} aria-hidden="true" /> map
          </button>
        </div>
        )}
        <PulseFeed pulses={pulses} now={now} onSelect={onSelect} running={running} />
      </div>
    </div>
  );
}
