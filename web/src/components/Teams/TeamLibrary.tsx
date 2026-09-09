import { useMemo, useState } from 'react';
import {
  Plus,
  Pencil,
  Trash2,
  Copy,
  Pin,
  PinOff,
  Search,
  Lock,
  UserCog,
} from 'lucide-react';
import clsx from 'clsx';
import type { TeamEvidence, TeamPreselect, TeamSpec } from '@/types';

// ── The library ──────────────────────────────────────────────────────────
//
// Teams the user authored, which exist whether or not a run is going. A team
// is created, edited, duplicated and deleted here, from existing agents — and
// given a project manager, which every team has whether its author named one
// or not: a team that names nobody answers to the run's default manager, and
// the card says so rather than showing an empty seat.

export interface TeamLibraryProps {
  teams: TeamSpec[];
  pinned: string[];
  pipelineTeams: string[];
  /** The last preview, so a card can show whether it was selected and why. */
  preselect: TeamPreselect | null;
  /** The agent that manages a team naming no manager of its own. */
  defaultManager: string;
  /** True while a run is in flight: the server refuses edits until it ends. */
  readOnly?: boolean;
  onTogglePin: (id: string) => void;
  onCreate: () => void;
  onEdit: (team: TeamSpec) => void;
  onDuplicate: (team: TeamSpec) => void;
  onDelete: (team: TeamSpec) => void;
  /** Give the team its own project manager (a triage agent named after it). */
  onGiveManager: (team: TeamSpec) => void;
}

export default function TeamLibrary({
  teams,
  pinned,
  pipelineTeams,
  preselect,
  defaultManager,
  readOnly = false,
  onTogglePin,
  onCreate,
  onEdit,
  onDuplicate,
  onDelete,
  onGiveManager,
}: TeamLibraryProps) {
  const [filter, setFilter] = useState('');

  const evidence = useMemo(() => {
    const m = new Map<string, TeamEvidence>();
    for (const e of preselect?.evidence ?? []) m.set(e.team_id, e);
    return m;
  }, [preselect]);

  const shown = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return teams;
    return teams.filter((t) =>
      [t.id, t.name, t.charter, t.description, ...(t.tags ?? []), ...(t.owns ?? [])]
        .filter(Boolean)
        .some((v) => String(v).toLowerCase().includes(q)),
    );
  }, [teams, filter]);

  return (
    <section className="space-y-3" aria-labelledby="team-library-heading">
      <header className="flex flex-wrap items-center gap-2">
        <h2 id="team-library-heading" className="text-sm font-bold text-gray-900 dark:text-gray-100">Team library</h2>
        <span className="badge-neutral text-[10px]">{teams.length}</span>
        <div className="relative ml-auto">
          <Search
            size={12}
            className="pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-gray-400"
            aria-hidden="true"
          />
          <input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            aria-label="Filter teams"
            placeholder="Filter…"
            className="input h-8 w-40 pl-6 text-xs"
          />
        </div>
        <button
          type="button"
          onClick={onCreate}
          disabled={readOnly}
          title={readOnly ? 'A run is in flight — the library is read-only until it ends' : 'Create a team'}
          className="btn-primary focus-ring h-8 gap-1.5 px-3 text-xs"
        >
          <Plus size={13} aria-hidden="true" />
          New team
        </button>
      </header>

      <p className="text-[11px] text-gray-500 dark:text-gray-400">
        A team is the people who own one part of the codebase: the paths it may write, the command
        that proves its half alone, the agents that staff it, the project manager that decides who
        takes its rejected work, and the evidence that puts it on a request. Teams selected together
        may never share a path — the overlap is resolved here, before a run can lose an edit to it.
      </p>

      {shown.length === 0 && (
        <p className="rounded-md border border-dashed border-gray-200 px-3 py-6 text-center text-xs text-gray-400 dark:border-gray-800">
          {teams.length === 0 ? 'No teams yet — create one.' : 'No team matches that filter.'}
        </p>
      )}

      <div className="grid gap-3 [grid-template-columns:repeat(auto-fill,minmax(21rem,1fr))]">
        {shown.map((t) => (
          <TeamCard
            key={t.id}
            team={t}
            evidence={evidence.get(t.id)}
            pinned={pinned.includes(t.id)}
            fromPipeline={pipelineTeams.includes(t.id)}
            defaultManager={defaultManager}
            readOnly={readOnly}
            onTogglePin={() => onTogglePin(t.id)}
            onEdit={() => onEdit(t)}
            onDuplicate={() => onDuplicate(t)}
            onDelete={() => onDelete(t)}
            onGiveManager={() => onGiveManager(t)}
          />
        ))}
      </div>
    </section>
  );
}

function TeamCard({
  team,
  evidence,
  pinned,
  fromPipeline,
  defaultManager,
  readOnly,
  onTogglePin,
  onEdit,
  onDuplicate,
  onDelete,
  onGiveManager,
}: {
  team: TeamSpec;
  evidence?: TeamEvidence;
  pinned: boolean;
  fromPipeline: boolean;
  defaultManager: string;
  readOnly: boolean;
  onTogglePin: () => void;
  onEdit: () => void;
  onDuplicate: () => void;
  onDelete: () => void;
  onGiveManager: () => void;
}) {
  const staffing = [
    ['worker', team.worker],
    ['reviewer', team.reviewer],
    ['tester', team.tester],
  ].filter(([, v]) => !!v) as [string, string][];
  const manualOnly = (team.match?.priority ?? 0) < 0;
  // The manager the run will actually use. A team that names nobody — or
  // names an agent that cannot answer the triage contract — answers to the
  // run default, and the card says so instead of showing an empty seat.
  const manager = team.effective_manager || team.manager || defaultManager;
  const managerIsDefault = team.manager_default ?? !team.manager;
  const managerOverridden = !!team.manager && team.effective_manager != null && team.effective_manager !== team.manager;

  return (
    <article
      className={clsx(
        'rounded-lg border p-3 transition-colors',
        evidence?.selected
          ? 'border-brand-400 bg-brand-50/40 dark:border-brand-600 dark:bg-brand-950/20'
          : 'border-gray-200 dark:border-gray-800',
      )}
    >
      <div className="mb-1.5 flex flex-wrap items-center gap-1.5">
        {team.icon && <span aria-hidden="true">{team.icon}</span>}
        <h3 className="min-w-0 flex-1 truncate text-xs font-semibold text-gray-900 dark:text-gray-100">
          {team.name || team.id}
        </h3>
        <span className="badge-neutral shrink-0 font-mono text-[10px]">{team.id}</span>
        {team.builtin && (
          <span className="badge-neutral shrink-0 gap-0.5 text-[10px]" title="Shipped with SLMCode. Editing writes a project override.">
            <Lock size={9} aria-hidden="true" /> builtin
          </span>
        )}
        {!team.builtin && team.source && (
          <span className="badge-brand shrink-0 text-[10px]">{team.source}</span>
        )}
      </div>

      {(team.charter || team.description) && (
        <p className="mb-2 line-clamp-2 text-[11px] text-gray-500 dark:text-gray-400">
          {team.charter || team.description}
        </p>
      )}

      <dl className="space-y-1 text-[10px]">
        <Row label="owns">
          {(team.owns ?? []).length === 0 ? (
            <span className="text-red-600 dark:text-red-400">nothing — no task can reach this team</span>
          ) : (
            <span className="font-mono">{(team.owns ?? []).join('  ')}</span>
          )}
        </Row>
        <Row label="acceptance">
          {team.acceptance ? (
            <span className="font-mono">{team.acceptance}</span>
          ) : (
            <span className="text-amber-700 dark:text-amber-400">
              none — a break here surfaces only at integration
            </span>
          )}
        </Row>
        {staffing.length > 0 && (
          <Row label="staffed by">
            <span className="flex flex-wrap gap-1">
              {staffing.map(([role, id]) => (
                <span key={role} className="badge-neutral text-[10px]" title={role}>
                  {id}
                </span>
              ))}
            </span>
          </Row>
        )}
        <Row label="manager">
          <span className="flex flex-wrap items-center gap-1">
            <span className="badge-neutral gap-1 text-[10px]" title="Decides who takes this team's rejected work next">
              <UserCog size={9} aria-hidden="true" />
              {manager}
            </span>
            {managerIsDefault && (
              <span className="text-gray-500 dark:text-gray-400" title="This team names no manager of its own, so the run's default project manager triages its rejected work">
                run default
              </span>
            )}
            {managerOverridden && (
              <span className="text-amber-700 dark:text-amber-400" title={`${team.manager} cannot answer the triage contract, so the run default manages this team`}>
                ({team.manager} cannot triage)
              </span>
            )}
          </span>
        </Row>
        {/* The rest of the team. A card that showed only the four seats would
            say a team IS four people, which is exactly the model the open
            roster exists to replace. */}
        {(team.agents ?? []).length > 0 && (
          <Row label="also on it">
            <span className="flex flex-wrap gap-1">
              {team.agents!.map((id) => (
                <span key={id} className="badge-neutral text-[10px]">
                  {id}
                </span>
              ))}
            </span>
          </Row>
        )}
        {(team.skills ?? []).length > 0 && (
          <Row label="skills">
            <span className="font-mono">{team.skills!.join('  ')}</span>
          </Row>
        )}
        <Row label="applies when">
          {manualOnly ? (
            <span className="text-gray-500 dark:text-gray-400">picked by hand only (priority &lt; 0)</span>
          ) : (team.match?.keywords ?? []).length ||
            (team.match?.files ?? []).length ||
            (team.match?.extensions ?? []).length ? (
            <span className="font-mono">
              {[...(team.match?.keywords ?? []), ...(team.match?.files ?? []), ...(team.match?.extensions ?? [])].join('  ')}
            </span>
          ) : (
            <span className="text-gray-500 dark:text-gray-400">picked by hand only (no match rules)</span>
          )}
        </Row>
      </dl>

      {evidence && (
        <p
          className={clsx(
            'mt-2 rounded px-1.5 py-1 text-[10px]',
            evidence.selected
              ? 'bg-brand-100/70 text-brand-800 dark:bg-brand-900/40 dark:text-brand-200'
              : 'bg-gray-100 text-gray-600 dark:bg-gray-800/60 dark:text-gray-400',
          )}
        >
          {evidence.selected ? 'selected' : 'not selected'} · score {evidence.score}
          {evidence.conflict && ` · ${evidence.conflict} already claims its paths`}
          {(evidence.reasons ?? []).length > 0 && ` · ${(evidence.reasons ?? []).join('; ')}`}
        </p>
      )}

      <div className="mt-2 flex flex-wrap items-center gap-1">
        <button
          type="button"
          onClick={onTogglePin}
          aria-pressed={pinned}
          title={
            pinned
              ? 'Drop from the selection you are about to Activate'
              : 'Include in the preview and in the next Activate, whatever the query says'
          }
          className={clsx('btn-ghost focus-ring h-7 gap-1 px-2 text-[11px]', pinned && 'text-brand-600 dark:text-brand-400')}
        >
          {pinned ? <PinOff size={11} aria-hidden="true" /> : <Pin size={11} aria-hidden="true" />}
          {pinned ? 'Unpin' : 'Pin'}
        </button>
        <button type="button" onClick={onEdit} disabled={readOnly} className="btn-ghost focus-ring h-7 gap-1 px-2 text-[11px]">
          <Pencil size={11} aria-hidden="true" />
          Edit
        </button>
        <button type="button" onClick={onDuplicate} disabled={readOnly} className="btn-ghost focus-ring h-7 gap-1 px-2 text-[11px]">
          <Copy size={11} aria-hidden="true" />
          Duplicate
        </button>
        {managerIsDefault && (
          <button
            type="button"
            onClick={onGiveManager}
            disabled={readOnly}
            title={`Create ${team.id}-triage — a project manager that knows this team's charter and people — and put it in charge`}
            className="btn-ghost focus-ring h-7 gap-1 px-2 text-[11px]"
          >
            <UserCog size={11} aria-hidden="true" />
            Give it a manager
          </button>
        )}
        <button
          type="button"
          onClick={onDelete}
          disabled={team.builtin || readOnly}
          title={
            team.builtin
              ? 'A builtin lives inside the binary — edit it to create an override instead'
              : `Delete ${team.id}`
          }
          className="btn-ghost focus-ring ml-auto h-7 gap-1 px-2 text-[11px] text-red-600 disabled:text-gray-400 dark:text-red-400"
        >
          <Trash2 size={11} aria-hidden="true" />
          Delete
        </button>
      </div>
      {fromPipeline && (
        <p className="mt-1 text-[10px] text-gray-500 dark:text-gray-400">
          attached to the active pipeline — on every run of it
        </p>
      )}
    </article>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex gap-1.5">
      <dt className="w-20 shrink-0 text-gray-400 dark:text-gray-500">{label}</dt>
      <dd className="min-w-0 flex-1 break-words text-gray-700 dark:text-gray-300">{children}</dd>
    </div>
  );
}
