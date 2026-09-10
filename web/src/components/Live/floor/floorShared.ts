import type { FloorAgent, FloorModel, FloorTeam, FloorTicket, SeatKind, TicketState } from './floorModel';
import { ROLE_GLYPHS, SEAT_GLYPHS, TICKET_STATE_LABELS } from '@/components/shared/labels';

// ── What both stages and the dossier share ───────────────────────────────
//
// The words — seat glyphs, ticket-state labels — come from the studio-wide
// vocabulary in components/shared/labels, so the floor, the board and the
// rail say the same thing about the same state.

/** What the user has clicked on the floor: a person or a ticket. */
export type FloorSelection = { kind: 'agent'; id: string; team: string } | { kind: 'ticket'; id: string; team: string } | null;

export const SEAT_GLYPH: Record<SeatKind, string> = SEAT_GLYPHS;

export const ROLE_GLYPH: Record<string, string> = ROLE_GLYPHS;

export const TICKET_HEX: Record<TicketState, string> = {
  queued: '#cbd5e1',
  working: '#f59e0b',
  review: '#fb923c',
  blocked: '#ef4444',
  done: '#10b981',
  failed: '#dc2626',
};

export const TICKET_LABEL: Record<TicketState, string> = TICKET_STATE_LABELS;

/** The legend both stages draw, in the order a reader scans it. */
export const LEGEND_STATES: TicketState[] = ['working', 'review', 'blocked', 'done', 'queued'];

export function glyphFor(agent: FloorAgent): string {
  if (agent.seat !== 'member') return SEAT_GLYPH[agent.seat];
  for (const [role, glyph] of Object.entries(ROLE_GLYPH)) {
    if (agent.id === role || agent.id.endsWith('-' + role)) return glyph;
  }
  return SEAT_GLYPH.member;
}

/** What a seat is called when a person is described. */
export function seatTitle(agent: FloorAgent, team: FloorTeam): string {
  if (agent.seat === 'manager') return team.managerDefault ? 'manager · run default' : 'manager';
  if (agent.borrowed) return `${agent.seat} · lent by the ${agent.borrowed}`;
  return agent.seat;
}

/** "12s ago", "3m ago", "1h ago". */
export function ago(at: number | undefined, now: number): string {
  if (!at) return '';
  const s = Math.max(0, Math.round((now - at) / 1000));
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  return `${Math.floor(m / 60)}h ${m % 60}m ago`;
}

/** "0:42", "12:05" — an elapsed clock. */
export function clock(since: number | undefined, now: number): string {
  if (!since) return '';
  const s = Math.max(0, Math.floor((now - since) / 1000));
  const m = Math.floor(s / 60);
  return `${m}:${String(s % 60).padStart(2, '0')}`;
}

export function findAgent(floor: FloorModel, sel: FloorSelection): { agent: FloorAgent; team: FloorTeam } | null {
  if (!sel || sel.kind !== 'agent') return null;
  const team = floor.teams.find((t) => t.id === sel.team) ?? floor.teams.find((t) => t.agents.some((a) => a.id === sel.id));
  const agent = team?.agents.find((a) => a.id === sel.id);
  return team && agent ? { agent, team } : null;
}

export function findTicket(floor: FloorModel, sel: FloorSelection): { ticket: FloorTicket; team: FloorTeam | null } | null {
  if (!sel || sel.kind !== 'ticket') return null;
  for (const team of floor.teams) {
    const ticket = team.tickets.find((t) => t.id === sel.id);
    if (ticket) return { ticket, team };
  }
  const loose = floor.unassigned.find((t) => t.id === sel.id);
  return loose ? { ticket: loose, team: null } : null;
}
