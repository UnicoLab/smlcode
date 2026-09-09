import { useEffect, useRef, useState } from 'react';
import { Users, ChevronDown, Check } from 'lucide-react';
import clsx from 'clsx';
import { teamColor } from '@/components/Board/teamColor';
import type { TeamSpec } from '@/types';

// ── Which teams this run goes to ─────────────────────────────────────────
//
// The Teams page can send a request to teams; this is the same control on the
// command bar, because most requests start here. Nothing picked means the
// library decides from the request and the workspace, which is the right
// default. Picking teams pins them for THIS run only — the server restores the
// saved pins when the run ends, so a one-off choice never quietly governs every
// later run.

export interface TeamPickerProps {
  teams: TeamSpec[];
  /** Teams the saved config already pins — shown, not pre-selected. */
  configPinned: string[];
  value: string[];
  disabled?: boolean;
  onChange: (next: string[]) => void;
}

export default function TeamPicker({ teams, configPinned, value, disabled, onChange }: TeamPickerProps) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    const esc = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', close);
    document.addEventListener('keydown', esc);
    return () => {
      document.removeEventListener('mousedown', close);
      document.removeEventListener('keydown', esc);
    };
  }, [open]);

  if (teams.length === 0) return null;

  const toggle = (id: string) =>
    onChange(value.includes(id) ? value.filter((v) => v !== id) : [...value, id]);

  const label =
    value.length === 0
      ? configPinned.length > 0
        ? `Teams: ${configPinned.join(', ')}`
        : 'Teams: auto'
      : value.length === 1
        ? `Team: ${value[0]}`
        : `Teams: ${value.length}`;

  return (
    <div ref={ref} className="relative shrink-0">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label="Teams for this run"
        title={
          value.length === 0
            ? configPinned.length > 0
              ? 'The saved config pins these teams; pick others to override for this run only'
              : 'The library picks the teams from the request and the workspace; pick teams to send the request to them instead'
            : 'Pinned for this run only'
        }
        className={clsx(
          'input focus-ring flex h-9 max-w-[14rem] items-center gap-1.5 px-2.5 text-xs',
          value.length > 0 && 'border-brand-300 text-brand-700 dark:border-brand-700 dark:text-brand-300',
        )}
      >
        <Users size={13} className="shrink-0" aria-hidden="true" />
        <span className="truncate">{label}</span>
        <ChevronDown size={12} className="shrink-0 text-gray-400" aria-hidden="true" />
      </button>
      {open && (
        <ul
          role="listbox"
          aria-multiselectable="true"
          aria-label="Teams for this run"
          className="absolute right-0 z-20 mt-1 max-h-72 w-72 overflow-auto rounded-md border border-gray-200 bg-white p-1 shadow-lg dark:border-gray-700 dark:bg-gray-900"
        >
          <li className="px-2 py-1 text-[10px] text-gray-500 dark:text-gray-400">
            Send this request to these teams. One team staffs the run; two or more build in parallel.
            Nothing picked lets the library decide.
          </li>
          {teams.map((t) => {
            const on = value.includes(t.id);
            return (
              <li
                key={t.id}
                role="option"
                aria-selected={on}
                onClick={() => toggle(t.id)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    toggle(t.id);
                  }
                }}
                tabIndex={0}
                className={clsx(
                  'focus-ring flex cursor-pointer items-start gap-2 rounded px-2 py-1.5 text-xs hover:bg-gray-50 dark:hover:bg-gray-800',
                  on && 'bg-brand-50/60 dark:bg-brand-950/30',
                )}
              >
                <span className={clsx('mt-0.5 flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded border', on ? 'border-brand-500 bg-brand-500 text-white' : 'border-gray-300 dark:border-gray-600')}>
                  {on && <Check size={10} aria-hidden="true" />}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="flex flex-wrap items-center gap-1">
                    <span className={clsx('rounded px-1 font-mono text-[10px]', teamColor(t.id).badge)}>{t.id}</span>
                    {t.name && t.name !== t.id && <span className="truncate text-gray-700 dark:text-gray-200">{t.name}</span>}
                  </span>
                  <span className="block truncate text-[10px] text-gray-500 dark:text-gray-400">
                    {[t.worker, t.reviewer, t.tester].filter(Boolean).join(' · ') || 'pipeline defaults'} · manager{' '}
                    {t.effective_manager || t.manager || 'triage'}
                  </span>
                </span>
              </li>
            );
          })}
          {value.length > 0 && (
            <li className="mt-1 border-t border-gray-100 px-2 pt-1 dark:border-gray-800">
              <button type="button" onClick={() => onChange([])} className="btn-ghost focus-ring h-7 px-2 text-[11px]">
                Clear — let the library decide
              </button>
            </li>
          )}
        </ul>
      )}
    </div>
  );
}
