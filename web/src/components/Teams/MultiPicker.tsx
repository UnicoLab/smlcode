import { useMemo, useState } from 'react';
import { Plus, X, Search } from 'lucide-react';
import clsx from 'clsx';

// ── Composing a team out of as many people as it needs ───────────────────
//
// A team is "these people", and how many there are is its author's business.
// A form with four fixed seats says otherwise — it teaches the user that a team
// IS a worker, a reviewer, a tester and a manager, and quietly refuses the
// fifth person they wanted.
//
// So the roster and the skill list are open: pick from what is installed, in
// any number, in the order you want them. Two rules the component enforces
// because the harness does:
//
//   • only what is INSTALLED can be picked, because an agent the harness cannot
//     dispatch is a name on a team that never does any work, and a skill that
//     does not exist loads nothing;
//   • a value already on the team that is no longer installed stays visible and
//     removable rather than silently disappearing on the next save.

export interface MultiPickerProps {
  id: string;
  label: string;
  hint?: string;
  /** What is installed and therefore pickable. */
  options: string[];
  /** What this team currently holds, in the author's order. */
  value: string[];
  /** Optional one-line description per option, shown in the menu. */
  describe?: (option: string) => string | undefined;
  addLabel?: string;
  emptyLabel?: string;
  onChange: (next: string[]) => void;
  disabled?: boolean;
}

export default function MultiPicker({
  id,
  label,
  hint,
  options,
  value,
  describe,
  addLabel = 'Add',
  emptyLabel = 'none yet',
  onChange,
  disabled,
}: MultiPickerProps) {
  const [open, setOpen] = useState(false);
  const [filter, setFilter] = useState('');

  const available = useMemo(() => {
    const held = new Set(value);
    const q = filter.trim().toLowerCase();
    return options
      .filter((o) => !held.has(o))
      .filter((o) => !q || o.toLowerCase().includes(q) || (describe?.(o) ?? '').toLowerCase().includes(q));
  }, [options, value, filter, describe]);

  const add = (o: string) => {
    onChange([...value, o]);
    setFilter('');
  };

  return (
    <div className="min-w-0">
      <div className="mb-1 flex items-center gap-2">
        <span id={`${id}-label`} className="label text-[10px]">
          {label}
        </span>
        <span className="badge-neutral text-[10px]">{value.length}</span>
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          disabled={disabled}
          aria-expanded={open}
          aria-controls={`${id}-menu`}
          // The list is the label: a page can hold several of these, and three
          // buttons all called "Add agent" is one a screen reader cannot
          // navigate and a test cannot address.
          aria-label={`${addLabel} to ${label}`}
          className="btn-ghost focus-ring ml-auto h-6 gap-1 px-1.5 text-[10px]"
        >
          <Plus size={11} aria-hidden="true" />
          {addLabel}
        </button>
      </div>

      <ul aria-labelledby={`${id}-label`} className="flex flex-wrap gap-1">
        {value.length === 0 && (
          <li className="text-[10px] text-gray-400 dark:text-gray-500">{emptyLabel}</li>
        )}
        {value.map((v) => {
          // A value the harness no longer offers is kept and flagged. Dropping
          // it would rewrite the user's team without telling them.
          const missing = !options.includes(v);
          return (
            <li key={v}>
              <span
                className={clsx(
                  'inline-flex items-center gap-1 rounded px-1.5 py-0.5 font-mono text-[10px]',
                  missing
                    ? 'bg-amber-100 text-amber-800 dark:bg-amber-950/50 dark:text-amber-200'
                    : 'bg-gray-100 text-gray-700 dark:bg-gray-800 dark:text-gray-200',
                )}
                title={missing ? `${v} is not installed` : describe?.(v)}
              >
                {v}
                {missing && <span className="not-italic">⚠</span>}
                <button
                  type="button"
                  onClick={() => onChange(value.filter((x) => x !== v))}
                  disabled={disabled}
                  aria-label={`Remove ${v} from ${label}`}
                  className="focus-ring rounded hover:text-red-600 dark:hover:text-red-400"
                >
                  <X size={10} aria-hidden="true" />
                </button>
              </span>
            </li>
          );
        })}
      </ul>

      {open && (
        <div
          id={`${id}-menu`}
          className="mt-1.5 rounded-md border border-gray-200 p-1.5 dark:border-gray-800"
        >
          <div className="relative mb-1">
            <Search
              size={11}
              className="pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-gray-400"
              aria-hidden="true"
            />
            <input
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              aria-label={`Filter ${label}`}
              placeholder="Filter…"
              className="input h-7 w-full pl-6 text-[11px]"
            />
          </div>
          {available.length === 0 ? (
            <p className="px-1 py-1 text-[10px] text-gray-400">
              {options.length === 0 ? 'Nothing installed to pick from.' : 'Everything matching is already on the team.'}
            </p>
          ) : (
            <ul className="max-h-40 overflow-y-auto">
              {available.map((o) => (
                <li key={o}>
                  <button
                    type="button"
                    onClick={() => add(o)}
                    disabled={disabled}
                    className="focus-ring flex w-full items-baseline gap-2 rounded px-1.5 py-1 text-left text-[11px] hover:bg-gray-100 dark:hover:bg-gray-800"
                  >
                    <code className="shrink-0 font-mono">{o}</code>
                    {describe?.(o) && (
                      <span className="min-w-0 flex-1 truncate text-[10px] text-gray-500 dark:text-gray-400">
                        {describe(o)}
                      </span>
                    )}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      {hint && <p className="mt-1 text-[10px] leading-tight text-gray-400 dark:text-gray-500">{hint}</p>}
    </div>
  );
}
