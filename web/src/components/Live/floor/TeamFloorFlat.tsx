import { useEffect, useMemo, useState } from 'react';
import clsx from 'clsx';
import { teamColor } from '@/components/Board/teamColor';
import type { FloorAgent, FloorHandoff, FloorModel, FloorPhase, FloorPulse, FloorStageSeat, FloorTeam, FloorTicket, TicketState } from './floorModel';
import { LEGEND_STATES, TICKET_LABEL, ago, glyphFor, type FloorSelection } from './floorShared';

// ── The team floor ───────────────────────────────────────────────────────
//
// A run is people on islands. Each team is a raised platform with its manager
// at the head, its members around it and its tickets on the desk; the frozen
// contract runs between platforms as conduits, and traffic flows along them
// from provider to consumer. Whoever is working glows and says what on, a
// consumer waiting on a clause it has not been given turns its conduit amber,
// a half that proved itself gets a green rim, and a manager moving a ticket
// from one person to another is a spark that crosses the island.
//
// It is drawn in SVG under a CSS perspective — a tilted, softly lit floor
// rather than a chart — because the page exists to be watched for eleven
// minutes at a time, and a thing that breathes reads as alive where a table
// reads as stuck. Every motion is CSS, so `prefers-reduced-motion` (honored
// globally) freezes it to a still picture with nothing lost.

export interface TeamFloorFlatProps {
  floor: FloorModel;
  running: boolean;
  /** Current time, injected for tests. */
  now?: number;
  /** Optional: what to do when a ticket is clicked (e.g. focus it in the rail). */
  onTicket?: (id: string) => void;
  /** The clicked person or ticket, drawn highlighted. */
  selection?: FloorSelection;
  onSelect?: (sel: FloorSelection) => void;
  /** What just changed; the flat stage flashes the tickets concerned. */
  pulses?: FloorPulse[];
}

const HEX: Record<string, string> = {
  teal: '#14b8a6',
  fuchsia: '#d946ef',
  cyan: '#06b6d4',
  rose: '#f43f5e',
  lime: '#84cc16',
  purple: '#a855f7',
  gray: '#9ca3af',
};

const TICKET_FILL: Record<TicketState, string> = {
  queued: '#cbd5e1',
  working: '#f59e0b',
  review: '#f97316',
  blocked: '#ef4444',
  done: '#10b981',
  failed: '#dc2626',
};

// Stage geometry, in SVG units. The viewBox scales to the container.
const W = 1000;
const H = 560;
const ISLAND_RX = 150;
const ISLAND_RY = 78;
const ISLAND_DEPTH = 16;

interface Placed {
  team: FloorTeam;
  cx: number;
  cy: number;
  hex: string;
  agentPos: Map<string, { x: number; y: number }>;
}

/** Islands are laid out on a shallow arc, so three read as a floor and not a row. */
function layout(teams: FloorTeam[]): Placed[] {
  const n = teams.length;
  return teams.map((team, i) => {
    const t = n === 1 ? 0.5 : i / (n - 1);
    const cx = n === 1 ? W / 2 : 190 + t * (W - 380);
    // Alternate rows so conduits have room to curve.
    const cy = n <= 2 ? H / 2 + 10 : 200 + (i % 2) * 190;
    const hex = HEX[teamColor(team.crew ? '' : team.id).name] ?? HEX.gray;
    const agentPos = new Map<string, { x: number; y: number }>();
    // Manager at the head; everyone else spaced along the front edge of the
    // island, where the tickets are within reach.
    const others = team.agents.filter((a) => a.seat !== 'manager');
    const manager = team.agents.find((a) => a.seat === 'manager');
    if (manager) agentPos.set(manager.id, { x: cx, y: cy - ISLAND_RY - 34 });
    const spread = Math.min(ISLAND_RX * 1.6, 56 * Math.max(others.length, 1));
    others.forEach((a, k) => {
      const f = others.length === 1 ? 0.5 : k / (others.length - 1);
      agentPos.set(a.id, { x: cx - spread / 2 + f * spread, y: cy + ISLAND_RY - 6 });
    });
    return { team, cx, cy, hex, agentPos };
  });
}

export default function TeamFloorFlat({ floor, running, now, onTicket, selection = null, onSelect, pulses }: TeamFloorFlatProps) {
  const placed = useMemo(() => layout(floor.teams), [floor.teams]);
  const byID = useMemo(() => new Map(placed.map((p) => [p.team.id, p])), [placed]);

  // A clock, for the "on this for 2m14s" bubble and for expiring handoffs.
  const [tick, setTick] = useState(() => now ?? Date.now());
  useEffect(() => {
    if (now !== undefined) {
      setTick(now);
      return undefined;
    }
    if (!running) return undefined;
    const id = window.setInterval(() => setTick(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [running, now]);

  // Tickets with a pulse still on them flash, whichever kind it was.
  const flashing = useMemo(() => new Set((pulses ?? []).filter((p) => p.ticket && tick - p.at < 4000).map((p) => p.ticket!)), [pulses, tick]);

  if (floor.mode === 'idle') {
    return <IdleFloor />;
  }

  return (
    <div className="floor-stage relative h-full w-full overflow-hidden" data-testid="team-floor">
      <div className="floor-ground absolute inset-0" aria-hidden="true" />
      {floor.mode === 'teams' && (floor.stage.length > 0 || floor.phase) && (
        <PipelineStrip stage={floor.stage} phase={floor.phase} running={running} selection={selection} onSelect={onSelect} />
      )}
      <div className="floor-tilt absolute inset-0">
        <svg
          viewBox={`0 0 ${W} ${H}`}
          preserveAspectRatio="xMidYMid meet"
          className="h-full w-full"
          role="img"
          aria-label={floor.mode === 'teams' ? `${floor.teams.length} teams on the floor` : 'the pipeline crew'}
        >
          <defs>
            <filter id="floor-glow" x="-50%" y="-50%" width="200%" height="200%">
              <feGaussianBlur stdDeviation="6" result="blur" />
              <feMerge>
                <feMergeNode in="blur" />
                <feMergeNode in="SourceGraphic" />
              </feMerge>
            </filter>
            <filter id="floor-shadow" x="-20%" y="-20%" width="140%" height="160%">
              <feDropShadow dx="0" dy="10" stdDeviation="8" floodColor="#000" floodOpacity="0.18" />
            </filter>
            {placed.map((p) => (
              <radialGradient key={p.team.id} id={`island-${cssID(p.team.id)}`} cx="50%" cy="35%" r="75%">
                <stop offset="0%" stopColor={p.hex} stopOpacity="0.34" />
                <stop offset="70%" stopColor={p.hex} stopOpacity="0.14" />
                <stop offset="100%" stopColor={p.hex} stopOpacity="0.06" />
              </radialGradient>
            ))}
          </defs>

          {/* Conduits first, under the islands. */}
          {floor.links.map((link) => {
            const a = byID.get(link.from);
            const b = byID.get(link.to);
            if (!a || !b) return null;
            return <Conduit key={link.id} from={a} to={b} label={link.interface} stalled={link.stalled} running={running} />;
          })}

          {placed.map((p) => (
            <Island key={p.team.id} placed={p} running={running} tick={tick} onTicket={onTicket} selection={selection} onSelect={onSelect} flashing={flashing} />
          ))}

          {floor.handoffs.map((h) => (
            <Handoff key={`${h.task}-${h.from}-${h.to}-${h.at}`} handoff={h} placed={byID} tick={tick} />
          ))}

          {floor.mode === 'teams' && placed.length > 1 && floor.integration && (
            <IntegrationPlate integration={floor.integration} unassigned={floor.unassigned.length} />
          )}
        </svg>
      </div>

      <Legend floor={floor} />
    </div>
  );
}

// ── Pieces ───────────────────────────────────────────────────────────────

function Island({
  placed,
  running,
  tick,
  onTicket,
  selection,
  onSelect,
  flashing,
}: {
  placed: Placed;
  running: boolean;
  tick: number;
  onTicket?: (id: string) => void;
  selection: FloorSelection;
  onSelect?: (sel: FloorSelection) => void;
  flashing: Set<string>;
}) {
  const { team, cx, cy, hex, agentPos } = placed;
  const pct = team.total > 0 ? Math.round((team.done / team.total) * 100) : 0;
  const rim =
    team.gate === 'green' ? '#10b981' : team.gate === 'red' ? '#ef4444' : team.waitingOn.length > 0 ? '#f59e0b' : hex;
  const rimWidth = team.gate !== '' || team.waitingOn.length > 0 ? 3.5 : 2;
  const active = team.agents.some((a) => a.active);

  return (
    <g data-testid={`island-${team.id}`} className={clsx('floor-island', active && running && 'floor-island-active')}>
      {/* Thickness, then the top face. */}
      <ellipse cx={cx} cy={cy + ISLAND_DEPTH} rx={ISLAND_RX} ry={ISLAND_RY} fill={hex} opacity="0.22" filter="url(#floor-shadow)" />
      <ellipse cx={cx} cy={cy} rx={ISLAND_RX} ry={ISLAND_RY} fill={`url(#island-${cssID(team.id)})`} stroke={rim} strokeWidth={rimWidth} className="floor-face" />
      {team.waitingOn.length > 0 && (
        <ellipse cx={cx} cy={cy} rx={ISLAND_RX + 8} ry={ISLAND_RY + 6} fill="none" stroke="#f59e0b" strokeWidth="1.5" strokeDasharray="6 8" className="floor-waiting" />
      )}

      {/* Name + progress ring on the desk. */}
      <text x={cx} y={cy - 34} textAnchor="middle" className="floor-title" fill="currentColor">
        {team.name}
      </text>
      <text x={cx} y={cy - 18} textAnchor="middle" className="floor-sub" fill="currentColor">
        {team.total > 0 ? `${team.done}/${team.total} done` : running ? 'no tickets yet' : 'idle'}
        {team.blocked > 0 ? ` · ${team.blocked} blocked` : ''}
        {team.gate === 'green' ? ' · proved' : team.gate === 'red' ? ' · RED' : team.gate === 'unverified' ? ' · unverified' : ''}
      </text>
      <ProgressArc cx={cx} cy={cy + 2} r={13} pct={pct} color={hex} />

      {/* Tickets, stacked on the desk in state order. */}
      <Tickets team={team} cx={cx} cy={cy} onTicket={onTicket} onSelect={onSelect} selected={selection?.kind === 'ticket' ? selection.id : ''} flashing={flashing} />

      {/* People. */}
      {team.agents.map((a) => {
        const pos = agentPos.get(a.id);
        if (!pos) return null;
        return <Avatar key={a.id} agent={a} x={pos.x} y={pos.y} hex={hex} running={running} tick={tick} team={team} selected={selection?.kind === 'agent' && selection.id === a.id && selection.team === team.id} onSelect={onSelect} />;
      })}

      {team.waitingOn.length > 0 && (
        <text x={cx} y={cy + ISLAND_RY + 46} textAnchor="middle" className="floor-note" fill="#d97706">
          waiting on {team.waitingOn.join(', ')}
        </text>
      )}
    </g>
  );
}

function ProgressArc({ cx, cy, r, pct, color }: { cx: number; cy: number; r: number; pct: number; color: string }) {
  const c = 2 * Math.PI * r;
  return (
    <g transform={`translate(${cx - ISLAND_RX + 26} ${cy - 22})`}>
      <circle r={r} fill="none" stroke={color} strokeOpacity="0.2" strokeWidth="4" />
      <circle
        r={r}
        fill="none"
        stroke={color}
        strokeWidth="4"
        strokeLinecap="round"
        strokeDasharray={`${(pct / 100) * c} ${c}`}
        transform="rotate(-90)"
        className="floor-arc"
      />
      <text y="3.5" textAnchor="middle" className="floor-pct" fill="currentColor">
        {pct}%
      </text>
    </g>
  );
}

const ORDER: TicketState[] = ['working', 'review', 'blocked', 'failed', 'queued', 'done'];

function Tickets({ team, cx, cy, onTicket, onSelect, selected, flashing }: { team: FloorTeam; cx: number; cy: number; onTicket?: (id: string) => void; onSelect?: (sel: FloorSelection) => void; selected: string; flashing: Set<string> }) {
  const sorted = [...team.tickets].sort((a, b) => ORDER.indexOf(a.state) - ORDER.indexOf(b.state));
  const shown = sorted.slice(0, 8);
  const more = sorted.length - shown.length;
  const perRow = 4;
  const w = 52;
  const h = 18;
  const gap = 6;
  const rows = Math.ceil(shown.length / perRow);
  const startY = cy + 14 - ((rows - 1) * (h + gap)) / 2;
  return (
    <g>
      {shown.map((t, i) => {
        const row = Math.floor(i / perRow);
        const inRow = Math.min(perRow, shown.length - row * perRow);
        const col = i % perRow;
        const x = cx - (inRow * (w + gap) - gap) / 2 + col * (w + gap);
        const y = startY + row * (h + gap);
        return <Ticket key={t.id} ticket={t} x={x} y={y} w={w} h={h} onTicket={onTicket} onSelect={onSelect} selected={selected === t.id} flashing={flashing.has(t.id)} />;
      })}
      {more > 0 && (
        <text x={cx + (perRow * (w + gap)) / 2 + 4} y={startY + (rows - 1) * (h + gap) + 13} className="floor-sub" fill="currentColor">
          +{more}
        </text>
      )}
    </g>
  );
}

function Ticket({
  ticket,
  x,
  y,
  w,
  h,
  onTicket,
  onSelect,
  selected,
  flashing,
}: {
  ticket: FloorTicket;
  x: number;
  y: number;
  w: number;
  h: number;
  onTicket?: (id: string) => void;
  onSelect?: (sel: FloorSelection) => void;
  selected: boolean;
  flashing: boolean;
}) {
  const live = ticket.state === 'working' || ticket.state === 'review';
  // A click opens the dossier when the floor has one; the dossier links on
  // to the Tasks rail. Without a dossier the click goes straight there.
  const pick = onSelect ? () => onSelect({ kind: 'ticket', id: ticket.id, team: ticket.team }) : onTicket ? () => onTicket(ticket.id) : undefined;
  return (
    <g
      className={clsx('floor-ticket', live && 'floor-ticket-live', pick && 'cursor-pointer', flashing && 'floor-ticket-flash')}
      data-testid={`ticket-${ticket.id}`}
      data-state={ticket.state}
      data-selected={selected ? 'true' : undefined}
      onClick={pick}
      role={pick ? 'button' : undefined}
      tabIndex={pick ? 0 : undefined}
      onKeyDown={
        pick
          ? (e) => {
              if (e.key === 'Enter' || e.key === ' ') pick();
            }
          : undefined
      }
    >
      <title>{`${ticket.id} · ${TICKET_LABEL[ticket.state]}${ticket.agent ? ` · ${ticket.agent}` : ''}\n${ticket.title}`}</title>
      {selected && <rect x={x - 3} y={y - 3} width={w + 6} height={h + 6} rx="7" fill="none" stroke="#7c3aed" strokeWidth="2" className="floor-selected" />}
      {flashing && <rect x={x - 5} y={y - 5} width={w + 10} height={h + 10} rx="8" fill="none" stroke="#fff" strokeWidth="2" className="floor-flash-ring" />}
      <rect x={x} y={y} width={w} height={h} rx="5" fill={TICKET_FILL[ticket.state]} opacity={ticket.state === 'queued' ? 0.55 : 0.92} filter={live ? 'url(#floor-glow)' : undefined} />
      <text x={x + w / 2} y={y + 12.5} textAnchor="middle" className="floor-ticket-id" fill={ticket.state === 'queued' ? '#334155' : '#fff'}>
        {ticket.id.length > 8 ? ticket.id.slice(0, 7) + '…' : ticket.id}
      </text>
    </g>
  );
}

function Avatar({
  agent,
  x,
  y,
  hex,
  running,
  tick,
  team,
  selected,
  onSelect,
}: {
  agent: FloorAgent;
  x: number;
  y: number;
  hex: string;
  running: boolean;
  tick: number;
  team: FloorTeam;
  selected: boolean;
  onSelect?: (sel: FloorSelection) => void;
}) {
  const r = agent.seat === 'manager' ? 17 : 15;
  const pick = onSelect ? () => onSelect({ kind: 'agent', id: agent.id, team: team.id }) : undefined;
  const label = agent.id.length > 16 ? agent.id.slice(0, 15) + '…' : agent.id;
  const isManager = agent.seat === 'manager';
  const glowing = agent.active && running;
  return (
    <g
      className={clsx('floor-avatar', glowing && 'floor-avatar-active', pick && 'cursor-pointer')}
      data-testid={`agent-${agent.id}`}
      data-active={glowing ? 'true' : undefined}
      data-selected={selected ? 'true' : undefined}
      transform={`translate(${x} ${y})`}
      onClick={pick}
      role={pick ? 'button' : undefined}
      tabIndex={pick ? 0 : undefined}
      aria-label={pick ? `${agent.id}, ${agent.seat} on ${team.name}` : undefined}
      onKeyDown={
        pick
          ? (e) => {
              if (e.key === 'Enter' || e.key === ' ') pick();
            }
          : undefined
      }
    >
      {selected && <circle r={r + 13} fill="none" stroke="#7c3aed" strokeWidth="2.5" strokeDasharray="4 4" className="floor-selected-ring" />}
      <title>
        {`${agent.id} · ${isManager ? (team.managerDefault ? 'project manager (run default)' : 'project manager') : agent.seat}` +
          (agent.borrowed ? ` (the team names none — lent by the ${agent.borrowed})` : '') +
          (agent.touched ? ` · ${agent.touched} ticket${agent.touched === 1 ? '' : 's'} touched` : '')}
      </title>
      {glowing && <circle r={r + 9} fill={hex} opacity="0.25" className="floor-halo" />}
      {glowing && <circle r={r + 4} fill="none" stroke={hex} strokeWidth="2" className="floor-halo-ring" />}
      <circle r={r} fill={isManager ? hex : '#ffffff'} stroke={hex} strokeWidth={isManager ? 0 : 2} strokeDasharray={agent.borrowed ? '3 3' : undefined} opacity={agent.borrowed ? 0.85 : 1} className="floor-avatar-body" />
      <text y="5" textAnchor="middle" className="floor-glyph">
        {glyphFor(agent)}
      </text>
      <text y={r + 13} textAnchor="middle" className={clsx('floor-name', isManager && 'floor-name-manager')} fill="currentColor">
        {label}
      </text>
      {isManager && (
        <text y={r + 24} textAnchor="middle" className="floor-note" fill="currentColor" opacity="0.7">
          {team.managerDefault ? 'manager · run default' : 'manager'}
        </text>
      )}
      {agent.borrowed && (
        <text y={r + 24} textAnchor="middle" className="floor-note" fill="#d97706" data-testid={`borrowed-${agent.id}`}>
          {agent.seat} · from the {agent.borrowed}
        </text>
      )}
      {glowing && (
        <g transform={`translate(0 ${r + (isManager || agent.borrowed ? 40 : 30)})`}>
          <rect x="-40" y="-11" width="80" height="20" rx="10" fill={hex} className="floor-bubble" />
          <text y="3.5" textAnchor="middle" className="floor-bubble-text" fill="#fff">
            {agent.task ? agent.task : 'working'} · {elapsed(tick, agent)}
          </text>
        </g>
      )}
    </g>
  );
}

function elapsed(tick: number, agent: FloorAgent): string {
  // Since the agent's last line — the closest thing the log has to "started".
  return agent.lastAt ? ago(agent.lastAt, tick).replace(' ago', '') : 'live';
}

function Conduit({ from, to, label, stalled, running }: { from: Placed; to: Placed; label: string; stalled: boolean; running: boolean }) {
  // From the provider's right edge to the consumer's left edge, bowing away
  // from the islands' centre line.
  const x1 = from.cx + (to.cx >= from.cx ? ISLAND_RX * 0.6 : -ISLAND_RX * 0.6);
  const y1 = from.cy;
  const x2 = to.cx + (to.cx >= from.cx ? -ISLAND_RX * 0.6 : ISLAND_RX * 0.6);
  const y2 = to.cy;
  const bow = from.cy === to.cy ? -70 : 0;
  const mx = (x1 + x2) / 2;
  const my = (y1 + y2) / 2 + bow;
  const d = `M ${x1} ${y1} Q ${mx} ${my} ${x2} ${y2}`;
  const color = stalled ? '#f59e0b' : '#8b5cf6';
  return (
    <g data-testid={`conduit-${from.team.id}-${to.team.id}`} data-stalled={stalled ? 'true' : undefined}>
      <path d={d} fill="none" stroke={color} strokeWidth="6" strokeOpacity="0.14" strokeLinecap="round" />
      <path d={d} fill="none" stroke={color} strokeWidth="2" strokeDasharray="8 10" strokeLinecap="round" className={clsx('floor-flow', running && !stalled && 'floor-flow-on', stalled && 'floor-flow-stalled')} />
      <g transform={`translate(${mx} ${(y1 + y2) / 2 + bow / 2})`}>
        <rect x="-70" y="-10" width="140" height="18" rx="9" fill={color} opacity={stalled ? 0.95 : 0.85} />
        <text y="3" textAnchor="middle" className="floor-bubble-text" fill="#fff">
          {label.length > 24 ? label.slice(0, 23) + '…' : label}
          {stalled ? ' · waiting' : ''}
        </text>
      </g>
    </g>
  );
}

function Handoff({ handoff, placed, tick }: { handoff: FloorHandoff; placed: Map<string, Placed>; tick: number }) {
  // Find both ends: on the ticket's team when known, else anywhere.
  const find = (id: string) => {
    const home = handoff.team ? placed.get(handoff.team) : undefined;
    if (home?.agentPos.get(id)) return home.agentPos.get(id)!;
    for (const p of placed.values()) {
      const pos = p.agentPos.get(id);
      if (pos) return pos;
    }
    return undefined;
  };
  const a = find(handoff.from);
  const b = find(handoff.to);
  if (!a || !b) return null;
  const age = Math.max(0, tick - handoff.at);
  const fade = Math.max(0.25, 1 - age / 45_000);
  const d = `M ${a.x} ${a.y - 40} Q ${(a.x + b.x) / 2} ${Math.min(a.y, b.y) - 90} ${b.x} ${b.y - 40}`;
  return (
    <g data-testid={`handoff-${handoff.task}`} opacity={fade}>
      <path d={d} fill="none" stroke="#7c3aed" strokeWidth="2" strokeDasharray="4 6" />
      <circle r="5" fill="#7c3aed" filter="url(#floor-glow)">
        <animateMotion dur="1.8s" repeatCount="indefinite" path={d} />
      </circle>
      <text x={(a.x + b.x) / 2} y={Math.min(a.y, b.y) - 74} textAnchor="middle" className="floor-note" fill="#7c3aed">
        {handoff.task} → {handoff.to}
      </text>
    </g>
  );
}

function IntegrationPlate({ integration, unassigned }: { integration: NonNullable<FloorModel['integration']>; unassigned: number }) {
  const ready = !!integration.ready;
  const color = ready ? '#10b981' : '#94a3b8';
  return (
    <g data-testid="integration-plate" transform={`translate(${W / 2} ${H - 52})`}>
      <rect x="-170" y="-20" width="340" height="40" rx="20" fill={color} opacity="0.14" stroke={color} strokeWidth="1.5" />
      <text y="-2" textAnchor="middle" className="floor-sub" fill="currentColor">
        {ready ? 'ready to join the halves' : `integration waits${integration.reason ? ` — ${integration.reason}` : ''}`}
      </text>
      <text y="12" textAnchor="middle" className="floor-note" fill="currentColor" opacity="0.7">
        {integration.acceptance ? integration.acceptance : 'no integration command'}
        {unassigned > 0 ? ` · ${unassigned} seam ticket${unassigned === 1 ? '' : 's'}` : ''}
      </text>
    </g>
  );
}

function Legend({ floor }: { floor: FloorModel }) {
  return (
    <div className="pointer-events-none absolute bottom-2 left-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-[10px] text-gray-500 dark:text-gray-400">
      <span className="font-semibold uppercase tracking-wider">{floor.summary}</span>
      {LEGEND_STATES.map((state) => (
        <span key={state} className="inline-flex items-center gap-1">
          <span className="inline-block h-2 w-3 rounded-sm" style={{ background: TICKET_FILL[state] }} />
          {TICKET_LABEL[state]}
        </span>
      ))}
    </div>
  );
}

/** The pipeline's own people, off to the side: the phase the run is in and who is speaking. */
function PipelineStrip({ stage, phase, running, selection, onSelect }: { stage: FloorStageSeat[]; phase: FloorPhase | null; running: boolean; selection: FloorSelection; onSelect?: (sel: FloorSelection) => void }) {
  return (
    <div className="pointer-events-none absolute bottom-10 left-2 z-[60] max-w-[16rem] rounded-lg border border-brand-200/70 bg-white/80 px-2.5 py-2 text-[10px] backdrop-blur dark:border-brand-900/60 dark:bg-gray-900/80" data-testid="pipeline-strip">
      <div className="flex items-baseline gap-2">
        <span className="font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">pipeline</span>
        <span className="font-mono text-[12px] font-extrabold uppercase text-brand-700 dark:text-brand-300" data-testid="pipeline-phase">{phase ? phase.id : running ? 'starting' : 'idle'}</span>
        {phase?.agent && <span className="font-mono text-gray-500 dark:text-gray-400">{phase.agent}</span>}
      </div>
      {phase?.message && <div className="mt-0.5 line-clamp-2 text-gray-600 dark:text-gray-300">{phase.message}</div>}
      {stage.length > 0 && (
        <ul className="mt-1.5 flex flex-wrap gap-1">
          {stage.map((a) => {
            const live = a.active && running;
            const selected = selection?.kind === 'agent' && selection.id === a.id;
            return (
              <li key={a.id}>
                <button
                  type="button"
                  onClick={onSelect ? () => onSelect(selected ? null : { kind: 'agent', id: a.id, team: '' }) : undefined}
                  data-testid={`stage-${a.id}`}
                  data-active={live ? 'true' : undefined}
                  className={clsx(
                    'pointer-events-auto focus-ring inline-flex items-center gap-1 rounded-full border px-1.5 py-px font-mono font-semibold',
                    live ? 'floor-stage-live border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-700 dark:bg-emerald-950/50 dark:text-emerald-200' : a.spoke ? 'border-gray-200 text-gray-700 dark:border-gray-700 dark:text-gray-200' : 'border-dashed border-gray-300 text-gray-400 dark:border-gray-700',
                    selected && 'ring-2 ring-brand-500',
                  )}
                  title={`${a.id}${a.phase ? ` · ${a.phase}` : ''} · ${live ? 'speaking' : a.spoke ? 'has spoken' : 'waiting'}`}
                >
                  {a.id}
                  {a.phase && <span className="font-normal opacity-60">{a.phase}</span>}
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

function IdleFloor() {
  return (
    <div className="floor-stage relative flex h-full w-full items-center justify-center overflow-hidden" data-testid="team-floor-idle">
      <div className="floor-ground absolute inset-0" aria-hidden="true" />
      <div className="floor-tilt absolute inset-0 opacity-60">
        <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="xMidYMid meet" className="h-full w-full" aria-hidden="true">
          {[0.3, 0.5, 0.7].map((f, i) => (
            <g key={f} className="floor-ghost" style={{ animationDelay: `${i * 0.6}s` }}>
              <ellipse cx={W * f} cy={H / 2 + 20} rx="120" ry="60" fill="#8b5cf6" opacity="0.08" stroke="#8b5cf6" strokeOpacity="0.25" strokeDasharray="6 8" />
            </g>
          ))}
        </svg>
      </div>
      <div className="relative max-w-sm text-center">
        <h2 className="text-base font-semibold text-gray-700 dark:text-gray-200">The floor is empty</h2>
        <p className="mt-1 text-sm text-gray-400">
          Describe a change above and press Run. The teams that take it, their managers, their tickets
          and who is working on what appear here as the run goes.
        </p>
      </div>
    </div>
  );
}

function cssID(id: string): string {
  return id.replace(/[^a-z0-9_-]/gi, '_');
}
