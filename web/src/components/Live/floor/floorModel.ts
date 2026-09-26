import type { DynamicComposition, RunEvent, SquadsView, Task } from '@/types';

// ── The team floor, as data ──────────────────────────────────────────────
//
// The Live page used to answer "what is happening" with a log: a thousand
// lines in which the shape of the work — who is on which team, which ticket
// each agent holds, which manager answers for them, which half is waiting on
// which — is present but invisible. This model is that shape, derived from
// the four sources the page already has:
//
//   the org chart   (GET /api/squads)   teams, managers, contract, stalls, gates
//   the board       (GET /api/tasks)    tickets, their state and their team
//   the event log   (SSE)               who is working right now, and on what
//   the composition (compose event)     the crew of a run that has no teams
//
// It is pure: same inputs, same floor. Everything the stage draws comes from
// here, so the stage stays a renderer and this stays testable.

export type TicketState = 'queued' | 'working' | 'review' | 'blocked' | 'done' | 'failed';

export interface FloorTicket {
  id: string;
  title: string;
  state: TicketState;
  /** The agent holding it right now, when the log says one does. */
  agent?: string;
  team: string;
  /** Every agent that has touched it this run, in first-touch order. */
  touchedBy: string[];
}

export type SeatKind = 'manager' | 'worker' | 'reviewer' | 'tester' | 'member';

export interface FloorAgent {
  id: string;
  seat: SeatKind;
  /** True when the log's most recent activity is this agent's. */
  active: boolean;
  /** The ticket it is on, when active. */
  task?: string;
  /** How many tickets on this team it has touched this run. */
  touched: number;
  /** Those tickets, by id, most recently touched last. */
  tickets: string[];
  /** The last thing the log heard from this agent, and when. */
  lastMessage?: string;
  lastAt?: number;
  lastTask?: string;
  /**
   * Set when the team left this seat empty and the pipeline lent the agent:
   * 'pipeline' or 'default' — or the name of the team they belong to, when
   * they came over from another table to work one of this table's tickets.
   * A borrowed seat is drawn as such.
   */
  borrowed?: string;
  /**
   * Set at a person's home table while they are working another table's
   * ticket: the name of that table. Their chair here is empty meanwhile.
   */
  away?: string;
}

export type GateState = 'green' | 'red' | 'unverified' | '';

export interface FloorTeam {
  id: string;
  name: string;
  charter?: string;
  manager: string;
  managerDefault: boolean;
  agents: FloorAgent[];
  tickets: FloorTicket[];
  total: number;
  done: number;
  blocked: number;
  inFlight: number;
  complete: boolean;
  gate: GateState;
  acceptance?: string;
  /** Interfaces this team is waiting on, by id. */
  waitingOn: string[];
  /** True for the crew of a run with no org chart. */
  crew?: boolean;
  /**
   * True for the harness's own table: the phase agents, the dispatcher and
   * anyone else the run heard from who works no team's tickets. It has no
   * manager, no tickets and no gate — it is where the run's thinking sits,
   * kept apart so a Python team's table seats the Python team and nobody else.
   */
  internal?: boolean;
}

/** The id of the harness's own table. */
export const HARNESS_ID = 'harness';

/** The tables that do the work — every table but the harness's own. */
export function workTables(floor: Pick<FloorModel, 'teams'>): FloorTeam[] {
  return floor.teams.filter((t) => !t.internal);
}

export interface FloorLink {
  id: string;
  from: string;
  to: string;
  interface: string;
  stalled: boolean;
}

export interface FloorHandoff {
  task: string;
  from: string;
  to: string;
  team: string;
  at: number;
  reason: string;
}

export interface FloorNow {
  agent: string;
  task: string;
  team: string;
  message: string;
  /** The model that agent's line named, when it did. */
  model: string;
  since: number;
}

export type FloorMode = 'teams' | 'crew' | 'idle';

/**
 * Someone on the pipeline's stage: a phase agent — planner, splitter,
 * architect, explorer, coordinator, docs, memory, composer — who is not on any
 * team's roster but does the run's thinking between the tables' typing.
 */
export interface FloorStageSeat {
  id: string;
  /** The phase this agent serves, when the composition says. */
  phase?: string;
  active: boolean;
  /** True once the log has heard from them this run. */
  spoke: boolean;
  lastMessage?: string;
  lastAt?: number;
}

/** The phase the run is in right now, from the newest line of the log. */
export interface FloorPhase {
  id: string;
  agent: string;
  message: string;
  since: number;
}

export interface FloorModel {
  mode: FloorMode;
  teams: FloorTeam[];
  /** The pipeline's stage: phase agents not seated at any table. */
  stage: FloorStageSeat[];
  phase: FloorPhase | null;
  links: FloorLink[];
  handoffs: FloorHandoff[];
  now: FloorNow | null;
  integration: { acceptance?: string; ready?: boolean; reason?: string } | null;
  /** The seam: tickets no team owns. */
  unassigned: FloorTicket[];
  summary: string;
}

export interface FloorInputs {
  squads: SquadsView | null | undefined;
  tasks: Task[];
  events: RunEvent[];
  composition: DynamicComposition | null | undefined;
  running: boolean;
  /** Now, for handoff freshness. Injected so tests are deterministic. */
  now?: number;
}

/** How long a handoff stays animated on the floor. */
export const HANDOFF_TTL_MS = 45_000;

/** How long a pulse stays in the feed and on the floor. */
export const PULSE_TTL_MS = 20_000;

export type PulseKind =
  | 'ticket-new'
  | 'ticket-moved'
  | 'ticket-working'
  | 'ticket-review'
  | 'ticket-blocked'
  | 'ticket-done'
  | 'ticket-failed'
  | 'agent-start'
  | 'phase'
  | 'handoff'
  | 'gate'
  | 'team-complete';

export type PulseTone = 'info' | 'good' | 'warn' | 'bad' | 'brand';

/**
 * A pulse is one thing that just changed on the floor — a ticket appearing,
 * a state flipping, someone starting on something — kept for PULSE_TTL_MS so
 * the stage can flash it and the feed can list it.
 */
export interface FloorPulse {
  id: string;
  kind: PulseKind;
  tone: PulseTone;
  at: number;
  team: string;
  /** The ticket concerned, when one is. */
  ticket?: string;
  /** The agent concerned, when one is. */
  agent?: string;
  /** One line for the feed. */
  text: string;
  /** A second, quieter line: the ticket title, the reason, the message. */
  detail?: string;
}

/** One line of an agent's recent history, for the dossier. */
export interface FloorTrailLine {
  at: number;
  kind: string;
  phase: string;
  task?: string;
  message: string;
  level?: string;
}

const CREW_ID = 'crew';

/** The voice the team decision speaks in (composer.DispatcherID). */
export const DISPATCHER_ID = 'dispatcher';

const WORKING_ROLES = ['worker', 'reviewer', 'tester', 'corrector', 'deep', 'editor'];

/** isWorkingRole: an agent whose job is a ticket (go-worker, reviewer-strict), not a phase. */
export function isWorkingRole(id: string | undefined): boolean {
  const v = (id ?? '').trim().toLowerCase().replace(/-(strict|l\d+)$/, '');
  return WORKING_ROLES.some((r) => v === r || v.endsWith('-' + r));
}

/** Agents the log has seen touch any of these tickets, in first-seen order. */
function ticketWorkers(activity: Activity, ticketIDs: Iterable<string>): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  for (const id of ticketIDs) {
    for (const a of activity.touchedBy.get(id) ?? []) {
      if (!seen.has(a)) {
        seen.add(a);
        out.push(a);
      }
    }
  }
  return out;
}

const reReassigned = /^(\S+) reassigned from (\S+) to (\S+)(?: — (.*))?/;
const reProposes = /^(\S+) proposes (\S+)(?: — (.*))?/;

/** ticketState folds a task's column and status into the six the floor draws. */
export function ticketState(t: Pick<Task, 'column' | 'status'>): TicketState {
  const status = (t.status || '').toLowerCase();
  const column = (t.column || '').toLowerCase();
  if (status === 'done' || column === 'done') return 'done';
  if (status === 'failed') return 'failed';
  if (status === 'blocked' || column === 'blocked') return 'blocked';
  if (status === 'review' || status === 'correcting' || column === 'in_review') return 'review';
  if (status === 'running' || column === 'in_progress') return 'working';
  return 'queued';
}

/**
 * buildFloor derives the floor.
 *
 * Mode: `teams` when the org chart names teams — one table per team, even
 * when there is only one, since a lone Python team still has a manager, a
 * roster and a gate worth drawing; `crew` when a run (or a composition)
 * exists without a chart — the pipeline's own roles staff a single island;
 * `idle` when there is nothing at all to show.
 */
export function buildFloor(inputs: FloorInputs): FloorModel {
  const { squads, tasks, events, composition, running } = inputs;
  const nowMs = inputs.now ?? Date.now();
  const activity = readActivity(events);
  const handoffs = readHandoffs(events, nowMs, squads);

  const chart = squads?.ok ? (squads.squads ?? []) : [];
  const floor =
    chart.length >= 1
      ? floorFromChart(squads!, tasks, activity, handoffs, composition, nowMs)
      : floorFromCrew(tasks, activity, handoffs, composition, running, events, nowMs);
  if (floor.mode === 'idle') return floor;
  floor.phase = activity.phase;
  // Everyone who works no table's tickets — the planner, the splitter, the
  // composer, the dispatcher — sits at the harness's own table, never at a
  // team's. A lone Python team's table seats the Python team.
  floor.stage = stageSeats(floor.teams, composition, activity);
  if (floor.stage.length > 0) {
    floor.teams = [harnessTable(floor.stage, activity), ...floor.teams];
    // The ticker names the table the speaker actually sits at.
    if (floor.now && floor.stage.some((a) => a.id === floor.now!.agent)) floor.now = { ...floor.now, team: HARNESS_ID };
  }
  return floor;
}

/** harnessTable seats the pipeline's own people at a table of their own. */
function harnessTable(stage: FloorStageSeat[], activity: Activity): FloorTeam {
  const agents: FloorAgent[] = stage.map((s) => {
    const w = activity.working.get(s.id);
    return {
      id: s.id,
      seat: 'member',
      active: s.active,
      task: s.active && w?.task ? w.task : undefined,
      touched: 0,
      tickets: [],
      lastMessage: s.lastMessage,
      lastAt: s.lastAt,
      lastTask: activity.lastBy.get(s.id)?.task,
    };
  });
  return {
    id: HARNESS_ID,
    name: 'Harness · internal',
    charter: 'The run\'s own machinery: the dispatcher that picks the teams, and the agents that explore, plan, split and compose between the teams\' work.',
    manager: '',
    managerDefault: false,
    agents,
    tickets: [],
    total: 0,
    done: 0,
    blocked: 0,
    inFlight: 0,
    complete: false,
    gate: '',
    waitingOn: [],
    internal: true,
  };
}

/**
 * stageSeats lists the pipeline's own people — the composition's phase agents
 * and anyone the log heard from who sits at no table — in phase order, with
 * whoever is speaking now marked active.
 */
function stageSeats(teams: FloorTeam[], composition: DynamicComposition | null | undefined, activity: Activity): FloorStageSeat[] {
  const seated = new Set<string>();
  for (const t of teams) for (const a of t.agents) seated.add(a.id);
  const out: FloorStageSeat[] = [];
  const have = new Set<string>();
  const add = (raw: string | undefined, phase?: string) => {
    const id = (raw ?? '').trim().toLowerCase();
    if (!id || seated.has(id) || have.has(id)) return;
    have.add(id);
    const w = activity.working.get(id);
    const last = activity.lastBy.get(id);
    out.push({ id, phase, active: !!w || activity.lastAgent === id, spoke: !!last, lastMessage: last?.message, lastAt: last?.at });
  };
  // The dispatcher is seated whenever the library decided the teams, so the
  // one who explains why the other tables exist is there to be asked.
  if (composition?.team_selection) add(DISPATCHER_ID, 'compose');
  for (const p of composition?.phases ?? []) {
    if (p.enabled && p.when !== 'never') add(p.agent, p.id);
  }
  for (const m of composition?.team ?? []) add(m.role);
  for (const id of activity.speakers) add(id, activity.lastBy.get(id) ? activity.phaseOf.get(id) : undefined);
  return out;
}

// ── The org chart's floor ────────────────────────────────────────────────

function floorFromChart(
  view: SquadsView,
  tasks: Task[],
  activity: Activity,
  handoffs: FloorHandoff[],
  composition: DynamicComposition | null | undefined,
  nowMs: number,
): FloorModel {
  const chart = view.squads ?? [];
  const taskTeams = view.task_teams ?? {};
  const stalls = view.stalls ?? [];
  const gates = new Map((view.gates ?? []).map((g) => [g.team, g]));
  const compTeams = new Map((composition?.teams ?? []).map((t) => [t.id, t]));

  const ticketsByTeam = new Map<string, FloorTicket[]>();
  const unassigned: FloorTicket[] = [];
  // With one team there is no seam: every ticket is that team's.
  const only = chart.length === 1 ? chart[0].id : '';
  for (const t of tasks) {
    const team = t.squad || taskTeams[t.id] || only;
    const ticket: FloorTicket = {
      id: t.id,
      title: t.title,
      state: ticketState(t),
      agent: activity.holder.get(t.id),
      team,
      touchedBy: [...(activity.touchedBy.get(t.id) ?? [])],
    };
    if (team === '') {
      unassigned.push(ticket);
      continue;
    }
    const list = ticketsByTeam.get(team) ?? [];
    list.push(ticket);
    ticketsByTeam.set(team, list);
  }

  // Each team's own roster, and which team everyone belongs to. Someone who
  // works a ticket of a table they are not on the roster of — a lent
  // specialist, a corrector — sits at THAT table while they do, whatever
  // other table they call home: a team with no worker of its own still shows
  // who is doing its work.
  const rosterOf = new Map<string, Set<string>>();
  const homeOf = new Map<string, string>();
  for (const s of chart) {
    const comp = compTeams.get(s.id);
    const own = new Set<string>();
    for (const id of [comp?.manager || s.manager || 'triage', s.worker, s.reviewer, s.tester, ...(s.agents ?? []), ...(comp?.seats ?? []).map((f) => f.agent)]) {
      const v = (id ?? '').trim().toLowerCase();
      if (!v) continue;
      own.add(v);
      if (!homeOf.has(v)) homeOf.set(v, s.name || s.id);
    }
    rosterOf.set(s.id, own);
  }

  const teams: FloorTeam[] = chart.map((s) => {
    const tickets = ticketsByTeam.get(s.id) ?? [];
    const comp = compTeams.get(s.id);
    // Someone off every roster who worked THIS team's tickets — a corrector,
    // an escalated worker — sits at this table for the run, drawn as lent by
    // the pipeline; the harness table is for those who worked none.
    const own = rosterOf.get(s.id) ?? new Set<string>();
    const visitors = ticketWorkers(activity, tickets.map((t) => t.id))
      .filter((id) => !own.has(id))
      .map((id) => ({ role: roleOf(id), agent: id, source: homeOf.get(id) ?? 'pipeline', visitor: true }));
    // The manager the run resolves: the composition knows (it applied the
    // triage-capability rule); the chart's own field is the author's wish.
    const manager = comp?.manager || s.manager || 'triage';
    const managerDefault = comp ? !!comp.manager_default : !s.manager;
    const gate = gates.get(s.id);
    return {
      id: s.id,
      name: s.name || s.id,
      charter: s.charter,
      manager,
      managerDefault,
      agents: withBorrowed(
        seats(
          [
            ['manager', manager],
            ['worker', s.worker],
            ['reviewer', s.reviewer],
            ['tester', s.tester],
            ...((s.agents ?? []).map((a) => ['member', a]) as [SeatKind, string][]),
          ],
          tickets,
          activity,
          s.id,
        ),
        [...(comp?.seats ?? []), ...visitors],
        tickets,
        activity,
        s.id,
      ),
      tickets,
      total: s.total,
      done: s.done,
      blocked: s.blocked,
      inFlight: s.in_flight,
      complete: s.complete,
      gate: !gate ? '' : !gate.ran ? 'unverified' : gate.ok ? 'green' : 'red',
      acceptance: s.acceptance,
      waitingOn: stalls.filter((st) => st.squad === s.id).map((st) => st.interface),
    };
  });

  markAway(teams);

  const stalledKeys = new Set(stalls.map((st) => `${st.provider}→${st.squad}:${st.interface}`));
  const links: FloorLink[] = [];
  for (const i of view.interfaces ?? []) {
    for (const consumer of i.consumers ?? []) {
      if (!consumer || consumer === i.provider) continue;
      links.push({
        id: `${i.provider}→${consumer}:${i.id}`,
        from: i.provider,
        to: consumer,
        interface: i.id,
        stalled: stalledKeys.has(`${i.provider}→${consumer}:${i.id}`),
      });
    }
  }

  return {
    mode: 'teams',
    teams,
    stage: [],
    phase: null,
    links,
    handoffs,
    now: nowFor(activity, taskTeams, nowMs),
    integration: view.integration
      ? { acceptance: view.integration.acceptance, ready: view.integration.ready, reason: view.integration.reason }
      : null,
    unassigned,
    summary: view.summary || `${teams.length} teams`,
  };
}

// ── One island: the crew ─────────────────────────────────────────────────
//
// Most runs have no org chart. They still have people — the planner, the
// splitter, a worker, a reviewer, a tester — and the same questions apply:
// who is on it, who is working, what tickets exist. The crew is those roles
// on one island: from the composition when the run has one (a single library
// team staffing the run shows as that team), else from whoever the log has
// seen speak.

function floorFromCrew(
  tasks: Task[],
  activity: Activity,
  handoffs: FloorHandoff[],
  composition: DynamicComposition | null | undefined,
  running: boolean,
  events: RunEvent[],
  nowMs: number,
): FloorModel {
  const single = composition?.team_mode === 'single' ? composition.teams?.[0] : undefined;
  const tickets: FloorTicket[] = tasks.map((t) => ({
    id: t.id,
    title: t.title,
    state: ticketState(t),
    agent: activity.holder.get(t.id),
    team: CREW_ID,
    touchedBy: [...(activity.touchedBy.get(t.id) ?? [])],
  }));

  // The table seats the people who do the tickets: the team's own seats when
  // one team staffs the run, else the execute loop's worker, reviewer,
  // corrector and the test phase's tester. The planner, the composer and the
  // rest of the pipeline sit at the harness table instead (buildFloor).
  const seatList: [SeatKind, string | undefined][] = [];
  if (single) {
    seatList.push(['manager', single.manager || 'triage']);
    seatList.push(['worker', single.worker], ['reviewer', single.reviewer], ['tester', single.tester]);
    for (const a of single.agents ?? []) seatList.push(['member', a]);
  } else {
    const exec = composition?.execute;
    const phase = (id: string) => (composition?.phases ?? []).find((p) => p.id === id && p.enabled && p.when !== 'never')?.agent;
    seatList.push(['worker', exec?.default_role || phase('execute')], ['reviewer', exec?.reviewer], ['member', exec?.corrector], ['tester', phase('test')]);
    // The composer's roster names the working specialists too (go-tester,
    // ts-reviewer); those do tickets and sit here, the rest are the harness's.
    for (const m of composition?.team ?? []) {
      if (isWorkingRole(m.role)) seatList.push(['member', m.role]);
    }
  }
  // Whoever the log saw work a ticket is on the crew too, whatever the plan said.
  // With one island every ticket the log names is this table's, board or not.
  for (const id of ticketWorkers(activity, activity.touchedBy.keys())) seatList.push(['member', id]);

  const agents = withBorrowed(seats(seatList, tickets, activity, CREW_ID), single?.seats, tickets, activity, CREW_ID);
  const hasAnything = agents.length > 0 || tickets.length > 0 || events.length > 0 || !!composition;
  if (!hasAnything && !running) {
    return {
      mode: 'idle',
      teams: [],
      stage: [],
      phase: null,
      links: [],
      handoffs: [],
      now: null,
      integration: null,
      unassigned: [],
      summary: '',
    };
  }

  const done = tickets.filter((t) => t.state === 'done').length;
  const blocked = tickets.filter((t) => t.state === 'blocked' || t.state === 'failed').length;
  const inFlight = tickets.filter((t) => t.state === 'working' || t.state === 'review').length;
  const team: FloorTeam = {
    id: single?.id ?? CREW_ID,
    name: single?.name || single?.id || 'Pipeline crew',
    charter: single?.charter,
    manager: single?.manager || 'triage',
    managerDefault: single ? !!single.manager_default : true,
    agents,
    tickets,
    total: tickets.length,
    done,
    blocked,
    inFlight,
    complete: tickets.length > 0 && done === tickets.length,
    gate: '',
    acceptance: single?.acceptance,
    waitingOn: [],
    crew: true,
  };
  return {
    mode: 'crew',
    teams: [team],
    stage: [],
    phase: null,
    links: [],
    handoffs,
    now: nowFor(activity, {}, nowMs, team.id),
    integration: null,
    unassigned: [],
    summary: composition?.team_note || composition?.summary || (single ? `team ${single.id} staffs this run` : 'one stream'),
  };
}

// ── Seats ────────────────────────────────────────────────────────────────

function seats(
  list: [SeatKind, string | undefined][],
  tickets: FloorTicket[],
  activity: Activity,
  team: string,
): FloorAgent[] {
  const out: FloorAgent[] = [];
  const index = new Map<string, number>();
  const ticketIDs = new Set(tickets.map((t) => t.id));
  for (const [seat, raw] of list) {
    const id = (raw ?? '').trim().toLowerCase();
    if (id === '') continue;
    const i = index.get(id);
    if (i !== undefined) {
      // The same agent in two seats keeps the more specific one.
      if (out[i].seat === 'member' && seat !== 'member') out[i].seat = seat;
      continue;
    }
    const { active, held } = workingOn(activity, id);
    // An active agent counts for THIS team only when its ticket is here (or
    // the run has one island); an agent shared by two teams must not glow on
    // both.
    const onThisTeam = team === CREW_ID || held === '' || ticketIDs.has(held);
    out.push({
      id,
      seat,
      active: active && onThisTeam,
      task: active && onThisTeam && held ? held : undefined,
      ...facts(activity, id, ticketIDs),
    });
    index.set(id, out.length - 1);
  }
  return out;
}

/** The seat a visitor takes, from their id: go-tester tests, ts-reviewer reviews. */
function roleOf(id: string): string {
  const v = id.replace(/-(strict|l\d+)$/, '');
  for (const r of ['worker', 'reviewer', 'tester'] as const) if (v === r || v.endsWith('-' + r)) return r;
  return 'member';
}

/**
 * markAway: someone lit at one table (working its ticket) is, at every other
 * table they sit at, away — their chair there is empty, not idle.
 */
function markAway(teams: FloorTeam[]): void {
  const litAt = new Map<string, string>();
  for (const t of teams) for (const a of t.agents) if (a.active && a.task) litAt.set(a.id, t.name);
  for (const t of teams) {
    for (const a of t.agents) {
      const there = litAt.get(a.id);
      if (!a.active && there && there !== t.name) a.away = there;
    }
  }
}

/**
 * withBorrowed adds the seats the pipeline lends a team (composer.FillSeats):
 * a tester the team never named still sits at its table for this run, drawn
 * as borrowed so nobody mistakes it for a member.
 */
function withBorrowed(
  own: FloorAgent[],
  fills: { role: string; agent: string; source: string; visitor?: boolean }[] | undefined,
  tickets: FloorTicket[],
  activity: Activity,
  team: string,
): FloorAgent[] {
  if (!fills || fills.length === 0) return own;
  const have = new Set(own.map((a) => a.id));
  const ticketIDs = new Set(tickets.map((t) => t.id));
  const out = [...own];
  for (const f of fills) {
    const id = (f.agent ?? '').trim().toLowerCase();
    if (!id || f.source === 'team' || f.role === 'manager' || have.has(id)) continue;
    have.add(id);
    const seat: SeatKind = f.role === 'worker' || f.role === 'reviewer' || f.role === 'tester' ? f.role : 'member';
    const { active, held } = workingOn(activity, id);
    // A visitor is here for this table's tickets only; with none in hand they are home.
    const onThisTeam = team === CREW_ID || (held === '' ? !f.visitor : ticketIDs.has(held));
    out.push({
      id,
      seat,
      active: active && onThisTeam,
      task: active && onThisTeam && held ? held : undefined,
      ...facts(activity, id, ticketIDs),
      borrowed: f.source,
    });
  }
  return out;
}

/**
 * workingOn says whether an agent is working right now and on which ticket:
 * inside an agent_start … agent_end pair, or the newest voice in the log (a
 * phase agent that never gets a start/end of its own still counts while it is
 * the one speaking).
 */
function workingOn(activity: Activity, id: string): { active: boolean; held: string } {
  const w = activity.working.get(id);
  if (w) return { active: true, held: w.task };
  if (activity.lastAgent === id) return { active: true, held: activity.lastTask };
  return { active: false, held: '' };
}

/** facts is what the dossier says about a person: their tickets here, their last words. */
function facts(activity: Activity, agent: string, tickets: Set<string>): Pick<FloorAgent, 'touched' | 'tickets' | 'lastMessage' | 'lastAt' | 'lastTask'> {
  const mine: string[] = [];
  for (const [task, agents] of activity.touchedBy) {
    if (tickets.has(task) && agents.has(agent)) mine.push(task);
  }
  const last = activity.lastBy.get(agent);
  return {
    touched: mine.length,
    tickets: mine,
    lastMessage: last?.message,
    lastAt: last?.at,
    lastTask: last?.task,
  };
}

// ── Reading the log ──────────────────────────────────────────────────────

export interface Activity {
  /** task → the agent that most recently started on it. */
  holder: Map<string, string>;
  /** task → every agent that has touched it. */
  touchedBy: Map<string, Set<string>>;
  /** The most recent agent line in the log. */
  lastAgent: string;
  lastTask: string;
  lastMessage: string;
  lastModel: string;
  lastAt: number;
  /** Every agent that has appeared, in first-seen order. */
  speakers: string[];
  /** agent → its most recent line. */
  lastBy: Map<string, { message: string; at: number; task: string }>;
  /** Agents inside an agent_start … agent_end pair right now, with their ticket and the phase they started in. */
  working: Map<string, { task: string; at: number; phase: string }>;
  /** The phase of the newest line, and who spoke it. */
  phase: FloorPhase | null;
  /** agent → the phase it last spoke in. */
  phaseOf: Map<string, string>;
}

/** An agent_start with no agent_end for this long is a crash, not work. */
const WORKING_TTL_MS = 10 * 60_000;

/**
 * The pipeline's own phases, in the order a run walks them. A `phase` event
 * naming one of these is the run moving on: anyone still "started" in an
 * earlier one never sent their agent_end (the split phase's "assigned
 * go-worker", the test phase's "verification pass") and is not working now.
 * `init` is absent on purpose: it is also said mid-run, about set-up.
 */
const RUN_PHASES = new Set(['explore', 'charter', 'plan', 'split', 'execute', 'test', 'verify', 'integrate', 'learn', 'memory', 'skills', 'session', 'done']);

/**
 * The generic role names the execute loop also speaks in. A `worker` line in
 * the middle of go-worker's turn (its file_change, its latency) is go-worker's,
 * not a second person's.
 */
const GENERIC_ROLES = new Set(['worker', 'tester', 'reviewer', 'corrector']);

/** Voices that are not people: the harness's own monitor, its bookkeeping. */
const NOT_PEOPLE = new Set(['manager', 'loop', 'harness', 'rewind', 'graph']);

/** The specialist a generic role line belongs to: whoever of that role is working now. */
function specialistFor(role: string, working: Map<string, { task: string; at: number; phase: string }>, task: string): string {
  let best = '';
  let bestAt = -Infinity;
  for (const [id, w] of working) {
    if (id === role || !id.endsWith('-' + role)) continue;
    if (task && w.task && w.task !== task) continue;
    if (w.at > bestAt) {
      best = id;
      bestAt = w.at;
    }
  }
  return best;
}

/**
 * readActivity is the floor's reading of the log: who is inside an
 * agent_start … agent_end pair right now, who spoke last, who holds which
 * ticket. It is exported because the NowBar's clock keys on the same working
 * set — when nobody is working, nothing is "in progress" to time.
 */
export function readActivity(events: RunEvent[]): Activity {
  const holder = new Map<string, string>();
  const touchedBy = new Map<string, Set<string>>();
  const seen = new Set<string>();
  const speakers: string[] = [];
  const lastBy = new Map<string, { message: string; at: number; task: string }>();
  const working = new Map<string, { task: string; at: number; phase: string }>();
  const phaseOf = new Map<string, string>();
  /** role → the specialist of that role heard from last (go-worker for worker). */
  const lastOfRole = new Map<string, string>();
  const cur: { phase: FloorPhase | null } = { phase: null };
  let lastAgent = '';
  let lastTask = '';
  let lastMessage = '';
  let lastModel = '';
  let lastAt = 0;
  for (const e of events) {
    let agent = (e.agent ?? '').trim().toLowerCase();
    // A generic role's line inside (or trailing) a specialist's turn is the specialist's.
    if (GENERIC_ROLES.has(agent)) agent = specialistFor(agent, working, e.task_id ?? '') || lastOfRole.get(agent) || agent;
    else for (const r of GENERIC_ROLES) if (agent.endsWith('-' + r)) lastOfRole.set(r, agent);
    if (e.kind === 'token' || e.kind === 'delta' || e.kind === 'token_delta') {
      // Tokens are proof of life for whoever is inside a start/end pair.
      const w = agent ? working.get(agent) : undefined;
      if (w) w.at = Date.parse(e.time) || w.at;
      continue;
    }
    const at = Date.parse(e.time) || lastAt;
    // Debug and latency lines are the harness's bookkeeping, not anyone speaking.
    if (e.kind === 'debug' || e.kind === 'latency') continue;
    // The run moved on: whoever started in an earlier phase and never said
    // agent_end is not working any more.
    if (e.kind === 'phase' && RUN_PHASES.has(e.phase)) {
      for (const [id, w] of working) {
        if (w.phase !== e.phase && RUN_PHASES.has(w.phase)) working.delete(id);
      }
    }
    if (e.phase && (e.kind !== 'agent_end' || !cur.phase)) {
      const prev = cur.phase;
      cur.phase = { id: e.phase, agent: agent === 'manager' || agent === 'loop' ? '' : agent, message: e.message ?? '', since: prev && prev.id === e.phase ? prev.since : at };
    }
    if (!agent) continue;
    // The charter voice, the loop and the harness's bookkeeping are not people on a team.
    if (NOT_PEOPLE.has(agent)) continue;
    if (e.phase) phaseOf.set(agent, e.phase);
    if (e.kind === 'agent_start') {
      // A nested start (phase "go-tester" inside phase "test") keeps the outer phase.
      const prev = working.get(agent);
      working.set(agent, { task: e.task_id || prev?.task || '', at, phase: RUN_PHASES.has(e.phase) ? e.phase : prev?.phase ?? e.phase });
    }
    if (e.kind === 'agent_end') working.delete(agent);
    if (!seen.has(agent)) {
      seen.add(agent);
      speakers.push(agent);
    }
    if (e.task_id) {
      if (e.kind === 'agent_end') {
        if (holder.get(e.task_id) === agent) holder.delete(e.task_id);
      } else {
        holder.set(e.task_id, agent);
      }
      const set = touchedBy.get(e.task_id) ?? new Set<string>();
      set.add(agent);
      touchedBy.set(e.task_id, set);
    }
    if (e.kind === 'agent_end') {
      if (lastAgent === agent) {
        lastAgent = '';
        lastTask = '';
        lastModel = '';
      }
      continue;
    }
    lastAgent = agent;
    lastTask = e.task_id ?? '';
    lastMessage = e.message ?? '';
    lastModel = e.model ?? '';
    lastAt = Date.parse(e.time) || lastAt;
    lastBy.set(agent, { message: lastMessage, at: lastAt, task: lastTask });
  }
  for (const [id, w] of working) {
    if (lastAt - w.at > WORKING_TTL_MS) working.delete(id);
  }
  return { holder, touchedBy, lastAgent, lastTask, lastMessage, lastModel, lastAt, speakers, lastBy, working, phase: cur.phase, phaseOf };
}

/** nowFor is what the floor's ticker says: who is working, on what, since when. */
export function nowFor(activity: Activity, taskTeams: Record<string, string>, nowMs: number, crewTeam?: string): FloorNow | null {
  if (!activity.lastAgent) return null;
  return {
    agent: activity.lastAgent,
    task: activity.lastTask,
    team: crewTeam ?? (activity.lastTask ? taskTeams[activity.lastTask] ?? '' : ''),
    message: activity.lastMessage,
    model: activity.lastModel,
    since: activity.lastAt || nowMs,
  };
}

/**
 * readHandoffs finds the manager decisions still worth animating: a ticket
 * moved from one agent to another within HANDOFF_TTL_MS, plus a manager's
 * proposal that has not been applied yet.
 */
function readHandoffs(events: RunEvent[], nowMs: number, squads: SquadsView | null | undefined): FloorHandoff[] {
  const taskTeams = squads?.ok ? (squads.task_teams ?? {}) : {};
  const out: FloorHandoff[] = [];
  for (let i = events.length - 1; i >= 0 && out.length < 6; i--) {
    const e = events[i];
    const at = Date.parse(e.time) || nowMs;
    if (nowMs - at > HANDOFF_TTL_MS) break;
    const msg = e.message ?? '';
    let m = reReassigned.exec(msg);
    if (m) {
      out.push({ task: m[1], from: m[2].toLowerCase(), to: m[3].toLowerCase(), team: taskTeams[m[1]] ?? '', at, reason: m[4] ?? '' });
      continue;
    }
    m = reProposes.exec(msg);
    if (m && e.task_id) {
      out.push({ task: e.task_id, from: m[1].toLowerCase(), to: m[2].toLowerCase(), team: taskTeams[e.task_id] ?? '', at, reason: m[3] ?? '' });
    }
  }
  return out.reverse();
}

// ── An agent's trail ─────────────────────────────────────────────────────

/**
 * agentTrail is the last `limit` things the log heard from one agent, newest
 * first — what the dossier shows when a person on the floor is clicked.
 */
export function agentTrail(events: RunEvent[], agentID: string, limit = 8): FloorTrailLine[] {
  const id = agentID.trim().toLowerCase();
  const out: FloorTrailLine[] = [];
  for (let i = events.length - 1; i >= 0 && out.length < limit; i--) {
    const e = events[i];
    if (e.kind === 'token' || e.kind === 'delta' || e.kind === 'token_delta') continue;
    if ((e.agent ?? '').trim().toLowerCase() !== id) continue;
    const message = (e.message ?? '').trim();
    if (!message) continue;
    out.push({ at: Date.parse(e.time) || 0, kind: e.kind, phase: e.phase, task: e.task_id || undefined, message, level: e.level });
  }
  return out;
}

// ── What just changed ────────────────────────────────────────────────────

const PULSE_CAP = 8;

/**
 * diffFloors lists what changed between two consecutive floors: tickets that
 * appeared or changed state or team, people who just started on something,
 * fresh handoffs, gates that ran, teams that finished. The first floor (prev
 * null) has no changes — the page must not open on a wall of "T1 appeared".
 */
export function diffFloors(prev: FloorModel | null, next: FloorModel, now: number): FloorPulse[] {
  if (!prev || prev.mode === 'idle') return [];
  const out: FloorPulse[] = [];
  const push = (p: Omit<FloorPulse, 'id' | 'at'>) => {
    out.push({ ...p, id: `${p.kind}:${p.ticket ?? p.agent ?? p.team}:${now}`, at: now });
  };

  const before = new Map<string, FloorTicket>();
  for (const t of prev.teams) for (const k of t.tickets) before.set(k.id, k);
  for (const k of prev.unassigned) before.set(k.id, k);
  const teamName = (id: string) => next.teams.find((t) => t.id === id)?.name || id;

  const all: FloorTicket[] = [...next.teams.flatMap((t) => t.tickets), ...next.unassigned];
  let ticketPulses = 0;
  for (const k of all) {
    const was = before.get(k.id);
    if (ticketPulses >= PULSE_CAP) break;
    if (!was) {
      ticketPulses++;
      push({ kind: 'ticket-new', tone: 'brand', team: k.team, ticket: k.id, agent: k.agent, text: `${k.id} appeared${k.team ? ` on ${teamName(k.team)}` : ''}`, detail: k.title });
      continue;
    }
    if (was.team !== k.team && k.team) {
      ticketPulses++;
      push({ kind: 'ticket-moved', tone: 'info', team: k.team, ticket: k.id, agent: k.agent, text: `${k.id} moved to ${teamName(k.team)}`, detail: k.title });
    }
    if (was.state !== k.state) {
      ticketPulses++;
      const who = k.agent ? ` · ${k.agent}` : '';
      switch (k.state) {
        case 'working':
          push({ kind: 'ticket-working', tone: 'info', team: k.team, ticket: k.id, agent: k.agent, text: `${k.id} picked up${who}`, detail: k.title });
          break;
        case 'review':
          push({ kind: 'ticket-review', tone: 'info', team: k.team, ticket: k.id, agent: k.agent, text: `${k.id} went to review${who}`, detail: k.title });
          break;
        case 'blocked':
          push({ kind: 'ticket-blocked', tone: 'warn', team: k.team, ticket: k.id, agent: k.agent, text: `${k.id} is blocked${who}`, detail: k.title });
          break;
        case 'done':
          push({ kind: 'ticket-done', tone: 'good', team: k.team, ticket: k.id, agent: k.agent ?? was.agent, text: `${k.id} done ✓${k.agent || was.agent ? ` · ${k.agent || was.agent}` : ''}`, detail: k.title });
          break;
        case 'failed':
          push({ kind: 'ticket-failed', tone: 'bad', team: k.team, ticket: k.id, agent: k.agent ?? was.agent, text: `${k.id} failed${k.agent || was.agent ? ` · ${k.agent || was.agent}` : ''}`, detail: k.title });
          break;
        default:
          ticketPulses--;
      }
    }
  }

  const wasActive = new Map<string, boolean>();
  for (const t of prev.teams) for (const a of t.agents) wasActive.set(`${t.id}/${a.id}`, a.active);
  const phaseOf = new Map(next.stage.map((a) => [a.id, a.phase]));
  for (const t of next.teams) {
    for (const a of t.agents) {
      if (a.active && !wasActive.get(`${t.id}/${a.id}`)) {
        const phase = phaseOf.get(a.id);
        push({
          kind: 'agent-start',
          tone: 'info',
          // The harness table's pulses are the run's, not a team's.
          team: t.internal ? '' : t.id,
          agent: a.id,
          ticket: a.task,
          text: t.internal ? `${a.id} is on${phase ? ` · ${phase}` : ''}` : `${a.id} started${a.task ? ` on ${a.task}` : ''}`,
          detail: a.lastMessage,
        });
      }
    }
  }
  if (next.phase && next.phase.id !== prev.phase?.id) {
    push({ kind: 'phase', tone: 'brand', team: '', agent: next.phase.agent || undefined, text: `phase: ${next.phase.id}`, detail: next.phase.message });
  }

  const seenHandoffs = new Set(prev.handoffs.map((h) => `${h.task}/${h.from}/${h.to}/${h.at}`));
  for (const h of next.handoffs) {
    if (seenHandoffs.has(`${h.task}/${h.from}/${h.to}/${h.at}`)) continue;
    push({ kind: 'handoff', tone: 'brand', team: h.team, ticket: h.task, agent: h.to, text: `${h.task}: ${h.from} → ${h.to}`, detail: h.reason || 'reassigned by the manager' });
  }

  const gateBefore = new Map(prev.teams.map((t) => [t.id, t.gate]));
  const doneBefore = new Map(prev.teams.map((t) => [t.id, t.complete]));
  for (const t of next.teams) {
    if (t.gate && t.gate !== 'unverified' && gateBefore.get(t.id) !== t.gate) {
      push({ kind: 'gate', tone: t.gate === 'green' ? 'good' : 'bad', team: t.id, text: `${t.name}: gate ${t.gate === 'green' ? 'green' : 'RED'}`, detail: t.acceptance });
    }
    if (t.complete && !doneBefore.get(t.id)) {
      push({ kind: 'team-complete', tone: 'good', team: t.id, text: `${t.name} finished every ticket`, detail: `${t.done}/${t.total}` });
    }
  }
  return out;
}

/** freshPulses drops what has aged out. */
export function freshPulses(pulses: FloorPulse[], now: number, ttl = PULSE_TTL_MS): FloorPulse[] {
  return pulses.filter((p) => now - p.at < ttl);
}
