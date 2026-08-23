import { useMemo } from 'react';
import clsx from 'clsx';
import type { DiffHunk, DiffOp } from '@/types';

// ── Diff renderer ──
//
// Hunks and per-line numbers come from the server (pkg/server/diff.go), so the
// browser never has to diff a large file itself.

export type DiffMode = 'unified' | 'split';

interface Props {
  hunks: DiffHunk[];
  mode: DiffMode;
  /** Shown instead of a diff when the file is binary or unchanged. */
  emptyLabel?: string;
  truncated?: boolean;
}

const ROW_CLASS: Record<DiffOp['type'], string> = {
  equal: '',
  insert: 'bg-emerald-50 dark:bg-emerald-950/40',
  delete: 'bg-red-50 dark:bg-red-950/40',
};

const SIGN: Record<DiffOp['type'], string> = { equal: ' ', insert: '+', delete: '-' };

export default function DiffView({ hunks, mode, emptyLabel, truncated }: Props) {
  if (!hunks.length) {
    return (
      <div className="px-4 py-6 text-center text-xs text-gray-400">
        {emptyLabel || 'No textual changes.'}
      </div>
    );
  }
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse font-mono text-[11px] leading-relaxed">
        <caption className="sr-only">
          {mode === 'split' ? 'Side-by-side file diff' : 'Unified file diff'}
        </caption>
        <tbody>
          {hunks.map((hunk, hi) => (
            <HunkRows key={`${hunk.old_start}-${hunk.new_start}-${hi}`} hunk={hunk} mode={mode} />
          ))}
        </tbody>
      </table>
      {truncated && (
        <p className="border-t border-gray-100 px-3 py-1.5 text-[10px] text-amber-600 dark:border-gray-800">
          File exceeded the diff line budget — the comparison shows a prefix only.
        </p>
      )}
    </div>
  );
}

function HunkRows({ hunk, mode }: { hunk: DiffHunk; mode: DiffMode }) {
  const header = `@@ -${hunk.old_start},${hunk.old_lines} +${hunk.new_start},${hunk.new_lines} @@`;
  const rows = useMemo(() => (mode === 'split' ? pairOps(hunk.ops) : null), [hunk.ops, mode]);

  return (
    <>
      <tr>
        <td
          colSpan={mode === 'split' ? 4 : 3}
          className="bg-gray-100 px-3 py-1 text-[10px] text-gray-500 dark:bg-gray-800/70 dark:text-gray-400"
        >
          {header}
        </td>
      </tr>
      {mode === 'unified'
        ? hunk.ops.map((op, i) => (
            <tr key={i} className={ROW_CLASS[op.type]}>
              <LineNo n={op.old_line} />
              <LineNo n={op.new_line} />
              <td className="whitespace-pre-wrap break-all px-2 py-0.5">
                <span
                  className={clsx(
                    'mr-2 select-none',
                    op.type === 'insert' && 'text-emerald-600 dark:text-emerald-400',
                    op.type === 'delete' && 'text-red-600 dark:text-red-400',
                    op.type === 'equal' && 'text-gray-300 dark:text-gray-600',
                  )}
                  aria-hidden="true"
                >
                  {SIGN[op.type]}
                </span>
                {op.text || ' '}
              </td>
            </tr>
          ))
        : rows?.map((row, i) => (
            <tr key={i}>
              <LineNo n={row.left?.old_line} />
              <td
                className={clsx(
                  'w-1/2 whitespace-pre-wrap break-all border-r border-gray-200 px-2 py-0.5 dark:border-gray-800',
                  row.left ? ROW_CLASS[row.left.type] : 'bg-gray-50/60 dark:bg-gray-900/40',
                )}
              >
                {row.left?.text || ' '}
              </td>
              <LineNo n={row.right?.new_line} />
              <td
                className={clsx(
                  'w-1/2 whitespace-pre-wrap break-all px-2 py-0.5',
                  row.right ? ROW_CLASS[row.right.type] : 'bg-gray-50/60 dark:bg-gray-900/40',
                )}
              >
                {row.right?.text || ' '}
              </td>
            </tr>
          ))}
    </>
  );
}

function LineNo({ n }: { n?: number }) {
  return (
    <td className="w-10 select-none border-r border-gray-200 px-2 py-0.5 text-right align-top text-[10px] text-gray-400 dark:border-gray-800">
      {n || ''}
    </td>
  );
}

interface SplitRow {
  left?: DiffOp;
  right?: DiffOp;
}

/**
 * pairOps aligns a unified op list into side-by-side rows: equal lines share a
 * row, and a run of deletes is zipped against the following run of inserts so a
 * modified line shows old and new next to each other.
 */
export function pairOps(ops: DiffOp[]): SplitRow[] {
  const rows: SplitRow[] = [];
  let i = 0;
  while (i < ops.length) {
    const op = ops[i];
    if (op.type === 'equal') {
      rows.push({ left: op, right: op });
      i += 1;
      continue;
    }
    const deletes: DiffOp[] = [];
    const inserts: DiffOp[] = [];
    while (i < ops.length && ops[i].type === 'delete') deletes.push(ops[i++]);
    while (i < ops.length && ops[i].type === 'insert') inserts.push(ops[i++]);
    const n = Math.max(deletes.length, inserts.length);
    for (let k = 0; k < n; k++) {
      rows.push({ left: deletes[k], right: inserts[k] });
    }
  }
  return rows;
}
