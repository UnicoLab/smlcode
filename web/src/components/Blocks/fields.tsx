// ── Small reusable form field components for the Block editor ──
import { useId } from 'react';
import type { ReactNode } from 'react';
import clsx from 'clsx';
import { Plus, X } from 'lucide-react';

export function splitComma(v: string): string[] {
  return v.split(',').map((s) => s.trim()).filter(Boolean);
}

// ── Field ──
export function Field({ label, hint, className, children }: { label?: string; hint?: string; className?: string; children: ReactNode }) {
  return (
    <div className={className}>
      {label && <label className="label">{label}</label>}
      {children}
      {hint && <p className="mt-1 text-[10px] text-gray-400 dark:text-gray-500">{hint}</p>}
    </div>
  );
}

// ── TextField ──
export function TextField({ label, value, onChange, placeholder, hint, className, mono, disabled }: {
  label?: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  hint?: string;
  className?: string;
  mono?: boolean;
  disabled?: boolean;
}) {
  return (
    <Field label={label} hint={hint} className={className}>
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        disabled={disabled}
        className={clsx(mono ? 'input-mono' : 'input', disabled && 'cursor-not-allowed opacity-60')}
      />
    </Field>
  );
}

// ── TextArea ──
export function TextArea({ label, value, onChange, rows, mono, placeholder, hint, className }: {
  label?: string;
  value: string;
  onChange: (v: string) => void;
  rows?: number;
  mono?: boolean;
  placeholder?: string;
  hint?: string;
  className?: string;
}) {
  return (
    <Field label={label} hint={hint} className={className}>
      <textarea
        value={value}
        onChange={(e) => onChange(e.target.value)}
        rows={rows ?? 3}
        placeholder={placeholder}
        className={clsx('input resize-y', mono && 'font-mono text-xs')}
      />
    </Field>
  );
}

// ── NumberField ──
export function NumberField({ label, value, onChange, step, min, max, hint, className }: {
  label?: string;
  value: number;
  onChange: (v: number) => void;
  step?: number | string;
  min?: number;
  max?: number;
  hint?: string;
  className?: string;
}) {
  return (
    <Field label={label} hint={hint} className={className}>
      <input
        type="number"
        step={step}
        min={min}
        max={max}
        value={Number.isFinite(value) ? value : ''}
        onChange={(e) => {
          if (e.target.value === '') {
            onChange(0);
            return;
          }
          const n = Number(e.target.value);
          onChange(Number.isNaN(n) ? 0 : n);
        }}
        className="input"
      />
    </Field>
  );
}

// ── SelectField ──
export interface SelectOption {
  value: string;
  label: string;
}

export function SelectField({ label, value, onChange, options, placeholder, hint, className, disabled }: {
  label?: string;
  value: string;
  onChange: (v: string) => void;
  options: Array<string | SelectOption>;
  placeholder?: string;
  hint?: string;
  className?: string;
  disabled?: boolean;
}) {
  const opts: SelectOption[] = options.map((o) => (typeof o === 'string' ? { value: o, label: o } : o));
  return (
    <Field label={label} hint={hint} className={className}>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className={clsx('input', disabled && 'cursor-not-allowed opacity-60')}
      >
        {placeholder !== undefined && <option value="">{placeholder}</option>}
        {opts.map((o) => (
          <option key={o.value} value={o.value}>{o.label}</option>
        ))}
      </select>
    </Field>
  );
}

// ── CheckboxField ──
export function CheckboxField({ label, checked, onChange, hint, className }: {
  label: string;
  checked: boolean;
  onChange: (v: boolean) => void;
  hint?: string;
  className?: string;
}) {
  return (
    <label className={clsx('flex cursor-pointer select-none items-start gap-2.5', className)}>
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="mt-0.5 h-4 w-4 rounded border-gray-300 text-brand-600 focus:ring-brand-500 dark:border-gray-600 dark:bg-gray-800"
      />
      <span className="text-sm leading-tight text-gray-700 dark:text-gray-300">
        {label}
        {hint && <span className="mt-0.5 block text-[10px] text-gray-400 dark:text-gray-500">{hint}</span>}
      </span>
    </label>
  );
}

// ── CommaField — comma-separated string list ──
export function CommaField({ label, value, onChange, placeholder, hint, className, mono }: {
  label?: string;
  value: string[];
  onChange: (v: string[]) => void;
  placeholder?: string;
  hint?: string;
  className?: string;
  mono?: boolean;
}) {
  return (
    <TextField
      label={label}
      value={value.join(', ')}
      onChange={(v) => onChange(splitComma(v))}
      placeholder={placeholder}
      hint={hint ?? 'comma separated'}
      className={className}
      mono={mono}
    />
  );
}

// ── SuggestInput — free text input with datalist suggestions ──
export function SuggestInput({ label, value, onChange, suggestions, placeholder, hint, className, mono }: {
  label?: string;
  value: string;
  onChange: (v: string) => void;
  suggestions: string[];
  placeholder?: string;
  hint?: string;
  className?: string;
  mono?: boolean;
}) {
  const listId = useId();
  return (
    <Field label={label} hint={hint} className={className}>
      <input
        type="text"
        list={listId}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className={mono ? 'input-mono' : 'input'}
      />
      <datalist id={listId}>
        {suggestions.map((s) => (
          <option key={s} value={s} />
        ))}
      </datalist>
    </Field>
  );
}

// ── CmdListEditor — repeatable {cmd, label?, optional?} rows ──
export interface CmdRow {
  cmd: string;
  label?: string;
  optional?: boolean;
}

export function CmdListEditor({ label, value, onChange, addLabel, className }: {
  label: string;
  value: CmdRow[];
  onChange: (rows: CmdRow[]) => void;
  addLabel?: string;
  className?: string;
}) {
  return (
    <div className={className}>
      <label className="label">{label}</label>
      <div className="space-y-2">
        {value.map((row, i) => (
          <div key={i} className="flex items-center gap-2">
            <input
              value={row.cmd}
              onChange={(e) => {
                const rows = [...value];
                rows[i] = { ...row, cmd: e.target.value };
                onChange(rows);
              }}
              className="input flex-1 font-mono text-xs"
              placeholder="command"
            />
            <input
              value={row.label ?? ''}
              onChange={(e) => {
                const rows = [...value];
                rows[i] = { ...row, label: e.target.value };
                onChange(rows);
              }}
              className="input w-24 text-xs sm:w-32"
              placeholder="label"
            />
            <label className="flex cursor-pointer items-center gap-1.5 whitespace-nowrap text-[11px] text-gray-500 dark:text-gray-400">
              <input
                type="checkbox"
                checked={!!row.optional}
                onChange={(e) => {
                  const rows = [...value];
                  rows[i] = { ...row, optional: e.target.checked };
                  onChange(rows);
                }}
                className="h-3.5 w-3.5 rounded border-gray-300 text-brand-600 focus:ring-brand-500 dark:border-gray-600 dark:bg-gray-800"
              />
              optional
            </label>
            <button
              onClick={() => onChange(value.filter((_, idx) => idx !== i))}
              className="btn-ghost shrink-0 rounded-lg p-1.5 text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20"
              title="Remove row"
            >
              <X size={14} />
            </button>
          </div>
        ))}
        <button
          onClick={() => onChange([...value, { cmd: '', optional: false }])}
          className="btn-ghost gap-1 text-xs"
        >
          <Plus size={12} />
          {addLabel ?? 'Add command'}
        </button>
      </div>
    </div>
  );
}

// ── Section — titled panel used inside the editor modal ──
export function Section({ title, actions, children, className }: {
  title: string;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={clsx('space-y-3 rounded-xl border border-gray-200 p-4 dark:border-gray-800', className)}>
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">{title}</h3>
        {actions}
      </div>
      {children}
    </div>
  );
}
