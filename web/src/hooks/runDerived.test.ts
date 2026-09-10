import { describe, expect, it } from 'vitest';
import { createDerived, deriveAll, foldDerived, snapshotDerived } from './runDerived';
import type { RunEvent } from '@/types';

const T0 = Date.parse('2026-09-10T10:00:00Z');
const ev = (over: Partial<RunEvent>, s = 0): RunEvent => ({
  phase: 'execute',
  kind: 'output',
  message: '',
  time: new Date(T0 + s * 1000).toISOString(),
  ...over,
});

describe('runDerived', () => {
  it('folds phases, agents, tasks, files and usage once per event', () => {
    const acc = createDerived();
    foldDerived(acc, ev({ phase: 'plan', agent: 'planner', tokens: 100, cost_usd: 0.001, model: 'm1' }, 0));
    foldDerived(acc, ev({ phase: 'split', agent: 'splitter', task_id: 'T1' }, 1));
    foldDerived(acc, ev({ phase: 'execute', kind: 'agent_start', agent: 'go-worker', task_id: 'T1', model: 'm2' }, 2));
    foldDerived(acc, ev({ phase: 'execute', kind: 'file_change', scope: 'cmd/main.go', agent: 'go-worker', task_id: 'T1', tokens: 50 }, 3));
    foldDerived(acc, ev({ phase: 'execute', kind: 'file_change', scope: 'cmd/main.go', task_id: 'T2' }, 4));
    foldDerived(acc, ev({ phase: 'execute', kind: 'output', tokens: -5 }, 5));

    const d = snapshotDerived(acc);
    expect(d.count).toBe(6);
    expect(d.phases).toEqual(['plan', 'split', 'execute']);
    expect(d.activePhase).toBe('execute');
    expect(d.agents).toEqual(['planner', 'splitter', 'go-worker']);
    expect(d.activeAgent).toBe('go-worker');
    expect(d.activeModel).toBe('');
    expect([...d.taskIds]).toEqual(['T1', 'T2']);
    expect([...d.files]).toEqual(['cmd/main.go']);
    // Negative or missing usage never subtracts.
    expect(d.tokens).toBe(150);
    expect(d.cost).toBeCloseTo(0.001);
    expect(d.firstAt).toBe(T0);
    expect(d.lastAt).toBe(T0 + 5000);
    expect(d.last?.kind).toBe('output');
  });

  it('remembers the model of the agent line that named it', () => {
    const acc = createDerived();
    foldDerived(acc, ev({ agent: 'go-worker', model: 'Qwen3-Coder-30B' }));
    expect(snapshotDerived(acc).activeModel).toBe('Qwen3-Coder-30B');
  });

  it('keeps the newest composition event', () => {
    const acc = createDerived();
    foldDerived(acc, ev({ kind: 'composition', data: { summary: 'first' } }));
    foldDerived(acc, ev({ kind: 'composition', data: { summary: 'second' } }));
    foldDerived(acc, ev({ kind: 'output' }));
    expect(snapshotDerived(acc).composition?.summary).toBe('second');
  });

  it('snapshots are independent of later folds', () => {
    const acc = createDerived();
    foldDerived(acc, ev({ task_id: 'T1' }));
    const before = snapshotDerived(acc);
    foldDerived(acc, ev({ task_id: 'T2' }));
    const after = snapshotDerived(acc);
    expect(before.taskIds.size).toBe(1);
    expect(after.taskIds.size).toBe(2);
    expect(after).not.toBe(before);
  });

  it('deriveAll equals folding one by one', () => {
    const events = [ev({ phase: 'plan', agent: 'planner' }), ev({ phase: 'execute', task_id: 'T1', tokens: 7 })];
    const acc = createDerived();
    for (const e of events) foldDerived(acc, e);
    expect(snapshotDerived(deriveAll(events))).toEqual(snapshotDerived(acc));
  });
});
