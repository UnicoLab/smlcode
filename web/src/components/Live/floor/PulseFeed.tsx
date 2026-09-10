import clsx from 'clsx';
import type { FloorPulse } from './floorModel';
import type { FloorSelection } from './floorShared';

// ── The pulse feed: what just happened, sliding in at the side ───────────
//
// Every change on the floor — a ticket appearing, a state flipping, a person
// starting, a handoff, a gate — becomes a card that slides in on the right
// and fades after PULSE_TTL_MS. Each card is a link into the dossier of the
// ticket or the person concerned, so a flash can be chased.

export interface PulseFeedProps {
  pulses: FloorPulse[];
  now: number;
  onSelect: (sel: FloorSelection) => void;
  running?: boolean;
}

const TONE_CLASS: Record<FloorPulse['tone'], string> = {
  info: 'border-sky-300/80 bg-sky-50/95 text-sky-900 dark:border-sky-700 dark:bg-sky-950/80 dark:text-sky-100',
  good: 'border-emerald-300/80 bg-emerald-50/95 text-emerald-900 dark:border-emerald-700 dark:bg-emerald-950/80 dark:text-emerald-100',
  warn: 'border-amber-300/80 bg-amber-50/95 text-amber-900 dark:border-amber-700 dark:bg-amber-950/80 dark:text-amber-100',
  bad: 'border-red-300/80 bg-red-50/95 text-red-900 dark:border-red-700 dark:bg-red-950/80 dark:text-red-100',
  brand: 'border-brand-300/80 bg-brand-50/95 text-brand-900 dark:border-brand-700 dark:bg-brand-950/80 dark:text-brand-100',
};

const KIND_GLYPH: Record<FloorPulse['kind'], string> = {
  'ticket-new': '🎫',
  'ticket-moved': '↪',
  'ticket-working': '🔧',
  'ticket-review': '👁️',
  'ticket-blocked': '⛔',
  'ticket-done': '✅',
  'ticket-failed': '💥',
  'agent-start': '⚡',
  phase: '🧭',
  handoff: '🔁',
  gate: '🚦',
  'team-complete': '🏁',
};

const SHOW = 7;

export default function PulseFeed({ pulses, now, onSelect, running }: PulseFeedProps) {
  const shown = pulses.slice(-SHOW).reverse();
  if (shown.length === 0) {
    return (
      <p className="px-1 pt-1 text-[10px] leading-snug text-gray-400 dark:text-gray-500" data-testid="floor-feed-empty">
        {running ? 'What happens next shows up here: tickets appearing, people starting, gates.' : 'Start a run and what happens shows up here.'}
      </p>
    );
  }
  return (
    <ol className="floor-feed flex min-h-0 flex-col gap-1.5 overflow-hidden" aria-live="polite" aria-label="What just happened" data-testid="floor-feed">
      {shown.map((p) => {
        const age = (now - p.at) / 1000;
        const target: FloorSelection = p.ticket ? { kind: 'ticket', id: p.ticket, team: p.team } : p.agent ? { kind: 'agent', id: p.agent, team: p.team } : null;
        return (
          <li key={p.id} className={clsx('floor-feed-card', age > 15 && 'floor-feed-card-fading')} style={{ animationDelay: '0s' }}>
            <button
              type="button"
              disabled={!target}
              onClick={target ? () => onSelect(target) : undefined}
              className={clsx(
                'flex w-full items-start gap-1.5 rounded-md border px-2 py-1 text-left text-[10.5px] shadow-sm',
                TONE_CLASS[p.tone],
                target ? 'focus-ring hover:brightness-95 dark:hover:brightness-110' : 'cursor-default',
              )}
              data-kind={p.kind}
            >
              <span className="text-[12px] leading-4" aria-hidden="true">{KIND_GLYPH[p.kind]}</span>
              <span className="min-w-0 flex-1">
                <span className="block truncate font-semibold">{p.text}</span>
                {p.detail ? <span className="block truncate opacity-75">{p.detail}</span> : null}
              </span>
              <span className="shrink-0 font-mono text-[9px] opacity-60">{age < 1 ? 'now' : `${Math.round(age)}s`}</span>
            </button>
          </li>
        );
      })}
    </ol>
  );
}
