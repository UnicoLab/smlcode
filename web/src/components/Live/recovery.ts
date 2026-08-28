import type { RunEvent } from '@/types';

// ── The self-healing story ───────────────────────────────────────────────
//
// The harness recovers from most of what goes wrong: a tester rejection raises
// a correction ticket, a repeat ticket goes past the project manager, the
// manager moves it to a language specialist with guidance the last attempt did
// not have, and a re-staffed wave runs it.
//
// None of that was VISIBLE. The failures are red and loud — "tester found 1
// failure", "T2 reassigned after its retries were spent" — and the recovery is
// four plain lines somewhere in a log of fifty. A user watching that sees a run
// going wrong and no evidence anything is handling it, which is the worst
// possible reading of a system that is, in fact, fixing itself.
//
// This folds the structured loop events into one row per defect: what was
// found, who has it now, and whether it closed. It is a pure reducer over the
// event stream so it can be tested without a browser, and so the panel has no
// state of its own to drift out of sync with the log.

/** What a recovery episode is doing right now. */
export type RecoveryState = 'healing' | 'resolved' | 'needs-you';

export interface RecoveryStep {
  /** The loop action, e.g. `restaffed_wave`. */
  action: string;
  /** One line a human can read. */
  label: string;
  time: string;
}

export interface RecoveryEpisode {
  /** Stable key: the wave the episode opened on. */
  id: string;
  /** What the gate found, in its own words. */
  found: string;
  /** The specific failures, when the gate cited any. */
  failures: string[];
  /** Who holds the work now, once a manager or the ladder has moved it. */
  handedTo?: string;
  steps: RecoveryStep[];
  state: RecoveryState;
  /** How many times this defect came back before closing. */
  attempts: number;
}

interface LoopPayload {
  action?: string;
  reason?: string;
  wave?: number;
  failures?: string[];
  from?: string;
  to?: string;
  awaiting?: boolean;
}

/** Actions that OPEN an episode: something was found wanting. */
const OPENERS = new Set(['tester_reject', 'placeholder_gaps', 'integration_failed']);

/** Actions that CLOSE an episode green. */
const RESOLVERS = new Set(['resolved', 'objective_met', 'escalate_resolved']);

/** Actions that mean a person has to look. */
const BLOCKERS = new Set(['unresolved', 'continue_pending', 'escalate_pending', 'escalate_timeout', 'aborted']);

/** Actions that are progress worth showing inside an episode. */
const STEP_LABELS: Record<string, string> = {
  tester_reject: 'the tester rejected the delivery',
  integration_failed: 'every team passed, the halves do not fit',
  placeholder_gaps: 'placeholders left in the work',
  rewrite: 'raised a correction ticket',
  replan: 'revised the plan',
  corrective_wave: 'ran a corrective wave',
  restaffed_wave: 'the project manager moved the ticket',
  reverify: 're-verified',
  continue_wave: 'ran one more wave',
  resolved: 'resolved',
  objective_met: 'the objective is green',
  escalate_resolved: 'resolved after review',
  unresolved: 'still failing',
  continue_pending: 'waiting for you',
  escalate_pending: 'waiting for you',
  escalate_timeout: 'nobody answered in time',
  aborted: 'stopped',
};

/**
 * buildRecovery folds the event stream into one episode per defect.
 *
 * A defect that comes back while its episode is open is the SAME episode with
 * another attempt, not a new one — that is what the correction key means on the
 * harness side, and splitting them here would show two rows for one problem and
 * make a run look twice as broken as it is.
 */
export function buildRecovery(events: RunEvent[]): RecoveryEpisode[] {
  const episodes: RecoveryEpisode[] = [];
  let open: RecoveryEpisode | null = null;

  for (const ev of events) {
    if (ev.kind !== 'loop') continue;
    const payload = parseLoop(ev);
    const action = payload.action ?? scopeAction(ev.scope);
    if (!action) continue;

    if (OPENERS.has(action)) {
      if (open) {
        // The same defect came back. One row, one more attempt.
        open.attempts += 1;
        pushStep(open, action, ev, payload);
        continue;
      }
      open = {
        id: `w${payload.wave ?? episodes.length + 1}-${episodes.length}`,
        found: firstLine(payload.reason) || ev.message || 'a gate rejected the work',
        failures: (payload.failures ?? []).filter(Boolean).slice(0, 4),
        steps: [],
        state: 'healing',
        attempts: 1,
      };
      pushStep(open, action, ev, payload);
      episodes.push(open);
      continue;
    }

    if (!open) continue;
    pushStep(open, action, ev, payload);

    if (action === 'restaffed_wave' || action === 'corrective_wave') {
      const to = handoffTarget(events, ev);
      if (to) open.handedTo = to;
    }
    if (RESOLVERS.has(action)) {
      open.state = 'resolved';
      open = null;
      continue;
    }
    if (BLOCKERS.has(action) || payload.awaiting) {
      open.state = 'needs-you';
    }
  }
  return episodes;
}

/** Summary counts for the tab badge and the header line. */
export function recoveryTally(episodes: RecoveryEpisode[]): {
  healing: number;
  resolved: number;
  needsYou: number;
} {
  let healing = 0;
  let resolved = 0;
  let needsYou = 0;
  for (const e of episodes) {
    if (e.state === 'resolved') resolved += 1;
    else if (e.state === 'needs-you') needsYou += 1;
    else healing += 1;
  }
  return { healing, resolved, needsYou };
}

function pushStep(ep: RecoveryEpisode, action: string, ev: RunEvent, payload: LoopPayload) {
  const label = STEP_LABELS[action] ?? firstLine(payload.reason) ?? action;
  const last = ep.steps[ep.steps.length - 1];
  // A re-verify that fires twice in a row says nothing the first did not.
  if (last && last.action === action) return;
  ep.steps.push({ action, label, time: ev.time });
}

/**
 * handoffTarget finds who the work was moved to, from the reassignment event
 * the harness emits alongside the wave.
 *
 * The wave event says a ticket moved; only the reassignment line says where to,
 * and "moved it to somebody" without the somebody is exactly the kind of half
 * answer that makes a user distrust the whole panel.
 */
function handoffTarget(events: RunEvent[], at: RunEvent): string | undefined {
  const cutoff = Date.parse(at.time);
  let best: string | undefined;
  for (const ev of events) {
    if (!ev.message?.includes('reassigned')) continue;
    if (Number.isFinite(cutoff) && Date.parse(ev.time) > cutoff) continue;
    const m = /reassigned (?:from \S+ )?to (\S+?)(?:\s|$|—|,)/.exec(ev.message);
    if (m) best = m[1];
  }
  return best;
}

function parseLoop(ev: RunEvent): LoopPayload {
  if (!ev.output) return {};
  try {
    return JSON.parse(ev.output) as LoopPayload;
  } catch {
    // A loop event whose body did not survive the stream is still a loop event;
    // the scope carries the action.
    return {};
  }
}

/** The scope is `<action>` or `<action>:awaiting`. */
function scopeAction(scope?: string): string {
  return (scope ?? '').split(':')[0];
}

function firstLine(s?: string): string {
  return (s ?? '').split('\n')[0].trim();
}
