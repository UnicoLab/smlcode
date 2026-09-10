import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import * as THREE from 'three';
import { Canvas, useFrame, useThree, type ThreeEvent } from '@react-three/fiber';
import { ContactShadows, Float, Html, OrbitControls, QuadraticBezierLine, RoundedBox, Sparkles } from '@react-three/drei';
import type { OrbitControls as OrbitControlsImpl } from 'three-stdlib';
import { teamColor } from '@/components/Board/teamColor';
import type { FloorAgent, FloorHandoff, FloorModel, FloorPhase, FloorPulse, FloorStageSeat, FloorTeam, FloorTicket, TicketState } from './floorModel';
import { TICKET_HEX, TICKET_LABEL, glyphFor, seatTitle, type FloorSelection } from './floorShared';

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
// Everything is clickable: a person or a ticket opens its dossier (owned by
// the wrapper), a table focuses the camera, empty floor clears. The camera is
// the user's: drag to orbit, wheel to zoom, right-drag to pan, "follow" keeps
// whoever is working in the middle. Everything animates on the GPU per frame
// and freezes (frameloop on demand) under prefers-reduced-motion.
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
/** The pipeline's stage: a platform at the back of the hall, behind the boards. */
const STAGE_R = 2.7;
const STAGE_H = 0.22;
const STAGE_Z = -(BOARD_BACK + 4.6);
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
function layout(teams: FloorTeam[]): Placed[] {
  const n = teams.length;
  return teams.map((team, i) => {
    const x = (i - (n - 1) / 2) * TABLE_GAP;
    const z = n <= 1 ? 0 : -Math.abs(i - (n - 1) / 2) * 1.2 + 0.6;
    const pos = new THREE.Vector3(x, 0, z);
    const hex = HEX[teamColor(team.crew ? '' : team.id).name] ?? HEX.gray;
    const seats = new Map<string, Seat>();
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

function nowSeconds(): number {
  return Date.now() / 1000;
}

export default function TeamFloor3D({ floor, running, dark, reducedMotion, selection, onSelect, pulses, onTicket }: TeamFloor3DProps) {
  const placed = useMemo(() => layout(floor.teams), [floor.teams]);
  const byID = useMemo(() => new Map(placed.map((p) => [p.team.id, p])), [placed]);
  const [focus, setFocus] = useState<THREE.Vector3 | null>(null);
  const [focusDistance, setFocusDistance] = useState<number | null>(null);
  const [autoRotate, setAutoRotate] = useState(false);
  const [follow, setFollow] = useState(false);
  const [resetKey, setResetKey] = useState(0);
  const animate = !reducedMotion;

  const width = Math.max(1, placed.length) * TABLE_GAP;
  const hasStage = floor.mode === 'teams' && (floor.stage.length > 0 || !!floor.phase);
  // Frame the tables with their rugs; the stage sits behind them, between the
  // boards (or beside the one board), and comes along for free.
  const edge = (width - TABLE_GAP) / 2 + SEAT_R + 1.2;
  const span = edge * 2;
  const home = useMemo(() => new THREE.Vector3(0, 1.6, hasStage ? -1.5 : 0), [hasStage]);
  const camZ = 5 + span * 0.92;
  const homeCam = useMemo(() => new THREE.Vector3(home.x, camZ * 0.55, camZ + 1), [home, camZ]);

  // Two or more tables leave a gap between their boards: the stage shows
  // through it. One table's board is in the middle, so the stage steps aside.
  const stageAt = useMemo(() => new THREE.Vector3(placed.length >= 2 ? 0 : -(BOARD_W / 2 + STAGE_R + 1.2), 0, STAGE_Z), [placed.length]);
  const stageSeats = useMemo(() => stageLayout(floor.stage, stageAt), [floor.stage, stageAt]);

  // World position of a person or a ticket, for focusing.
  const worldOf = useCallback(
    (sel: FloorSelection): THREE.Vector3 | null => {
      if (!sel) return null;
      if (sel.kind === 'agent') {
        const onStage = stageSeats.get(sel.id);
        if (onStage && !sel.team) return onStage.clone().setY(1.2);
      }
      const home = byID.get(sel.team);
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
      if (sel.kind === 'agent') {
        const onStage = stageSeats.get(sel.id);
        if (onStage) return onStage.clone().setY(1.2);
      }
      return null;
    },
    [byID, placed, stageSeats],
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

  return (
    <div className="relative h-full w-full" data-testid="team-floor-3d">
      <Canvas
        key={resetKey}
        shadows={{ type: THREE.PCFShadowMap }}
        dpr={[1, 1.75]}
        frameloop={reducedMotion ? 'demand' : 'always'}
        camera={{ position: [home.x, camZ * 0.55, camZ + 1], fov: 44, near: 0.1, far: 200 }}
        gl={{ antialias: true, alpha: true, powerPreference: 'high-performance' }}
        onPointerMissed={clear}
      >
        <Suspense fallback={null}>
          <Lights dark={dark} />
          <Ground dark={dark} width={width} animate={animate} />
          {floor.links.map((link) => {
            const a = byID.get(link.from);
            const b = byID.get(link.to);
            if (!a || !b) return null;
            return <Conduit key={link.id} from={a} to={b} label={link.interface} stalled={link.stalled} running={running} animate={animate} />;
          })}
          {placed.map((p) => (
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
              onFocus={() => {
                setFocus(p.pos.clone().setY(0.8));
                setFocusDistance(13);
              }}
            />
          ))}
          {floor.handoffs.map((h) => (
            <Spark key={`${h.task}-${h.from}-${h.to}-${h.at}`} handoff={h} placed={byID} animate={animate} />
          ))}
          {floor.mode === 'teams' && (floor.stage.length > 0 || floor.phase) && (
            <Stage
              seats={floor.stage}
              phase={floor.phase}
              at={stageAt}
              positions={stageSeats}
              running={running}
              animate={animate}
              dark={dark}
              selection={selection}
              onSelect={onSelect}
              onFocus={() => {
                setFocus(stageAt.clone().setY(1));
                setFocusDistance(11);
              }}
            />
          )}
          {floor.mode === 'teams' && placed.length > 1 && floor.integration && (
            <IntegrationPad integration={floor.integration} unassigned={floor.unassigned.length} />
          )}
          <ContactShadows position={[0, 0.01, 0]} opacity={dark ? 0.55 : 0.35} scale={width + 20} blur={2.6} far={5} color={dark ? '#000' : '#4c1d95'} />
          <CameraRig focus={focus} distance={focusDistance} home={home} homeCam={homeCam} autoRotate={autoRotate && animate} />
        </Suspense>
      </Canvas>

      <div className="pointer-events-none absolute inset-x-0 bottom-0 flex flex-wrap items-end justify-between gap-2 p-2">
        <Legend floor={floor} />
        <div className="pointer-events-auto flex items-center gap-1 rounded-md border border-gray-200/80 bg-white/80 p-1 text-[10px] backdrop-blur dark:border-gray-700/80 dark:bg-gray-900/80">
          <button
            type="button"
            onClick={() => setFollow((v) => !v)}
            aria-pressed={follow}
            title="Keep the camera on whoever is working"
            className={follow ? 'focus-ring rounded bg-brand-500 px-1.5 py-0.5 text-white' : 'focus-ring rounded px-1.5 py-0.5 hover:bg-gray-100 dark:hover:bg-gray-800'}
          >
            {follow ? 'following' : 'follow'}
          </button>
          <button type="button" onClick={() => setAutoRotate((v) => !v)} aria-pressed={autoRotate} className="focus-ring rounded px-1.5 py-0.5 hover:bg-gray-100 dark:hover:bg-gray-800">
            {autoRotate ? 'stop spin' : 'spin'}
          </button>
          <button
            type="button"
            onClick={() => {
              clear();
              setFollow(false);
              setResetKey((k) => k + 1);
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

  // Pulses on this table, still fresh enough to draw.
  const mine = pulses.filter((p) => p.team === team.id || (team.crew && !p.team));
  const latestByTicket = new Map<string, FloorPulse>();
  for (const p of mine) if (p.ticket) latestByTicket.set(p.ticket, p);

  // The board stands behind the head seat; the team's sign hangs above it.
  const boardDir = managerPos ? managerPos.pos.clone().setY(0).normalize() : new THREE.Vector3(0, 0, -1);
  const signAt = boardDir.clone().multiplyScalar(BOARD_BACK).setY(BOARD_BOTTOM + BOARD_H + 0.75);

  return (
    <group position={pos}>
      <Rug hex={hex} dark={dark} />
      {activeAgent && animate && <Sparkles count={24} scale={[TABLE_R * 2.4, 1.6, TABLE_R * 2.4]} position={[0, TABLE_Y + 1.1, 0]} size={3} speed={0.6} opacity={0.7} color={hex} />}
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
      {mine.map((p) => {
        const age = nowSeconds() - p.at / 1000;
        if (age > BURST_S) return null;
        const where = p.ticket ? slots.get(p.ticket) : undefined;
        const big = p.kind === 'gate' || p.kind === 'team-complete';
        if (!where && !big) return null;
        return <Burst key={`burst-${p.id}`} at={p.at} position={where ?? new THREE.Vector3(0, TABLE_Y + 0.3, 0)} color={TONE_HEX[p.tone]} size={big ? 2.2 : 1} animate={animate} />;
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
  const pulseAt = pulse ? pulse.at / 1000 : -Infinity;
  const spawned = pulse?.kind === 'ticket-new';

  useFrame(({ clock }) => {
    const t = clock.getElapsedTime();
    const age = nowSeconds() - pulseAt;
    if (mat.current) {
      // A fresh pulse flashes the card white-hot, then it settles into its
      // state's glow: breathing when live, steady when selected or hovered.
      if (animate && age < FLASH_S) {
        mat.current.emissive.set('#ffffff');
        mat.current.emissiveIntensity = 1.6 * (1 - age / FLASH_S);
      } else {
        mat.current.emissive.set(color);
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
  onSelect: () => void;
}) {
  const isManager = agent.seat === 'manager';
  const active = agent.active && running;
  const screen = useRef<THREE.MeshStandardMaterial>(null);
  const body = useRef<THREE.Group>(null);
  const head = useRef<THREE.Group>(null);
  const flash = useRef<THREE.Mesh>(null);
  const halo = useRef<THREE.Mesh>(null);
  const pulse = useRef<THREE.Mesh>(null);
  const code = useRef<THREE.Mesh[]>([]);
  const flashStart = useRef<number>(-1);
  const wasActive = useRef(false);
  const [hover, setHover] = useState(false);

  // A flash ring when this person starts working: the "message received"
  // cue, once per activation.
  useEffect(() => {
    if (active && !wasActive.current) flashStart.current = performance.now();
    wasActive.current = active;
  }, [active]);

  // Seat faces the table centre: rotate the whole person so its screen is
  // between them and the table.
  const yaw = Math.atan2(-pos.x, -pos.z);
  const headYaw = useMemo(() => {
    if (!lookAt) return 0;
    const local = lookAt.clone().sub(pos).applyAxisAngle(UP, -yaw);
    return THREE.MathUtils.clamp(Math.atan2(-local.x, -local.z), -1.1, 1.1);
  }, [lookAt, pos, yaw]);

  useFrame(({ clock }) => {
    const t = clock.getElapsedTime();
    if (screen.current) {
      screen.current.emissiveIntensity = active ? (animate ? 1.2 + Math.sin(t * 6) * 0.5 : 1.4) : 0.12;
    }
    if (body.current) {
      if (animate) {
        // Typing: a small nod while active; a slow breath otherwise.
        body.current.position.y = active ? Math.abs(Math.sin(t * 9)) * 0.03 : Math.sin(t * 1.2 + angle) * 0.01;
        body.current.rotation.x = active ? Math.sin(t * 9) * 0.04 : 0;
      }
      const target = hover || selected ? 1.08 : 1;
      body.current.scale.lerp(new THREE.Vector3(target, target, target), 0.2);
    }
    if (head.current) {
      // Idle people glance at whoever is working; the worker watches the screen.
      const want = active ? 0 : headYaw;
      head.current.rotation.y = animate ? THREE.MathUtils.lerp(head.current.rotation.y, want, 0.06) : want;
    }
    if (flash.current) {
      const age = flashStart.current < 0 ? Infinity : (performance.now() - flashStart.current) / 1000;
      const visible = age < 1.2 && animate;
      flash.current.visible = visible;
      if (visible) {
        const s = 0.6 + age * 2.2;
        flash.current.scale.set(s, s, s);
        (flash.current.material as THREE.MeshBasicMaterial).opacity = Math.max(0, 0.8 - age * 0.7);
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
  const skin = isManager ? hex : '#e5e7eb';
  const pick = {
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
      {/* Person */}
      <group ref={body}>
        <mesh position={[0, 0.62, 0.1]} castShadow {...pick}>
          <capsuleGeometry args={[0.2, 0.42, 6, 14]} />
          <meshStandardMaterial color={isManager ? hex : agent.borrowed ? '#e5e7eb' : '#c7d2fe'} roughness={0.55} transparent={!!agent.borrowed} opacity={agent.borrowed ? 0.75 : 1} />
        </mesh>
        <group ref={head} position={[0, 1.13, 0.1]}>
          <mesh castShadow {...pick}>
            <sphereGeometry args={[0.19, 20, 20]} />
            <meshStandardMaterial color={skin} roughness={0.5} />
          </mesh>
          {/* Eyes, so a turned head reads as a glance. */}
          <mesh position={[-0.06, 0.03, -0.165]}>
            <sphereGeometry args={[0.025, 8, 8]} />
            <meshBasicMaterial color="#111827" />
          </mesh>
          <mesh position={[0.06, 0.03, -0.165]}>
            <sphereGeometry args={[0.025, 8, 8]} />
            <meshBasicMaterial color="#111827" />
          </mesh>
          {isManager && (
            <mesh position={[0, 0.29, 0]} rotation={[Math.PI / 2, 0, 0]}>
              <torusGeometry args={[0.13, 0.03, 10, 24]} />
              <meshStandardMaterial color="#fbbf24" emissive="#fbbf24" emissiveIntensity={0.6} metalness={0.6} roughness={0.3} />
            </mesh>
          )}
        </group>
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
      {/* A mug on the idle desk; it goes when the typing starts. */}
      {!active && (
        <mesh position={[0.26, 0.78, -0.4]} castShadow>
          <cylinderGeometry args={[0.04, 0.035, 0.07, 10]} />
          <meshStandardMaterial color={isManager ? '#fbbf24' : '#f9a8d4'} roughness={0.6} />
        </mesh>
      )}
      {/* Activation flash. */}
      <mesh ref={flash} position={[0, 1.1, 0.1]} rotation={[-Math.PI / 2, 0, 0]} visible={false}>
        <ringGeometry args={[0.35, 0.42, 32]} />
        <meshBasicMaterial color={hex} transparent opacity={0.8} side={THREE.DoubleSide} />
      </mesh>
      {/* Name + the pop-out when working. */}
      <Html position={[0, tall ? 2.0 : 1.4, 0.1]} distanceFactor={12} zIndexRange={[30, 0]} style={{ pointerEvents: 'none' }}>
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
          <span className={isManager ? 'floor3d-name floor3d-name-manager' : 'floor3d-name'} title={`${agent.id} · ${seatTitle(agent, team)}${agent.touched ? ` · ${agent.touched} tickets touched` : ''} · click for the dossier`}>
            <span aria-hidden="true">{glyphFor(agent)}</span> {label}
            {isManager && <em>{team.managerDefault ? ' · manager (default)' : ' · manager'}</em>}
            {agent.borrowed && <em>{` · ${agent.seat} from the ${agent.borrowed}`}</em>}
          </span>
        </div>
      </Html>
    </group>
  );
}

/** Standing places on the stage: an arc facing the camera, the newest speaker in the middle. */
function stageLayout(seats: FloorStageSeat[], at: THREE.Vector3): Map<string, THREE.Vector3> {
  const out = new Map<string, THREE.Vector3>();
  const n = seats.length;
  if (n === 0) return out;
  const span = Math.min(Math.PI * 0.95, 0.9 * Math.max(n - 1, 0) + 0.001);
  seats.forEach((s, k) => {
    const f = n === 1 ? 0.5 : k / (n - 1);
    const angle = Math.PI / 2 - span / 2 + f * span;
    out.set(s.id, new THREE.Vector3(at.x + Math.cos(angle) * (STAGE_R - 0.5), STAGE_H, at.z + Math.sin(angle) * (STAGE_R - 0.5) * 0.55 - 0.5));
  });
  return out;
}

/**
 * The pipeline's stage: where the run's thinking happens between the tables'
 * typing. A platform with a screen naming the phase the run is in and what is
 * being said, and the phase agents — planner, splitter, architect… — standing
 * at lecterns; whoever is speaking is lit, the rest wait in the wings.
 */
function Stage({
  seats,
  phase,
  at,
  positions,
  running,
  animate,
  dark,
  selection,
  onSelect,
  onFocus,
}: {
  seats: FloorStageSeat[];
  phase: FloorPhase | null;
  at: THREE.Vector3;
  positions: Map<string, THREE.Vector3>;
  running: boolean;
  animate: boolean;
  dark: boolean;
  selection: FloorSelection;
  onSelect: (sel: FloorSelection) => void;
  onFocus: () => void;
}) {
  const live = running && !!phase;
  const glow = useRef<THREE.MeshStandardMaterial>(null);
  useFrame(({ clock }) => {
    if (!glow.current) return;
    const t = clock.getElapsedTime();
    glow.current.emissiveIntensity = live ? (animate ? 0.55 + Math.sin(t * 2.2) * 0.3 : 0.7) : 0.12;
  });
  const message = phase?.message && phase.message.length > 90 ? phase.message.slice(0, 89) + '…' : phase?.message;
  return (
    <group position={at}>
      {/* Platform */}
      <mesh position={[0, STAGE_H / 2, 0]} castShadow receiveShadow onClick={(e) => { e.stopPropagation(); onFocus(); }}>
        <cylinderGeometry args={[STAGE_R, STAGE_R + 0.25, STAGE_H, 48]} />
        <meshStandardMaterial color={dark ? '#1f2340' : '#e9e5f7'} roughness={0.7} />
      </mesh>
      <mesh position={[0, STAGE_H + 0.005, 0]} rotation={[-Math.PI / 2, 0, 0]}>
        <ringGeometry args={[STAGE_R - 0.1, STAGE_R, 64]} />
        <meshStandardMaterial ref={glow} color="#8b5cf6" emissive="#8b5cf6" emissiveIntensity={0.3} side={THREE.DoubleSide} />
      </mesh>
      {/* The screen at the back of the stage. */}
      <group position={[0, 2.1, -STAGE_R + 0.2]}>
        <mesh position={[-1.3, -1.0, 0]}>
          <boxGeometry args={[0.06, 1.9, 0.06]} />
          <meshStandardMaterial color="#374151" />
        </mesh>
        <mesh position={[1.3, -1.0, 0]}>
          <boxGeometry args={[0.06, 1.9, 0.06]} />
          <meshStandardMaterial color="#374151" />
        </mesh>
        <mesh castShadow>
          <boxGeometry args={[3.2, 1.5, 0.08]} />
          <meshStandardMaterial color={dark ? '#0b0f19' : '#111827'} roughness={0.4} metalness={0.4} />
        </mesh>
        <mesh position={[0, 0, 0.045]}>
          <planeGeometry args={[3.0, 1.3]} />
          <meshStandardMaterial color={live ? '#312e81' : '#1e293b'} emissive={live ? '#4c1d95' : '#0f172a'} emissiveIntensity={live ? 0.8 : 0.2} toneMapped={false} />
        </mesh>
        <Html position={[0, 0, 0.06]} center distanceFactor={12} zIndexRange={[20, 0]} style={{ pointerEvents: 'none' }}>
          <div className="floor3d-screen">
            <span className="floor3d-screen-kicker">pipeline</span>
            <span className="floor3d-screen-phase">{phase ? phase.id : running ? 'starting' : 'idle'}</span>
            {phase?.agent && <span className="floor3d-screen-who">{phase.agent}</span>}
            {message && <span className="floor3d-screen-say">{message}</span>}
          </div>
        </Html>
      </group>
      <Html position={[0, 3.35, -STAGE_R + 0.2]} center distanceFactor={16} zIndexRange={[20, 0]}>
        <button type="button" onClick={onFocus} className="floor3d-label focus-ring" style={{ borderColor: '#8b5cf6' }} title="The pipeline's own people: the phases between the tables' work">
          <span className="floor3d-dot" style={{ background: '#8b5cf6' }} />
          <span className="floor3d-label-name">Pipeline</span>
          <span className="floor3d-label-sub">{seats.length === 0 ? 'no phase agents this run' : `${seats.filter((x) => x.spoke).length}/${seats.length} have spoken`}</span>
        </button>
      </Html>
      {seats.map((seat, i) => {
        const p = positions.get(seat.id);
        if (!p) return null;
        const selected = selection?.kind === 'agent' && selection.id === seat.id;
        return (
          <StageFigure
            key={seat.id}
            seat={seat}
            pos={p.clone().sub(at)}
            tall={i % 2 === 1}
            running={running}
            animate={animate}
            selected={selected}
            onSelect={() => onSelect(selected ? null : { kind: 'agent', id: seat.id, team: '' })}
          />
        );
      })}
    </group>
  );
}

/** One person standing at a lectern on the stage. */
function StageFigure({ seat, pos, tall, running, animate, selected, onSelect }: { seat: FloorStageSeat; pos: THREE.Vector3; tall: boolean; running: boolean; animate: boolean; selected: boolean; onSelect: () => void }) {
  const active = seat.active && running;
  const body = useRef<THREE.Group>(null);
  const ring = useRef<THREE.Mesh>(null);
  const [hover, setHover] = useState(false);
  const hex = '#8b5cf6';
  const glyph = ROLE_GLYPH_STAGE[seat.id] ?? ROLE_GLYPH_STAGE[seat.phase ?? ''] ?? '🎓';
  useFrame(({ clock }) => {
    const t = clock.getElapsedTime();
    if (body.current) {
      if (animate) {
        // Speaking: a lively sway; waiting: a slow breath; dim before they have spoken.
        body.current.position.y = active ? Math.abs(Math.sin(t * 5)) * 0.04 : Math.sin(t * 1.1 + pos.x) * 0.01;
        body.current.rotation.z = active ? Math.sin(t * 2.5) * 0.05 : 0;
      }
      const target = hover || selected ? 1.08 : 1;
      body.current.scale.lerp(new THREE.Vector3(target, target, target), 0.2);
    }
    if (ring.current) {
      ring.current.visible = active || selected;
      if (animate) {
        const k = (t * 0.9) % 1;
        const s = active ? 0.8 + k * 0.9 : 1;
        ring.current.scale.set(s, s, s);
        (ring.current.material as THREE.MeshBasicMaterial).opacity = active ? 0.55 * (1 - k) : 0.9;
      }
    }
  });
  const pick = {
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
  const saying = seat.lastMessage && seat.lastMessage.length > 54 ? seat.lastMessage.slice(0, 53) + '…' : seat.lastMessage;
  const tint = seat.spoke ? '#ddd6fe' : '#e5e7eb';
  return (
    <group position={pos}>
      <mesh ref={ring} position={[0, 0.01, 0]} rotation={[-Math.PI / 2, 0, 0]} visible={false}>
        <ringGeometry args={[0.55, 0.64, 48]} />
        <meshBasicMaterial color={selected && !active ? SELECT : hex} transparent opacity={0.6} side={THREE.DoubleSide} toneMapped={false} />
      </mesh>
      {active && <pointLight position={[0, 3.2, 0]} intensity={animate ? 6 : 4} distance={5.5} decay={2} color={hex} />}
      {/* Lectern */}
      <mesh position={[0, 0.55, 0.45]} castShadow {...pick}>
        <boxGeometry args={[0.5, 1.1, 0.3]} />
        <meshStandardMaterial color="#1f2937" roughness={0.6} metalness={0.3} />
      </mesh>
      <mesh position={[0, 1.12, 0.42]} rotation={[-0.5, 0, 0]} {...pick}>
        <boxGeometry args={[0.56, 0.05, 0.36]} />
        <meshStandardMaterial color={active ? hex : '#374151'} emissive={active ? hex : '#000'} emissiveIntensity={active ? 0.7 : 0} />
      </mesh>
      <group ref={body}>
        <mesh position={[0, 0.8, 0]} castShadow {...pick}>
          <capsuleGeometry args={[0.2, 0.7, 6, 14]} />
          <meshStandardMaterial color={tint} roughness={0.55} transparent={!seat.spoke} opacity={seat.spoke ? 1 : 0.7} />
        </mesh>
        <mesh position={[0, 1.45, 0]} castShadow {...pick}>
          <sphereGeometry args={[0.19, 20, 20]} />
          <meshStandardMaterial color={seat.spoke ? '#f5f3ff' : '#e5e7eb'} roughness={0.5} transparent={!seat.spoke} opacity={seat.spoke ? 1 : 0.7} />
        </mesh>
      </group>
      <Html position={[0, tall ? 2.35 : 1.75, 0]} distanceFactor={12} zIndexRange={[30, 0]} style={{ pointerEvents: 'none' }}>
        <div className={['floor3d-person', selected && 'floor3d-person-selected', hover && 'floor3d-person-hover'].filter(Boolean).join(' ')}>
          {active && (
            <div className="floor3d-bubble" style={{ borderColor: hex }}>
              <span className="floor3d-bubble-head" style={{ color: hex }}>{seat.phase ? `${seat.phase} · speaking` : 'speaking'}</span>
              <span className="floor3d-bubble-dots" aria-hidden="true"><i /><i /><i /></span>
              {saying && <span className="floor3d-bubble-say">{saying}</span>}
            </div>
          )}
          <span className={seat.spoke ? 'floor3d-name' : 'floor3d-name floor3d-name-waiting'} title={`${seat.id}${seat.phase ? ` · ${seat.phase}` : ''} · ${seat.spoke ? 'has spoken' : 'has not spoken yet'} · click for the dossier`}>
            <span aria-hidden="true">{glyph}</span> {seat.id}
            {seat.phase && <em>{` · ${seat.phase}`}</em>}
          </span>
        </div>
      </Html>
    </group>
  );
}

const ROLE_GLYPH_STAGE: Record<string, string> = {
  planner: '📋',
  plan: '📋',
  splitter: '✂️',
  split: '✂️',
  explorer: '🔍',
  explore: '🔍',
  architect: '🏗️',
  coordinator: '🎯',
  coord: '🎯',
  docs: '📖',
  memory: '💾',
  context: '📝',
  composer: '🎼',
  clarify: '💬',
  skills: '🧰',
  learn: '🎓',
  polish: '✨',
  qa: '🧪',
  test: '🧪',
};

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
      m.position.copy(curve.getPoint(u));
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
    const age = nowSeconds() - at / 1000;
    if (age > DISPATCH_S) {
      if (alive) setAlive(false);
      return;
    }
    if (dot.current) {
      // The message flies for 1.2s, lands, and dissolves — nothing lingers on the person.
      const flight = animate ? Math.min(1, age / 1.2) : 1;
      dot.current.position.copy(curve.getPoint(flight));
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
    const age = nowSeconds() - at / 1000;
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
      m.position.copy(curve.getPoint(u));
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
    dot.current.position.copy(curve.getPoint(u));
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
function CameraRig({ focus, distance, home, homeCam, autoRotate }: { focus: THREE.Vector3 | null; distance: number | null; home: THREE.Vector3; homeCam: THREE.Vector3; autoRotate: boolean }) {
  const controls = useRef<OrbitControlsImpl>(null);
  const target = useRef(home.clone());
  const want = useRef<number | null>(null);
  const glide = useRef<THREE.Vector3 | null>(null);
  const { invalidate, camera } = useThree();
  // The floor grew or shrank (a stage appeared, a team joined): re-frame it,
  // unless the user is looking at something in particular.
  useEffect(() => {
    if (focus) return;
    glide.current = homeCam.clone();
    invalidate();
  }, [homeCam, focus, invalidate]);
  useEffect(() => {
    const t = focus ? focus.clone() : home.clone();
    if (focus && distance !== null && distance < 10) {
      const right = new THREE.Vector3().setFromMatrixColumn(camera.matrixWorld, 0).setY(0).normalize();
      t.sub(right.multiplyScalar(distance * 0.28));
    }
    target.current.copy(t);
    want.current = focus ? distance : null;
    invalidate();
  }, [focus, distance, home, invalidate, camera]);
  useEffect(() => {
    const c = controls.current;
    if (!c) return undefined;
    const release = () => {
      want.current = null;
      glide.current = null;
    };
    c.addEventListener('start', release);
    return () => c.removeEventListener('start', release);
  }, []);
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
      const dir = camera.position.clone().sub(c.target);
      const have = dir.length();
      if (Math.abs(have - want.current) > 0.05) {
        const goal = c.target.clone().add(dir.normalize().multiplyScalar(want.current));
        camera.position.lerp(goal, 0.08);
        moved = true;
      } else {
        want.current = null;
      }
    }
    if (moved) c.update();
  });
  return <OrbitControls ref={controls} makeDefault enableDamping dampingFactor={0.08} minDistance={4} maxDistance={70} maxPolarAngle={Math.PI / 2.05} autoRotate={autoRotate} autoRotateSpeed={0.6} target={[home.x, home.y, home.z]} />;
}

function Legend({ floor }: { floor: FloorModel }) {
  const items: [TicketState, string][] = [
    ['working', 'in progress'],
    ['review', 'in review'],
    ['blocked', 'blocked'],
    ['done', 'done'],
    ['queued', 'queued'],
  ];
  return (
    <div className="pointer-events-none flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md bg-white/70 px-2 py-1 text-[10px] text-gray-600 backdrop-blur dark:bg-gray-900/70 dark:text-gray-300">
      <span className="font-semibold uppercase tracking-wider">{floor.summary}</span>
      {items.map(([state, label]) => (
        <span key={state} className="inline-flex items-center gap-1">
          <span className="inline-block h-2 w-3 rounded-sm" style={{ background: TICKET_HEX[state] }} />
          {label}
        </span>
      ))}
    </div>
  );
}
