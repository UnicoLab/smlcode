import { useEffect, useMemo, useRef } from 'react';
import { ArrowUpRight, X } from 'lucide-react';
import clsx from 'clsx';
import type { RunEvent } from '@/types';
import { teamColor } from '@/components/Board/teamColor';
import { isTypingTarget } from '@/hooks/useKeyboard';
import { agentTrail, type FloorAgent, type FloorModel, type FloorStageSeat, type FloorTeam, type FloorTicket } from './floorModel';
import { TICKET_HEX, TICKET_LABEL, ago, clock, findAgent, findTicket, glyphFor, seatTitle, type FloorSelection } from './floorShared';

// ── The dossier: who is this, what are they doing ────────────────────────
//
// Click a person on the floor and this opens beside the stage: their seat
// and team, who manages them, whether they are working right now and on
// what, the last thing they said, the tickets they have touched, and their
// recent lines from the log. Click a ticket and it is the ticket's turn: its
// state, who holds it, everyone who has touched it. Every name in here is a
// link to the next dossier, so the floor can be explored by clicking around.

export interface FloorDossierProps {
  floor: FloorModel;
  events: RunEvent[];
  selection: FloorSelection;
  running: boolean;
  now: number;
  onSelect: (sel: FloorSelection) => void;
  /** Jump to the ticket in the Tasks rail. */
  onTicket?: (id: string) => void;
}

const TONE: Record<string, string> = {
  info: 'text-gray-500 dark:text-gray-400',
  warning: 'text-amber-600 dark:text-amber-400',
  error: 'text-red-600 dark:text-red-400',
  problem: 'text-red-600 dark:text-red-400',
  success: 'text-emerald-600 dark:text-emerald-400',
};

export default function FloorDossier({ floor, events, selection, running, now, onSelect, onTicket }: FloorDossierProps) {
  const person = findAgent(floor, selection);
  const ticket = findTicket(floor, selection);
  const onStage = !person && selection?.kind === 'agent' ? floor.stage.find((a) => a.id === selection.id) ?? null : null;
  const open = !!(person || ticket || onStage);

  // Esc closes, like every other overlay in the studio — unless the key was
  // typed into a field, where Esc means "clear what I typed", not "close".
  useEffect(() => {
    if (!selection) return undefined;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !isTypingTarget(e.target)) onSelect(null);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [selection, onSelect]);

  // Opening moves focus to the close button so a keyboard user lands inside
  // the dossier they just opened; closing gives focus back to whatever opened
  // it (the sr-only button, a feed card, a chip), so they are not dropped at
  // the top of the document.
  const asideRef = useRef<HTMLElement>(null);
  const returnTo = useRef<HTMLElement | null>(null);
  const openKey = open ? `${selection?.kind}:${selection?.id}` : '';
  useEffect(() => {
    if (!openKey) return undefined;
    const active = document.activeElement;
    if (active instanceof HTMLElement && !asideRef.current?.contains(active)) returnTo.current = active;
    const close = asideRef.current?.querySelector<HTMLElement>('[data-dossier-close]');
    close?.focus({ preventScroll: true });
    return () => {
      const back = returnTo.current;
      if (back && back.isConnected && document.contains(back)) back.focus({ preventScroll: true });
    };
    // Re-run per dossier, not per render: `openKey` names the thing open.
  }, [openKey]);

  if (!open) return null;

  return (
    <aside
      ref={asideRef}
      className="floor-dossier pointer-events-auto absolute left-2 top-2 z-[60] flex max-h-[calc(100%-1rem)] w-[min(20rem,calc(100%-1rem))] flex-col overflow-hidden rounded-lg border border-gray-200/90 bg-white/92 text-xs shadow-xl backdrop-blur-md dark:border-gray-700/80 dark:bg-gray-900/92"
      data-testid="floor-dossier"
      role="dialog"
      aria-label={person ? `About ${person.agent.id}` : onStage ? `About ${onStage.id}` : `About ${ticket!.ticket.id}`}
    >
      {person ? (
        <AgentDossier {...person} floor={floor} events={events} running={running} now={now} onSelect={onSelect} onTicket={onTicket} />
      ) : onStage ? (
        <StageDossier seat={onStage} floor={floor} events={events} running={running} now={now} onSelect={onSelect} />
      ) : (
        <TicketDossier {...ticket!} floor={floor} now={now} onSelect={onSelect} onTicket={onTicket} />
      )}
    </aside>
  );
}

function Header({ hex, glyph, title, sub, onClose }: { hex: string; glyph: string; title: string; sub: string; onClose: () => void }) {
  return (
    <header className="flex items-start gap-2 border-b border-gray-200/80 px-3 py-2 dark:border-gray-800" style={{ borderTopColor: hex, borderTopWidth: 3 }}>
      <span className="text-xl leading-none" aria-hidden="true">{glyph}</span>
      <div className="min-w-0 flex-1">
        <div className="truncate font-mono text-[13px] font-bold text-gray-900 dark:text-gray-100">{title}</div>
        <div className="truncate text-[10.5px] font-semibold text-gray-500 dark:text-gray-400">{sub}</div>
      </div>
      <button type="button" onClick={onClose} aria-label="Close" data-dossier-close className="focus-ring -mr-1 rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-gray-800 dark:hover:text-gray-200">
        <X size={13} aria-hidden="true" />
      </button>
    </header>
  );
}

function AgentDossier({
  agent,
  team,
  floor,
  events,
  running,
  now,
  onSelect,
  onTicket,
}: {
  agent: FloorAgent;
  team: FloorTeam;
  floor: FloorModel;
  events: RunEvent[];
  running: boolean;
  now: number;
  onSelect: (sel: FloorSelection) => void;
  onTicket?: (id: string) => void;
}) {
  const hex = hexFor(team);
  const trail = useMemo(() => agentTrail(events, agent.id, 8), [events, agent.id]);
  const isManager = agent.seat === 'manager';
  const active = agent.active && running;
  const manager = team.agents.find((a) => a.seat === 'manager');
  const reports = team.agents.filter((a) => a.seat !== 'manager');
  const held = agent.task ? team.tickets.find((t) => t.id === agent.task) : undefined;
  const mine = agent.tickets.map((id) => team.tickets.find((t) => t.id === id)).filter((t): t is FloorTicket => !!t);
  const holding = team.tickets.filter((t) => t.agent === agent.id);

  return (
    <>
      <Header
        hex={hex}
        glyph={glyphFor(agent)}
        title={agent.id}
        sub={`${seatTitle(agent, team)} · ${team.name}`}
        onClose={() => onSelect(null)}
      />
      <div className="min-h-0 flex-1 space-y-2.5 overflow-y-auto px-3 py-2.5">
        {/* Status */}
        <section data-testid="dossier-status">
          <div className={clsx('flex items-center gap-1.5 font-semibold', active ? 'text-emerald-700 dark:text-emerald-300' : 'text-gray-600 dark:text-gray-300')}>
            <span className={clsx('inline-block h-2 w-2 rounded-full', active ? 'floor-dot-live bg-emerald-500' : 'bg-gray-300 dark:bg-gray-600')} aria-hidden="true" />
            {active ? (
              <>
                working{agent.task ? <> on <TicketLink id={agent.task} onSelect={onSelect} team={team.id} /></> : null}
                {agent.lastAt ? <span className="font-mono font-normal text-gray-400">· {clock(agent.lastAt, now)}</span> : null}
              </>
            ) : agent.lastAt ? (
              <>idle · last heard {ago(agent.lastAt, now)}</>
            ) : running ? (
              <>waiting for work</>
            ) : (
              <>has not spoken this run</>
            )}
          </div>
          {agent.lastMessage && (
            <blockquote className="mt-1 line-clamp-3 rounded border-l-2 pl-2 text-[11px] italic text-gray-600 dark:text-gray-300" style={{ borderColor: hex }}>
              {agent.lastMessage}
            </blockquote>
          )}
          {held && held.title && <div className="mt-1 truncate text-[10.5px] text-gray-500 dark:text-gray-400" title={held.title}>{held.title}</div>}
        </section>

        {/* Management */}
        <section className="rounded-md bg-gray-50 px-2 py-1.5 text-[11px] dark:bg-gray-800/60" data-testid="dossier-management">
          {isManager ? (
            <>
              <div className="font-semibold text-gray-700 dark:text-gray-200">
                Manages {reports.length} {reports.length === 1 ? 'person' : 'people'}
                {team.managerDefault ? <span className="font-normal text-gray-500"> · the run's default manager, since the team names none</span> : null}
              </div>
              <div className="mt-1 flex flex-wrap gap-1">
                {reports.map((r) => (
                  <AgentChip key={r.id} agent={r} team={team.id} running={running} onSelect={onSelect} />
                ))}
              </div>
              <div className="mt-1 text-gray-500 dark:text-gray-400">
                Splits the request into tickets, dispatches each to a seat, moves stuck ones, and answers for the gate
                {team.acceptance ? <> (<code className="font-mono">{team.acceptance}</code>)</> : null}.
              </div>
            </>
          ) : (
            <>
              <div className="text-gray-700 dark:text-gray-200">
                Managed by{' '}
                {manager ? <AgentChip agent={manager} team={team.id} running={running} onSelect={onSelect} /> : <span className="font-mono">{team.manager}</span>}
              </div>
              {agent.borrowed && (
                <div className="mt-1 text-amber-700 dark:text-amber-300">
                  {team.name} names no {agent.seat}; the {agent.borrowed === 'pipeline' ? 'pipeline lent this one for the run' : 'run default fills the seat'}.
                </div>
              )}
            </>
          )}
        </section>

        {/* Tickets */}
        <section data-testid="dossier-tickets">
          <div className="mb-1 flex items-baseline justify-between">
            <span className="font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">Tickets</span>
            <span className="text-[10px] text-gray-400">
              {holding.length > 0 ? `${holding.length} in hand · ` : ''}{mine.length} touched
            </span>
          </div>
          {mine.length === 0 && holding.length === 0 ? (
            <div className="text-[11px] text-gray-400">none yet{isManager ? ` — the manager's ${team.total} tickets are on the table` : ''}</div>
          ) : (
            <ul className="space-y-1">
              {dedupe([...holding, ...mine]).map((t) => (
                <li key={t.id}>
                  <button
                    type="button"
                    onClick={() => onSelect({ kind: 'ticket', id: t.id, team: team.id })}
                    className="focus-ring flex w-full items-center gap-1.5 rounded px-1 py-0.5 text-left hover:bg-gray-100 dark:hover:bg-gray-800"
                  >
                    <span className="inline-block h-2 w-3 shrink-0 rounded-sm" style={{ background: TICKET_HEX[t.state] }} aria-hidden="true" />
                    <span className="font-mono font-semibold">{t.id}</span>
                    <span className="truncate text-gray-500 dark:text-gray-400">{t.title}</span>
                    {t.agent === agent.id && <span className="ml-auto shrink-0 rounded bg-emerald-100 px-1 text-[9px] font-bold uppercase text-emerald-700 dark:bg-emerald-900/50 dark:text-emerald-300">holds</span>}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </section>

        {/* Trail */}
        <section data-testid="dossier-trail">
          <div className="mb-1 font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">Recent lines</div>
          {trail.length === 0 ? (
            <div className="text-[11px] text-gray-400">nothing in the log from {agent.id} yet</div>
          ) : (
            <ol className="space-y-1">
              {trail.map((l, i) => (
                <li key={`${l.at}-${i}`} className="grid grid-cols-[3.2rem_1fr] gap-x-1.5 text-[10.5px] leading-snug">
                  <span className="font-mono text-gray-400" title={new Date(l.at).toLocaleTimeString()}>{ago(l.at, now)}</span>
                  <span className={clsx('min-w-0', TONE[l.level ?? ''] ?? 'text-gray-700 dark:text-gray-200')}>
                    {l.task ? <button type="button" onClick={() => onSelect({ kind: 'ticket', id: l.task!, team: team.id })} className="focus-ring mr-1 rounded font-mono font-semibold hover:underline">{l.task}</button> : null}
                    <span className="line-clamp-2">{l.message}</span>
                  </span>
                </li>
              ))}
            </ol>
          )}
        </section>
      </div>
      <footer className="flex items-center justify-between gap-2 border-t border-gray-200/80 px-3 py-1.5 text-[10px] text-gray-500 dark:border-gray-800 dark:text-gray-400">
        <span>{floor.teams.length > 1 ? `${floor.teams.length} teams on the floor` : team.crew ? 'the pipeline crew' : `team ${team.id}`}</span>
        {onTicket && (held || holding[0]) && (
          <button type="button" onClick={() => onTicket((held ?? holding[0]).id)} className="focus-ring inline-flex items-center gap-0.5 rounded px-1 py-0.5 font-semibold text-brand-700 hover:bg-brand-50 dark:text-brand-300 dark:hover:bg-brand-950/40">
            open {(held ?? holding[0]).id} in Tasks <ArrowUpRight size={11} aria-hidden="true" />
          </button>
        )}
      </footer>
    </>
  );
}

function TicketDossier({
  ticket,
  team,
  floor,
  now,
  onSelect,
  onTicket,
}: {
  ticket: FloorTicket;
  team: FloorTeam | null;
  floor: FloorModel;
  now: number;
  onSelect: (sel: FloorSelection) => void;
  onTicket?: (id: string) => void;
}) {
  void now;
  const hex = team ? hexFor(team) : '#94a3b8';
  const color = TICKET_HEX[ticket.state];
  const seatOf = (id: string) => team?.agents.find((a) => a.id === id) ?? floor.teams.flatMap((t) => t.agents).find((a) => a.id === id);
  const holder = ticket.agent ? seatOf(ticket.agent) : undefined;
  const others = ticket.touchedBy.filter((id) => id !== ticket.agent);
  const handoffs = floor.handoffs.filter((h) => h.task === ticket.id);
  return (
    <>
      <Header hex={hex} glyph="🎫" title={ticket.id} sub={team ? `${team.name}` : 'no team — the seam'} onClose={() => onSelect(null)} />
      <div className="min-h-0 flex-1 space-y-2.5 overflow-y-auto px-3 py-2.5">
        <section>
          <div className="text-[12px] font-semibold text-gray-900 dark:text-gray-100">{ticket.title || 'untitled'}</div>
          <div className="mt-1 inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider" style={{ background: color, color: ticket.state === 'queued' ? '#1f2937' : '#fff' }} data-testid="dossier-ticket-state">
            {TICKET_LABEL[ticket.state]}
          </div>
        </section>
        <section className="rounded-md bg-gray-50 px-2 py-1.5 text-[11px] dark:bg-gray-800/60" data-testid="dossier-holder">
          {holder ? (
            <div className="text-gray-700 dark:text-gray-200">
              In the hands of <AgentChip agent={holder} team={team?.id ?? ''} running onSelect={onSelect} />
              {holder.active ? <span className="ml-1 text-emerald-600 dark:text-emerald-300">· typing now</span> : null}
            </div>
          ) : ticket.state === 'done' ? (
            <div className="text-emerald-700 dark:text-emerald-300">Finished{ticket.touchedBy.length ? ` — last by ${ticket.touchedBy[ticket.touchedBy.length - 1]}` : ''}.</div>
          ) : (
            <div className="text-gray-500 dark:text-gray-400">Nobody holds it right now{team ? ` — ${team.manager} decides who is next` : ''}.</div>
          )}
          {others.length > 0 && (
            <div className="mt-1 flex flex-wrap items-center gap-1 text-gray-500 dark:text-gray-400">
              also touched by
              {others.map((id) => {
                const a = seatOf(id);
                return a ? <AgentChip key={id} agent={a} team={team?.id ?? ''} running onSelect={onSelect} /> : <span key={id} className="font-mono">{id}</span>;
              })}
            </div>
          )}
          {handoffs.map((h) => (
            <div key={`${h.from}-${h.to}-${h.at}`} className="mt-1 text-brand-700 dark:text-brand-300">
              moved {h.from} → {h.to}{h.reason ? ` — ${h.reason}` : ''}
            </div>
          ))}
        </section>
        {team && (
          <section className="text-[11px] text-gray-500 dark:text-gray-400">
            One of {team.total} on {team.name} · {team.done} done{team.blocked ? ` · ${team.blocked} blocked` : ''}
            {team.manager ? <> · managed by <button type="button" className="focus-ring rounded font-mono font-semibold text-gray-700 hover:underline dark:text-gray-200" onClick={() => onSelect({ kind: 'agent', id: team.manager, team: team.id })}>{team.manager}</button></> : null}
          </section>
        )}
      </div>
      {onTicket && (
        <footer className="border-t border-gray-200/80 px-3 py-1.5 text-right dark:border-gray-800">
          <button type="button" onClick={() => onTicket(ticket.id)} className="focus-ring inline-flex items-center gap-0.5 rounded px-1 py-0.5 text-[10px] font-semibold text-brand-700 hover:bg-brand-50 dark:text-brand-300 dark:hover:bg-brand-950/40">
            open in Tasks <ArrowUpRight size={11} aria-hidden="true" />
          </button>
        </footer>
      )}
    </>
  );
}

/** A phase agent on the pipeline's stage: what phase, whether speaking, what was said. */
function StageDossier({ seat, floor, events, running, now, onSelect }: { seat: FloorStageSeat; floor: FloorModel; events: RunEvent[]; running: boolean; now: number; onSelect: (sel: FloorSelection) => void }) {
  const trail = useMemo(() => agentTrail(events, seat.id, 8), [events, seat.id]);
  const active = seat.active && running;
  const others = floor.stage.filter((a) => a.id !== seat.id);
  return (
    <>
      <Header hex="#8b5cf6" glyph="🎓" title={seat.id} sub={`pipeline${seat.phase ? ` · ${seat.phase} phase` : ''}`} onClose={() => onSelect(null)} />
      <div className="min-h-0 flex-1 space-y-2.5 overflow-y-auto px-3 py-2.5">
        <section data-testid="dossier-status">
          <div className={clsx('flex items-center gap-1.5 font-semibold', active ? 'text-emerald-700 dark:text-emerald-300' : 'text-gray-600 dark:text-gray-300')}>
            <span className={clsx('inline-block h-2 w-2 rounded-full', active ? 'floor-dot-live bg-emerald-500' : 'bg-gray-300 dark:bg-gray-600')} aria-hidden="true" />
            {active ? (
              <>speaking{seat.lastAt ? <span className="font-mono font-normal text-gray-400">· {clock(seat.lastAt, now)}</span> : null}</>
            ) : seat.spoke ? (
              <>done · last heard {ago(seat.lastAt, now)}</>
            ) : running ? (
              <>waiting for the {seat.phase ?? 'next'} phase</>
            ) : (
              <>has not spoken this run</>
            )}
          </div>
          {seat.lastMessage && (
            <blockquote className="mt-1 line-clamp-3 rounded border-l-2 border-brand-400 pl-2 text-[11px] italic text-gray-600 dark:text-gray-300">{seat.lastMessage}</blockquote>
          )}
        </section>
        <section className="rounded-md bg-gray-50 px-2 py-1.5 text-[11px] text-gray-600 dark:bg-gray-800/60 dark:text-gray-300" data-testid="dossier-management">
          One of the pipeline's own: not on a team, but the run's thinking between the tables' work
          {seat.phase ? <> — the <b>{seat.phase}</b> phase is theirs</> : null}.
          {others.length > 0 && (
            <div className="mt-1 flex flex-wrap items-center gap-1">
              also on the stage:
              {others.map((a) => (
                <button key={a.id} type="button" onClick={() => onSelect({ kind: 'agent', id: a.id, team: '' })} className={clsx('focus-ring inline-flex items-center gap-1 rounded-full border px-1.5 py-px font-mono text-[10px] font-semibold', a.active && running ? 'border-emerald-300 text-emerald-700 dark:border-emerald-700 dark:text-emerald-300' : 'border-gray-200 text-gray-700 dark:border-gray-700 dark:text-gray-200')}>
                  {a.id}
                </button>
              ))}
            </div>
          )}
        </section>
        <section data-testid="dossier-trail">
          <div className="mb-1 font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">Recent lines</div>
          {trail.length === 0 ? (
            <div className="text-[11px] text-gray-400">nothing in the log from {seat.id} yet</div>
          ) : (
            <ol className="space-y-1">
              {trail.map((l, i) => (
                <li key={`${l.at}-${i}`} className="grid grid-cols-[3.2rem_1fr] gap-x-1.5 text-[10.5px] leading-snug">
                  <span className="font-mono text-gray-400" title={new Date(l.at).toLocaleTimeString()}>{ago(l.at, now)}</span>
                  <span className={clsx('line-clamp-2', TONE[l.level ?? ''] ?? 'text-gray-700 dark:text-gray-200')}>{l.message}</span>
                </li>
              ))}
            </ol>
          )}
        </section>
      </div>
    </>
  );
}

function AgentChip({ agent, team, running, onSelect }: { agent: FloorAgent; team: string; running: boolean; onSelect: (sel: FloorSelection) => void }) {
  const live = agent.active && running;
  return (
    <button
      type="button"
      onClick={() => onSelect({ kind: 'agent', id: agent.id, team })}
      className={clsx(
        'focus-ring inline-flex items-center gap-1 rounded-full border px-1.5 py-px font-mono text-[10px] font-semibold hover:bg-white dark:hover:bg-gray-900',
        live ? 'border-emerald-300 text-emerald-700 dark:border-emerald-700 dark:text-emerald-300' : 'border-gray-200 text-gray-700 dark:border-gray-700 dark:text-gray-200',
      )}
      title={`${agent.id} · ${agent.seat}`}
    >
      <span aria-hidden="true">{glyphFor(agent)}</span>
      {agent.id}
    </button>
  );
}

function TicketLink({ id, team, onSelect }: { id: string; team: string; onSelect: (sel: FloorSelection) => void }) {
  return (
    <button type="button" onClick={() => onSelect({ kind: 'ticket', id, team })} className="focus-ring rounded font-mono font-bold hover:underline">
      {id}
    </button>
  );
}

function dedupe(list: FloorTicket[]): FloorTicket[] {
  const seen = new Set<string>();
  return list.filter((t) => (seen.has(t.id) ? false : (seen.add(t.id), true)));
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

function hexFor(team: FloorTeam): string {
  return HEX[teamColor(team.crew ? '' : team.id).name] ?? HEX.gray;
}
