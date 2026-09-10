import { Suspense, lazy, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { Box, ChevronDown, ChevronUp, LayoutTemplate } from 'lucide-react';
import clsx from 'clsx';
import { AppContext } from '@/App';
import { usePersistentState } from '@/hooks/useUiState';
import { useToast } from '@/components/ui/Toast';
import type { RunEvent } from '@/types';
import TeamFloorFlat from './TeamFloorFlat';
import FloorDossier from './FloorDossier';
import PulseFeed from './PulseFeed';
import { diffFloors, freshPulses, type FloorModel, type FloorPulse } from './floorModel';
import { floorStore } from './floorStore';
import type { FloorSelection } from './floorShared';

// ── The team floor: 3D when the machine can, flat when it cannot ────────
//
// The scene is WebGL, and WebGL is not a given: a remote desktop, a locked
// down browser, a test runner. The flat SVG stage draws the same model, so
// the page always has a floor — the 3D one is loaded lazily on top of it,
// only where it can run, and the user can pin either.
//
// Whichever stage draws, this wrapper owns what is interactive about it:
// the selection (a clicked person or ticket, opened in the dossier — held by
// the page in the URL when it passes one, else here), and the pulses (what
// changed since the last floor, flashed on the stage and listed in the feed;
// kept in a module-level store so leaving and returning does not blank them).

export interface TeamFloorProps {
  floor: FloorModel;
  running: boolean;
  /** The run's log, for the dossier's trail. */
  events?: RunEvent[];
  onTicket?: (id: string) => void;
  /** Current time, injected for tests. */
  now?: number;
  /**
   * Controlled selection. LiveView keeps it in the URL (?task / ?agent) so a
   * dossier survives navigation and reload; left undefined, the floor owns it.
   */
  selection?: FloorSelection;
  onSelect?: (sel: FloorSelection) => void;
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

/** A touch-first device: the flat map is the better default there. */
function coarsePointer(): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false;
  try {
    return window.matchMedia('(pointer: coarse)').matches;
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
 * re-renders without changing produces nothing. Both the list and the last
 * floor live in the module-level store: coming back from another page picks
 * up where the feed left off, and the first floor after a return is diffed
 * against the last one seen, so what happened in between still pulses.
 */
function usePulses(floor: FloorModel, now: number, fixed?: number): FloorPulse[] {
  const [pulses, setPulses] = useState<FloorPulse[]>(() => freshPulses(floorStore.pulses, fixed ?? Date.now()));
  useEffect(() => {
    const at = fixed ?? Date.now();
    const found = diffFloors(floorStore.prevFloor, floor, at);
    floorStore.prevFloor = floor;
    if (found.length > 0) {
      const next = freshPulses([...floorStore.pulses, ...found], at).slice(-PULSE_KEEP);
      floorStore.pulses = next;
      setPulses(next);
    }
  }, [floor, fixed]);
  // Pruning: driven by the shared clock so the feed's "12s" labels and its
  // fading stay in step.
  useEffect(() => {
    setPulses((list) => {
      const kept = freshPulses(list, now);
      if (kept.length === list.length) return list;
      floorStore.pulses = kept;
      return kept;
    });
  }, [now]);
  return pulses;
}

export default function TeamFloor({ floor, running, events = NO_EVENTS, onTicket, now: fixedNow, selection: selectionProp, onSelect: onSelectProp }: TeamFloorProps) {
  const ctx = useContext(AppContext);
  const toast = useToast();
  const dark = ctx?.dark ?? false;
  const reducedMotion = useReducedMotion();
  const canGL = useMemo(() => webGLAvailable(), []);
  // The user's pick outlives the session: someone on a weak GPU who chose the
  // flat floor once should not have to choose it on every visit. With no pick
  // stored, a touch device starts on the map — orbiting a scene with a thumb
  // on a phone GPU is not the first thing to offer.
  const [prefer, setPrefer] = usePersistentState<'3d' | 'flat'>('live.floor.mode', coarsePointer() ? 'flat' : '3d');
  // A lost WebGL context (a GPU reset, a tab evicted from VRAM) drops the
  // floor to the map for the rest of this visit rather than leaving a blank
  // canvas. Choosing 3D again is a retry.
  const [glLost, setGlLost] = useState(false);
  const use3D = canGL && prefer === '3d' && !glLost;
  const onContextLost = useCallback(() => {
    setGlLost(true);
    toast.info('The 3D floor lost its graphics context', 'Showing the flat map instead. Pick 3D again to retry.');
  }, [toast]);
  const pick = useCallback(
    (mode: '3d' | 'flat') => {
      setPrefer(mode);
      if (mode === '3d') setGlLost(false);
    },
    [setPrefer],
  );

  // Selection: the page's when it passes one (kept in the URL), else ours.
  const [ownSelection, setOwnSelection] = useState<FloorSelection>(null);
  const controlled = selectionProp !== undefined;
  const selection = controlled ? selectionProp : ownSelection;
  const onSelect = useCallback(
    (sel: FloorSelection) => {
      if (onSelectProp) onSelectProp(sel);
      if (!controlled) setOwnSelection(sel);
    },
    [controlled, onSelectProp],
  );
  // The clock runs while there is something to time: a run, an open dossier,
  // or pulses still fading — so they drain after a run ends instead of
  // staying "fresh" until something else happens to tick.
  const pulsesAlive = useRef(floorStore.pulses.length > 0);
  const now = useClock(running || selection !== null || pulsesAlive.current, fixedNow);
  const pulses = usePulses(floor, now, fixedNow);
  pulsesAlive.current = pulses.length > 0;
  // The feed as a sheet at the bottom on a phone, where the side column has
  // no room. Closed by default: the floor is what a phone screen is for.
  const [sheetOpen, setSheetOpen] = useState(false);
  const freshCount = useMemo(() => freshPulses(pulses, now).length, [pulses, now]);

  if (floor.mode === 'idle') {
    return <TeamFloorFlat floor={floor} running={running} onTicket={onTicket} />;
  }

  const stageProps = { floor, running, selection, onSelect, pulses, onTicket };

  const toggle = canGL && (
    <div className="flex overflow-hidden rounded-md border border-gray-200/80 bg-white/80 text-[10px] backdrop-blur dark:border-gray-700/80 dark:bg-gray-900/80" role="group" aria-label="Floor view">
      <button
        type="button"
        onClick={() => pick('3d')}
        aria-pressed={use3D}
        title="Walk the floor in 3D — drag to orbit, wheel to zoom"
        className={clsx('focus-ring inline-flex items-center gap-1 px-2 py-1', use3D ? 'bg-brand-500 text-white' : 'text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800')}
      >
        <Box size={11} aria-hidden="true" /> 3D
      </button>
      <button
        type="button"
        onClick={() => pick('flat')}
        aria-pressed={!use3D}
        title="The flat map of the same floor — lighter on the GPU"
        className={clsx('focus-ring inline-flex items-center gap-1 px-2 py-1', !use3D ? 'bg-brand-500 text-white' : 'text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800')}
      >
        <LayoutTemplate size={11} aria-hidden="true" /> map
      </button>
    </div>
  );

  // The stage gets the width minus a fixed column at the right for the
  // 3D/map toggle and the feed: what slides in there never covers a label in
  // the scene, whatever the camera is doing. Below `sm` that column is gone;
  // the toggle moves into a strip along the bottom and the feed becomes a
  // sheet that slides up from it.
  return (
    <div className="relative flex h-full w-full" data-testid="team-floor-shell">
      <div className="relative flex min-w-0 flex-1 flex-col">
        <div className="relative min-h-0 flex-1">
          {use3D ? (
            <Suspense fallback={<TeamFloorFlat {...stageProps} now={fixedNow} />}>
              <TeamFloor3D {...stageProps} dark={dark} reducedMotion={reducedMotion} onContextLost={onContextLost} />
            </Suspense>
          ) : (
            <TeamFloorFlat {...stageProps} now={fixedNow} />
          )}
          <FloorDossier floor={floor} events={events} selection={selection} running={running} now={now} onSelect={onSelect} onTicket={onTicket} />
        </div>
        {/* Phone: the controls strip, and the feed as a sheet above it. */}
        <div className="relative shrink-0 border-t border-gray-200/70 bg-gray-50/80 dark:border-gray-800 dark:bg-gray-950/60 sm:hidden" data-testid="floor-mobile-strip">
          {sheetOpen && (
            <div id="floor-sheet" className="max-h-[40vh] overflow-y-auto border-b border-gray-200/70 p-2 dark:border-gray-800" data-testid="floor-sheet">
              <PulseFeed pulses={pulses} now={now} onSelect={(sel) => { onSelect(sel); setSheetOpen(false); }} running={running} />
            </div>
          )}
          <div className="flex items-center justify-between gap-2 px-2 py-1.5">
            {toggle || <span />}
            <button
              type="button"
              onClick={() => setSheetOpen((v) => !v)}
              aria-expanded={sheetOpen}
              aria-controls="floor-sheet"
              className="focus-ring inline-flex items-center gap-1 rounded-md border border-gray-200/80 bg-white/80 px-2 py-1 text-[10px] font-semibold text-gray-600 dark:border-gray-700/80 dark:bg-gray-900/80 dark:text-gray-300"
            >
              {sheetOpen ? <ChevronDown size={11} aria-hidden="true" /> : <ChevronUp size={11} aria-hidden="true" />}
              feed
              {freshCount > 0 && <span className="rounded-full bg-brand-500 px-1.5 text-[9px] text-white">{freshCount}</span>}
            </button>
          </div>
        </div>
      </div>
      <div className="relative hidden w-52 shrink-0 flex-col gap-2 border-l border-gray-200/70 bg-gray-50/60 p-2 dark:border-gray-800 dark:bg-gray-950/40 sm:flex" data-testid="floor-side">
        {toggle && <div className="flex self-end">{toggle}</div>}
        <PulseFeed pulses={pulses} now={now} onSelect={onSelect} running={running} />
      </div>
    </div>
  );
}
