import { Sparkles, Play, Pin, AlertTriangle, Users, Layers, Loader2, ShieldCheck } from 'lucide-react';
import clsx from 'clsx';
import { teamColor } from '@/components/Board/teamColor';
import type { DynamicComposition, TeamPreselect, TeamSpec, TeamStaffing } from '@/types';

// ── Sending a request to teams ───────────────────────────────────────────
//
// The library answers "which teams exist". This panel answers the question a
// person actually has at the keyboard: "if I send THIS request, who works on
// it, who manages them, and what pipeline runs" — and then lets them send it.
//
// Three things are shown before anything starts, all from the code the run
// itself will execute (never a second implementation that could drift):
//
//   • the teams the request would get, with the evidence that picked each and
//     the staffing the run will actually dispatch — worker, reviewer, tester,
//     and the MANAGER, resolved: a team that named nobody, or named an agent
//     that cannot answer the triage contract, is shown with the run default;
//   • what the selection does to the run — two or more teams build in
//     parallel behind a frozen contract, one team staffs the whole run, none
//     means the plain single stream;
//   • the pipeline the composer would assemble for it — the phases and the
//     execute loop — so a request that would be handed to the wrong specialist
//     is caught here rather than in the log.
//
// Then two actions: RUN — start the run with exactly these teams pinned, which
// is "send this request to these teams" — and ACTIVATE, which writes the teams
// as the project's org chart so every later run inherits them.

export interface TeamComposerProps {
  teams: TeamSpec[];
  pinned: string[];
  pipelineTeams: string[];
  query: string;
  preselect: TeamPreselect | null;
  composition: DynamicComposition | null;
  probing: boolean;
  activating: boolean;
  starting: boolean;
  /** True while a run is in flight: a second run cannot start. */
  running: boolean;
  squadsEnabled: boolean;
  dynamicEnabled: boolean;
  defaultManager: string;
  onQueryChange: (v: string) => void;
  onProbe: () => void;
  onTogglePin: (id: string) => void;
  onActivate: (ids: string[]) => void;
  onRun: (ids: string[]) => void;
}

export default function TeamComposer({
  teams,
  pinned,
  pipelineTeams,
  query,
  preselect,
  composition,
  probing,
  activating,
  starting,
  running,
  squadsEnabled,
  dynamicEnabled,
  defaultManager,
  onQueryChange,
  onProbe,
  onTogglePin,
  onActivate,
  onRun,
}: TeamComposerProps) {
  // What "Run" and "Activate" would send. The preview's selection when there
  // is one (it already folded the pins in), else the pins alone.
  const selected = preselect?.selected?.length ? preselect.selected : pinned;
  const canActivate = selected.length >= 2 && !running;
  const canRun = query.trim().length > 0 && !running && !starting;
  const byID = new Map(teams.map((t) => [t.id, t]));

  return (
    <section className="space-y-3" aria-labelledby="team-composer-heading">
      <header className="flex flex-wrap items-center gap-2">
        <Sparkles size={14} className="shrink-0 text-brand-500" aria-hidden="true" />
        <h2 id="team-composer-heading" className="text-sm font-bold text-gray-900 dark:text-gray-100">
          Send a request to teams
        </h2>
        {running && (
          <span className="badge-brand inline-flex items-center gap-1 text-[10px]">
            <Loader2 size={10} className="animate-spin" aria-hidden="true" /> a run is in flight
          </span>
        )}
      </header>
      <p className="text-[11px] text-gray-500 dark:text-gray-400">
        Type a request and see who would work on it before anything starts: the teams it selects and
        why, who staffs and manages each, and the pipeline the composer assembles. Pin a team on its
        card to force it onto the run. <strong>Run</strong> sends the request with exactly these teams;{' '}
        <strong>Activate</strong> makes them the project&rsquo;s org chart for every later run.
      </p>

      <div className="rounded-lg border border-gray-200 p-3 dark:border-gray-800">
        <div className="flex flex-wrap gap-2">
          <input
            value={query}
            onChange={(e) => onQueryChange(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') onProbe();
            }}
            aria-label="Request to preselect teams for"
            placeholder="Add a Go API endpoint and the React page that calls it"
            className="input h-8 min-w-0 flex-1 text-xs"
            disabled={starting}
          />
          <button
            type="button"
            onClick={onProbe}
            disabled={probing || !query.trim()}
            className="btn-secondary focus-ring h-8 gap-1.5 px-3 text-xs"
          >
            {probing ? 'Checking…' : 'Preselect'}
          </button>
          <button
            type="button"
            onClick={() => onRun(selected)}
            disabled={!canRun}
            title={
              running
                ? 'A run is already in flight'
                : selected.length > 0
                  ? `Start a run with ${selected.join(', ')} pinned`
                  : 'Start a run — the library picks the teams from the request and the workspace'
            }
            className="btn-primary focus-ring h-8 gap-1.5 px-3 text-xs"
          >
            <Play size={12} aria-hidden="true" />
            {starting ? 'Starting…' : `Run${selected.length ? ` with ${selected.length} team${selected.length === 1 ? '' : 's'}` : ''}`}
          </button>
          <button
            type="button"
            onClick={() => onActivate(selected)}
            disabled={!canActivate || activating}
            title={
              canActivate
                ? 'Write these teams as the org chart, and pin them so every later run keeps them'
                : 'Two teams minimum — one team is the single-stream pipeline wearing a hat'
            }
            className="btn-secondary focus-ring h-8 gap-1.5 px-3 text-xs"
          >
            <Pin size={12} aria-hidden="true" />
            {activating ? 'Activating…' : `Activate${selected.length ? ` (${selected.length})` : ''}`}
          </button>
        </div>

        {(pinned.length > 0 || pipelineTeams.length > 0) && (
          <div className="mt-2 flex flex-wrap items-center gap-1.5 text-[10px] text-gray-500 dark:text-gray-400">
            {pinned.length > 0 && (
              <>
                <Pin size={10} aria-hidden="true" />
                <span>pinned:</span>
                {pinned.map((id) => (
                  <button
                    key={id}
                    type="button"
                    onClick={() => onTogglePin(id)}
                    title={`Unpin ${id}`}
                    className={clsx('focus-ring rounded px-1.5 py-0.5 text-[10px]', teamColor(id).badge)}
                  >
                    {id} ×
                  </button>
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

        {preselect && (
          <PreselectSummary
            preselect={preselect}
            squadsEnabled={squadsEnabled}
            defaultManager={defaultManager}
            byID={byID}
          />
        )}

        {preselect && composition && (
          <CompositionPreview composition={composition} dynamicEnabled={dynamicEnabled} />
        )}
      </div>
    </section>
  );
}

/**
 * PreselectSummary says what would happen AND why.
 *
 * "Teams: backend, frontend" is not enough to argue with. The reasons are what
 * turn a preselection the user disagrees with into an edit they can make — a
 * keyword to add, a marker file to name, a glob to narrow — and the staffing is
 * what turns "the backend team" into the people who will actually show up.
 */
function PreselectSummary({
  preselect,
  squadsEnabled,
  defaultManager,
  byID,
}: {
  preselect: TeamPreselect;
  squadsEnabled: boolean;
  defaultManager: string;
  byID: Map<string, TeamSpec>;
}) {
  const chosen = preselect.selected ?? [];
  const rejected = (preselect.evidence ?? []).filter((e) => !e.selected);
  const evidence = new Map((preselect.evidence ?? []).map((e) => [e.team_id, e]));
  // The server resolves managers; a preview from an older server (no `teams`)
  // falls back to the library card's own answer.
  const staffing: TeamStaffing[] =
    preselect.teams && preselect.teams.length > 0
      ? preselect.teams
      : chosen.map((id) => {
          const t = byID.get(id);
          return {
            id,
            name: t?.name,
            worker: t?.worker,
            reviewer: t?.reviewer,
            tester: t?.tester,
            manager: t?.effective_manager || t?.manager || defaultManager,
            manager_default: t?.manager_default ?? !t?.manager,
            skills: t?.skills,
            owns: t?.owns,
          };
        });
  const parallel = preselect.enabled ?? (chosen.length >= 2 && squadsEnabled);

  return (
    <div className="mt-3 space-y-2">
      <p
        className={clsx(
          'flex items-start gap-1.5 text-[11px]',
          parallel ? 'text-gray-700 dark:text-gray-200' : 'text-amber-700 dark:text-amber-300',
        )}
      >
        <Users size={12} className="mt-0.5 shrink-0" aria-hidden="true" />
        <span>
          {parallel
            ? `${chosen.length} teams would run in parallel: ${chosen.join(', ')}`
            : chosen.length === 1
              ? `Only ${chosen[0]} matched — it staffs the run, which would run as one stream: its worker, reviewer, tester and skills take the pipeline.`
              : chosen.length > 1
                ? `${chosen.join(', ')} matched, but teams are turned off (squads: false) — this request would run as one stream.`
                : 'No team matched — this request would run as a single stream.'}
        </span>
      </p>

      {staffing.length > 0 && (
        <div className="grid gap-2 [grid-template-columns:repeat(auto-fill,minmax(16rem,1fr))]">
          {staffing.map((s) => {
            const ev = evidence.get(s.id);
            const color = teamColor(s.id);
            return (
              <div
                key={s.id}
                className="rounded-md border border-gray-200 p-2 text-[10px] dark:border-gray-800"
                data-testid={`staffing-${s.id}`}
              >
                <div className="mb-1 flex flex-wrap items-center gap-1.5">
                  <span className={clsx('rounded px-1.5 py-0.5 font-mono font-semibold', color.badge)}>{s.id}</span>
                  {ev?.pinned ? (
                    <span className="badge-brand text-[10px]">pinned</span>
                  ) : ev ? (
                    <span className="text-gray-400">score {ev.score}</span>
                  ) : null}
                </div>
                <dl className="space-y-0.5">
                  <Seat label="worker" id={s.worker} />
                  <Seat label="reviewer" id={s.reviewer} />
                  <Seat label="tester" id={s.tester} />
                  <div className="flex gap-1.5">
                    <dt className="w-14 shrink-0 text-gray-400">manager</dt>
                    <dd className="min-w-0 flex-1 text-gray-700 dark:text-gray-300">
                      <span className="font-mono">{s.manager || defaultManager}</span>
                      {(s.manager_default ?? !s.manager) && (
                        <span className="ml-1 text-gray-400" title="This team names no manager of its own, or names one that cannot answer the triage contract; the run's default manager triages its rejected work.">
                          (run default)
                        </span>
                      )}
                    </dd>
                  </div>
                  {(s.skills ?? []).length > 0 && (
                    <div className="flex gap-1.5">
                      <dt className="w-14 shrink-0 text-gray-400">skills</dt>
                      <dd className="min-w-0 flex-1 font-mono text-gray-700 dark:text-gray-300">{s.skills!.join('  ')}</dd>
                    </div>
                  )}
                </dl>
                {ev && !ev.pinned && (ev.reasons ?? []).length > 0 && (
                  <p className="mt-1 text-gray-500 dark:text-gray-400">{ev.reasons!.join('; ')}</p>
                )}
                {ev?.pinned && <p className="mt-1 text-gray-500 dark:text-gray-400">pinned by hand</p>}
              </div>
            );
          })}
        </div>
      )}

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

function Seat({ label, id }: { label: string; id?: string }) {
  return (
    <div className="flex gap-1.5">
      <dt className="w-14 shrink-0 text-gray-400">{label}</dt>
      <dd className="min-w-0 flex-1 font-mono text-gray-700 dark:text-gray-300">
        {id || <span className="font-sans text-gray-400">pipeline default</span>}
      </dd>
    </div>
  );
}

/**
 * CompositionPreview is the pipeline the composer would assemble — the
 * deterministic guess, from the same code the run falls back to. It is shown
 * here because the team decision and the pipeline decision are one decision
 * now: a team's worker is who execute gets bound to.
 */
function CompositionPreview({
  composition,
  dynamicEnabled,
}: {
  composition: DynamicComposition;
  dynamicEnabled: boolean;
}) {
  const phases = (composition.phases ?? []).filter((p) => p.enabled && p.when !== 'never');
  const exec = composition.execute;
  return (
    <div className="mt-3 rounded-md border border-dashed border-gray-200 p-2 dark:border-gray-800" data-testid="composition-preview">
      <div className="mb-1.5 flex flex-wrap items-center gap-1.5 text-[10px] font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">
        <Layers size={11} className="text-brand-500" aria-hidden="true" />
        Pipeline the composer would assemble
        {composition.complexity && (
          <span className="badge-brand normal-case tracking-normal">
            {composition.complexity}
            {composition.kind ? `:${composition.kind}` : ''}
          </span>
        )}
        {!dynamicEnabled && (
          <span className="badge-neutral normal-case tracking-normal" title="dynamic_pipeline is off; the static pipeline runs instead">
            dynamic pipeline off
          </span>
        )}
      </div>
      {composition.team_note && (
        <p className="mb-1.5 flex items-start gap-1 text-[10px] text-gray-600 dark:text-gray-300">
          <ShieldCheck size={10} className="mt-0.5 shrink-0 text-brand-500" aria-hidden="true" />
          {composition.team_note}
        </p>
      )}
      <div className="flex flex-wrap gap-1">
        {phases.map((p, i) => (
          <span key={`${p.id}-${i}`} className="badge-neutral font-mono text-[10px]" title={p.when ? `${p.id} (${p.when})` : p.id}>
            {p.id}
            {p.agent && <span className="ml-0.5 text-gray-400">@{p.agent}</span>}
          </span>
        ))}
      </div>
      <p className="mt-1.5 text-[10px] text-gray-500 dark:text-gray-400">
        loop: worker <span className="font-mono">{exec?.default_role || 'worker'}</span> · reviewer{' '}
        <span className="font-mono">{exec?.reviewer || 'reviewer'}</span> · corrector{' '}
        <span className="font-mono">{exec?.corrector || 'corrector'}</span>
        {exec?.max_waves ? <> · waves {exec.max_waves}</> : null}
      </p>
    </div>
  );
}
