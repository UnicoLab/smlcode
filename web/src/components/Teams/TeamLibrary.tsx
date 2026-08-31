import { useMemo, useState } from 'react';
import {
  Plus,
  Pencil,
  Trash2,
  Copy,
  Sparkles,
  Pin,
  PinOff,
  Play,
  Search,
  AlertTriangle,
  Lock,
} from 'lucide-react';
import clsx from 'clsx';
import type { TeamEvidence, TeamPreselect, TeamSpec } from '@/types';

// ── The library ──────────────────────────────────────────────────────────
//
// Teams the user authored, which exist whether or not a run is going. Two
// things happen here that could not happen before:
//
//   • a team is created, edited, duplicated and deleted, from existing agents;
//   • a request is TRIED against the library — "which teams would this get, and
//     why" — before anything is started. The answer comes from the same code
//     the run uses, so it cannot drift from the decision.

export interface TeamLibraryProps {
  teams: TeamSpec[];
  pinned: string[];
  pipelineTeams: string[];
  preselect: TeamPreselect | null;
  probeQuery: string;
  probing: boolean;
  activating: boolean;
  onProbeQueryChange: (v: string) => void;
  onProbe: () => void;
  onTogglePin: (id: string) => void;
  onCreate: () => void;
  onEdit: (team: TeamSpec) => void;
  onDuplicate: (team: TeamSpec) => void;
  onDelete: (team: TeamSpec) => void;
  onActivate: (ids: string[]) => void;
}

export default function TeamLibrary({
  teams,
  pinned,
  pipelineTeams,
  preselect,
  probeQuery,
  probing,
  activating,
  onProbeQueryChange,
  onProbe,
  onTogglePin,
  onCreate,
  onEdit,
  onDuplicate,
  onDelete,
  onActivate,
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

  // What "Activate" would compose. Pins come first because an explicit choice
  // outranks a scored one everywhere else in the system too.
  const selected = preselect?.selected?.length ? preselect.selected : pinned;
  const canActivate = selected.length >= 2;

  return (
    <section className="space-y-3">
      <header className="flex flex-wrap items-center gap-2">
        <h2 className="text-sm font-bold text-gray-900 dark:text-gray-100">Team library</h2>
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
        <button type="button" onClick={onCreate} className="btn-primary focus-ring h-8 gap-1.5 px-3 text-xs">
          <Plus size={13} aria-hidden="true" />
          New team
        </button>
      </header>

      <p className="text-[11px] text-gray-500 dark:text-gray-400">
        A team is a squad template: the paths it owns, the command that proves its half alone, the
        agents that staff it, and the evidence that says when it applies. Teams selected together
        may never share a path — the overlap is resolved here, before a run can lose an edit to it.
      </p>

      {/* ── Try a request ── */}
      <div className="rounded-lg border border-gray-200 p-3 dark:border-gray-800">
        <div className="mb-2 flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">
          <Sparkles size={12} className="text-brand-500" aria-hidden="true" />
          Try a request
        </div>
        <div className="flex flex-wrap gap-2">
          <input
            value={probeQuery}
            onChange={(e) => onProbeQueryChange(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') onProbe();
            }}
            aria-label="Request to preselect teams for"
            placeholder="Add a Go API endpoint and the React page that calls it"
            className="input h-8 min-w-0 flex-1 text-xs"
          />
          <button
            type="button"
            onClick={onProbe}
            disabled={probing || !probeQuery.trim()}
            className="btn-secondary focus-ring h-8 gap-1.5 px-3 text-xs"
          >
            {probing ? 'Checking…' : 'Preselect'}
          </button>
          <button
            type="button"
            onClick={() => onActivate(selected)}
            disabled={!canActivate || activating}
            title={
              canActivate
                ? 'Write these teams as the org chart, and pin them so the next run keeps them'
                : 'Two teams minimum — one team is the single-stream pipeline wearing a hat'
            }
            className="btn-primary focus-ring h-8 gap-1.5 px-3 text-xs"
          >
            <Play size={12} aria-hidden="true" />
            {activating ? 'Activating…' : `Activate${selected.length ? ` (${selected.length})` : ''}`}
          </button>
        </div>

        {preselect && <PreselectSummary preselect={preselect} />}

        {(pinned.length > 0 || pipelineTeams.length > 0) && (
          <div className="mt-2 flex flex-wrap items-center gap-1.5 text-[10px] text-gray-500 dark:text-gray-400">
            {pinned.length > 0 && (
              <>
                <Pin size={10} aria-hidden="true" />
                <span>pinned:</span>
                {pinned.map((id) => (
                  <span key={id} className="badge-brand text-[10px]">
                    {id}
                  </span>
                ))}
              </>
            )}
            {pipelineTeams.length > 0 && (
              <>
                <span className="ml-1">from the pipeline:</span>
                {pipelineTeams.map((id) => (
                  <span key={id} className="badge-neutral text-[10px]">
                    {id}
                  </span>
                ))}
              </>
            )}
          </div>
        )}
      </div>

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
            onTogglePin={() => onTogglePin(t.id)}
            onEdit={() => onEdit(t)}
            onDuplicate={() => onDuplicate(t)}
            onDelete={() => onDelete(t)}
          />
        ))}
      </div>
    </section>
  );
}

/**
 * PreselectSummary says what would happen AND why.
 *
 * "Teams: backend, frontend" is not enough to argue with. The reasons are what
 * turn a preselection the user disagrees with into an edit they can make — a
 * keyword to add, a marker file to name, a glob to narrow.
 */
function PreselectSummary({ preselect }: { preselect: TeamPreselect }) {
  const chosen = preselect.selected ?? [];
  const rejected = (preselect.evidence ?? []).filter((e) => !e.selected);
  return (
    <div className="mt-2 space-y-1.5">
      <p className={clsx('text-[11px]', preselect.enabled ? 'text-gray-700 dark:text-gray-200' : 'text-amber-700 dark:text-amber-300')}>
        {preselect.enabled
          ? `${chosen.length} teams would run in parallel: ${chosen.join(', ')}`
          : chosen.length === 1
            ? `Only ${chosen[0]} matched — one team is the single-stream pipeline wearing a hat, so this request would run as one stream.`
            : 'No team matched — this request would run as a single stream.'}
      </p>
      <ul className="space-y-0.5">
        {(preselect.evidence ?? [])
          .filter((e) => e.selected)
          .map((e) => (
            <li key={e.team_id} className="text-[10px] text-gray-500 dark:text-gray-400">
              <span className="font-mono font-semibold text-brand-600 dark:text-brand-400">{e.team_id}</span>{' '}
              {e.pinned ? 'pinned by hand' : `score ${e.score} — ${(e.reasons ?? []).join('; ')}`}
            </li>
          ))}
      </ul>
      {rejected.length > 0 && (
        <details className="text-[10px] text-gray-500 dark:text-gray-400">
          <summary className="cursor-pointer">{rejected.length} team(s) considered and not selected</summary>
          <ul className="mt-1 space-y-0.5 pl-3">
            {rejected.map((e) => (
              <li key={e.team_id}>
                <span className="font-mono">{e.team_id}</span> (score {e.score})
                {e.conflict && <> — territory already claimed by <span className="font-mono">{e.conflict}</span></>}
                {!e.conflict && (e.reasons ?? []).length > 0 && <> — {(e.reasons ?? []).join('; ')}</>}
              </li>
            ))}
          </ul>
        </details>
      )}
      {(preselect.staffing ?? []).length > 0 && (
        <ul className="space-y-0.5">
          {preselect.staffing!.map((n) => (
            <li key={n} className="flex items-start gap-1 text-[10px] text-amber-700 dark:text-amber-300">
              <AlertTriangle size={10} className="mt-0.5 shrink-0" aria-hidden="true" />
              {n}
            </li>
          ))}
        </ul>
      )}
      {(preselect.problems ?? []).length > 0 && (
        <ul className="space-y-0.5">
          {preselect.problems!.map((p) => (
            <li key={p} className="text-[10px] text-red-600 dark:text-red-400">
              {p}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function TeamCard({
  team,
  evidence,
  pinned,
  fromPipeline,
  onTogglePin,
  onEdit,
  onDuplicate,
  onDelete,
}: {
  team: TeamSpec;
  evidence?: TeamEvidence;
  pinned: boolean;
  fromPipeline: boolean;
  onTogglePin: () => void;
  onEdit: () => void;
  onDuplicate: () => void;
  onDelete: () => void;
}) {
  const staffing = [
    ['worker', team.worker],
    ['reviewer', team.reviewer],
    ['tester', team.tester],
    ['manager', team.manager],
  ].filter(([, v]) => !!v) as [string, string][];
  const manualOnly = (team.match?.priority ?? 0) < 0;

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
        <button type="button" onClick={onEdit} className="btn-ghost focus-ring h-7 gap-1 px-2 text-[11px]">
          <Pencil size={11} aria-hidden="true" />
          Edit
        </button>
        <button type="button" onClick={onDuplicate} className="btn-ghost focus-ring h-7 gap-1 px-2 text-[11px]">
          <Copy size={11} aria-hidden="true" />
          Duplicate
        </button>
        <button
          type="button"
          onClick={onDelete}
          disabled={team.builtin}
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
