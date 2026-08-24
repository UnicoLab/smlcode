import { useCallback, useEffect, useRef, useState } from 'react';
import { createEventSource, getHealth, getLatestRun } from '@/api/client';
import type { ConnectionState, LatestRunResponse, RunEvent } from '@/types';

// ── The live event stream ──
//
// This hook replaces the previous per-page EventSource wiring, which lost every
// event but the last one. The bug: `connectSSE` was a `useCallback(…, [])`, so
// `es.onmessage` permanently captured the FIRST render's closure; the `setEvents`
// helper it called was a plain function closing over the `localEvents` *value*
// from that render, so `[...prev, data]` was always computed against a frozen,
// empty array and the log reset to a single entry on every message.
//
// The fix is structural, not cosmetic: **no callback here ever reads React
// state**. All mutable stream state lives in refs, and `setState` is only ever
// called with a value derived from those refs. A stale closure therefore cannot
// produce a stale result, no matter how the effect is scheduled.
//
// It also implements the reconnect contract the server now offers:
//   • every frame carries `id: <seq>` → we track the highest seq seen and hand
//     it back as `last_event_id` on reconnect, so a laptop wake or a server
//     restart resumes instead of re-rendering the whole run;
//   • duplicates are dropped by seq, so a snapshot + a replay cannot double up;
//   • the named `connected` and `gap` frames are actually listened for —
//     `onmessage` never fires for named events, so the old code could not see
//     them at all.

const MAX_EVENTS = 2000;
const HEALTH_POLL_MS = 10_000;
const RECONNECT_BASE_MS = 1_000;
const RECONNECT_MAX_MS = 15_000;
/** Failed attempts before we call the backend "down" rather than "reconnecting". */
const DOWN_AFTER_ATTEMPTS = 3;

export interface LiveStream {
  events: RunEvent[];
  running: boolean;
  connection: ConnectionState;
  /** Highest SSE sequence number applied. */
  lastSeq: number;
  /** Set when the server reported dropped events; cleared by `clearGap`. */
  gap: string | null;
  clearGap: () => void;
  /**
   * Increments whenever an `ask` event arrives. HITL UI reacts to this instead
   * of polling five endpoints every two seconds.
   */
  askSignal: number;
  /** Concatenated token deltas for the current agent turn (capability B). */
  tokenStream: string;
  latest: LatestRunResponse | null;
  setRunning: (running: boolean) => void;
  /** Clear the log — used when a new run starts from this tab. */
  reset: () => void;
  /** Force an immediate reconnect (used by the "retry" affordance). */
  reconnect: () => void;
}

/** Token-delta event kind. Tolerated whether or not the engine emits it yet. */
const TOKEN_KINDS = new Set(['token', 'delta', 'token_delta']);

export function isTokenEvent(ev: Pick<RunEvent, 'kind'>): boolean {
  return TOKEN_KINDS.has(ev.kind);
}

/** Extract the streamed text from a token event, whatever field carries it. */
export function tokenText(ev: RunEvent): string {
  if (!isTokenEvent(ev)) return '';
  return ev.output || ev.message || '';
}

interface StreamOptions {
  /** Seed the log from sessionStorage so navigation does not blank the view. */
  initialEvents?: RunEvent[];
  initialRunning?: boolean;
  /** Persist hook — called with the current log after each batch. */
  onEvents?: (events: RunEvent[]) => void;
  onRunning?: (running: boolean) => void;
  /** Disable network work (tests, storybook). */
  enabled?: boolean;
}

export function useLiveStream(opts: StreamOptions = {}): LiveStream {
  const { enabled = true } = opts;

  // ── Mutable stream state (never read from a closure over React state) ──
  const eventsRef = useRef<RunEvent[]>(opts.initialEvents ?? []);
  const seenRef = useRef<Set<number>>(new Set());
  const lastSeqRef = useRef(0);
  const tokenRef = useRef('');
  const esRef = useRef<EventSource | null>(null);
  const retryRef = useRef<number | null>(null);
  const attemptsRef = useRef(0);
  const mountedRef = useRef(true);
  const runningRef = useRef(opts.initialRunning ?? false);
  const onEventsRef = useRef(opts.onEvents);
  const onRunningRef = useRef(opts.onRunning);
  onEventsRef.current = opts.onEvents;
  onRunningRef.current = opts.onRunning;

  const [events, setEvents] = useState<RunEvent[]>(eventsRef.current);
  const [running, setRunningState] = useState(runningRef.current);
  const [connection, setConnection] = useState<ConnectionState>(enabled ? 'connecting' : 'down');
  const [lastSeq, setLastSeq] = useState(0);
  const [gap, setGap] = useState<string | null>(null);
  const [askSignal, setAskSignal] = useState(0);
  const [tokenStream, setTokenStream] = useState('');
  const [latest, setLatest] = useState<LatestRunResponse | null>(null);

  /** Publish the ref-held log to React with a fresh array identity. */
  const publish = useCallback(() => {
    if (!mountedRef.current) return;
    const next = eventsRef.current;
    setEvents(next.slice());
    setLastSeq(lastSeqRef.current);
    onEventsRef.current?.(next);
  }, []);

  const setRunning = useCallback((next: boolean) => {
    runningRef.current = next;
    if (mountedRef.current) setRunningState(next);
    onRunningRef.current?.(next);
  }, []);

  /** Append one event. `seq` of 0 means "no id" (a locally synthesised event). */
  const append = useCallback((ev: RunEvent, seq: number): boolean => {
    if (seq > 0) {
      if (seenRef.current.has(seq)) return false;
      seenRef.current.add(seq);
      if (seq > lastSeqRef.current) lastSeqRef.current = seq;
    }
    const next = eventsRef.current.concat(ev);
    eventsRef.current = next.length > MAX_EVENTS ? next.slice(next.length - MAX_EVENTS) : next;
    return true;
  }, []);

  const reset = useCallback(() => {
    eventsRef.current = [];
    tokenRef.current = '';
    // Sequence numbers stay monotonic across runs, so `seen` must NOT be
    // cleared here — clearing it would let a replayed event re-appear.
    if (mountedRef.current) {
      setEvents([]);
      setTokenStream('');
    }
    onEventsRef.current?.([]);
  }, []);

  const clearGap = useCallback(() => setGap(null), []);

  // ── SSE connection. Stable identity, zero state dependencies. ──
  const connect = useCallback(() => {
    if (!enabled || typeof EventSource === 'undefined') return;
    if (retryRef.current !== null) {
      window.clearTimeout(retryRef.current);
      retryRef.current = null;
    }
    esRef.current?.close();

    const es = createEventSource(lastSeqRef.current);
    esRef.current = es;
    if (mountedRef.current) {
      // Once escalated to 'down', a dispatched retry must not silently
      // downgrade the banner back to 'reconnecting' — the user would lose
      // the severity signal on every subsequent attempt, right when it
      // matters most. 'down' only clears on an actual successful connection
      // (the 'connected'/onopen handlers reset attemptsRef to 0 below).
      setConnection(
        attemptsRef.current >= DOWN_AFTER_ATTEMPTS
          ? 'down'
          : attemptsRef.current > 0
            ? 'reconnecting'
            : 'connecting',
      );
    }

    es.addEventListener('connected', () => {
      attemptsRef.current = 0;
      if (mountedRef.current) setConnection('live');
    });

    es.addEventListener('gap', (raw) => {
      try {
        const info = JSON.parse((raw as MessageEvent).data);
        if (mountedRef.current) {
          setGap(
            typeof info?.message === 'string'
              ? info.message
              : 'Some events were dropped while disconnected.',
          );
        }
      } catch {
        if (mountedRef.current) setGap('Some events were dropped while disconnected.');
      }
    });

    es.onopen = () => {
      attemptsRef.current = 0;
      if (mountedRef.current) setConnection('live');
    };

    es.onmessage = (raw: MessageEvent) => {
      let data: RunEvent;
      try {
        data = JSON.parse(raw.data) as RunEvent;
      } catch {
        return;
      }
      const seq = Number.parseInt(raw.lastEventId || '0', 10) || 0;

      if (data.kind === 'run_start') {
        reset();
      }
      if (isTokenEvent(data)) {
        // Token deltas accumulate into a single live buffer instead of adding
        // thousands of rows to the log.
        if (seq > 0 && seenRef.current.has(seq)) return;
        if (seq > 0) {
          seenRef.current.add(seq);
          if (seq > lastSeqRef.current) lastSeqRef.current = seq;
        }
        tokenRef.current = (tokenRef.current + tokenText(data)).slice(-20_000);
        if (mountedRef.current) {
          setTokenStream(tokenRef.current);
          setLastSeq(lastSeqRef.current);
        }
        return;
      }
      if (data.kind === 'agent_start' || data.kind === 'agent_end') {
        tokenRef.current = '';
        if (mountedRef.current) setTokenStream('');
      }

      if (!append(data, seq)) return;
      publish();

      if (data.kind === 'ask') {
        if (mountedRef.current) setAskSignal((n) => n + 1);
      }
      if (data.phase === 'done' || data.phase === 'error' || data.kind === 'run_end') {
        setRunning(false);
        getLatestRun()
          .then((r) => {
            if (mountedRef.current) setLatest(r);
          })
          .catch(() => {
            /* the connection banner already reports API trouble */
          });
      }
      if (data.kind === 'run_start') {
        setRunning(true);
      }
    };

    es.onerror = () => {
      es.close();
      if (esRef.current !== es) return;
      attemptsRef.current += 1;
      if (mountedRef.current) {
        setConnection(attemptsRef.current >= DOWN_AFTER_ATTEMPTS ? 'down' : 'reconnecting');
      }
      const delay = Math.min(RECONNECT_BASE_MS * 2 ** (attemptsRef.current - 1), RECONNECT_MAX_MS);
      retryRef.current = window.setTimeout(() => {
        retryRef.current = null;
        if (esRef.current === es) connect();
      }, delay);
    };
  }, [append, enabled, publish, reset, setRunning]);

  const reconnect = useCallback(() => {
    attemptsRef.current = 0;
    connect();
  }, [connect]);

  // ── Mount: seed from the server snapshot, then stream ──
  useEffect(() => {
    mountedRef.current = true;
    if (!enabled) return undefined;

    getLatestRun()
      .then((r) => {
        if (!mountedRef.current) return;
        setLatest(r);
        setRunning(Boolean(r.running));
        // Seed the seq baseline so the stream does not replay the snapshot.
        const seqs = r.event_seqs ?? [];
        if (eventsRef.current.length === 0 && Array.isArray(r.events)) {
          const kept: RunEvent[] = [];
          r.events.forEach((ev, i) => {
            const seq = seqs[i] ?? 0;
            if (seq > 0) {
              seenRef.current.add(seq);
              if (seq > lastSeqRef.current) lastSeqRef.current = seq;
            }
            if (!isTokenEvent(ev)) kept.push(ev);
          });
          eventsRef.current = kept.slice(-MAX_EVENTS);
          publish();
        } else if (typeof r.last_seq === 'number' && r.last_seq > lastSeqRef.current) {
          lastSeqRef.current = r.last_seq;
        }
      })
      .catch(() => {
        if (mountedRef.current) setConnection('down');
      })
      .finally(() => {
        if (mountedRef.current) connect();
      });

    return () => {
      mountedRef.current = false;
      if (retryRef.current !== null) window.clearTimeout(retryRef.current);
      const es = esRef.current;
      esRef.current = null;
      es?.close();
    };
    // `connect`/`publish`/`setRunning` are stable useCallbacks with no state deps.
  }, [connect, enabled, publish, setRunning]);

  // ── Health poll: an idle EventSource can look alive after the server dies ──
  useEffect(() => {
    if (!enabled) return undefined;
    let cancelled = false;
    const tick = async () => {
      try {
        const h = await getHealth();
        if (cancelled || !mountedRef.current) return;
        const es = esRef.current;
        // EventSource.OPEN === 1; CONNECTING === 0. A successful health
        // check only means the plain HTTP API answered — it says nothing
        // about the SSE connection itself. Only promote state (and reset
        // the backoff) when the EventSource is genuinely open; otherwise
        // leave connection/attemptsRef alone so the escalated 'down' state
        // from repeated onerror failures survives until the stream
        // actually reconnects, instead of flapping back to 'reconnecting'
        // (and resetting backoff to its fastest delay) on every poll tick.
        if (es && es.readyState === 1) {
          attemptsRef.current = 0;
          setConnection('live');
        }
        if (typeof h.running === 'boolean' && h.running !== runningRef.current) {
          setRunning(h.running);
        }
      } catch {
        if (!cancelled && mountedRef.current) setConnection('down');
      }
    };
    const id = window.setInterval(tick, HEALTH_POLL_MS);
    return () => {
      cancelled = true;
      window.clearInterval(id);
    };
  }, [enabled, setRunning]);

  return {
    events,
    running,
    connection,
    lastSeq,
    gap,
    clearGap,
    askSignal,
    tokenStream,
    latest,
    setRunning,
    reset,
    reconnect,
  };
}
