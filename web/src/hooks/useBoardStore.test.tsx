import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { EMPTY_BOARD, foldBoardEvent, isBoardEvent, isStructuralEvent, upsertTaskIn, useBoardStoreSource } from './useBoardStore';
import type { RunEvent, Task } from '@/types';

const getTasks = vi.fn();
const getSquads = vi.fn();

vi.mock('@/api/client', async () => {
  const actual = await vi.importActual<typeof import('@/api/client')>('@/api/client');
  return {
    ...actual,
    getTasks: (...a: unknown[]) => getTasks(...a),
    getSquads: (...a: unknown[]) => getSquads(...a),
  };
});

function task(id: string, over: Partial<Task> = {}): Task {
  return {
    id, title: `Task ${id}`, description: '', role: 'worker', assignee: '', column: 'ready_to_dev', status: 'ready',
    priority: 0, depends_on: [], files: [], acceptance: '', checklist: [], output: '', review: '', retries: 0,
    error: '', updated_at: '', notes: '', ...over,
  };
}

/** A `task_update` frame as the server sends it: the task in `data.task`. */
function taskUpdate(t: Task): RunEvent {
  return { phase: 'execute', kind: 'task_update', message: '', task_id: t.id, time: new Date().toISOString(), data: { task: t } as RunEvent['data'] };
}

beforeEach(() => {
  vi.clearAllMocks();
  getTasks.mockResolvedValue({ plan: { summary: 'the plan' }, tasks: [task('T1'), task('T2')], columns: ['to_scope', 'done'], by_column: {} });
  getSquads.mockResolvedValue({ ok: true, squads: [{ id: 'backend-go', total: 2, done: 0, blocked: 0, in_flight: 0, complete: false, stuck: false }] });
});

describe('foldBoardEvent', () => {
  it('replaces a task by id and keeps the order', () => {
    const snap = { ...EMPTY_BOARD, tasks: [task('T1'), task('T2', { column: 'ready_to_dev' }), task('T3')], columns: ['ready_to_dev'] };
    const next = foldBoardEvent(snap, taskUpdate(task('T2', { column: 'in_progress', status: 'running' })), 1000);
    expect(next.tasks.map((t) => t.id)).toEqual(['T1', 'T2', 'T3']);
    expect(next.tasks[1].column).toBe('in_progress');
    expect(next.live).toBe(true);
    expect(next.updatedAt).toBe(1000);
    // A column the board had not seen joins the list, so the Board can draw it.
    expect(next.columns).toEqual(['ready_to_dev', 'in_progress']);
  });

  it('appends a task it has not seen', () => {
    const next = foldBoardEvent({ ...EMPTY_BOARD, tasks: [task('T1')] }, taskUpdate(task('T9')));
    expect(next.tasks.map((t) => t.id)).toEqual(['T1', 'T9']);
  });

  it('ignores everything that is not a task_update with a task', () => {
    const snap = { ...EMPTY_BOARD, tasks: [task('T1')] };
    expect(foldBoardEvent(snap, { phase: 'execute', kind: 'task_done', message: 'done', task_id: 'T1', time: '' })).toBe(snap);
    expect(foldBoardEvent(snap, { phase: 'execute', kind: 'task_update', message: '', time: '' })).toBe(snap);
    expect(foldBoardEvent(snap, { phase: 'execute', kind: 'review_pending', message: '', time: '', data: { pending: 3 } as RunEvent['data'] })).toBe(snap);
  });

  it('keeps the array identity when nothing changed', () => {
    const t = task('T1');
    const tasks = [t];
    expect(upsertTaskIn(tasks, t)).toBe(tasks);
  });

  it('knows which kinds are board bookkeeping and which are structural', () => {
    expect(isBoardEvent({ kind: 'task_update' })).toBe(true);
    expect(isBoardEvent({ kind: 'review_pending' })).toBe(true);
    expect(isBoardEvent({ kind: 'task_done' })).toBe(false);
    expect(isStructuralEvent({ kind: 'task_done', phase: 'execute' })).toBe(true);
    expect(isStructuralEvent({ kind: 'output', phase: 'done' })).toBe(true);
    expect(isStructuralEvent({ kind: 'output', phase: 'execute' })).toBe(false);
  });
});

describe('useBoardStoreSource', () => {
  it('seeds from /api/tasks and /api/squads, then folds task_update events without another request', async () => {
    const { result } = renderHook(() => useBoardStoreSource());
    await waitFor(() => expect(result.current.store.ready).toBe(true));
    expect(result.current.store.tasks.map((t) => t.id)).toEqual(['T1', 'T2']);
    expect(result.current.store.plan?.summary).toBe('the plan');
    expect(result.current.store.squads?.ok).toBe(true);
    expect(result.current.store.live).toBe(false);
    expect(getTasks).toHaveBeenCalledTimes(1);

    act(() => {
      result.current.applyEvent(taskUpdate(task('T2', { column: 'done', status: 'done' })));
      result.current.applyEvent(taskUpdate(task('T3', { title: 'a new one' })));
    });
    expect(result.current.store.tasks.map((t) => `${t.id}:${t.column}`)).toEqual(['T1:ready_to_dev', 'T2:done', 'T3:ready_to_dev']);
    expect(result.current.store.live).toBe(true);
    // The event was the update; no poll was spent on it.
    expect(getTasks).toHaveBeenCalledTimes(1);
  });

  it('paints an optimistic edit and removes a task locally', async () => {
    const { result } = renderHook(() => useBoardStoreSource());
    await waitFor(() => expect(result.current.store.ready).toBe(true));
    act(() => result.current.store.upsertTask(task('T1', { column: 'in_progress' })));
    expect(result.current.store.tasks[0].column).toBe('in_progress');
    act(() => result.current.store.removeTask('T2'));
    expect(result.current.store.tasks.map((t) => t.id)).toEqual(['T1']);
  });

  it('records a board error without losing the org chart, and refresh() re-reads both', async () => {
    getTasks.mockRejectedValueOnce(new Error('boom'));
    const { result } = renderHook(() => useBoardStoreSource());
    await waitFor(() => expect(result.current.store.ready).toBe(true));
    expect(result.current.store.error).toMatch(/boom/);
    expect(result.current.store.squads?.ok).toBe(true);

    await act(async () => {
      await result.current.store.refresh();
    });
    expect(result.current.store.error).toBeNull();
    expect(result.current.store.tasks).toHaveLength(2);
    expect(getTasks).toHaveBeenCalledTimes(2);
  });

  it('coalesces concurrent refreshes into at most one follow-up request', async () => {
    const { result } = renderHook(() => useBoardStoreSource());
    await waitFor(() => expect(result.current.store.ready).toBe(true));
    getTasks.mockClear();
    await act(async () => {
      await Promise.all([result.current.store.refresh(), result.current.store.refresh(), result.current.store.refresh()]);
    });
    // One in flight, one queued behind it for whatever the first predates.
    expect(getTasks.mock.calls.length).toBeLessThanOrEqual(2);
    expect(getTasks.mock.calls.length).toBeGreaterThanOrEqual(1);
  });

  it('re-reads after a structural log event only until the server pushes task_update', async () => {
    const { result, rerender } = renderHook((props: { last: RunEvent | null }) => useBoardStoreSource({ lastEvent: props.last }), {
      initialProps: { last: null as RunEvent | null },
    });
    await waitFor(() => expect(result.current.store.ready).toBe(true));
    getTasks.mockClear();

    rerender({ last: { phase: 'execute', kind: 'task_done', message: 'T1 done', task_id: 'T1', time: new Date().toISOString() } });
    await waitFor(() => expect(getTasks).toHaveBeenCalledTimes(1), { timeout: 3000 });

    // Once the board is live, a structural line is no longer a cue to poll.
    act(() => result.current.applyEvent(taskUpdate(task('T1', { column: 'done' }))));
    getTasks.mockClear();
    rerender({ last: { phase: 'execute', kind: 'task_start', message: 'T2', task_id: 'T2', time: new Date().toISOString() } });
    await new Promise((r) => setTimeout(r, 1200));
    expect(getTasks).not.toHaveBeenCalled();
  });

  it('does no network work when disabled', async () => {
    renderHook(() => useBoardStoreSource({ enabled: false }));
    await new Promise((r) => setTimeout(r, 20));
    expect(getTasks).not.toHaveBeenCalled();
    expect(getSquads).not.toHaveBeenCalled();
  });
});
