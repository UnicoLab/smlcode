import type { DynamicComposition, RunEvent } from '@/types';

// ── What the log adds up to, folded once per event ───────────────────────
//
// Five components used to answer the same questions by walking the whole log
// on every stream flush: which phases have been seen, which is current, who
// spoke last, how many tasks and files the run has touched, what the tokens
// cost. Seven full scans of up to two thousand events, sixty times a second
// at the peak of a burst — and every one of them recomputing a number that
// only the newest event could have changed.
//
// This is that arithmetic done once, in the stream hook, as each event lands.
// The accumulator is mutable and private to the hook; what components see is
// a snapshot taken at flush time, a new identity whenever the log changed so
// memoised consumers re-run exactly then.

export interface RunDerived {
  /** Events folded so far, including any the log has since trimmed. */
  count: number;
  /** Phases in first-seen order. */
  phases: string[];
  phaseSet: ReadonlySet<string>;
  /** The phase of the newest event that named one. */
  activePhase: string | null;
  /** Agents in first-seen order. */
  agents: string[];
  /** The agent of the newest event that named one. */
  activeAgent: string | null;
  /** The model that agent's line named, if any. */
  activeModel: string;
  taskIds: ReadonlySet<string>;
  /** Paths the run wrote to (`file_change` scopes). */
  files: ReadonlySet<string>;
  tokens: number;
  cost: number;
  /** The newest `composition` event's payload. */
  composition: DynamicComposition | null;
  last: RunEvent | null;
  /** Wall-clock bounds of the run, ms; 0 until an event carries a time. */
  firstAt: number;
  lastAt: number;
}

/** The mutable working copy. Never handed to React. */
export interface RunDerivedAcc {
  count: number;
  phases: string[];
  phaseSet: Set<string>;
  activePhase: string | null;
  agents: string[];
  agentSet: Set<string>;
  activeAgent: string | null;
  activeModel: string;
  taskIds: Set<string>;
  files: Set<string>;
  tokens: number;
  cost: number;
  composition: DynamicComposition | null;
  last: RunEvent | null;
  firstAt: number;
  lastAt: number;
}

export function createDerived(): RunDerivedAcc {
  return {
    count: 0,
    phases: [],
    phaseSet: new Set(),
    activePhase: null,
    agents: [],
    agentSet: new Set(),
    activeAgent: null,
    activeModel: '',
    taskIds: new Set(),
    files: new Set(),
    tokens: 0,
    cost: 0,
    composition: null,
    last: null,
    firstAt: 0,
    lastAt: 0,
  };
}

/** Fold one appended event into the accumulator. Mutates and returns it. */
export function foldDerived(acc: RunDerivedAcc, ev: RunEvent): RunDerivedAcc {
  acc.count += 1;
  acc.last = ev;
  if (ev.phase) {
    if (!acc.phaseSet.has(ev.phase)) {
      acc.phaseSet.add(ev.phase);
      acc.phases.push(ev.phase);
    }
    acc.activePhase = ev.phase;
  }
  if (ev.agent) {
    if (!acc.agentSet.has(ev.agent)) {
      acc.agentSet.add(ev.agent);
      acc.agents.push(ev.agent);
    }
    acc.activeAgent = ev.agent;
    acc.activeModel = ev.model ?? '';
  }
  if (ev.task_id) acc.taskIds.add(ev.task_id);
  if (ev.kind === 'file_change' && ev.scope) acc.files.add(ev.scope);
  if (typeof ev.tokens === 'number' && ev.tokens > 0) acc.tokens += ev.tokens;
  if (typeof ev.cost_usd === 'number' && ev.cost_usd > 0) acc.cost += ev.cost_usd;
  if (ev.kind === 'composition' && ev.data && typeof ev.data.summary === 'string') acc.composition = ev.data;
  const t = Date.parse(ev.time || '');
  if (!Number.isNaN(t)) {
    if (!acc.firstAt) acc.firstAt = t;
    acc.lastAt = t;
  }
  return acc;
}

/** Fold a whole log — the seed from a snapshot or sessionStorage. */
export function deriveAll(events: readonly RunEvent[]): RunDerivedAcc {
  const acc = createDerived();
  for (const ev of events) foldDerived(acc, ev);
  return acc;
}

/**
 * A copy for React: fresh identity, and fresh collections so a consumer that
 * keys on `derived.taskIds` sees the change too.
 */
export function snapshotDerived(acc: RunDerivedAcc): RunDerived {
  return {
    count: acc.count,
    phases: acc.phases.slice(),
    phaseSet: new Set(acc.phaseSet),
    activePhase: acc.activePhase,
    agents: acc.agents.slice(),
    activeAgent: acc.activeAgent,
    activeModel: acc.activeModel,
    taskIds: new Set(acc.taskIds),
    files: new Set(acc.files),
    tokens: acc.tokens,
    cost: acc.cost,
    composition: acc.composition,
    last: acc.last,
    firstAt: acc.firstAt,
    lastAt: acc.lastAt,
  };
}

export const EMPTY_DERIVED: RunDerived = snapshotDerived(createDerived());
