import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { MockEventSource } from '@/test/setup';
import { useLiveStream, isTokenEvent, tokenText } from './useLiveStream';
import type { RunEvent } from '@/types';

vi.mock('@/api/client', async () => {
  const actual = await vi.importActual<typeof import('@/api/client')>('@/api/client');
  return {
    ...actual,
    getLatestRun: vi.fn(async () => ({ running: false, result: null, events: [], event_seqs: [], last_seq: 0 })),
    getHealth: vi.fn(async () => ({ ok: true, running: false })),
    createEventSource: vi.fn((lastEventId?: number) => {
      const url = lastEventId ? `/api/events?last_event_id=${lastEventId}` : '/api/events';
      return new MockEventSource(url) as unknown as EventSource;
    }),
  };
});

const ev = (message: string, extra: Partial<RunEvent> = {}): RunEvent => ({
  phase: 'execute',
  kind: 'output',
  message,
  time: new Date().toISOString(),
  ...extra,
});

async function mountStream() {
  const hook = renderHook(() => useLiveStream());
  // The hook fetches the snapshot before connecting.
  await waitFor(() => expect(MockEventSource.instances.length).toBeGreaterThan(0));
  await act(async () => {
    MockEventSource.last.open();
  });
  return hook;
}

describe('useLiveStream — event accumulation', () => {
  beforeEach(() => {
    MockEventSource.reset();
  });

  // This is the regression test for the defect that made the flagship Live page
  // useless: the log only ever showed the LAST event, because the message
  // handler closed over a frozen copy of the events array.
  it('accumulates every event instead of resetting to the last one', async () => {
    const { result } = await mountStream();

    await act(async () => {
      MockEventSource.last.emitMessage(ev('first'), 1);
    });
    await act(async () => {
      MockEventSource.last.emitMessage(ev('second'), 2);
    });
    await act(async () => {
      MockEventSource.last.emitMessage(ev('third'), 3);
    });

    expect(result.current.events).toHaveLength(3);
    expect(result.current.events.map((e) => e.message)).toEqual(['first', 'second', 'third']);
    expect(result.current.lastSeq).toBe(3);
  });

  it('keeps accumulating across many messages delivered in one tick', async () => {
    const { result } = await mountStream();
    await act(async () => {
      for (let i = 1; i <= 25; i++) {
        MockEventSource.last.emitMessage(ev(`e${i}`), i);
      }
    });
    expect(result.current.events).toHaveLength(25);
    expect(result.current.events[0].message).toBe('e1');
    expect(result.current.events[24].message).toBe('e25');
  });

  it('deduplicates by sequence id so a replay cannot double-render', async () => {
    const { result } = await mountStream();
    await act(async () => {
      MockEventSource.last.emitMessage(ev('a'), 1);
      MockEventSource.last.emitMessage(ev('b'), 2);
      MockEventSource.last.emitMessage(ev('a'), 1); // replayed
      MockEventSource.last.emitMessage(ev('b'), 2);
    });
    expect(result.current.events.map((e) => e.message)).toEqual(['a', 'b']);
  });

  it('clears the log when the server announces a new run', async () => {
    const { result } = await mountStream();
    await act(async () => {
      MockEventSource.last.emitMessage(ev('old run'), 1);
    });
    expect(result.current.events).toHaveLength(1);

    await act(async () => {
      MockEventSource.last.emitMessage(ev('run started', { kind: 'run_start', phase: 'init' }), 2);
    });
    expect(result.current.events.map((e) => e.message)).toEqual(['run started']);
    expect(result.current.running).toBe(true);
  });

  it('raises askSignal on an ask event so HITL can react without polling', async () => {
    const { result } = await mountStream();
    expect(result.current.askSignal).toBe(0);
    await act(async () => {
      MockEventSource.last.emitMessage(ev('needs input', { kind: 'ask', phase: 'clarify' }), 1);
    });
    expect(result.current.askSignal).toBe(1);
  });

  it('accumulates token deltas into a buffer instead of log rows', async () => {
    const { result } = await mountStream();
    await act(async () => {
      MockEventSource.last.emitMessage(ev('Hel', { kind: 'token', output: 'Hel' }), 1);
      MockEventSource.last.emitMessage(ev('lo ', { kind: 'token', output: 'lo ' }), 2);
      MockEventSource.last.emitMessage(ev('world', { kind: 'token', output: 'world' }), 3);
    });
    expect(result.current.tokenStream).toBe('Hello world');
    // Token deltas must not flood the structural log.
    expect(result.current.events).toHaveLength(0);
  });

  it('resets the token buffer between agent turns', async () => {
    const { result } = await mountStream();
    await act(async () => {
      MockEventSource.last.emitMessage(ev('abc', { kind: 'token', output: 'abc' }), 1);
      MockEventSource.last.emitMessage(ev('worker done', { kind: 'agent_end' }), 2);
    });
    expect(result.current.tokenStream).toBe('');
  });

  it('surfaces a gap frame reported by the server', async () => {
    const { result } = await mountStream();
    await act(async () => {
      MockEventSource.last.dispatch('gap', JSON.stringify({ from: 5, to: 9, message: 'events were dropped' }));
    });
    expect(result.current.gap).toBe('events were dropped');
    act(() => result.current.clearGap());
    expect(result.current.gap).toBeNull();
  });
});

describe('useLiveStream — connection state', () => {
  beforeEach(() => {
    MockEventSource.reset();
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });

  it('reports live once connected, then reconnecting on failure', async () => {
    const { result } = await mountStream();
    expect(result.current.connection).toBe('live');

    await act(async () => {
      MockEventSource.last.fail();
    });
    expect(result.current.connection).toBe('reconnecting');
  });

  it('escalates to down after repeated failures and resumes from the last seq', async () => {
    const { result } = await mountStream();
    await act(async () => {
      MockEventSource.last.emitMessage(ev('x'), 7);
    });

    for (let i = 0; i < 3; i++) {
      await act(async () => {
        MockEventSource.last.fail();
        await vi.advanceTimersByTimeAsync(20_000);
      });
    }
    expect(result.current.connection).toBe('down');

    // Reconnects must ask for only what was missed.
    expect(MockEventSource.last.url).toContain('last_event_id=7');
  });
});

describe('token event helpers', () => {
  it('recognises the token kinds the engine may emit', () => {
    expect(isTokenEvent({ kind: 'token' })).toBe(true);
    expect(isTokenEvent({ kind: 'delta' })).toBe(true);
    expect(isTokenEvent({ kind: 'token_delta' })).toBe(true);
    expect(isTokenEvent({ kind: 'output' })).toBe(false);
  });

  it('prefers output over message for the delta text', () => {
    expect(tokenText(ev('msg', { kind: 'token', output: 'out' }))).toBe('out');
    expect(tokenText(ev('msg', { kind: 'token' }))).toBe('msg');
    expect(tokenText(ev('msg', { kind: 'output', output: 'out' }))).toBe('');
  });
});
