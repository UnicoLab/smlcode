import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import * as THREE from 'three';
import { Canvas, useFrame, useThree, type ThreeEvent } from '@react-three/fiber';
import { ContactShadows, Float, Html, OrbitControls, QuadraticBezierLine, RoundedBox, Sparkles } from '@react-three/drei';
import type { OrbitControls as OrbitControlsImpl } from 'three-stdlib';
import { teamColor } from '@/components/Board/teamColor';
import { STAGE_GLYPHS } from '@/components/shared/labels';
import { HARNESS_ID, PULSE_TTL_MS, workTables, type FloorAgent, type FloorHandoff, type FloorModel, type FloorPhase, type FloorPulse, type FloorStageSeat, type FloorTeam, type FloorTicket, type TicketState } from './floorModel';
import { LEGEND_STATES, TICKET_HEX, TICKET_LABEL, glyphFor, seatTitle, type FloorSelection } from './floorShared';
import { floorStore, rememberCamera, rememberCameraPrefs } from './floorStore';
import { MOOD_GLYPH, MOOD_LABEL, floorShipped, isFootballRound, isParty, moodFor, moodShows, seedOf, tableParties, type LifeInputs, type Mood } from './floorLife';
import { lookOf, makeWalk, poseRig, stillPose, turnToward, useRig, walkAt, type Walk } from './floorRig';
import RigBody from './RigBody';

// ── The team floor, in three dimensions ──────────────────────────────────
//
// Each team sits at its own round table. The manager has the head seat with
// the team's board on the wall behind them; the members sit around it, each
// with a monitor whose screen lights up when the log says that agent is
// working — with a pop-out over their head saying which ticket. The tickets
// lie on the table, colored by state, and a thread runs from each one to the
// monitor of whoever holds it. The frozen contract runs between tables as
// glowing conduits with packets travelling provider → consumer, turning
// amber and slowing when the consumer waits on a clause it has not been
// given. A manager dispatching someone onto a ticket is an arc from the head
// seat; a reassignment is a spark across the table; a ticket finishing or
// failing bursts. Gates paint the table's rim green or red.
//
// The harness — the dispatcher and the phase agents who plan, split and
// compose between the tables' typing — lives in the command center: a glass
// room with a mission screen on its back wall. The crew waits inside; whoever
// the run hands the microphone to walks out through the door onto the pad in
// front, says their piece, and walks back in. A room of ten figures stays a
// room, not a crowd of name tags.
//
// Nobody is a statue. Everyone who is not working has a mood (floorLife):
// they watch the worker, sip a coffee, stretch, take a walk round the table,
// and doze off at the desk when nothing comes their way. A table whose every
// ticket is done stands up for a beer, a kick-about and a dance; when the
// whole run ships, the whole floor — command center included — joins in.
//
// Everything is clickable: a person or a ticket opens its dossier (owned by
// the wrapper), a table focuses the camera, empty floor clears. The camera is
// the user's: drag to orbit, wheel to zoom, right-drag to pan, "follow" keeps
// whoever is working in the middle. Everything animates on the GPU per frame
// while there is something to animate — a run, live tickets, fresh pulses,
// the camera gliding, follow or spin. After a run the floor keeps its life at
// an ambient 30 fps for a few minutes (the party, the naps) and then settles
// to on-demand; a hidden tab renders nothing. Under prefers-reduced-motion
// nothing moves: everyone holds a still pose of their mood.
//
// Per-frame work allocates nothing: the lerps, colours and curve samples go
// through scratch objects made once, and the wall clock is read once per
// frame (FrameClock) rather than once per animated thing.
//
// All data comes from floorModel; this file only draws it.

export interface TeamFloor3DProps {
  floor: FloorModel;
  running: boolean;
  dark: boolean;
  reducedMotion: boolean;
  selection: FloorSelection;
  onSelect: (sel: FloorSelection) => void;
  pulses: FloorPulse[];
  onTicket?: (id: string) => void;
  /** The WebGL context went away; the wrapper falls back to the flat map. */
  onContextLost?: () => void;
}

const HEX: Record<string, string> = {
  teal: '#14b8a6',
  fuchsia: '#d946ef',
  cyan: '#06b6d4',
  rose: '#f43f5e',
  lime: '#84cc16',
  purple: '#a855f7',
  gray: '#94a3b8',
};

const TONE_HEX: Record<FloorPulse['tone'], string> = {
  info: '#38bdf8',
  good: '#10b981',
  warn: '#f59e0b',
  bad: '#ef4444',
  brand: '#8b5cf6',
};

const TABLE_R = 2.1;
const TABLE_Y = 0.95;
const SEAT_R = TABLE_R + 0.95;
/** Table centre to table centre; the rugs (SEAT_R + 1) never touch. */
const TABLE_GAP = 10.4;
const SELECT = '#7c3aed';
/** The wall board: its size, how far behind the head seat it stands, how high its bottom edge is. */
const BOARD_W = 3.4;
const BOARD_H = 1.9;
const BOARD_BACK = SEAT_R + 2.4;
const BOARD_BOTTOM = 1.9;
/** The command center: the harness's glass room. */
const HQ_HEX = '#8b5cf6';
const HQ_WALL_H = 2.7;
const HQ_GLASS_H = 1.05;
const HQ_DOOR_W = 0.95;
const HQ_DOOR_H = 2.05;
/** How long the floor keeps living (ambient frames) after it last changed. */
const AMBIENT_MS = 4 * 60_000;
/** When this page first saw the floor: the idle clock for anyone the log has not heard from. */
const FLOOR_BORN = Date.now();
const BURST_S = 1.7;
const DISPATCH_S = 5;
const FLASH_S = 0.9;

interface Seat {
  pos: THREE.Vector3;
  angle: number;
  /** Where this seat's monitor is, in table space — the end of a ticket thread. */
  screen: THREE.Vector3;
}

interface Placed {
  team: FloorTeam;
  pos: THREE.Vector3;
  hex: string;
  seats: Map<string, Seat>;
  /** Ticket card centres in table space, for threads and bursts. */
  slots: Map<string, THREE.Vector3>;
  /** The command center's floor plan, for the harness. */
  hq?: HQ;
}

/** The command center's floor plan, in its own space (the room's centre at the origin). */
interface HQ {
  w: number;
  d: number;
  /** Where each of the crew waits inside, on a stool. */
  spots: Map<string, THREE.Vector3>;
  /** Where whoever is on stands, on the pad outside the door. */
  pads: Map<string, THREE.Vector3>;
  doorIn: THREE.Vector3;
  doorOut: THREE.Vector3;
  padAt: THREE.Vector3;
}

/**
 * commandLayout plans the room: stools in rows facing the front glass, the
 * console along the back wall, the door in the middle of the front, and the
 * pad outside it — one place on the pad per agent who is on right now.
 */
function commandLayout(agents: FloorAgent[], running: boolean): HQ {
  const n = Math.max(agents.length, 1);
  const cols = Math.max(3, Math.min(5, Math.ceil(Math.sqrt(n * 2))));
  const rows = Math.ceil(n / cols);
  const cellW = 0.95;
  const cellD = 1.0;
  const w = Math.max(4.8, cols * cellW + 1.5);
  const d = Math.max(3.2, rows * cellD + 2.0);
  const spots = new Map<string, THREE.Vector3>();
  agents.forEach((a, i) => {
    const row = Math.floor(i / cols);
    const inRow = Math.min(cols, agents.length - row * cols);
    const col = i % cols;
    // Stagger alternate rows, so the back row is seen between the heads of the front.
    const x = -((inRow - 1) * cellW) / 2 + col * cellW + (row % 2 ? cellW * 0.25 : 0);
    spots.set(a.id, new THREE.Vector3(x, 0, -d / 2 + 1.45 + row * cellD));
  });
  const on = running ? agents.filter((a) => a.active) : [];
  const padAt = new THREE.Vector3(0, 0, d / 2 + 1.75);
  const pads = new Map<string, THREE.Vector3>();
  on.forEach((a, k) => pads.set(a.id, padAt.clone().setX((k - (on.length - 1) / 2) * 1.1)));
  return { w, d, spots, pads, doorIn: new THREE.Vector3(0, 0, d / 2 - 0.5), doorOut: new THREE.Vector3(0, 0, d / 2 + 0.55), padAt };
}

const ORDER: TicketState[] = ['working', 'review', 'blocked', 'failed', 'queued', 'done'];
const CARD_W = 0.62;
const CARD_D = 0.42;
const CARD_GAP = 0.14;
const CARDS_PER_ROW = 4;
const CARDS_MAX = 12;

/** Where each ticket card lies on a table, in state order, four to a row. */
function ticketSlots(team: FloorTeam): { shown: FloorTicket[]; more: number; slots: Map<string, THREE.Vector3> } {
  const sorted = [...team.tickets].sort((a, b) => ORDER.indexOf(a.state) - ORDER.indexOf(b.state));
  const shown = sorted.slice(0, CARDS_MAX);
  const rows = Math.ceil(shown.length / CARDS_PER_ROW);
  const slots = new Map<string, THREE.Vector3>();
  shown.forEach((t, i) => {
    const row = Math.floor(i / CARDS_PER_ROW);
    const inRow = Math.min(CARDS_PER_ROW, shown.length - row * CARDS_PER_ROW);
    const col = i % CARDS_PER_ROW;
    const x = -((inRow - 1) * (CARD_W + CARD_GAP)) / 2 + col * (CARD_W + CARD_GAP);
    const z = -((rows - 1) * (CARD_D + CARD_GAP)) / 2 + row * (CARD_D + CARD_GAP);
    slots.set(t.id, new THREE.Vector3(x, TABLE_Y + 0.12, z));
  });
  return { shown, more: sorted.length - shown.length, slots };
}

/** Tables on a shallow arc facing the camera; seats around each table. */
function layout(teams: FloorTeam[], running: boolean): Placed[] {
  const n = teams.length;
  return teams.map((team, i) => {
    const x = (i - (n - 1) / 2) * TABLE_GAP;
    const z = n <= 1 ? 0 : -Math.abs(i - (n - 1) / 2) * 1.2 + 0.6;
    const pos = new THREE.Vector3(x, 0, z);
    const hex = team.internal ? '#8b5cf6' : HEX[teamColor(team.crew ? '' : team.id).name] ?? HEX.gray;
    const seats = new Map<string, Seat>();
    if (team.internal) {
      // The harness waits in the command center; whoever is on is out on the pad.
      const hq = commandLayout(team.agents, running);
      for (const a of team.agents) {
        const p = hq.pads.get(a.id) ?? hq.spots.get(a.id)!;
        seats.set(a.id, { pos: p, angle: Math.PI / 2, screen: p.clone().setY(1.0) });
      }
      return { team, pos, hex, seats, slots: new Map(), hq };
    }
    const manager = team.agents.find((a) => a.seat === 'manager');
    const others = team.agents.filter((a) => a.seat !== 'manager');
    const seatAt = (angle: number): Seat => {
      const p = new THREE.Vector3(Math.cos(angle) * SEAT_R, 0, Math.sin(angle) * SEAT_R);
      // The monitor sits 0.5 toward the table from the chair, at screen height.
      const toward = p.clone().multiplyScalar(-1).normalize().multiplyScalar(0.5);
      return { pos: p, angle, screen: p.clone().add(toward).setY(1.0) };
    };
    // Manager at the far side (facing the camera), others spread along the
    // near arc so their screens are visible.
    if (manager) seats.set(manager.id, seatAt(-Math.PI / 2));
    const span = Math.min(Math.PI * 1.2, 0.64 * Math.max(others.length - 1, 0) + 0.001);
    others.forEach((a, k) => {
      const f = others.length === 1 ? 0.5 : k / (others.length - 1);
      const angle = Math.PI / 2 - span / 2 + f * span; // centred on +z (toward camera)
      seats.set(a.id, seatAt(angle));
    });
    return { team, pos, hex, seats, slots: ticketSlots(team).slots };
  });
}

/**
 * The wall clock, in seconds, read once per frame by FrameClock (which runs
 * before every other frame callback) and shared by everything that ages a
 * pulse. Before the first frame it holds the time the module loaded.
 */
const frame = { now: Date.now() / 1000 };

function FrameClock() {
  useFrame(() => {
    frame.now = Date.now() / 1000;
  }, -100);
  return null;
}

/**
 * Asks for ~30 frames a second while `on`, for AMBIENT_MS after `resetKey`
 * last changed — so a finished floor keeps partying (or napping) for a while
 * without holding the GPU at full rate forever in a forgotten tab.
 */
function AmbientPump({ on, resetKey }: { on: boolean; resetKey: unknown }) {
  const invalidate = useThree((s) => s.invalidate);
  useEffect(() => {
    if (!on) return undefined;
    const until = Date.now() + AMBIENT_MS;
    const id = window.setInterval(() => {
      if (Date.now() > until) {
        window.clearInterval(id);
        return;
      }
      invalidate();
    }, 33);
    return () => window.clearInterval(id);
  }, [on, resetKey, invalidate]);
  return null;
}

/**
 * useMood is what a person is doing, re-read every frame and set as state
 * only when it changes (a few times a minute), so the labels follow without
 * the scene re-rendering per frame.
 */
function useMood(inputs: Omit<LifeInputs, 'now'>): Mood {
  const [mood, setMood] = useState<Mood>(() => moodFor({ ...inputs, now: Date.now() }));
  const { active, running, partying, since, seed } = inputs;
  // Inputs changed (the log moved): re-read at once, even with no frame coming.
  useEffect(() => {
    setMood(moodFor({ active, running, partying, since, seed, now: Date.now() }));
  }, [active, running, partying, since, seed]);
  useFrame(() => {
    const next = moodFor({ active, running, partying, since, seed, now: frame.now * 1000 });
    if (next !== mood) setMood(next);
  });
  return mood;
}

/** Scratch objects for per-frame maths — allocated once, never per frame. */
const _scale = new THREE.Vector3();
const _dir = new THREE.Vector3();
const _goal = new THREE.Vector3();
const _right = new THREE.Vector3();
const WHITE = new THREE.Color('#ffffff');

/** Whether the tab is visible; a hidden tab renders nothing. */
function useDocumentVisible(): boolean {
  const [visible, setVisible] = useState(() => typeof document === 'undefined' || document.visibilityState !== 'hidden');
  useEffect(() => {
    if (typeof document === 'undefined') return undefined;
    const onChange = () => setVisible(document.visibilityState !== 'hidden');
    document.addEventListener('visibilitychange', onChange);
    return () => document.removeEventListener('visibilitychange', onChange);
  }, []);
  return visible;
}

export default function TeamFloor3D({ floor, running, dark, reducedMotion, selection, onSelect, pulses, onTicket, onContextLost }: TeamFloor3DProps) {
  const placed = useMemo(() => layout(floor.teams, running), [floor.teams, running]);
  const byID = useMemo(() => new Map(placed.map((p) => [p.team.id, p])), [placed]);
  const [focus, setFocus] = useState<THREE.Vector3 | null>(null);
  const [focusDistance, setFocusDistance] = useState<number | null>(null);
  // Follow and spin outlive a visit: they are the user's, kept in the floor
  // store with the camera.
  const [autoRotate, setAutoRotateState] = useState(() => floorStore.autoRotate);
  const [follow, setFollowState] = useState(() => floorStore.follow);
  const setAutoRotate = useCallback((v: boolean) => {
    setAutoRotateState(v);
    rememberCameraPrefs({ autoRotate: v });
  }, []);
  const setFollow = useCallback((v: boolean) => {
    setFollowState(v);
    rememberCameraPrefs({ follow: v });
  }, []);
  // "reset view" asks the controls to restore their saved state (home)
  // instead of remounting the Canvas, which threw away every GPU resource
  // and the scene's warm state for a camera move.
  const [resetSignal, setResetSignal] = useState(0);
  const animate = !reducedMotion;
  const visible = useDocumentVisible();

  // The render loop: 'always' while something moves, 'demand' when the floor
  // is still (a click, a camera drag or a glide asks for frames as needed),
  // 'never' while the tab is hidden.
  const anyLive = useMemo(
    () => floor.teams.some((t) => t.tickets.some((k) => k.state === 'working' || k.state === 'review')),
    [floor.teams],
  );
  const freshPulse = pulses.length > 0 && Date.now() - pulses[pulses.length - 1].at < PULSE_TTL_MS;
  const lively = running || autoRotate || follow || anyLive || freshPulse;
  const frameloop: 'always' | 'demand' | 'never' = !visible ? 'never' : reducedMotion ? 'demand' : lively ? 'always' : 'demand';
  // Between runs the floor still lives — naps, the party — at an ambient
  // frame rate, for a while after it last changed.
  const ambient = visible && !reducedMotion && !lively;
  const shipped = useMemo(() => floorShipped(floor, running), [floor, running]);

  // A lost context (GPU reset, VRAM eviction) is reported up; the wrapper
  // swaps in the flat map and says so. The listener is removed on unmount.
  const lostRef = useRef<{ el: HTMLCanvasElement; fn: (e: Event) => void } | null>(null);
  const onContextLostRef = useRef(onContextLost);
  onContextLostRef.current = onContextLost;
  useEffect(
    () => () => {
      const l = lostRef.current;
      if (l) l.el.removeEventListener('webglcontextlost', l.fn);
      lostRef.current = null;
      document.body.style.cursor = '';
    },
    [],
  );

  const width = Math.max(1, placed.length) * TABLE_GAP;
  // Frame the tables with their rugs.
  const edge = (width - TABLE_GAP) / 2 + SEAT_R + 1.2;
  const span = edge * 2;
  const home = useMemo(() => new THREE.Vector3(0, 1.6, 0), []);
  const camZ = 5 + span * 0.92;
  const homeCam = useMemo(() => new THREE.Vector3(home.x, camZ * 0.55, camZ + 1), [home, camZ]);

  const working = workTables(floor).length;

  // World position of a person or a ticket, for focusing.
  const worldOf = useCallback(
    (sel: FloorSelection): THREE.Vector3 | null => {
      if (!sel) return null;
      const home = byID.get(sel.team) ?? (sel.kind === 'agent' && !sel.team ? byID.get(HARNESS_ID) : undefined);
      const tables = home ? [home, ...placed.filter((p) => p !== home)] : placed;
      for (const p of tables) {
        if (sel.kind === 'agent') {
          const s = p.seats.get(sel.id);
          if (s) return s.pos.clone().add(p.pos).setY(1.1);
        } else {
          const s = p.slots.get(sel.id);
          if (s) return s.clone().add(p.pos);
        }
      }
      return null;
    },
    [byID, placed],
  );

  // Selecting something brings the camera to it.
  useEffect(() => {
    if (!selection) return;
    const w = worldOf(selection);
    if (w) {
      setFocus(w);
      setFocusDistance(selection.kind === 'agent' ? 7.5 : 6);
    }
  }, [selection, worldOf]);

  // Follow mode: keep whoever is working in the middle of the picture.
  const nowAgent = floor.now?.agent ?? '';
  const nowTeam = floor.now?.team ?? '';
  useEffect(() => {
    if (!follow || !nowAgent) return;
    const w = worldOf({ kind: 'agent', id: nowAgent, team: nowTeam });
    if (w) {
      setFocus(w);
      setFocusDistance(8);
    }
  }, [follow, nowAgent, nowTeam, worldOf]);

  const clear = () => {
    setFocus(null);
    setFocusDistance(null);
    onSelect(null);
  };

  // What a keyboard or a screen reader can reach: the same people and
  // tickets the scene draws, as buttons that open the same dossier.
  const reachable = useMemo(() => {
    const out: { key: string; label: string; sel: NonNullable<FloorSelection> }[] = [];
    for (const t of floor.teams) {
      for (const a of t.agents) out.push({ key: `${t.id}/${a.id}`, label: `${a.id}, ${seatTitle(a, t)} on ${t.name}${a.active ? ', working' : ''}`, sel: { kind: 'agent', id: a.id, team: t.id } });
      for (const k of t.tickets) out.push({ key: `${t.id}/${k.id}`, label: `Task ${k.id}, ${TICKET_LABEL[k.state]} on ${t.name}${k.agent ? `, held by ${k.agent}` : ''}`, sel: { kind: 'ticket', id: k.id, team: t.id } });
    }
    for (const k of floor.unassigned) out.push({ key: `seam/${k.id}`, label: `Task ${k.id}, ${TICKET_LABEL[k.state]}, no team`, sel: { kind: 'ticket', id: k.id, team: '' } });
    return out;
  }, [floor.teams, floor.unassigned]);

  return (
    <div
      className="relative h-full w-full"
      data-testid="team-floor-3d"
      // The pointer cursor is set on the body by hovers inside the canvas; a
      // pointer that leaves the canvas mid-hover must not take it along.
      onPointerLeave={() => {
        document.body.style.cursor = '';
      }}
    >
      <Canvas
        shadows={{ type: THREE.PCFShadowMap }}
        dpr={[1, 1.75]}
        frameloop={frameloop}
        camera={{ position: [home.x, camZ * 0.55, camZ + 1], fov: 44, near: 0.1, far: 200 }}
        gl={{ antialias: true, alpha: true, powerPreference: 'high-performance' }}
        onPointerMissed={clear}
        onCreated={({ gl }) => {
          const el = gl.domElement;
          const fn = (e: Event) => {
            e.preventDefault();
            onContextLostRef.current?.();
          };
          el.addEventListener('webglcontextlost', fn);
          lostRef.current = { el, fn };
        }}
      >
        <Suspense fallback={null}>
          <FrameClock />
          <AmbientPump on={ambient} resetKey={floor} />
          <Lights dark={dark} />
          <Ground dark={dark} width={width} animate={animate} />
          {floor.links.map((link) => {
            const a = byID.get(link.from);
            const b = byID.get(link.to);
            if (!a || !b) return null;
            return <Conduit key={link.id} from={a} to={b} label={link.interface} stalled={link.stalled} running={running} animate={animate} />;
          })}
          {placed.map((p) =>
            p.team.internal && p.hq ? (
              <CommandCenter
                key={p.team.id}
                team={p.team}
                seats={floor.stage}
                phase={floor.phase}
                at={p.pos}
                hq={p.hq}
                running={running}
                animate={animate}
                dark={dark}
                partying={shipped}
                selection={selection}
                onSelect={onSelect}
                onFocus={() => {
                  setFocus(p.pos.clone().setY(1));
                  setFocusDistance(11);
                }}
              />
            ) : (
            <Table
              key={p.team.id}
              placed={p}
              running={running}
              animate={animate}
              dark={dark}
              selection={selection}
              onSelect={onSelect}
              pulses={pulses}
              onTicket={onTicket}
              partying={tableParties(p.team, shipped)}
              onFocus={() => {
                setFocus(p.pos.clone().setY(0.8));
                setFocusDistance(13);
              }}
            />
            ),
          )}
          {floor.handoffs.map((h) => (
            <Spark key={`${h.task}-${h.from}-${h.to}-${h.at}`} handoff={h} placed={byID} animate={animate} />
          ))}
          {floor.mode === 'teams' && working > 1 && floor.integration && (
            <IntegrationPad integration={floor.integration} unassigned={floor.unassigned.length} />
          )}
          <ContactShadows position={[0, 0.01, 0]} opacity={dark ? 0.55 : 0.35} scale={width + 20} blur={2.6} far={5} color={dark ? '#000' : '#4c1d95'} />
          <CameraRig focus={focus} distance={focusDistance} home={home} homeCam={homeCam} autoRotate={autoRotate && animate} resetSignal={resetSignal} />
        </Suspense>
      </Canvas>

      {/* The scene for a keyboard: every person and ticket as a button that
          opens the same dossier a click would. Visually hidden; the dossier
          it opens is not. */}
      <ul className="sr-only" aria-label="People and tasks on the floor" data-testid="floor-a11y-list">
        {reachable.map((r) => (
          <li key={r.key}>
            <button type="button" onClick={() => onSelect(r.sel)}>
              {r.label}
            </button>
          </li>
        ))}
      </ul>

      <div className="pointer-events-none absolute inset-x-0 bottom-0 flex flex-wrap items-end justify-between gap-2 p-2">
        <Legend floor={floor} />
        <div className="pointer-events-auto flex items-center gap-1 rounded-md border border-gray-200/80 bg-white/80 p-1 text-[10px] backdrop-blur dark:border-gray-700/80 dark:bg-gray-900/80">
          <button
            type="button"
            onClick={() => setFollow(!follow)}
            aria-pressed={follow}
            title="Keep the camera on whoever is working"
            className={follow ? 'focus-ring rounded bg-brand-500 px-1.5 py-0.5 text-white' : 'focus-ring rounded px-1.5 py-0.5 hover:bg-gray-100 dark:hover:bg-gray-800'}
          >
            {follow ? 'following' : 'follow'}
          </button>
          <button type="button" onClick={() => setAutoRotate(!autoRotate)} aria-pressed={autoRotate} className="focus-ring rounded px-1.5 py-0.5 hover:bg-gray-100 dark:hover:bg-gray-800">
            {autoRotate ? 'stop spin' : 'spin'}
          </button>
          <button
            type="button"
            onClick={() => {
              clear();
              setFollow(false);
              setResetSignal((k) => k + 1);
            }}
            className="focus-ring rounded px-1.5 py-0.5 hover:bg-gray-100 dark:hover:bg-gray-800"
          >
            reset view
          </button>
          <span className="hidden px-1 text-gray-400 sm:inline">drag to orbit · wheel to zoom · click a person or a ticket</span>
        </div>
      </div>
    </div>
  );
}

// ── Scene pieces ─────────────────────────────────────────────────────────

function Lights({ dark }: { dark: boolean }) {
  return (
    <>
      <ambientLight intensity={dark ? 0.35 : 0.6} />
      <hemisphereLight args={[dark ? '#6d5cff' : '#ffffff', dark ? '#0b0d14' : '#e9e5f5', dark ? 0.5 : 0.7]} />
      <directionalLight position={[10, 16, 8]} intensity={dark ? 1.1 : 1.4} castShadow shadow-mapSize={[2048, 2048]} shadow-bias={-0.0005}>
        <orthographicCamera attach="shadow-camera" args={[-30, 30, 30, -30, 0.5, 60]} />
      </directionalLight>
      <pointLight position={[-12, 6, -8]} intensity={dark ? 18 : 8} color="#8b5cf6" distance={40} />
      <pointLight position={[12, 5, 6]} intensity={dark ? 12 : 5} color="#22d3ee" distance={40} />
    </>
  );
}

function Ground({ dark, width, animate }: { dark: boolean; width: number; animate: boolean }) {
  return (
    <group>
      <mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, -0.02, 0]} receiveShadow>
        <planeGeometry args={[400, 400]} />
        <meshStandardMaterial color={dark ? '#0f1220' : '#f4f2fb'} roughness={0.95} metalness={0} />
      </mesh>
      {/* A pool of light under the floor's centre, so the tables sit in a room and not on a void. */}
      <mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, -0.01, 0]}>
        <circleGeometry args={[width / 2 + 9, 64]} />
        <meshBasicMaterial color={dark ? '#171a2e' : '#ffffff'} transparent opacity={dark ? 0.9 : 0.7} />
      </mesh>
      {animate && <Sparkles count={Math.round(60 + width * 3)} scale={[width + 14, 5, 16]} position={[0, 2.6, 0]} size={2.2} speed={0.25} opacity={dark ? 0.5 : 0.35} color={dark ? '#c4b5fd' : '#8b5cf6'} noise={0.4} />}
    </group>
  );
}

/** The soft round rug a team's table stands on, in its color. */
function Rug({ hex, dark }: { hex: string; dark: boolean }) {
  return (
    <mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, 0.005, 0]} receiveShadow>
      <circleGeometry args={[SEAT_R + 0.6, 64]} />
      <meshStandardMaterial color={hex} transparent opacity={dark ? 0.16 : 0.12} roughness={1} />
    </mesh>
  );
}

function Table({
  placed,
  running,
  animate,
  dark,
  selection,
  onSelect,
  pulses,
  onTicket,
  partying,
  onFocus,
}: {
  placed: Placed;
  running: boolean;
  animate: boolean;
  dark: boolean;
  selection: FloorSelection;
  onSelect: (sel: FloorSelection) => void;
  pulses: FloorPulse[];
  onTicket?: (id: string) => void;
  /** Every ticket here is done: beers, a kick-about, a dance. */
  partying: boolean;
  onFocus: () => void;
}) {
  const { team, pos, hex, seats, slots } = placed;
  const rim =
    team.gate === 'green' ? '#10b981' : team.gate === 'red' ? '#ef4444' : team.waitingOn.length > 0 ? '#f59e0b' : hex;
  const rimRef = useRef<THREE.MeshStandardMaterial>(null);
  const waiting = team.waitingOn.length > 0;
  useFrame(({ clock }) => {
    if (!animate || !rimRef.current) return;
    const t = clock.getElapsedTime();
    rimRef.current.emissiveIntensity = waiting ? 0.6 + Math.sin(t * 4) * 0.5 : team.gate ? 0.9 : 0.35 + Math.sin(t * 1.5) * 0.15;
  });
  const pct = team.total > 0 ? Math.round((team.done / team.total) * 100) : 0;

  // Who everyone else glances at: the person working.
  const activeAgent = running ? team.agents.find((a) => a.active) : undefined;
  const lookAt = activeAgent ? seats.get(activeAgent.id)?.pos : undefined;
  const managerSeat = team.agents.find((a) => a.seat === 'manager');
  const managerPos = managerSeat ? seats.get(managerSeat.id) : undefined;

  // Pulses on this table, still fresh enough to draw — filtered once per
  // pulse list, not once per render of every table.
  const mine = useMemo(() => pulses.filter((p) => p.team === team.id || (team.crew && !p.team)), [pulses, team.id, team.crew]);
  const latestByTicket = useMemo(() => {
    const m = new Map<string, FloorPulse>();
    for (const p of mine) if (p.ticket) m.set(p.ticket, p);
    return m;
  }, [mine]);
  const bursting = useMemo(() => mine.filter((p) => frame.now - p.at / 1000 <= BURST_S), [mine]);

  // The board stands behind the head seat; the team's sign hangs above it.
  const boardDir = useMemo(() => (managerPos ? managerPos.pos.clone().setY(0).normalize() : new THREE.Vector3(0, 0, -1)), [managerPos]);
  const signAt = useMemo(() => boardDir.clone().multiplyScalar(BOARD_BACK).setY(BOARD_BOTTOM + BOARD_H + 0.75), [boardDir]);
  const tableCentre = useMemo(() => new THREE.Vector3(0, TABLE_Y + 0.3, 0), []);
  // Where the players stand for the kick-about: behind their chairs, in seat order round the table.
  const pitch = useMemo(
    () =>
      team.agents
        .filter((a) => !(a.active && running))
        .map((a) => seats.get(a.id))
        .filter((x): x is Seat => !!x)
        .sort((a, b) => a.angle - b.angle)
        .map((x) => x.pos.clone().setY(0).multiplyScalar((SEAT_R + 0.8) / SEAT_R)),
    [team.agents, seats, running],
  );

  return (
    <group position={pos}>
      <Rug hex={hex} dark={dark} />
      {activeAgent && animate && <Sparkles count={24} scale={[TABLE_R * 2.4, 1.6, TABLE_R * 2.4]} position={[0, TABLE_Y + 1.1, 0]} size={3} speed={0.6} opacity={0.7} color={hex} />}
      {partying && animate && (
        <>
          <Sparkles count={36} scale={[TABLE_R * 3, 2.6, TABLE_R * 3]} position={[0, TABLE_Y + 1.8, 0]} size={5} speed={0.9} opacity={0.9} color="#fbbf24" />
          <Sparkles count={24} scale={[TABLE_R * 3, 2.6, TABLE_R * 3]} position={[0, TABLE_Y + 1.8, 0]} size={4} speed={0.7} opacity={0.8} color="#f472b6" />
        </>
      )}
      {partying && pitch.length >= 2 && <Football spots={pitch} animate={animate} />}
      {/* Pedestal + top. */}
      <mesh position={[0, TABLE_Y / 2, 0]} castShadow receiveShadow>
        <cylinderGeometry args={[0.45, 0.7, TABLE_Y, 24]} />
        <meshStandardMaterial color="#1f2937" roughness={0.6} metalness={0.4} />
      </mesh>
      <mesh
        position={[0, TABLE_Y, 0]}
        castShadow
        receiveShadow
        onClick={(e) => {
          e.stopPropagation();
          onFocus();
        }}
        onPointerOver={() => (document.body.style.cursor = 'pointer')}
        onPointerOut={() => (document.body.style.cursor = '')}
      >
        <cylinderGeometry args={[TABLE_R, TABLE_R, 0.14, 48]} />
        <meshStandardMaterial color={hex} roughness={0.35} metalness={0.15} />
      </mesh>
      <mesh position={[0, TABLE_Y + 0.075, 0]} rotation={[-Math.PI / 2, 0, 0]}>
        <ringGeometry args={[TABLE_R - 0.12, TABLE_R + 0.06, 64]} />
        <meshStandardMaterial ref={rimRef} color={rim} emissive={rim} emissiveIntensity={0.5} roughness={0.3} side={THREE.DoubleSide} />
      </mesh>
      {/* The team's sign, above its board — nothing else is drawn up there. */}
      <Html position={signAt} center distanceFactor={16} zIndexRange={[20, 0]}>
        <button type="button" onClick={onFocus} className="floor3d-label focus-ring" style={{ borderColor: hex }} title={team.charter || team.name}>
          <span className="floor3d-dot" style={{ background: hex }} />
          <span className="floor3d-label-name">{team.name}</span>
          <span className="floor3d-label-sub">
            {partying ? '🎉 all done · ' : ''}
            {team.total > 0 ? `${team.done}/${team.total} · ${pct}%` : running ? 'no tickets yet' : 'idle'}
            {team.blocked > 0 ? ` · ${team.blocked} blocked` : ''}
            {team.gate === 'green' ? ' · proved' : team.gate === 'red' ? ' · RED' : team.gate === 'unverified' ? ' · unverified' : ''}
          </span>
          {waiting && <span className="floor3d-label-wait">waiting on {team.waitingOn.join(', ')}</span>}
        </button>
      </Html>

      {/* The board on the wall behind the manager. */}
      <WallBoard team={team} hex={hex} dark={dark} dir={boardDir} running={running} />

      <Tickets team={team} animate={animate} selection={selection} onSelect={onSelect} latestByTicket={latestByTicket} onTicket={onTicket} />

      {/* Threads: each held ticket to its holder's monitor. */}
      {team.tickets.map((t) => {
        if (!t.agent) return null;
        const seat = seats.get(t.agent);
        const slot = slots.get(t.id);
        if (!seat || !slot) return null;
        const holder = team.agents.find((a) => a.id === t.agent);
        return <Thread key={`thread-${t.id}`} from={slot} to={seat.screen} color={TICKET_HEX[t.state]} live={!!holder?.active && running} animate={animate} />;
      })}

      {team.agents.map((a, i) => {
        const seat = seats.get(a.id);
        if (!seat) return null;
        const selected = selection?.kind === 'agent' && selection.id === a.id && selection.team === team.id;
        return (
          <Person
            key={a.id}
            agent={a}
            team={team}
            pos={seat.pos}
            angle={seat.angle}
            hex={hex}
            running={running}
            animate={animate}
            selected={selected}
            tall={a.seat !== 'manager' && i % 2 === 1}
            lookAt={a.active ? undefined : lookAt}
            partying={partying}
            onSelect={() => onSelect(selected ? null : { kind: 'agent', id: a.id, team: team.id })}
          />
        );
      })}

      {/* Dispatches: the manager sending someone onto a ticket. */}
      {managerPos &&
        mine
          .filter((p) => p.kind === 'agent-start' && p.agent && p.agent !== managerSeat?.id && seats.has(p.agent))
          .map((p, i) => <Dispatch key={p.id} from={managerPos.pos} to={seats.get(p.agent!)!.pos} label={p.ticket ? `${p.agent} ← ${p.ticket}` : `${p.agent}`} at={p.at} slot={i} animate={animate} />)}

      {/* Bursts: a ticket finishing, failing, appearing; a gate; a team done. */}
      {bursting.map((p) => {
        const where = p.ticket ? slots.get(p.ticket) : undefined;
        const big = p.kind === 'gate' || p.kind === 'team-complete';
        if (!where && !big) return null;
        return <Burst key={`burst-${p.id}`} at={p.at} position={where ?? tableCentre} color={TONE_HEX[p.tone]} size={big ? 2.2 : 1} animate={animate} />;
      })}
    </group>
  );
}

function Tickets({
  team,
  animate,
  selection,
  onSelect,
  latestByTicket,
  onTicket,
}: {
  team: FloorTeam;
  animate: boolean;
  selection: FloorSelection;
  onSelect: (sel: FloorSelection) => void;
  latestByTicket: Map<string, FloorPulse>;
  onTicket?: (id: string) => void;
}) {
  void onTicket;
  const { shown, more, slots } = useMemo(() => ticketSlots(team), [team]);
  return (
    <group>
      {shown.map((t) => {
        const slot = slots.get(t.id)!;
        const selected = selection?.kind === 'ticket' && selection.id === t.id;
        return (
          <TicketCard
            key={t.id}
            ticket={t}
            x={slot.x}
            z={slot.z}
            animate={animate}
            selected={selected}
            pulse={latestByTicket.get(t.id)}
            onSelect={() => onSelect(selected ? null : { kind: 'ticket', id: t.id, team: team.id })}
          />
        );
      })}
      {more > 0 && (
        <Html position={[(CARDS_PER_ROW * (CARD_W + CARD_GAP)) / 2 + 0.2, TABLE_Y + 0.18, 0]} center distanceFactor={14}>
          <span className="floor3d-more">+{more}</span>
        </Html>
      )}
    </group>
  );
}

function TicketCard({
  ticket,
  x,
  z,
  animate,
  selected,
  pulse,
  onSelect,
}: {
  ticket: FloorTicket;
  x: number;
  z: number;
  animate: boolean;
  selected: boolean;
  pulse?: FloorPulse;
  onSelect: () => void;
}) {
  const live = ticket.state === 'working' || ticket.state === 'review';
  const mat = useRef<THREE.MeshStandardMaterial>(null);
  const grp = useRef<THREE.Group>(null);
  const ring = useRef<THREE.Mesh>(null);
  const [hover, setHover] = useState(false);
  const color = TICKET_HEX[ticket.state];
  const glow = useMemo(() => new THREE.Color(color), [color]);
  const pulseAt = pulse ? pulse.at / 1000 : -Infinity;
  const spawned = pulse?.kind === 'ticket-new';

  useFrame(({ clock }) => {
    const t = clock.getElapsedTime();
    const age = frame.now - pulseAt;
    if (mat.current) {
      // A fresh pulse flashes the card white-hot, then it settles into its
      // state's glow: breathing when live, steady when selected or hovered.
      if (animate && age < FLASH_S) {
        mat.current.emissive.copy(WHITE);
        mat.current.emissiveIntensity = 1.6 * (1 - age / FLASH_S);
      } else {
        mat.current.emissive.copy(glow);
        if (selected || hover) mat.current.emissiveIntensity = 0.7;
        else if (live && animate) mat.current.emissiveIntensity = 0.5 + Math.sin(t * 5) * 0.4;
        else mat.current.emissiveIntensity = live ? 0.6 : 0.08;
      }
    }
    if (grp.current) {
      // A card that just appeared drops onto the table with a little bounce.
      if (animate && spawned && age < 0.7) {
        const k = age / 0.7;
        const ease = 1 - Math.pow(1 - k, 3);
        grp.current.position.y = (1 - ease) * 1.4;
        grp.current.scale.setScalar(0.4 + ease * 0.6);
      } else {
        grp.current.position.y = 0;
        grp.current.scale.setScalar(selected ? 1.12 : 1);
      }
    }
    if (ring.current) {
      ring.current.visible = selected;
      if (selected && animate) ring.current.rotation.z = t * 1.2;
    }
  });

  const body = (
    <RoundedBox
      args={[CARD_W, 0.06, CARD_D]}
      radius={0.03}
      smoothness={3}
      position={[0, live ? 0.05 : 0.03, 0]}
      castShadow
      onClick={(e: ThreeEvent<MouseEvent>) => {
        e.stopPropagation();
        onSelect();
      }}
      onPointerOver={(e: ThreeEvent<PointerEvent>) => {
        e.stopPropagation();
        setHover(true);
        document.body.style.cursor = 'pointer';
      }}
      onPointerOut={() => {
        setHover(false);
        document.body.style.cursor = '';
      }}
    >
      <meshStandardMaterial ref={mat} color={color} emissive={color} emissiveIntensity={0.1} roughness={0.5} />
    </RoundedBox>
  );
  return (
    <group position={[x, TABLE_Y + 0.08, z]}>
      <group ref={grp}>
        {live && animate ? <Float speed={3} floatIntensity={0.15} rotationIntensity={0}>{body}</Float> : body}
        <mesh ref={ring} position={[0, 0.02, 0]} rotation={[-Math.PI / 2, 0, 0]} visible={false}>
          <ringGeometry args={[0.42, 0.47, 40, 1, 0, Math.PI * 1.6]} />
          <meshBasicMaterial color={SELECT} transparent opacity={0.95} side={THREE.DoubleSide} toneMapped={false} />
        </mesh>
      </group>
      <Html position={[0, 0.11, 0]} center distanceFactor={11} zIndexRange={[10, 0]} style={{ pointerEvents: 'none' }}>
        <span className={hover || selected ? 'floor3d-ticket floor3d-ticket-hover' : 'floor3d-ticket'} style={{ background: color, color: ticket.state === 'queued' ? '#1f2937' : '#fff' }}>
          {ticket.state === 'done' ? '✓ ' : ticket.state === 'blocked' || ticket.state === 'failed' ? '! ' : ''}
          {ticket.id.length > 9 ? ticket.id.slice(0, 8) + '…' : ticket.id}
          {hover && !selected && (
            <span className="floor3d-ticket-tip">
              {TICKET_LABEL[ticket.state]}
              {ticket.agent ? ` · ${ticket.agent}` : ''}
              <br />
              {ticket.title}
            </span>
          )}
        </span>
      </Html>
    </group>
  );
}

const UP = new THREE.Vector3(0, 1, 0);
const _walkPos = new THREE.Vector3();

/** Where a seated figure is, in its seat's space: on the chair. */
const SEATED_AT = new THREE.Vector3(0, 0, 0.1);
/** Where it stands when it gets up: behind the chair, facing the table. */
const STANDING_AT = new THREE.Vector3(0, 0, 0.85);

/** The stroll: up, round behind the chairs, and back to sit down. */
const STROLL = [STANDING_AT, new THREE.Vector3(0.85, 0, 1.25), new THREE.Vector3(-0.85, 0, 1.25), STANDING_AT, SEATED_AT];

function hoverPick(onSelect: () => void, setHover: (v: boolean) => void) {
  return {
    onClick: (e: ThreeEvent<MouseEvent>) => {
      e.stopPropagation();
      onSelect();
    },
    onPointerOver: (e: ThreeEvent<PointerEvent>) => {
      e.stopPropagation();
      setHover(true);
      document.body.style.cursor = 'pointer';
    },
    onPointerOut: () => {
      setHover(false);
      document.body.style.cursor = '';
    },
  };
}

/** Floating z's over a sleeper. */
function Zzz() {
  return (
    <span className="floor3d-zzz" aria-hidden="true">
      <i>z</i>
      <i>z</i>
      <i>Z</i>
    </span>
  );
}

function Person({
  agent,
  team,
  pos,
  angle,
  hex,
  running,
  animate,
  selected,
  tall,
  lookAt,
  partying,
  onSelect,
}: {
  agent: FloorAgent;
  team: FloorTeam;
  pos: THREE.Vector3;
  angle: number;
  hex: string;
  running: boolean;
  animate: boolean;
  selected: boolean;
  /** Every other seat's pill sits higher, so neighbours' names never touch. */
  tall?: boolean;
  /** Table-space point this person glances at when idle (the one working). */
  lookAt?: THREE.Vector3;
  /** The table is done: this person is at the party. */
  partying: boolean;
  onSelect: () => void;
}) {
  const isManager = agent.seat === 'manager';
  const active = agent.active && running;
  const screen = useRef<THREE.MeshStandardMaterial>(null);
  const flash = useRef<THREE.Mesh>(null);
  const halo = useRef<THREE.Mesh>(null);
  const pulse = useRef<THREE.Mesh>(null);
  const code = useRef<THREE.Mesh[]>([]);
  const mug = useRef<THREE.Mesh>(null);
  const lift = useRef<THREE.Group>(null);
  const flashStart = useRef<number>(-1);
  const wasActive = useRef(false);
  const [hover, setHover] = useState(false);
  const invalidate = useThree((s) => s.invalidate);
  const rig = useRig();
  const seed = useMemo(() => seedOf(agent.id), [agent.id]);
  const look = useMemo(() => lookOf(seed), [seed]);
  const phase = (seed % 628) / 100;
  const mood = useMood({ active, running, partying, since: agent.lastAt ?? FLOOR_BORN, seed });
  const standing = isParty(mood);
  // The motion a mood carries: when it began, the stroll's path, how seated.
  const moodStart = useRef<{ mood: Mood; at: number }>({ mood, at: -1 });
  const walk = useRef<Walk | null>(null);
  const sit = useRef(standing ? 0 : 1);
  const prev = useRef(new THREE.Vector3().copy(standing ? STANDING_AT : SEATED_AT));

  // A flash ring when this person starts working: the "message received"
  // cue, once per activation.
  useEffect(() => {
    if (active && !wasActive.current) flashStart.current = performance.now();
    wasActive.current = active;
    invalidate();
  }, [active, invalidate]);
  // Hover, selection and a new mood ease over several frames; on-demand
  // rendering has to be asked for each of them.
  useEffect(() => {
    invalidate();
  }, [hover, selected, mood, invalidate]);

  // Seat faces the table centre: rotate the whole person so its screen is
  // between them and the table.
  const yaw = Math.atan2(-pos.x, -pos.z);
  const headYaw = useMemo(() => {
    if (!lookAt) return 0;
    const local = lookAt.clone().sub(pos).applyAxisAngle(UP, -yaw);
    return THREE.MathUtils.clamp(Math.atan2(-local.x, -local.z), -1.1, 1.1);
  }, [lookAt, pos, yaw]);
  const glance = mood === 'watch' || mood === 'coffee' ? headYaw : 0;

  useFrame(({ clock }, delta) => {
    const t = clock.getElapsedTime();
    if (screen.current) {
      screen.current.emissiveIntensity = active ? (animate ? 1.2 + Math.sin(t * 6) * 0.5 : 1.4) : mood === 'nap' ? 0.04 : 0.12;
    }
    const root = rig.root.current;
    if (root) {
      if (moodStart.current.mood !== mood || moodStart.current.at < 0) {
        moodStart.current = { mood, at: t };
        walk.current = mood === 'stroll' && animate ? makeWalk([root.position.clone(), ...STROLL], t, 0.85) : null;
      }
      if (!animate) {
        root.position.copy(standing ? STANDING_AT : SEATED_AT);
        root.rotation.y = 0;
        sit.current = standing ? 0 : 1;
        stillPose(rig, mood, sit.current, glance);
      } else {
        let facing = 0;
        if (walk.current) {
          const dir = walkAt(walk.current, t, _walkPos);
          if (dir === null) walk.current = null;
          else facing = dir;
          root.position.copy(_walkPos);
        } else {
          root.position.lerp(standing ? STANDING_AT : SEATED_AT, 0.07);
        }
        const speed = delta > 0 ? root.position.distanceTo(prev.current) / delta : 0;
        prev.current.copy(root.position);
        root.rotation.y = turnToward(root.rotation.y, walk.current && speed > 0.05 ? facing : 0, 0.15);
        // Seated only once back at the chair.
        const atChair = root.position.distanceToSquared(SEATED_AT) < 0.02;
        sit.current = THREE.MathUtils.lerp(sit.current, !standing && atChair && !walk.current ? 1 : 0, 0.14);
        poseRig(rig, { mood, t, since: t - moodStart.current.at, phase, sit: sit.current, speed, glance });
      }
      const target = hover || selected ? 1.08 : 1;
      if (Math.abs(root.scale.x - target) > 0.002) {
        root.scale.lerp(_scale.setScalar(target), 0.2);
        invalidate();
      }
      if (lift.current) lift.current.position.y = (1 - sit.current) * 0.28;
    }
    if (mug.current) mug.current.visible = !active && mood !== 'coffee';
    if (flash.current) {
      const age = flashStart.current < 0 ? Infinity : (performance.now() - flashStart.current) / 1000;
      const visible = age < 1.2 && animate;
      flash.current.visible = visible;
      if (visible) {
        const s = 0.6 + age * 2.2;
        flash.current.scale.set(s, s, s);
        (flash.current.material as THREE.MeshBasicMaterial).opacity = Math.max(0, 0.8 - age * 0.7);
        invalidate();
      }
    }
    if (halo.current) {
      halo.current.visible = selected;
      if (selected && animate) {
        halo.current.rotation.z = t * 0.8;
        const s = 1 + Math.sin(t * 3) * 0.04;
        halo.current.scale.set(s, s, s);
      }
    }
    if (pulse.current) {
      // The working ring: a slow breath under the chair, in the team's color.
      pulse.current.visible = active;
      if (active && animate) {
        const k = (t * 0.9 + angle) % 1;
        const s = 0.8 + k * 0.9;
        pulse.current.scale.set(s, s, s);
        (pulse.current.material as THREE.MeshBasicMaterial).opacity = 0.55 * (1 - k);
      }
    }
    // Lines of code rising off the screen while they type.
    code.current.forEach((m, i) => {
      if (!m) return;
      m.visible = active && animate;
      if (!m.visible) return;
      const k = (t * 0.55 + i / code.current.length) % 1;
      m.position.set(-0.12 + (i % 3) * 0.12, 1.2 + k * 0.7, -0.5);
      m.scale.set(0.5 + ((i * 7) % 5) * 0.12, 1, 1);
      (m.material as THREE.MeshBasicMaterial).opacity = k < 0.15 ? k / 0.15 : 1 - (k - 0.15) / 0.85;
    });
  });

  const label = agent.id.length > 18 ? agent.id.slice(0, 17) + '…' : agent.id;
  const saying = agent.lastMessage && agent.lastMessage.length > 54 ? agent.lastMessage.slice(0, 53) + '…' : agent.lastMessage;
  const pick = hoverPick(onSelect, setHover);
  const quiet = !active && !selected && !hover && !isManager;

  return (
    <group position={pos} rotation={[0, yaw, 0]}>
      {/* Selection halo on the floor + a spot from above. */}
      <mesh ref={halo} position={[0, 0.015, 0.1]} rotation={[-Math.PI / 2, 0, 0]} visible={false}>
        <ringGeometry args={[0.55, 0.66, 48, 1, 0, Math.PI * 1.7]} />
        <meshBasicMaterial color={SELECT} transparent opacity={0.9} side={THREE.DoubleSide} toneMapped={false} />
      </mesh>
      {selected && <pointLight position={[0, 2.4, 0.1]} intensity={6} distance={4} color={SELECT} />}
      {/* Working: a pool of light from above, a breathing ring on the floor. */}
      {active && <pointLight position={[0, 3.0, -0.2]} intensity={animate ? 5 : 4} distance={5} decay={2} color={hex} />}
      <mesh ref={pulse} position={[0, 0.02, 0]} rotation={[-Math.PI / 2, 0, 0]} visible={false}>
        <ringGeometry args={[0.7, 0.8, 48]} />
        <meshBasicMaterial color={hex} transparent opacity={0.5} side={THREE.DoubleSide} toneMapped={false} />
      </mesh>
      {Array.from({ length: 6 }).map((_, i) => (
        <mesh key={i} ref={(el) => { if (el) code.current[i] = el; }} visible={false}>
          <planeGeometry args={[0.16, 0.025]} />
          <meshBasicMaterial color={hex} transparent opacity={0.8} toneMapped={false} side={THREE.DoubleSide} />
        </mesh>
      ))}
      {/* Chair */}
      <mesh position={[0, 0.28, 0.15]} castShadow {...pick}>
        <boxGeometry args={[0.55, 0.08, 0.5]} />
        <meshStandardMaterial color="#374151" roughness={0.7} />
      </mesh>
      <mesh position={[0, 0.55, 0.38]} castShadow {...pick}>
        <boxGeometry args={[0.55, 0.55, 0.06]} />
        <meshStandardMaterial color="#374151" roughness={0.7} />
      </mesh>
      {/* The person: gets up, walks about, sits back down. */}
      <group position={[0, 0, 0]}>
        <RigBody rig={rig} look={{ shirt: isManager ? hex : agent.borrowed ? '#e5e7eb' : look.shirt, skin: look.skin, hair: look.hair, ghost: !!agent.borrowed, crown: isManager }} pick={pick}>
          <group ref={lift}>
            {/* Name + the pop-out when working, riding with the person. */}
            <Html position={[0, tall ? 2.05 : 1.55, 0]} distanceFactor={12} zIndexRange={[30, 0]} style={{ pointerEvents: 'none' }}>
              <div className={['floor3d-person', selected && 'floor3d-person-selected', hover && 'floor3d-person-hover'].filter(Boolean).join(' ')}>
                {active && (
                  <div className="floor3d-bubble" style={{ borderColor: hex }}>
                    <span className="floor3d-bubble-head" style={{ color: hex }}>
                      {agent.task ? `on ${agent.task}` : 'working'}
                    </span>
                    <span className="floor3d-bubble-dots" aria-hidden="true">
                      <i /><i /><i />
                    </span>
                    {saying && <span className="floor3d-bubble-say">{saying}</span>}
                  </div>
                )}
                {mood === 'nap' && <Zzz />}
                <span
                  className={['floor3d-name', isManager && 'floor3d-name-manager', quiet && 'floor3d-name-quiet'].filter(Boolean).join(' ')}
                  title={`${agent.id} · ${seatTitle(agent, team)} · ${MOOD_LABEL[mood]}${agent.touched ? ` · ${agent.touched} tickets touched` : ''} · click for the dossier`}
                >
                  {moodShows(mood) && <span className="floor3d-mood" aria-hidden="true">{MOOD_GLYPH[mood]}</span>}
                  <span aria-hidden="true">{glyphFor(agent)}</span> {label}
                  {isManager && <em>{team.managerDefault ? ' · manager (default)' : ' · manager'}</em>}
                  {agent.borrowed && <em>{` · ${agent.seat} from the ${agent.borrowed}`}</em>}
                </span>
              </div>
            </Html>
          </group>
        </RigBody>
      </group>
      {/* Desk + monitor, toward the table (−z after the yaw). */}
      <mesh position={[0, 0.72, -0.45]} castShadow {...pick}>
        <boxGeometry args={[0.7, 0.05, 0.3]} />
        <meshStandardMaterial color="#111827" roughness={0.5} metalness={0.3} />
      </mesh>
      <mesh position={[0, 0.98, -0.52]} castShadow {...pick}>
        <boxGeometry args={[0.6, 0.4, 0.04]} />
        <meshStandardMaterial color="#0b0f19" roughness={0.4} metalness={0.5} />
      </mesh>
      <mesh position={[0, 0.98, -0.495]} {...pick}>
        <planeGeometry args={[0.52, 0.32]} />
        <meshStandardMaterial ref={screen} color={active ? hex : '#1e293b'} emissive={active ? hex : '#334155'} emissiveIntensity={0.12} toneMapped={false} />
      </mesh>
      {/* The mug waits on the desk — in the hand on a coffee break, gone while typing. */}
      <mesh ref={mug} position={[0.26, 0.78, -0.4]} castShadow>
        <cylinderGeometry args={[0.04, 0.035, 0.07, 10]} />
        <meshStandardMaterial color={isManager ? '#fbbf24' : '#f9a8d4'} roughness={0.6} />
      </mesh>
      {/* Activation flash. */}
      <mesh ref={flash} position={[0, 1.1, 0.1]} rotation={[-Math.PI / 2, 0, 0]} visible={false}>
        <ringGeometry args={[0.35, 0.42, 32]} />
        <meshBasicMaterial color={hex} transparent opacity={0.8} side={THREE.DoubleSide} />
      </mesh>
    </group>
  );
}

/**
 * The kick-about at a done table: a ball passed round the players behind
 * their chairs and back again, lobbed over the table when it has to be.
 * Only on the party's football rounds; between them it rests out of sight.
 */
function Football({ spots, animate }: { spots: THREE.Vector3[]; animate: boolean }) {
  const ball = useRef<THREE.Group>(null);
  const shadow = useRef<THREE.Mesh>(null);
  const PASS_S = 1.3;
  useFrame(() => {
    const m = ball.current;
    if (!m) return;
    const on = isFootballRound(frame.now * 1000);
    m.visible = on;
    if (shadow.current) shadow.current.visible = on;
    if (!on) return;
    const n = spots.length;
    const legs = 2 * (n - 1);
    const k = animate ? frame.now / PASS_S : 0.5;
    const leg = Math.floor(k) % legs;
    const u = k - Math.floor(k);
    const from = leg < n - 1 ? leg : legs - leg;
    const to = leg < n - 1 ? leg + 1 : legs - leg - 1;
    const a = spots[from];
    const b = spots[to];
    const h = Math.sin(u * Math.PI) * (0.3 + a.distanceTo(b) * 0.32);
    m.position.lerpVectors(a, b, u);
    m.position.y = 0.13 + h;
    if (animate) {
      m.rotation.x += 0.18;
      m.rotation.z += 0.07;
    }
    if (shadow.current) {
      shadow.current.position.set(m.position.x, 0.012, m.position.z);
      const s = 1 / (1 + h * 0.8);
      shadow.current.scale.set(s, s, s);
    }
  });
  return (
    <group>
      <group ref={ball} visible={false}>
        <mesh castShadow>
          <icosahedronGeometry args={[0.13, 1]} />
          <meshStandardMaterial color="#ffffff" roughness={0.45} flatShading />
        </mesh>
        <mesh scale={1.01}>
          <icosahedronGeometry args={[0.13, 0]} />
          <meshBasicMaterial color="#111827" wireframe />
        </mesh>
      </group>
      <mesh ref={shadow} rotation={[-Math.PI / 2, 0, 0]} visible={false}>
        <circleGeometry args={[0.13, 20]} />
        <meshBasicMaterial color="#000" transparent opacity={0.25} depthWrite={false} />
      </mesh>
    </group>
  );
}

/**
 * The command center: the harness's glass room. A mission screen on the back
 * wall names the phase; the crew — dispatcher, planner, splitter, composer… —
 * wait inside on stools; whoever the run hands the microphone to walks out
 * through the sliding door onto the pad in front, says their piece and walks
 * back in. The room's name and phase hang above it.
 */
function CommandCenter({
  team,
  seats,
  phase,
  at,
  hq,
  running,
  animate,
  dark,
  partying,
  selection,
  onSelect,
  onFocus,
}: {
  team: FloorTeam;
  seats: FloorStageSeat[];
  phase: FloorPhase | null;
  at: THREE.Vector3;
  hq: HQ;
  running: boolean;
  animate: boolean;
  dark: boolean;
  /** The run shipped: the crew parties too. */
  partying: boolean;
  selection: FloorSelection;
  onSelect: (sel: FloorSelection) => void;
  onFocus: () => void;
}) {
  const { w, d } = hq;
  const out = seats.filter((s) => s.active && running && hq.pads.has(s.id));
  const live = running && !!phase;
  const padGlow = useRef<THREE.MeshStandardMaterial>(null);
  const screenGlow = useRef<THREE.MeshStandardMaterial>(null);
  const lamp = useRef<THREE.MeshStandardMaterial>(null);
  const doorL = useRef<THREE.Mesh>(null);
  const doorR = useRef<THREE.Mesh>(null);
  /** Scene seconds until which the door stays open: walkers keep pushing it. */
  const door = useRef({ until: 0 });
  const invalidate = useThree((s) => s.invalidate);
  const busy = out.length > 0;
  useFrame(({ clock }) => {
    const t = clock.getElapsedTime();
    if (padGlow.current) padGlow.current.emissiveIntensity = busy ? (animate ? 0.8 + Math.sin(t * 3) * 0.35 : 1) : 0.15;
    if (screenGlow.current) screenGlow.current.emissiveIntensity = live ? (animate ? 0.7 + Math.sin(t * 1.7) * 0.15 : 0.8) : 0.2;
    if (lamp.current) lamp.current.emissiveIntensity = busy ? (animate ? 1.2 + Math.sin(t * 8) * 0.8 : 1.5) : 0.2;
    // The door slides open for anyone passing, and shuts behind them.
    const open = t < door.current.until;
    const goal = open ? HQ_DOOR_W * 0.75 : HQ_DOOR_W / 4;
    for (const [ref, side] of [[doorL, -1], [doorR, 1]] as const) {
      const m = ref.current;
      if (!m) continue;
      const want = side * goal;
      if (!animate) m.position.x = want;
      else if (Math.abs(m.position.x - want) > 0.002) {
        m.position.x = THREE.MathUtils.lerp(m.position.x, want, 0.14);
        invalidate();
      }
    }
  });

  const glass = <meshStandardMaterial color="#c4b5fd" transparent opacity={dark ? 0.16 : 0.22} roughness={0.1} metalness={0.2} depthWrite={false} side={THREE.DoubleSide} />;
  const frameMat = <meshStandardMaterial color={dark ? '#4c4f6b' : '#6b7280'} roughness={0.5} metalness={0.5} />;
  const message = phase?.message && phase.message.length > 90 ? phase.message.slice(0, 89) + '…' : phase?.message;
  const sideW = (w - HQ_DOOR_W) / 2;
  const inside = seats.length - out.length;
  const pathLen = hq.padAt.z - hq.doorOut.z;
  return (
    <group position={at}>
      {/* The room's floor: click it to go in. */}
      <mesh
        position={[0, 0.04, 0]}
        receiveShadow
        onClick={(e) => {
          e.stopPropagation();
          onFocus();
        }}
      >
        <boxGeometry args={[w, 0.08, d]} />
        <meshStandardMaterial color={dark ? '#1b1f36' : '#ede9fe'} roughness={0.8} />
      </mesh>
      {/* Back wall, its console and the mission screen. */}
      <mesh position={[0, HQ_WALL_H / 2, -d / 2]} castShadow receiveShadow>
        <boxGeometry args={[w, HQ_WALL_H, 0.12]} />
        <meshStandardMaterial color={dark ? '#161a2e' : '#f5f3ff'} roughness={0.8} />
      </mesh>
      <mesh position={[0, HQ_WALL_H - 0.05, -d / 2 + 0.07]}>
        <boxGeometry args={[w, 0.06, 0.02]} />
        <meshStandardMaterial color={HQ_HEX} emissive={HQ_HEX} emissiveIntensity={0.8} toneMapped={false} />
      </mesh>
      <group position={[0, 1.75, -d / 2 + 0.08]}>
        <mesh>
          <boxGeometry args={[3.1, 1.35, 0.05]} />
          <meshStandardMaterial color="#0b0f19" roughness={0.4} metalness={0.4} />
        </mesh>
        <mesh position={[0, 0, 0.03]}>
          <planeGeometry args={[2.95, 1.2]} />
          <meshStandardMaterial ref={screenGlow} color={live ? '#312e81' : '#1e293b'} emissive={live ? '#4c1d95' : '#0f172a'} emissiveIntensity={0.2} toneMapped={false} />
        </mesh>
        <Html position={[0, 0, 0.05]} center distanceFactor={12} zIndexRange={[20, 0]} style={{ pointerEvents: 'none' }}>
          <div className="floor3d-screen">
            <span className="floor3d-screen-kicker">mission · phase</span>
            <span className="floor3d-screen-phase">{phase ? phase.id : partying ? 'shipped 🎉' : running ? 'starting' : 'standing by'}</span>
            {phase?.agent && <span className="floor3d-screen-who">{phase.agent}</span>}
            {message && <span className="floor3d-screen-say">{message}</span>}
          </div>
        </Html>
      </group>
      <mesh position={[0, 0.4, -d / 2 + 0.36]} castShadow>
        <boxGeometry args={[w - 0.7, 0.8, 0.42]} />
        <meshStandardMaterial color="#1f2937" roughness={0.6} metalness={0.3} />
      </mesh>
      {Array.from({ length: Math.max(2, Math.floor((w - 1) / 0.9)) }).map((_, i, all) => (
        <mesh key={i} position={[-(w - 1.4) / 2 + (i * (w - 1.4)) / Math.max(1, all.length - 1), 0.83, -d / 2 + 0.3]} rotation={[-0.5, 0, 0]}>
          <planeGeometry args={[0.5, 0.18]} />
          <meshStandardMaterial color={i % 2 ? '#22d3ee' : HQ_HEX} emissive={i % 2 ? '#22d3ee' : HQ_HEX} emissiveIntensity={live ? 0.9 : 0.25} toneMapped={false} />
        </mesh>
      ))}
      {/* Glass: the sides, and the front either side of the door. */}
      {([-1, 1] as const).map((side) => (
        <group key={side}>
          <mesh position={[side * (w / 2), HQ_GLASS_H / 2 + 0.08, 0]}>
            <boxGeometry args={[0.05, HQ_GLASS_H, d]} />
            {glass}
          </mesh>
          <mesh position={[side * (w / 2 - sideW / 2), HQ_GLASS_H / 2 + 0.08, d / 2]}>
            <boxGeometry args={[sideW, HQ_GLASS_H, 0.05]} />
            {glass}
          </mesh>
          <mesh position={[side * (w / 2), (HQ_GLASS_H + 0.1) / 2, d / 2]} castShadow>
            <boxGeometry args={[0.1, HQ_GLASS_H + 0.1, 0.1]} />
            {frameMat}
          </mesh>
          <mesh position={[side * (w / 2), HQ_WALL_H / 2, -d / 2 + 0.02]}>
            <boxGeometry args={[0.1, HQ_WALL_H, 0.1]} />
            {frameMat}
          </mesh>
          {/* The door frame. */}
          <mesh position={[side * (HQ_DOOR_W / 2 + 0.05), HQ_DOOR_H / 2 + 0.08, d / 2]} castShadow>
            <boxGeometry args={[0.1, HQ_DOOR_H, 0.12]} />
            {frameMat}
          </mesh>
        </group>
      ))}
      <mesh position={[0, HQ_DOOR_H + 0.12, d / 2]} castShadow>
        <boxGeometry args={[HQ_DOOR_W + 0.2, 0.1, 0.12]} />
        {frameMat}
      </mesh>
      {/* The on-air lamp over the door: lit while someone is out on the pad. */}
      <mesh position={[0, HQ_DOOR_H + 0.24, d / 2 + 0.02]}>
        <boxGeometry args={[0.36, 0.12, 0.06]} />
        <meshStandardMaterial ref={lamp} color={busy ? '#ef4444' : '#7f1d1d'} emissive="#ef4444" emissiveIntensity={0.2} toneMapped={false} />
      </mesh>
      {/* The sliding door. */}
      {([-1, 1] as const).map((side) => (
        <mesh key={side} ref={side < 0 ? doorL : doorR} position={[(side * HQ_DOOR_W) / 4, HQ_DOOR_H / 2 + 0.08, d / 2 + 0.07]}>
          <boxGeometry args={[HQ_DOOR_W / 2, HQ_DOOR_H - 0.06, 0.04]} />
          <meshStandardMaterial color="#a78bfa" transparent opacity={0.4} roughness={0.1} metalness={0.3} depthWrite={false} />
        </mesh>
      ))}
      {/* A plant, and the coffee machine the coffee breaks come from. */}
      <group position={[-w / 2 + 0.35, 0.08, d / 2 - 0.35]}>
        <mesh position={[0, 0.16, 0]} castShadow>
          <cylinderGeometry args={[0.14, 0.11, 0.32, 12]} />
          <meshStandardMaterial color="#b45309" roughness={0.8} />
        </mesh>
        <mesh position={[0, 0.52, 0]} castShadow>
          <icosahedronGeometry args={[0.26, 0]} />
          <meshStandardMaterial color="#16a34a" roughness={0.8} flatShading />
        </mesh>
      </group>
      <group position={[w / 2 - 0.35, 0.08, d / 2 - 0.35]}>
        <mesh position={[0, 0.45, 0]} castShadow>
          <boxGeometry args={[0.34, 0.9, 0.3]} />
          <meshStandardMaterial color="#374151" roughness={0.5} metalness={0.4} />
        </mesh>
        <mesh position={[0, 0.72, 0.16]}>
          <planeGeometry args={[0.16, 0.08]} />
          <meshStandardMaterial color="#f97316" emissive="#f97316" emissiveIntensity={0.9} toneMapped={false} />
        </mesh>
      </group>
      {/* Stools. */}
      {team.agents.map((a) => {
        const s = hq.spots.get(a.id);
        if (!s) return null;
        return (
          <mesh key={a.id} position={[s.x, 0.2, s.z + 0.05]} castShadow>
            <cylinderGeometry args={[0.19, 0.15, 0.24, 14]} />
            <meshStandardMaterial color={dark ? '#312e81' : '#c4b5fd'} roughness={0.6} />
          </mesh>
        );
      })}
      {/* The walkway and the pad. */}
      <mesh position={[0, 0.012, hq.doorOut.z + pathLen / 2 - 0.3]} rotation={[-Math.PI / 2, 0, 0]}>
        <planeGeometry args={[0.7, pathLen]} />
        <meshStandardMaterial color={HQ_HEX} transparent opacity={0.22} emissive={HQ_HEX} emissiveIntensity={0.3} depthWrite={false} />
      </mesh>
      <group position={hq.padAt}>
        <mesh position={[0, 0.04, 0]} receiveShadow>
          <cylinderGeometry args={[Math.max(0.95, out.length * 0.6), Math.max(1.05, out.length * 0.6 + 0.1), 0.08, 40]} />
          <meshStandardMaterial color={dark ? '#1f2340' : '#ede9fe'} roughness={0.6} />
        </mesh>
        <mesh position={[0, 0.085, 0]} rotation={[-Math.PI / 2, 0, 0]}>
          <ringGeometry args={[Math.max(0.85, out.length * 0.6 - 0.1), Math.max(0.95, out.length * 0.6), 48]} />
          <meshStandardMaterial ref={padGlow} color={HQ_HEX} emissive={HQ_HEX} emissiveIntensity={0.2} side={THREE.DoubleSide} toneMapped={false} />
        </mesh>
        {busy && <pointLight position={[0, 3.2, 0]} intensity={animate ? 7 : 5} distance={6} decay={2} color={HQ_HEX} />}
      </group>
      {partying && animate && <Sparkles count={40} scale={[w + 1, 2.6, d + 1]} position={[0, 2.2, 0]} size={5} speed={0.9} opacity={0.9} color="#fbbf24" />}
      <Html position={[0, HQ_WALL_H + 0.75, -d / 2]} center distanceFactor={16} zIndexRange={[20, 0]}>
        <button type="button" onClick={onFocus} className="floor3d-label focus-ring" style={{ borderColor: HQ_HEX }} title="The harness's own room: the dispatcher and the phase agents — not a team, no tickets. Whoever is on walks out to the pad.">
          <span className="floor3d-dot" style={{ background: HQ_HEX }} />
          <span className="floor3d-label-name">Command center</span>
          <span className="floor3d-label-sub">
            {seats.length === 0
              ? 'harness · no crew this run'
              : out.length > 0
                ? `${out.map((s) => s.id).join(', ')} on the pad · ${inside} inside`
                : `harness · ${seats.length} crew · ${seats.filter((x) => x.spoke).length} have spoken`}
          </span>
        </button>
      </Html>
      {seats.map((seat) => {
        const spot = hq.spots.get(seat.id);
        if (!spot) return null;
        const selected = selection?.kind === 'agent' && selection.id === seat.id;
        return (
          <CrewFigure
            key={seat.id}
            seat={seat}
            spot={spot}
            pad={hq.pads.get(seat.id)}
            hq={hq}
            running={running}
            animate={animate}
            partying={partying}
            selected={selected}
            door={door}
            onSelect={() => onSelect(selected ? null : { kind: 'agent', id: seat.id, team: HARNESS_ID })}
          />
        );
      })}
    </group>
  );
}

/** One of the crew: on a stool inside, or out on the pad when it is their turn. */
function CrewFigure({
  seat,
  spot,
  pad,
  hq,
  running,
  animate,
  partying,
  selected,
  door,
  onSelect,
}: {
  seat: FloorStageSeat;
  spot: THREE.Vector3;
  pad?: THREE.Vector3;
  hq: HQ;
  running: boolean;
  animate: boolean;
  partying: boolean;
  selected: boolean;
  door: { current: { until: number } };
  onSelect: () => void;
}) {
  const active = seat.active && running && !!pad;
  const rig = useRig();
  const lift = useRef<THREE.Group>(null);
  const [hover, setHover] = useState(false);
  const invalidate = useThree((s) => s.invalidate);
  const clock = useThree((s) => s.clock);
  const seed = useMemo(() => seedOf(seat.id), [seat.id]);
  const look = useMemo(() => lookOf(seed), [seed]);
  const phase = (seed % 628) / 100;
  const raw = useMood({ active, running, partying, since: seat.lastAt ?? FLOOR_BORN, seed });
  // No pitch in here: the crew dances instead.
  const mood: Mood = raw === 'football' ? 'dance' : raw;
  const dest = active ? pad! : spot;
  const destKey = `${dest.x.toFixed(2)},${dest.z.toFixed(2)}`;
  const pos = useRef(dest.clone());
  const prev = useRef(dest.clone());
  const walk = useRef<Walk | null>(null);
  const planned = useRef(destKey);
  const moodStart = useRef<{ mood: Mood; at: number }>({ mood, at: -1 });
  const sit = useRef(active || isParty(mood) ? 0 : 1);

  // A new place to be: walk there — through the door when it is on the other side of it.
  useEffect(() => {
    if (planned.current === destKey) return;
    planned.current = destKey;
    const inside = (v: THREE.Vector3) => v.z < hq.d / 2 - 0.1;
    const from = pos.current.clone();
    const pts = [from];
    if (inside(from) && !inside(dest)) pts.push(hq.doorIn, hq.doorOut);
    else if (!inside(from) && inside(dest)) pts.push(hq.doorOut, hq.doorIn);
    pts.push(dest.clone());
    walk.current = animate ? makeWalk(pts, clock.getElapsedTime(), 1.7) : null;
    if (!animate) pos.current.copy(dest);
    invalidate();
  }, [destKey, dest, hq, animate, clock, invalidate]);
  useEffect(() => {
    invalidate();
  }, [hover, selected, mood, invalidate]);

  useFrame(({ clock: c }, delta) => {
    const root = rig.root.current;
    if (!root) return;
    const t = c.getElapsedTime();
    const inside = pos.current.z < hq.d / 2 - 0.1;
    if (moodStart.current.mood !== mood || moodStart.current.at < 0) {
      moodStart.current = { mood, at: t };
      if (mood === 'stroll' && animate && inside && !walk.current) {
        walk.current = makeWalk([pos.current.clone(), spot.clone().add(new THREE.Vector3(0.38, 0, 0.42)), spot.clone().add(new THREE.Vector3(-0.38, 0, 0.42)), spot.clone()], t, 0.6);
      }
    }
    let facing = Math.PI;
    let walking = false;
    if (walk.current && animate) {
      const dir = walkAt(walk.current, t, pos.current);
      if (dir === null) walk.current = null;
      else {
        facing = dir;
        walking = true;
        if (pos.current.distanceTo(hq.doorIn) < 1.1 || pos.current.distanceTo(hq.doorOut) < 1.1) door.current.until = t + 0.4;
        invalidate();
      }
    } else {
      walk.current = null;
      pos.current.copy(dest);
    }
    root.position.copy(pos.current);
    const standing = active || isParty(mood) || walking || !inside;
    if (!animate) {
      root.rotation.y = Math.PI;
      sit.current = standing ? 0 : 1;
      stillPose(rig, mood, sit.current, 0, active);
    } else {
      const speed = delta > 0 ? pos.current.distanceTo(prev.current) / delta : 0;
      root.rotation.y = turnToward(root.rotation.y, facing, 0.15);
      sit.current = THREE.MathUtils.lerp(sit.current, standing ? 0 : 1, 0.14);
      poseRig(rig, { mood, t, since: t - moodStart.current.at, phase, sit: sit.current, speed: walking ? speed : 0, glance: 0, talk: active });
    }
    prev.current.copy(pos.current);
    const target = hover || selected ? 1.1 : 1;
    if (Math.abs(root.scale.x - target) > 0.002) {
      root.scale.lerp(_scale.setScalar(target), 0.2);
      invalidate();
    }
    if (lift.current) lift.current.position.y = (1 - sit.current) * 0.28;
  });

  const pick = hoverPick(onSelect, setHover);
  const glyph = STAGE_GLYPHS[seat.id] ?? STAGE_GLYPHS[seat.phase ?? ''] ?? '🎓';
  const saying = seat.lastMessage && seat.lastMessage.length > 54 ? seat.lastMessage.slice(0, 53) + '…' : seat.lastMessage;
  const named = active || hover || selected;
  const title = `${seat.id}${seat.phase ? ` · ${seat.phase}` : ''} · ${active ? 'on the pad' : MOOD_LABEL[mood]} · ${seat.spoke ? 'has spoken' : 'has not spoken yet'} · click for the dossier`;
  return (
    <group>
      <RigBody rig={rig} look={{ shirt: seat.spoke ? '#c4b5fd' : '#e5e7eb', skin: look.skin, hair: look.hair, ghost: !seat.spoke }} pick={pick}>
        <group ref={lift}>
          <Html position={[0, 1.55, 0]} distanceFactor={12} zIndexRange={[30, 0]} style={{ pointerEvents: 'none' }}>
            <div className={['floor3d-person', selected && 'floor3d-person-selected', hover && 'floor3d-person-hover'].filter(Boolean).join(' ')}>
              {active && (
                <div className="floor3d-bubble" style={{ borderColor: HQ_HEX }}>
                  <span className="floor3d-bubble-head" style={{ color: HQ_HEX }}>{seat.phase ? `${seat.phase} · on air` : 'on air'}</span>
                  <span className="floor3d-bubble-dots" aria-hidden="true"><i /><i /><i /></span>
                  {saying && <span className="floor3d-bubble-say">{saying}</span>}
                </div>
              )}
              {mood === 'nap' && !active && <Zzz />}
              {named ? (
                <span className={seat.spoke ? 'floor3d-name' : 'floor3d-name floor3d-name-waiting'} title={title}>
                  {moodShows(mood) && !active && <span className="floor3d-mood" aria-hidden="true">{MOOD_GLYPH[mood]}</span>}
                  <span aria-hidden="true">{glyph}</span> {seat.id}
                  {seat.phase && <em>{` · ${seat.phase}`}</em>}
                </span>
              ) : (
                // Inside, a chip, not a name tag: the room stays a room.
                <span className={seat.spoke ? 'floor3d-chip' : 'floor3d-chip floor3d-name-waiting'} title={title}>
                  <span aria-hidden="true">{glyph}</span>
                  {moodShows(mood) && <span aria-hidden="true">{MOOD_GLYPH[mood]}</span>}
                </span>
              )}
            </div>
          </Html>
        </group>
      </RigBody>
    </group>
  );
}

/** The team's board on the wall behind the manager: a column per state, a tile per ticket. */
function WallBoard({ team, hex, dark, dir, running }: { team: FloorTeam; hex: string; dark: boolean; dir: THREE.Vector3; running: boolean }) {
  const columns: [TicketState[], string, string][] = [
    [['queued'], 'queued', TICKET_HEX.queued],
    [['working'], 'doing', TICKET_HEX.working],
    [['review'], 'review', TICKET_HEX.review],
    [['blocked', 'failed'], 'stuck', TICKET_HEX.blocked],
    [['done'], 'done', TICKET_HEX.done],
  ];
  const W = BOARD_W;
  const H = BOARD_H;
  const colW = W / columns.length;
  const legH = BOARD_BOTTOM;
  // Behind the manager's chair, facing the table, high enough that the
  // manager's name never crosses it from the camera's side.
  const pos = dir.clone().multiplyScalar(BOARD_BACK).setY(BOARD_BOTTOM + H / 2);
  const yaw = Math.atan2(-dir.x, -dir.z);
  const counts = columns.map(([states]) => team.tickets.filter((t) => states.includes(t.state)).length);
  return (
    <group position={pos} rotation={[0, yaw, 0]}>
      {/* Legs + panel */}
      <mesh position={[-W / 2 + 0.15, -H / 2 - legH / 2, 0]}>
        <boxGeometry args={[0.06, legH, 0.06]} />
        <meshStandardMaterial color="#374151" />
      </mesh>
      <mesh position={[W / 2 - 0.15, -H / 2 - legH / 2, 0]}>
        <boxGeometry args={[0.06, legH, 0.06]} />
        <meshStandardMaterial color="#374151" />
      </mesh>
      <mesh castShadow receiveShadow>
        <boxGeometry args={[W, H, 0.08]} />
        <meshStandardMaterial color={dark ? '#111827' : '#ffffff'} roughness={0.8} />
      </mesh>
      <mesh position={[0, H / 2 - 0.02, 0.045]}>
        <boxGeometry args={[W, 0.06, 0.02]} />
        <meshStandardMaterial color={hex} emissive={hex} emissiveIntensity={0.4} />
      </mesh>
      {columns.map(([states, label, color], ci) => {
        const x = -W / 2 + colW * (ci + 0.5);
        const tickets = team.tickets.filter((t) => states.includes(t.state));
        const tileH = 0.11;
        const tileGap = 0.04;
        const maxTiles = Math.floor((H - 0.55) / (tileH + tileGap));
        const shown = tickets.slice(0, maxTiles);
        return (
          <group key={label} position={[x, 0, 0.045]}>
            {/* The column's cap, in its state's color. */}
            <mesh position={[0, H / 2 - 0.2, 0.01]}>
              <boxGeometry args={[colW - 0.16, 0.05, 0.02]} />
              <meshStandardMaterial color={color} emissive={color} emissiveIntensity={tickets.length > 0 ? 0.5 : 0.05} />
            </mesh>
            {shown.map((t, i) => (
              <mesh key={t.id} position={[0, H / 2 - 0.45 - i * (tileH + tileGap), 0.02]}>
                <boxGeometry args={[colW - 0.12, tileH, 0.03]} />
                <meshStandardMaterial color={TICKET_HEX[t.state]} emissive={TICKET_HEX[t.state]} emissiveIntensity={t.state === 'working' || t.state === 'review' ? 0.5 : 0.1} />
              </mesh>
            ))}
            {ci < columns.length - 1 && (
              <mesh position={[colW / 2, 0, 0.005]}>
                <boxGeometry args={[0.01, H - 0.3, 0.01]} />
                <meshStandardMaterial color={dark ? '#374151' : '#e5e7eb'} />
              </mesh>
            )}
          </group>
        );
      })}
      {/* One line of counts above the board — the columns below are the picture. */}
      <Html position={[0, H / 2 + 0.22, 0.06]} center distanceFactor={10} zIndexRange={[5, 0]} style={{ pointerEvents: 'none' }}>
        <span className="floor3d-board-head" title={columns.map(([, label], i) => `${counts[i]} ${label}`).join(' · ')}>
          {team.tickets.length === 0
            ? running
              ? 'the manager is splitting the work…'
              : 'no tickets'
            : columns.map(([, label, color], i) => (
                <span key={label} className="floor3d-board-cell" aria-label={`${counts[i]} ${label}`}>
                  <i style={{ background: color }} />
                  <b>{counts[i]}</b>
                </span>
              ))}
        </span>
      </Html>
    </group>
  );
}

/** A thread from a ticket to the monitor of whoever holds it; beads run along it while they type. */
function Thread({ from, to, color, live, animate }: { from: THREE.Vector3; to: THREE.Vector3; color: string; live: boolean; animate: boolean }) {
  const mid = useMemo(() => from.clone().lerp(to, 0.5).add(new THREE.Vector3(0, 0.55, 0)), [from, to]);
  const curve = useMemo(() => new THREE.QuadraticBezierCurve3(from, mid, to), [from, mid, to]);
  const beads = useRef<THREE.Mesh[]>([]);
  const N = live ? 2 : 0;
  useFrame(({ clock }) => {
    if (!animate) return;
    const t = clock.getElapsedTime();
    beads.current.forEach((m, i) => {
      if (!m) return;
      const u = ((t * 0.5 + i / 2) % 1 + 1) % 1;
      curve.getPoint(u, m.position);
    });
  });
  return (
    <group>
      <QuadraticBezierLine start={from} end={to} mid={mid} color={color} lineWidth={live ? 1.6 : 0.9} transparent opacity={live ? 0.9 : 0.45} dashed={!live} dashScale={10} />
      {Array.from({ length: N }).map((_, i) => (
        <mesh key={i} ref={(el) => { if (el) beads.current[i] = el; }} position={from}>
          <sphereGeometry args={[0.05, 10, 10]} />
          <meshStandardMaterial color="#fff" emissive={color} emissiveIntensity={2} toneMapped={false} />
        </mesh>
      ))}
    </group>
  );
}

/** The manager sending someone onto a ticket: an arc from the head seat, alive for DISPATCH_S. */
function Dispatch({ from, to, label, at, slot, animate }: { from: THREE.Vector3; to: THREE.Vector3; label: string; at: number; slot: number; animate: boolean }) {
  const curve = useMemo(() => {
    const s = from.clone().add(new THREE.Vector3(0, 1.5, 0));
    const e = to.clone().add(new THREE.Vector3(0, 2.3, 0));
    const m = s.clone().lerp(e, 0.5).add(new THREE.Vector3(0, 2.4, 0));
    return new THREE.QuadraticBezierCurve3(s, m, e);
  }, [from, to]);
  const dot = useRef<THREE.Mesh>(null);
  const grp = useRef<THREE.Group>(null);
  const [alive, setAlive] = useState(true);
  useFrame(() => {
    const age = frame.now - at / 1000;
    if (age > DISPATCH_S) {
      if (alive) setAlive(false);
      return;
    }
    if (dot.current) {
      // The message flies for 1.2s, lands, and dissolves — nothing lingers on the person.
      const flight = animate ? Math.min(1, age / 1.2) : 1;
      curve.getPoint(flight, dot.current.position);
      const fade = age < 1.2 ? 1 : Math.max(0, 1 - (age - 1.2) / 0.6);
      dot.current.scale.setScalar(fade);
      dot.current.visible = fade > 0;
    }
    if (grp.current) grp.current.visible = true;
  });
  if (!alive) return null;
  // The chip stacks over the manager's head, one row per dispatch, so two
  // sent in the same breath never share a spot.
  const chipAt = from.clone().add(new THREE.Vector3(0, 2.45 + slot * 0.36, 0));
  return (
    <group ref={grp}>
      <QuadraticBezierLine start={curve.v0} end={curve.v2} mid={curve.v1} color="#38bdf8" lineWidth={1.6} transparent opacity={0.8} />
      <mesh ref={dot} position={curve.v0}>
        <sphereGeometry args={[0.13, 14, 14]} />
        <meshStandardMaterial color="#e0f2fe" emissive="#38bdf8" emissiveIntensity={2.4} toneMapped={false} />
      </mesh>
      <Html position={chipAt} center distanceFactor={13} zIndexRange={[40, 0]} style={{ pointerEvents: 'none' }}>
        <span className="floor3d-dispatch">👔 → {label}</span>
      </Html>
    </group>
  );
}

/** A burst: a ring that expands and sparks that rise, gone after BURST_S. */
function Burst({ at, position, color, size, animate }: { at: number; position: THREE.Vector3; color: string; size: number; animate: boolean }) {
  const ring = useRef<THREE.Mesh>(null);
  const sparks = useRef<THREE.Mesh[]>([]);
  const [alive, setAlive] = useState(true);
  const N = 8;
  const dirs = useMemo(
    () => Array.from({ length: N }, (_, i) => new THREE.Vector3(Math.cos((i / N) * Math.PI * 2), 1.6 + (i % 3) * 0.3, Math.sin((i / N) * Math.PI * 2))),
    [],
  );
  useFrame(() => {
    const age = frame.now - at / 1000;
    if (age > BURST_S || !animate) {
      if (alive) setAlive(false);
      return;
    }
    const k = age / BURST_S;
    if (ring.current) {
      const s = size * (0.3 + k * 2.4);
      ring.current.scale.set(s, s, s);
      (ring.current.material as THREE.MeshBasicMaterial).opacity = Math.max(0, 1 - k) * 0.9;
    }
    sparks.current.forEach((m, i) => {
      if (!m) return;
      const d = dirs[i];
      m.position.set(d.x * k * size * 1.4, d.y * k * size - 2.2 * k * k * size, d.z * k * size * 1.4);
      (m.material as THREE.MeshStandardMaterial).opacity = Math.max(0, 1 - k);
    });
  });
  if (!alive || !animate) return null;
  return (
    <group position={position}>
      <mesh ref={ring} rotation={[-Math.PI / 2, 0, 0]}>
        <ringGeometry args={[0.3, 0.36, 40]} />
        <meshBasicMaterial color={color} transparent opacity={0.9} side={THREE.DoubleSide} toneMapped={false} />
      </mesh>
      {dirs.map((_, i) => (
        <mesh key={i} ref={(el) => { if (el) sparks.current[i] = el; }}>
          <sphereGeometry args={[0.05 * size, 8, 8]} />
          <meshStandardMaterial color="#fff" emissive={color} emissiveIntensity={2.5} transparent opacity={1} toneMapped={false} />
        </mesh>
      ))}
    </group>
  );
}

function Conduit({ from, to, label, stalled, running, animate }: { from: Placed; to: Placed; label: string; stalled: boolean; running: boolean; animate: boolean }) {
  const start = useMemo(() => from.pos.clone().add(new THREE.Vector3(0, TABLE_Y + 0.4, 0)), [from.pos]);
  const end = useMemo(() => to.pos.clone().add(new THREE.Vector3(0, TABLE_Y + 0.4, 0)), [to.pos]);
  const mid = useMemo(() => start.clone().lerp(end, 0.5).add(new THREE.Vector3(0, 2.6, 0)), [start, end]);
  const curve = useMemo(() => new THREE.QuadraticBezierCurve3(start, mid, end), [start, mid, end]);
  const packets = useRef<THREE.Mesh[]>([]);
  const color = stalled ? '#f59e0b' : '#8b5cf6';
  const N = 3;
  useFrame(({ clock }) => {
    if (!animate) return;
    const t = clock.getElapsedTime();
    const speed = stalled ? 0.05 : running ? 0.28 : 0.1;
    packets.current.forEach((m, i) => {
      if (!m) return;
      const u = ((t * speed + i / N) % 1 + 1) % 1;
      curve.getPoint(u, m.position);
      const s = stalled ? 0.9 + Math.sin(t * 8) * 0.3 : 1;
      m.scale.setScalar(s);
    });
  });
  return (
    <group>
      <QuadraticBezierLine start={start} end={end} mid={mid} color={color} lineWidth={stalled ? 2.5 : 1.8} dashed={stalled} dashScale={6} transparent opacity={0.85} />
      {Array.from({ length: N }).map((_, i) => (
        <mesh key={i} ref={(el) => { if (el) packets.current[i] = el; }} position={start}>
          <sphereGeometry args={[0.11, 12, 12]} />
          <meshStandardMaterial color={color} emissive={color} emissiveIntensity={1.6} toneMapped={false} />
        </mesh>
      ))}
      {/* The label rides a third of the way along, off the stage behind. */}
      <Html position={curve.getPoint(0.3).add(new THREE.Vector3(0, 0.35, 0))} center distanceFactor={16} zIndexRange={[15, 0]} style={{ pointerEvents: 'none' }}>
        <span className="floor3d-conduit" style={{ background: color }}>
          {label.length > 28 ? label.slice(0, 27) + '…' : label}
          {stalled ? ' · waiting' : ''}
        </span>
      </Html>
    </group>
  );
}

function Spark({ handoff, placed, animate }: { handoff: FloorHandoff; placed: Map<string, Placed>; animate: boolean }) {
  const curve = useMemo(() => {
    const find = (id: string) => {
      const at = (p: Placed) => {
        const s = p.seats.get(id);
        return s ? s.pos.clone().add(p.pos) : undefined;
      };
      const home = handoff.team ? placed.get(handoff.team) : undefined;
      if (home) {
        const w = at(home);
        if (w) return w;
      }
      for (const p of placed.values()) {
        const w = at(p);
        if (w) return w;
      }
      return undefined;
    };
    const a = find(handoff.from);
    const b = find(handoff.to);
    if (!a || !b) return null;
    const s = a.clone().add(new THREE.Vector3(0, 1.5, 0));
    const e = b.clone().add(new THREE.Vector3(0, 1.5, 0));
    const m = s.clone().lerp(e, 0.5).add(new THREE.Vector3(0, 1.8, 0));
    return new THREE.QuadraticBezierCurve3(s, m, e);
  }, [handoff.from, handoff.to, handoff.team, placed]);
  const dot = useRef<THREE.Mesh>(null);
  useFrame(({ clock }) => {
    if (!curve || !dot.current) return;
    const u = animate ? (clock.getElapsedTime() * 0.6) % 1 : 1;
    curve.getPoint(u, dot.current.position);
  });
  if (!curve) return null;
  const mid = curve.getPoint(0.5);
  return (
    <group>
      <QuadraticBezierLine start={curve.v0} end={curve.v2} mid={curve.v1} color="#7c3aed" lineWidth={1.4} dashed dashScale={8} transparent opacity={0.7} />
      <mesh ref={dot}>
        <sphereGeometry args={[0.14, 14, 14]} />
        <meshStandardMaterial color="#c4b5fd" emissive="#7c3aed" emissiveIntensity={2.2} toneMapped={false} />
      </mesh>
      <Html position={mid.clone().add(new THREE.Vector3(0, 0.4, 0))} center distanceFactor={13} zIndexRange={[40, 0]} style={{ pointerEvents: 'none' }}>
        <span className="floor3d-spark">
          <b>{handoff.task}</b> → {handoff.to}
          {handoff.reason ? <i> — {handoff.reason.length > 60 ? handoff.reason.slice(0, 59) + '…' : handoff.reason}</i> : null}
        </span>
      </Html>
    </group>
  );
}

function IntegrationPad({ integration, unassigned }: { integration: NonNullable<FloorModel['integration']>; unassigned: number }) {
  const ready = !!integration.ready;
  const color = ready ? '#10b981' : '#94a3b8';
  return (
    <group position={[0, 0, SEAT_R + 3.6]}>
      <mesh position={[0, 0.06, 0]} receiveShadow>
        <cylinderGeometry args={[1.6, 1.8, 0.12, 40]} />
        <meshStandardMaterial color={color} emissive={color} emissiveIntensity={ready ? 0.5 : 0.1} roughness={0.5} />
      </mesh>
      <Html position={[0, 0.5, 0]} center distanceFactor={11} zIndexRange={[8, 0]} style={{ pointerEvents: 'none' }}>
        <span className="floor3d-integration" style={{ borderColor: color }}>
          <b>{ready ? 'ready to join the halves' : 'integration waits'}</b>
          {!ready && integration.reason ? <span> — {integration.reason}</span> : null}
          <br />
          <span className="opacity-70">{integration.acceptance || 'no integration command'}{unassigned > 0 ? ` · ${unassigned} seam ticket${unassigned === 1 ? '' : 's'}` : ''}</span>
        </span>
      </Html>
    </group>
  );
}

/**
 * Eases the orbit target to a focused point — and the camera in to `distance`
 * of it when one is asked for — and hosts the controls. A close focus (a
 * person, a ticket) is nudged toward the camera's left so the subject lands
 * beside the dossier, not under it. The glide stops the moment the user takes
 * the camera back (any drag or wheel).
 */
function CameraRig({ focus, distance, home, homeCam, autoRotate, resetSignal }: { focus: THREE.Vector3 | null; distance: number | null; home: THREE.Vector3; homeCam: THREE.Vector3; autoRotate: boolean; resetSignal: number }) {
  const controls = useRef<OrbitControlsImpl>(null);
  const target = useRef(home.clone());
  const want = useRef<number | null>(null);
  const glide = useRef<THREE.Vector3 | null>(null);
  const { invalidate, camera } = useThree();
  // A camera the user left on a previous visit: restored on mount, and the
  // first "re-frame home" below is skipped so it is not undone a frame later.
  const restoredRef = useRef(false);
  const skipHomeRef = useRef(floorStore.camera !== null);
  // The floor grew or shrank (a stage appeared, a team joined): re-frame it,
  // unless the user is looking at something in particular.
  useEffect(() => {
    if (focus) return;
    if (skipHomeRef.current) {
      skipHomeRef.current = false;
      return;
    }
    glide.current = homeCam.clone();
    invalidate();
  }, [homeCam, focus, invalidate]);
  useEffect(() => {
    const t = focus ? focus.clone() : home.clone();
    if (focus && distance !== null && distance < 10) {
      _right.setFromMatrixColumn(camera.matrixWorld, 0).setY(0).normalize();
      t.sub(_right.multiplyScalar(distance * 0.28));
    }
    target.current.copy(t);
    want.current = focus ? distance : null;
    invalidate();
  }, [focus, distance, home, invalidate, camera]);
  useEffect(() => {
    const c = controls.current;
    if (!c) return undefined;
    // Home is what "reset view" returns to.
    c.saveState();
    if (!restoredRef.current) {
      restoredRef.current = true;
      const saved = floorStore.camera;
      if (saved) {
        camera.position.set(saved.position[0], saved.position[1], saved.position[2]);
        c.target.set(saved.target[0], saved.target[1], saved.target[2]);
        target.current.copy(c.target);
        c.update();
        invalidate();
      }
    }
    const release = () => {
      want.current = null;
      glide.current = null;
    };
    const remember = () => {
      rememberCamera({
        position: [camera.position.x, camera.position.y, camera.position.z],
        target: [c.target.x, c.target.y, c.target.z],
      });
    };
    c.addEventListener('start', release);
    c.addEventListener('end', remember);
    return () => {
      c.removeEventListener('start', release);
      c.removeEventListener('end', remember);
      // Leaving the page keeps the view for the return, glide or not.
      remember();
    };
  }, [camera, invalidate]);
  // reset view: back to the saved home, forgetting the remembered camera.
  const firstReset = useRef(true);
  useEffect(() => {
    if (firstReset.current) {
      firstReset.current = false;
      return;
    }
    const c = controls.current;
    if (!c) return;
    want.current = null;
    glide.current = null;
    c.reset();
    target.current.copy(c.target);
    rememberCamera(null);
    invalidate();
  }, [resetSignal, invalidate]);
  useFrame(() => {
    const c = controls.current;
    if (!c) return;
    let moved = false;
    if (c.target.distanceToSquared(target.current) > 0.0004) {
      c.target.lerp(target.current, 0.08);
      moved = true;
    }
    if (glide.current) {
      if (camera.position.distanceToSquared(glide.current) > 0.01) {
        camera.position.lerp(glide.current, 0.06);
        moved = true;
      } else {
        glide.current = null;
      }
    }
    if (want.current !== null) {
      glide.current = null;
      _dir.copy(camera.position).sub(c.target);
      const have = _dir.length();
      if (Math.abs(have - want.current) > 0.05) {
        _goal.copy(c.target).add(_dir.normalize().multiplyScalar(want.current));
        camera.position.lerp(_goal, 0.08);
        moved = true;
      } else {
        want.current = null;
      }
    }
    if (moved) {
      c.update();
      // On-demand rendering: a glide asks for the next frame itself.
      invalidate();
    }
  });
  return <OrbitControls ref={controls} makeDefault enableDamping dampingFactor={0.08} minDistance={4} maxDistance={70} maxPolarAngle={Math.PI / 2.05} autoRotate={autoRotate} autoRotateSpeed={0.6} target={[home.x, home.y, home.z]} />;
}

function Legend({ floor }: { floor: FloorModel }) {
  return (
    <div className="pointer-events-none flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md bg-white/70 px-2 py-1 text-[10px] text-gray-600 backdrop-blur dark:bg-gray-900/70 dark:text-gray-300">
      <span className="font-semibold uppercase tracking-wider">{floor.summary}</span>
      {LEGEND_STATES.map((state) => (
        <span key={state} className="inline-flex items-center gap-1">
          <span className="inline-block h-2 w-3 rounded-sm" style={{ background: TICKET_HEX[state] }} />
          {TICKET_LABEL[state]}
        </span>
      ))}
    </div>
  );
}
