
// ── Reading a correction ticket off the board ────────────────────────────
//
// A correction ticket looks exactly like every other card: same shape, same
// badges, a role and a title. But it is not the same thing at all — it is a
// defect a gate found, with a reproduction and an owner, possibly on its second
// attempt and possibly reassigned by the project manager because the first
// specialist could not fix it.
//
// Somebody managing the board needs to see that at a glance. Reading the whole
// description to work out that a card is a rework of failed work is exactly the
// friction that makes a board feel like a log rather than a plan.
//
// The harness stamps this into the task notes (pkg/plan/correction.go); this
// reads it back.

export interface TicketInfo {
  /** True when this card is a correction ticket rather than planned work. */
  isTicket: boolean;
  /** Which attempt at its defect this is; 0 when unstamped. */
  attempt: number;
  /** Who the project manager moved it to, when it was moved. */
  reassignedTo?: string;
}

const KEY_MARKER = 'correction-key:';
const ATTEMPT_MARKER = 'correction-attempt:';
const REASSIGNED_MARKER = 'reassigned-to:';

/** ticketInfo reads the harness's ticket markers out of a task's notes. */
export function ticketInfo(task: { notes?: string }): TicketInfo {
  const notes = task.notes ?? '';
  const attemptRaw = markerValue(notes, ATTEMPT_MARKER);
  const attempt = attemptRaw ? Number.parseInt(attemptRaw, 10) : 0;
  return {
    isTicket: markerValue(notes, KEY_MARKER) !== '',
    attempt: Number.isFinite(attempt) && attempt > 0 ? attempt : 0,
    // The marker line is `reassigned-to: go-corrector (why)`; the agent id is
    // the first word, and the parenthetical is for the notes, not the badge.
    reassignedTo: markerValue(notes, REASSIGNED_MARKER).split(' ')[0] || undefined,
  };
}

/**
 * markerValue reads one `marker: value` line out of a notes field.
 *
 * Anchored to the start of a line so a marker MENTIONED in prose is not read as
 * one — the ticket bodies quote their own markers back when an agent describes
 * what it did.
 */
function markerValue(notes: string, marker: string): string {
  for (const line of notes.split('\n')) {
    const trimmed = line.trim();
    if (trimmed.startsWith(marker)) {
      return trimmed.slice(marker.length).trim();
    }
  }
  return '';
}
