import { useEffect, useState } from 'react';
import { Modal } from '@/components/ui/Modal';
import { slugify } from './teamId';
import MultiPicker from './MultiPicker';
import type { Skill, TeamSpec } from '@/types';

// ── Authoring one team ───────────────────────────────────────────────────
//
// Every field here is one the harness actually reads. That constraint is the
// whole design: a form offering a setting the run ignores teaches the user a
// model of the system that is wrong, and they only find out when the team does
// something other than what the form said.
//
// The two that carry the most weight are the two people get wrong:
//
//   owns   — teams may NEVER share a path. Two teams writing one file in
//            parallel loses one of the edits, silently, so an overlap is
//            refused whole rather than merged.
//   match  — this is what preselects the team WITHOUT a model call. A team with
//            no match is authored-only: still pickable by hand, never automatic.

export interface TeamEditorProps {
  open: boolean;
  /** null creates; a team edits it (a builtin edits into a project override). */
  team: TeamSpec | null;
  /** Agent ids the harness can dispatch. A picker must not offer anything else. */
  agents: string[];
  /** The narrower roster that can answer the triage contract. */
  managers: string[];
  /** Skills installed in this workspace — a team may load as many as it needs. */
  skills?: Skill[];
  /** Ids already taken, so a create cannot silently overwrite a team. */
  takenIds: string[];
  saving?: boolean;
  onCancel: () => void;
  onSave: (team: TeamSpec) => void;
}

const BLANK: TeamSpec = {
  id: '',
  name: '',
  charter: '',
  owns: [],
  acceptance: '',
  worker: '',
  reviewer: '',
  tester: '',
  manager: '',
  agents: [],
  skills: [],
  match: { keywords: [], files: [], extensions: [], priority: 0 },
};

export default function TeamEditor({
  open,
  team,
  agents,
  managers,
  skills = [],
  takenIds,
  saving,
  onCancel,
  onSave,
}: TeamEditorProps) {
  const [draft, setDraft] = useState<TeamSpec>(BLANK);

  // Reset on every open. A draft left over from the last team is how someone
  // saves the frontend's globs onto the backend team.
  useEffect(() => {
    if (!open) return;
    setDraft(team ? { ...team, match: { ...(team.match ?? {}) } } : { ...BLANK, match: { ...BLANK.match } });
  }, [open, team]);

  const creating = !team;
  const id = slugify(draft.id ?? '');
  const clash = creating && id !== '' && takenIds.includes(id);
  const problem = !id
    ? 'An id is required — it is what tasks and events name this team by.'
    : clash
      ? `"${id}" already exists. Pick another id, or edit that team instead.`
      : (draft.owns ?? []).length === 0
        ? 'A team owning no paths can never be routed a task — it would sit idle for the whole run.'
        : '';

  const patch = (next: Partial<TeamSpec>) => setDraft((d) => ({ ...d, ...next }));
  const patchMatch = (next: Partial<NonNullable<TeamSpec['match']>>) =>
    setDraft((d) => ({ ...d, match: { ...(d.match ?? {}), ...next } }));

  return (
    <Modal
      open={open}
      title={creating ? 'New team' : `Edit ${team?.name || team?.id}`}
      description={
        team?.builtin
          ? 'This is a builtin. Saving writes a project override that shadows it — the builtin itself stays available.'
          : 'Teams may never share a path. An overlapping team is refused when the plan is composed.'
      }
      onClose={onCancel}
      className="max-h-[88vh] max-w-3xl overflow-y-auto"
      footer={
        <>
          <button type="button" className="btn-secondary focus-ring text-xs" onClick={onCancel}>
            Cancel
          </button>
          <button
            type="button"
            className="btn-primary focus-ring text-xs"
            disabled={!!problem || saving}
            onClick={() => onSave({ ...draft, id })}
          >
            {saving ? 'Saving…' : creating ? 'Create team' : 'Save team'}
          </button>
        </>
      }
    >
      <div className="space-y-3">
        {problem && (
          <p role="alert" className="rounded-md bg-amber-50 px-2.5 py-1.5 text-[11px] text-amber-800 dark:bg-amber-950/40 dark:text-amber-200">
            {problem}
          </p>
        )}

        <div className="grid gap-3 [grid-template-columns:repeat(auto-fit,minmax(12rem,1fr))]">
          <Field id="team-id" label="Id" hint="lowercase, used on tasks and in events">
            <input
              id="team-id"
              value={draft.id ?? ''}
              onChange={(e) => patch({ id: e.target.value })}
              disabled={!creating}
              placeholder="backend-go"
              className="input-mono h-8 w-full text-xs"
            />
          </Field>
          <Field id="team-name" label="Name" hint="the human label">
            <input
              id="team-name"
              value={draft.name ?? ''}
              onChange={(e) => patch({ name: e.target.value })}
              placeholder="Backend · Go"
              className="input h-8 w-full text-xs"
            />
          </Field>
          <Field id="team-icon" label="Icon" hint="one emoji, shown on the card">
            <input
              id="team-icon"
              value={draft.icon ?? ''}
              onChange={(e) => patch({ icon: e.target.value })}
              placeholder="🐹"
              className="input h-8 w-full text-xs"
            />
          </Field>
        </div>

        <Field
          id="team-charter"
          label="Charter"
          hint="injected into every task pack for this team — it is what keeps a worker from drifting into another team's half"
        >
          <textarea
            id="team-charter"
            value={draft.charter ?? ''}
            onChange={(e) => patch({ charter: e.target.value })}
            rows={2}
            placeholder="Own the Go service: handlers, domain types and their tests."
            className="input w-full text-xs"
          />
        </Field>

        <Field
          id="team-owns"
          label="Owns"
          hint="one glob per line — no other team may claim these paths"
        >
          <textarea
            id="team-owns"
            value={(draft.owns ?? []).join('\n')}
            onChange={(e) => patch({ owns: splitLines(e.target.value) })}
            rows={3}
            placeholder={'cmd/**\ninternal/**\ngo.mod'}
            className="input-mono w-full text-[11px]"
          />
        </Field>

        <Field
          id="team-acceptance"
          label="Acceptance"
          hint="proves this team's half works ALONE — without it, a break here surfaces only at integration"
        >
          <input
            id="team-acceptance"
            value={draft.acceptance ?? ''}
            onChange={(e) => patch({ acceptance: e.target.value })}
            placeholder="go build ./... && go test ./..."
            className="input-mono h-8 w-full text-[11px]"
          />
        </Field>

        <fieldset className="rounded-lg border border-gray-200 p-2.5 dark:border-gray-800">
          <legend className="px-1 text-[10px] font-bold uppercase tracking-wider text-gray-500">
            Staffing
          </legend>
          <p className="mb-2 text-[11px] text-gray-500 dark:text-gray-400">
            Blank means the pipeline default, which always exists. An agent this harness cannot
            dispatch is dropped with a warning when the plan is composed, so only registered ones
            are offered.
          </p>
          <div className="grid gap-2 [grid-template-columns:repeat(auto-fit,minmax(11rem,1fr))]">
            <AgentPicker
              id="team-worker"
              label="Worker"
              value={draft.worker ?? ''}
              agents={agents}
              onChange={(v) => patch({ worker: v })}
            />
            <AgentPicker
              id="team-reviewer"
              label="Reviewer"
              value={draft.reviewer ?? ''}
              agents={agents}
              onChange={(v) => patch({ reviewer: v })}
            />
            <AgentPicker
              id="team-tester"
              label="Tester"
              value={draft.tester ?? ''}
              agents={agents}
              onChange={(v) => patch({ tester: v })}
            />
            <AgentPicker
              id="team-manager"
              label="Project manager"
              value={draft.manager ?? ''}
              agents={managers}
              onChange={(v) => patch({ manager: v })}
            />
          </div>
          {/* The roster, in whatever number the team needs. Four seats is a
              shape the harness dispatches, not a shape a team has to be — and a
              form that offers only four quietly refuses the fifth person the
              user wanted. Its members are listed FIRST when this team's manager
              triages a rejected delivery, and offered first for its tasks. */}
          <div className="mt-3">
            <MultiPicker
              id="team-roster"
              label="Also on this team"
              addLabel="Add agent"
              emptyLabel="just the seats above"
              hint="Any number. These are offered first for this team's tasks, and listed first when its project manager decides who takes a rejected delivery."
              options={agents}
              value={draft.agents ?? []}
              onChange={(next) => patch({ agents: next })}
              disabled={saving}
            />
          </div>
        </fieldset>

        <fieldset className="rounded-lg border border-gray-200 p-2.5 dark:border-gray-800">
          <legend className="px-1 text-[10px] font-bold uppercase tracking-wider text-gray-500">
            Skills
          </legend>
          <MultiPicker
            id="team-skills"
            label="Loaded into this team's task packs"
            addLabel="Add skill"
            emptyLabel="none — the run's matching rules decide"
            hint="Pinned for every task this team takes, on top of whatever the query matches."
            options={skills.map((s) => s.name)}
            value={draft.skills ?? []}
            describe={(name) => skills.find((s) => s.name === name)?.description}
            onChange={(next) => patch({ skills: next })}
            disabled={saving}
          />
        </fieldset>

        <fieldset className="rounded-lg border border-gray-200 p-2.5 dark:border-gray-800">
          <legend className="px-1 text-[10px] font-bold uppercase tracking-wider text-gray-500">
            When this team applies
          </legend>
          <p className="mb-2 text-[11px] text-gray-500 dark:text-gray-400">
            This is what picks the team for a request — with no model call, which is why it is
            reliable on a small model. Leave it all empty to make the team manual-only: still
            pickable by hand, never automatic.
          </p>
          <div className="grid gap-2 [grid-template-columns:repeat(auto-fit,minmax(12rem,1fr))]">
            <Field id="team-keywords" label="Query keywords" hint="matched on word boundaries">
              <input
                id="team-keywords"
                value={(draft.match?.keywords ?? []).join(', ')}
                onChange={(e) => patchMatch({ keywords: splitCommas(e.target.value) })}
                placeholder="backend, api, handler"
                className="input-mono h-8 w-full text-[11px]"
              />
            </Field>
            <Field id="team-files" label="Marker files" hint="found anywhere in the tree">
              <input
                id="team-files"
                value={(draft.match?.files ?? []).join(', ')}
                onChange={(e) => patchMatch({ files: splitCommas(e.target.value) })}
                placeholder="go.mod, web/package.json"
                className="input-mono h-8 w-full text-[11px]"
              />
            </Field>
            <Field id="team-extensions" label="Extensions" hint="present in the workspace">
              <input
                id="team-extensions"
                value={(draft.match?.extensions ?? []).join(', ')}
                onChange={(e) => patchMatch({ extensions: splitCommas(e.target.value) })}
                placeholder=".go, .tsx"
                className="input-mono h-8 w-full text-[11px]"
              />
            </Field>
            <Field
              id="team-priority"
              label="Priority"
              hint="tiebreak; NEGATIVE means never auto-selected"
            >
              <input
                id="team-priority"
                type="number"
                value={draft.match?.priority ?? 0}
                onChange={(e) => patchMatch({ priority: Number(e.target.value) || 0 })}
                className="input h-8 w-full text-[11px]"
              />
            </Field>
          </div>
        </fieldset>
      </div>
    </Modal>
  );
}

function Field({
  id,
  label,
  hint,
  children,
}: {
  id: string;
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="min-w-0">
      <label htmlFor={id} className="label mb-1 block text-[10px]">
        {label}
      </label>
      {children}
      {hint && <p className="mt-1 text-[10px] leading-tight text-gray-400 dark:text-gray-500">{hint}</p>}
    </div>
  );
}

/**
 * AgentPicker offers only agents the harness reported it can dispatch.
 *
 * A value already on the team that is no longer registered stays selectable and
 * says so: dropping it would silently rewrite the user's choice the next time
 * they opened the form, and they would never know which field changed.
 */
function AgentPicker({
  id,
  label,
  value,
  agents,
  onChange,
}: {
  id: string;
  label: string;
  value: string;
  agents: string[];
  onChange: (v: string) => void;
}) {
  const missing = value !== '' && !agents.includes(value);
  return (
    <div>
      <label htmlFor={id} className="label mb-1 block text-[10px]">
        {label}
      </label>
      <select
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="input h-8 w-full text-[11px]"
      >
        <option value="">pipeline default</option>
        {missing && <option value={value}>{value} (not registered)</option>}
        {agents.map((a) => (
          <option key={a} value={a}>
            {a}
          </option>
        ))}
      </select>
    </div>
  );
}

function splitLines(v: string): string[] {
  return v
    .split('\n')
    .map((s) => s.trim())
    .filter(Boolean);
}

function splitCommas(v: string): string[] {
  return v
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
}
