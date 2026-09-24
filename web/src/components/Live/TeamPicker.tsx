import { useEffect, useRef, useState } from 'react';
import { Users, ChevronDown, Check, Compass, Lock, X } from 'lucide-react';
import clsx from 'clsx';
import { teamColor } from '@/components/Board/teamColor';
import type { TeamSpec } from '@/types';

// ── Who chooses the teams for this run ───────────────────────────────────
//
// Two modes, always both on screen, so the default is visible as a choice and
// never a mystery:
//
//   Dynamic (the default)  the dispatcher picks the teams from the request and
//                          the workspace, decides whether they build in
//                          parallel or one staffs the run, and names who
//                          manages each. Saved pins are ignored for the run.
//   Strict                 exactly the teams picked here (or, with none picked,
//                          the ones the saved config pins). Nothing is added
//                          on evidence.
//
// Picking teams switches to Strict; one click on Dynamic — or the ✕ on the
// button — goes back. A choice here governs THIS run only: the server restores
// the saved pins when the run ends.

export type TeamSelectionMode = 'dynamic' | 'strict';

export interface TeamPickerProps {
  teams: TeamSpec[];
  /** Teams the saved config already pins — what Strict uses when nothing is picked. */
  configPinned: string[];
  value: string[];
  mode: TeamSelectionMode;
  /** The dispatcher's current pick for the typed request, from the preview. */
  dispatcherPick?: string[];
  /** One line on why the dispatcher picked what it picked. */
  dispatcherNote?: string;
  disabled?: boolean;
  onChange: (next: string[]) => void;
  onModeChange: (mode: TeamSelectionMode) => void;
}

export default function TeamPicker({
  teams,
  configPinned,
  value,
  mode,
  dispatcherPick = [],
  dispatcherNote,
  disabled,
  onChange,
  onModeChange,
}: TeamPickerProps) {
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

  const strict = mode === 'strict';
  const strictTeams = value.length > 0 ? value : configPinned;
  const toggle = (id: string) => {
    const base = value.length > 0 ? value : strict ? configPinned : [];
    const next = base.includes(id) ? base.filter((v) => v !== id) : [...base, id];
    // Unpicking the last team is not "strict about nothing" — an empty pick
    // would fall back to the saved pins and re-check the box just cleared.
    // It means: let the dispatcher choose.
    if (next.length === 0) {
      backToDynamic();
      return;
    }
    onChange(next);
    if (next.length > 0 && !strict) onModeChange('strict');
  };
  const backToDynamic = () => {
    onChange([]);
    onModeChange('dynamic');
  };

  const label = strict
    ? strictTeams.length === 0
      ? 'Strict · pick teams'
      : strictTeams.length === 1
        ? `Strict · ${strictTeams[0]}`
        : `Strict · ${strictTeams.length} teams`
    : dispatcherPick.length > 0
      ? `Dynamic · ${dispatcherPick.join(' + ')}`
      : 'Dynamic';

  return (
    <div ref={ref} className="relative flex shrink-0 items-center">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        disabled={disabled}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={`Teams for this run: ${label}`}
        data-testid="team-picker"
        data-mode={mode}
        title={
          strict
            ? 'Strict: only the teams you chose work on this run. Click to change, or ✕ to go back to Dynamic.'
            : dispatcherNote || 'Dynamic: the dispatcher picks the teams from your request and the repository, and who manages them.'
        }
        className={clsx(
          'input focus-ring flex h-9 max-w-[16rem] items-center gap-1.5 px-2.5 text-xs',
          strict && 'rounded-r-none border-amber-300 text-amber-800 dark:border-amber-700 dark:text-amber-300',
          !strict && 'border-brand-300 text-brand-700 dark:border-brand-700 dark:text-brand-300',
        )}
      >
        {strict ? <Lock size={13} className="shrink-0" aria-hidden="true" /> : <Compass size={13} className="shrink-0" aria-hidden="true" />}
        <span className="truncate">{label}</span>
        <ChevronDown size={12} className="shrink-0 text-gray-400" aria-hidden="true" />
      </button>
      {strict && (
        <button
          type="button"
          onClick={backToDynamic}
          disabled={disabled}
          aria-label="Back to Dynamic — let the dispatcher choose"
          title="Back to Dynamic — let the dispatcher choose"
          data-testid="team-picker-dynamic"
          className="input focus-ring flex h-9 items-center rounded-l-none border-l-0 border-amber-300 px-1.5 text-amber-700 hover:bg-amber-50 dark:border-amber-700 dark:text-amber-300 dark:hover:bg-amber-950/40"
        >
          <X size={12} aria-hidden="true" />
        </button>
      )}
      {open && (
        <div
          role="dialog"
          aria-label="Who chooses the teams"
          className="absolute right-0 top-full z-20 mt-1 w-80 rounded-md border border-gray-200 bg-white p-1.5 shadow-lg dark:border-gray-700 dark:bg-gray-900"
        >
          <div role="radiogroup" aria-label="Team selection mode" className="grid grid-cols-2 gap-1">
            <button
              type="button"
              role="radio"
              aria-checked={!strict}
              onClick={backToDynamic}
              data-testid="team-mode-dynamic"
              className={clsx(
                'focus-ring rounded-md border px-2 py-1.5 text-left',
                !strict ? 'border-brand-400 bg-brand-50 dark:border-brand-600 dark:bg-brand-950/40' : 'border-gray-200 hover:bg-gray-50 dark:border-gray-700 dark:hover:bg-gray-800',
              )}
            >
              <span className="flex items-center gap-1 text-xs font-semibold text-gray-800 dark:text-gray-100">
                <Compass size={12} aria-hidden="true" /> Dynamic
                <span className="ml-auto rounded bg-brand-100 px-1 text-[9px] font-medium text-brand-700 dark:bg-brand-900/60 dark:text-brand-300">default</span>
              </span>
              <span className="mt-0.5 block text-[10px] leading-snug text-gray-500 dark:text-gray-400">
                The dispatcher picks the teams, how they work and who manages them.
              </span>
            </button>
            <button
              type="button"
              role="radio"
              aria-checked={strict}
              onClick={() => onModeChange('strict')}
              data-testid="team-mode-strict"
              className={clsx(
                'focus-ring rounded-md border px-2 py-1.5 text-left',
                strict ? 'border-amber-400 bg-amber-50 dark:border-amber-600 dark:bg-amber-950/40' : 'border-gray-200 hover:bg-gray-50 dark:border-gray-700 dark:hover:bg-gray-800',
              )}
            >
              <span className="flex items-center gap-1 text-xs font-semibold text-gray-800 dark:text-gray-100">
                <Lock size={12} aria-hidden="true" /> Strict
              </span>
              <span className="mt-0.5 block text-[10px] leading-snug text-gray-500 dark:text-gray-400">
                Only the teams you pick below. Nothing is added.
              </span>
            </button>
          </div>

          {!strict && (
            <p className="mt-1.5 rounded bg-gray-50 px-2 py-1 text-[10px] text-gray-600 dark:bg-gray-800/60 dark:text-gray-300" data-testid="dispatcher-note">
              {dispatcherPick.length > 0 ? (
                <>
                  For this request: <b>{dispatcherPick.join(' + ')}</b>
                  {dispatcherNote ? <> — {dispatcherNote}</> : null}
                </>
              ) : (
                dispatcherNote || 'Type a request to see which teams the dispatcher would pick.'
              )}
            </p>
          )}

          <div className="mt-1.5 flex items-center gap-1 px-1 text-[10px] font-semibold uppercase tracking-wider text-gray-400">
            <Users size={11} aria-hidden="true" /> {strict ? 'Teams on this run' : 'Pick teams to switch to Strict'}
          </div>
          <ul role="listbox" aria-multiselectable="true" aria-label="Teams for this run" className="max-h-64 overflow-auto">
            {teams.map((t) => {
              const on = strict && strictTeams.includes(t.id);
              const suggested = !strict && dispatcherPick.includes(t.id);
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
                    on && 'bg-amber-50/70 dark:bg-amber-950/30',
                  )}
                >
                  <span className={clsx('mt-0.5 flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded border', on ? 'border-amber-500 bg-amber-500 text-white' : 'border-gray-300 dark:border-gray-600')}>
                    {on && <Check size={10} aria-hidden="true" />}
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="flex flex-wrap items-center gap-1">
                      <span className={clsx('rounded px-1 font-mono text-[10px]', teamColor(t.id).badge)}>{t.id}</span>
                      {t.name && t.name !== t.id && <span className="truncate text-gray-700 dark:text-gray-200">{t.name}</span>}
                      {suggested && <span className="rounded bg-brand-100 px-1 text-[9px] text-brand-700 dark:bg-brand-900/60 dark:text-brand-300">dispatcher's pick</span>}
                    </span>
                    <span className="block truncate text-[10px] text-gray-500 dark:text-gray-400">
                      {[t.worker, t.reviewer, t.tester].filter(Boolean).join(' · ') || 'pipeline defaults'} · manager{' '}
                      {t.effective_manager || t.manager || 'triage'}
                    </span>
                  </span>
                </li>
              );
            })}
          </ul>
          {strict && (
            <div className="mt-1 border-t border-gray-100 px-1 pt-1 dark:border-gray-800">
              <button type="button" onClick={backToDynamic} className="btn-ghost focus-ring h-7 px-2 text-[11px]">
                <Compass size={11} aria-hidden="true" /> Back to Dynamic — let the dispatcher choose
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
