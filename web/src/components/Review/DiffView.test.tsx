import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import DiffView, { pairOps } from './DiffView';
import type { DiffHunk, DiffOp } from '@/types';

const hunk: DiffHunk = {
  old_start: 1,
  old_lines: 3,
  new_start: 1,
  new_lines: 3,
  ops: [
    { type: 'equal', old_line: 1, new_line: 1, text: 'package a' },
    { type: 'delete', old_line: 2, text: 'func A() int { return 1 }' },
    { type: 'insert', new_line: 2, text: 'func A() int { return 2 }' },
    { type: 'equal', old_line: 3, new_line: 3, text: '' },
  ],
};

describe('DiffView', () => {
  it('renders a unified diff with both sides of a modified line', () => {
    render(<DiffView hunks={[hunk]} mode="unified" />);
    expect(screen.getByText('func A() int { return 1 }')).toBeInTheDocument();
    expect(screen.getByText('func A() int { return 2 }')).toBeInTheDocument();
    expect(screen.getByText(/@@ -1,3 \+1,3 @@/)).toBeInTheDocument();
  });

  it('renders a split diff', () => {
    render(<DiffView hunks={[hunk]} mode="split" />);
    expect(screen.getByText(/side-by-side/i)).toBeInTheDocument();
  });

  it('shows a reason when there is nothing to diff', () => {
    render(<DiffView hunks={[]} mode="unified" emptyLabel="Binary file — no textual diff available." />);
    expect(screen.getByText(/binary file/i)).toBeInTheDocument();
  });

  it('warns when the diff was truncated', () => {
    render(<DiffView hunks={[hunk]} mode="unified" truncated />);
    expect(screen.getByText(/line budget/i)).toBeInTheDocument();
  });
});

describe('pairOps', () => {
  it('zips a delete run against the following insert run', () => {
    const ops: DiffOp[] = [
      { type: 'delete', old_line: 1, text: 'old1' },
      { type: 'delete', old_line: 2, text: 'old2' },
      { type: 'insert', new_line: 1, text: 'new1' },
    ];
    const rows = pairOps(ops);
    expect(rows).toHaveLength(2);
    expect(rows[0]).toEqual({ left: ops[0], right: ops[2] });
    // A removed line with no replacement leaves the right side empty.
    expect(rows[1].left).toEqual(ops[1]);
    expect(rows[1].right).toBeUndefined();
  });

  it('puts an equal line on both sides', () => {
    const op: DiffOp = { type: 'equal', old_line: 1, new_line: 1, text: 'same' };
    expect(pairOps([op])).toEqual([{ left: op, right: op }]);
  });

  it('handles a pure insertion (new file)', () => {
    const ops: DiffOp[] = [
      { type: 'insert', new_line: 1, text: 'a' },
      { type: 'insert', new_line: 2, text: 'b' },
    ];
    const rows = pairOps(ops);
    expect(rows.map((r) => r.left)).toEqual([undefined, undefined]);
    expect(rows.map((r) => r.right?.text)).toEqual(['a', 'b']);
  });
});
