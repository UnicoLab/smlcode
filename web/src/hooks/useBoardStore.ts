import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { errorText, getSquads, getTasks } from '@/api/client';
import type { Plan, RunEvent, SquadsView, Task } from '@/types';

// ── One live board for the whole app ─────────────────────────────────────
//
// Four pages used to poll the same two endpoints on four timers: the Board
// every 3 s forever, the Live floor and the task rail every 5 s while a run
// went, the Teams page every 5 s for the org chart. Four copies of the board,
// each a few seconds stale in its own way, and a request every 750 ms on
// average to a server that already tells us when a task changes.
//
// This is the one copy. It is seeded from GET /api/tasks and GET /api/squads,
// then kept current by the live stream: the server's `task_update` event
// carries the task exactly as GET /api/tasks would return it, and folding it
// in is a replace-by-id. Those events never reach the log — one per column
// move, per assignee change, per retry counter would bury the narrative
// under bookkeeping, and the log already has task_start / task_done for the
// story. A slow refresh (30 s) stays as the safety net for a server that does
// not emit task_update yet, and for the org chart, which has no event of its
// own; while the server has not yet pushed a single task_update, the store
// also refreshes shortly after a structural event (a task starting or
// finishing, a run starting or ending), so the older server still feels live.
//
// Pages read it with `useBoardStore()`. Edits go through `upsertTask` /
// `removeTask` for the optimistic paint and `refresh()` for the truth.

export interface BoardSnapshot {
  tasks: Task[];
  /** The board's column ids, in the server's order. */
  columns: string[];
  plan: Plan | null;
  /** The current run's org chart; null when there is none. */
  squads: SquadsView | null;
  /** True once both endpoints have been asked at least once. */
  ready: boolean;
  /** The last fetch error, or null. */
  error: string | null;
  /** When the store last changed, ms since the epoch. */
  updatedAt: number;
  /** True once the server has pushed a task_update — the board is live. */
  live: boolean;
}

export interface BoardStore extends BoardSnapshot {
  refreshing: boolean;
  /** Re-read the board and the org chart from the server. */
  refresh: () => Promise<void>;
  /** Paint an edit before the server confirms it (or in place of an event). */
  upsertTask: (task: Task) => void;
  removeTask: (id: string) => void;
}

export const FALLBACK_REFRESH_MS = 30_000;
/** How long after a structural log event a non-live store waits before re-reading. */
const STRUCTURAL_REFRESH_DELAY_MS = 800;

/** The kinds the stream folds into the store instead of the log. */
export function isBoardEvent(ev: Pick<RunEvent, 'kind'>): boolean {
  return ev.kind === 'task_update' || ev.kind === 'review_pending';
}

/** Log events after which a board that is not yet pushed to should re-read. */
const STRUCTURAL_KINDS = new Set(['task_start', 'task_done', 'task_fail', 'wave', 'run_start', 'run_end', 'coord', 'split']);

export function isStructuralEvent(ev: Pick<RunEvent, 'kind' | 'phase'>): boolean {
  return STRUCTURAL_KINDS.has(ev.kind) || ev.phase === 'done' || ev.phase === 'error';
}

export const EMPTY_BOARD: BoardSnapshot = {
  tasks: [],
  columns: [],
  plan: null,
  squads: null,
  ready: false,
  error: null,
  updatedAt: 0,
  live: false,
};

/** Replace a task by id, or append it. Order is preserved; identity changes only when something did. */
export function upsertTaskIn(tasks: Task[], task: Task): Task[] {
  const i = tasks.findIndex((t) => t.id === task.id);
  if (i < 0) return [...tasks, task];
  if (tasks[i] === task) return tasks;
  const next = tasks.slice();
  next[i] = task;
  return next;
}

/**
 * Fold one stream event into a snapshot. Returns the same snapshot when the
 * event is not one the board cares about, so callers can compare identities.
 */
export function foldBoardEvent(snap: BoardSnapshot, ev: RunEvent, now = Date.now()): BoardSnapshot {
  if (ev.kind !== 'task_update') return snap;
  const task = ev.data?.task;
  if (!task || typeof task.id !== 'string' || task.id === '') return snap;
  const tasks = upsertTaskIn(snap.tasks, task);
  const column = task.column || task.status;
  const columns = column && !snap.columns.includes(column) ? [...snap.columns, column] : snap.columns;
  return { ...snap, tasks, columns, updatedAt: now, live: true };
}

const BoardStoreContext = createContext<BoardStore | null>(null);
export const BoardStoreProvider = BoardStoreContext.Provider;

const NOOP_STORE: BoardStore = {
  ...EMPTY_BOARD,
  refreshing: false,
  refresh: async () => {},
  upsertTask: () => {},
  removeTask: () => {},
};

/** The live board. Outside the provider (tests, storybook) it is empty and inert. */
export function useBoardStore(): BoardStore {
  return useContext(BoardStoreContext) ?? NOOP_STORE;
}

export interface BoardSourceOptions {
  /** Disable network work (tests). */
  enabled?: boolean;
  /** The stream's connection state; a return to 'live' re-reads the board. */
  connection?: string;
  /** Whether a run is going; both edges re-read the board. */
  running?: boolean;
  /** The newest log event, for the structural-refresh fallback. */
  lastEvent?: RunEvent | null;
}

/**
 * The store's owner — mounted once, in App. Returns the store for the
 * provider and `applyEvent`, which the stream hook calls with every board
 * event it receives.
 */
export function useBoardStoreSource(opts: BoardSourceOptions = {}): { store: BoardStore; applyEvent: (ev: RunEvent) => void } {
  const { enabled = true, connection, running, lastEvent } = opts;
  const [snap, setSnap] = useState<BoardSnapshot>(EMPTY_BOARD);
  const [refreshing, setRefreshing] = useState(false);
  const mountedRef = useRef(true);
  const inflightRef = useRef<Promise<void> | null>(null);
  const queuedRef = useRef<Promise<void> | null>(null);
  const liveRef = useRef(false);

  const fetchOnce = useCallback((): Promise<void> => {
    setRefreshing(true);
    const p = Promise.allSettled([getTasks(), getSquads()])
      .then(([board, chart]) => {
        if (!mountedRef.current) return;
        setSnap((prev) => {
          const next: BoardSnapshot = { ...prev, ready: true, updatedAt: Date.now() };
          if (board.status === 'fulfilled') {
            const b = board.value;
            next.tasks = b?.tasks ?? [];
            next.columns = b?.columns ?? [];
            next.plan = b?.plan ?? null;
            next.error = null;
          } else {
            next.error = errorText(board.reason, 'Could not load the board');
          }
          // No org chart is the normal state, not an error.
          next.squads = chart.status === 'fulfilled' ? chart.value : null;
          return next;
        });
      })
      .finally(() => {
        inflightRef.current = null;
        if (mountedRef.current) setRefreshing(false);
      });
    inflightRef.current = p;
    return p;
  }, []);

  const refresh = useCallback((): Promise<void> => {
    if (!enabled) return Promise.resolve();
    // Coalesce: three pages asking at once is one request — but a request
    // made WHILE one is in flight may be asking about a write that request
    // predates, so exactly one more follows it.
    if (inflightRef.current) {
      if (!queuedRef.current) {
        queuedRef.current = inflightRef.current.then(() => {
          queuedRef.current = null;
          return mountedRef.current ? fetchOnce() : undefined;
        });
      }
      return queuedRef.current;
    }
    return fetchOnce();
  }, [enabled, fetchOnce]);

  const applyEvent = useCallback((ev: RunEvent) => {
    if (ev.kind !== 'task_update') return;
    liveRef.current = true;
    setSnap((prev) => foldBoardEvent(prev, ev));
  }, []);

  const upsertTask = useCallback((task: Task) => {
    setSnap((prev) => {
      const tasks = upsertTaskIn(prev.tasks, task);
      return tasks === prev.tasks ? prev : { ...prev, tasks, updatedAt: Date.now() };
    });
  }, []);

  const removeTask = useCallback((id: string) => {
    setSnap((prev) => {
      if (!prev.tasks.some((t) => t.id === id)) return prev;
      return { ...prev, tasks: prev.tasks.filter((t) => t.id !== id), updatedAt: Date.now() };
    });
  }, []);

  // Seed, then the slow safety net.
  useEffect(() => {
    mountedRef.current = true;
    if (!enabled) return undefined;
    void refresh();
    const id = window.setInterval(() => void refresh(), FALLBACK_REFRESH_MS);
    return () => {
      mountedRef.current = false;
      window.clearInterval(id);
    };
  }, [enabled, refresh]);

  // A reconnect may have missed events; both edges of a run move the board
  // (tasks appear at split, the org chart at charter, the tail lands at done).
  const firstRef = useRef(true);
  useEffect(() => {
    if (!enabled) return;
    if (firstRef.current) {
      firstRef.current = false;
      return;
    }
    if (connection === 'live') void refresh();
  }, [connection, enabled, refresh]);

  const runningRef = useRef(running);
  useEffect(() => {
    if (!enabled) return;
    if (runningRef.current !== running) {
      runningRef.current = running;
      void refresh();
    }
  }, [running, enabled, refresh]);

  // The degraded path: no task_update yet, so a structural line in the log is
  // the cue to re-read — debounced, since a wave starts several tasks at once.
  const structuralTimer = useRef<number | null>(null);
  useEffect(() => {
    if (!enabled || !lastEvent || liveRef.current || !isStructuralEvent(lastEvent)) return undefined;
    if (structuralTimer.current !== null) window.clearTimeout(structuralTimer.current);
    structuralTimer.current = window.setTimeout(() => {
      structuralTimer.current = null;
      void refresh();
    }, STRUCTURAL_REFRESH_DELAY_MS);
    return undefined;
  }, [lastEvent, enabled, refresh]);
  useEffect(
    () => () => {
      if (structuralTimer.current !== null) window.clearTimeout(structuralTimer.current);
    },
    [],
  );

  const store = useMemo<BoardStore>(
    () => ({ ...snap, refreshing, refresh, upsertTask, removeTask }),
    [snap, refreshing, refresh, upsertTask, removeTask],
  );
  return { store, applyEvent };
}
