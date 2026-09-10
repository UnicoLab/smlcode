import { useCallback, useContext, useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { useNavigate } from 'react-router-dom';
import { Users, Info } from 'lucide-react';
import {
  getTeams,
  createTeam,
  updateTeam,
  deleteTeam,
  preselectTeams,
  activateTeams,
  getSkills,
  previewComposition,
  startRun,
  getTeamActivity,
  createTeamManager,
  ApiError,
} from '@/api/client';
import { AppContext } from '@/App';
import { useBoardStore } from '@/hooks/useBoardStore';
import { useToast } from '@/components/ui/Toast';
import { useConfirm } from '@/components/ui/Modal';
import TeamLibrary from './TeamLibrary';
import TeamEditor from './TeamEditor';
import TeamComposer from './TeamComposer';
import TeamActivity from './TeamActivity';
import { nextFreeID } from './teamId';
import ActiveTeams from './ActiveTeams';
import type {
  DynamicComposition,
  Skill,
  TeamActivity as TeamActivityData,
  TeamPreselect,
  TeamSpec,
  TeamsLibrary,
} from '@/types';

// ── The Teams page ───────────────────────────────────────────────────────
//
// Four things, in the order the decisions are made in:
//
//   1. the LIBRARY — teams the user authored, composed from existing agents,
//      each with the manager that will answer for it. Create, edit, duplicate,
//      delete, give a team its own manager, pin.
//   2. SEND A REQUEST — type a request and see who would work on it (the teams,
//      their staffing and managers, the pipeline the composer assembles), then
//      RUN it with exactly those teams, or ACTIVATE them as the org chart.
//   3. the ORG CHART — what this project currently runs with, editable down to
//      the frozen contract.
//   4. HOW THE TEAMS WORKED — the managers' decisions, handoffs, stalls and
//      gates of the current run, live while it goes.

const ACTIVITY_POLL_MS = 5000;

export default function TeamsView() {
  const toast = useToast();
  const confirm = useConfirm();
  const navigate = useNavigate();
  const ctx = useContext(AppContext);

  const [library, setLibrary] = useState<TeamsLibrary | null>(null);
  // The org chart comes from the shared board store: one copy for the whole
  // app, refreshed by the stream, rather than this page's own 5 s poll.
  const board = useBoardStore();
  const squads = board.squads;
  const refreshChart = board.refresh;
  const [skills, setSkills] = useState<Skill[]>([]);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [editing, setEditing] = useState<TeamSpec | null>(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const [saving, setSaving] = useState(false);

  const [pinned, setPinned] = useState<string[]>([]);
  const [probeQuery, setProbeQuery] = useState('');
  const [preselect, setPreselect] = useState<TeamPreselect | null>(null);
  const [composition, setComposition] = useState<DynamicComposition | null>(null);
  const [probing, setProbing] = useState(false);
  const [activating, setActivating] = useState(false);
  const [starting, setStarting] = useState(false);

  const [activity, setActivity] = useState<TeamActivityData | null>(null);
  const [activityLoading, setActivityLoading] = useState(false);

  // A run in flight: from the shared stream when the page has it, else from
  // the library payload. Edits are refused server-side while it goes, so the
  // page says so rather than letting a save fail with a 409.
  const running = ctx?.liveRunning ?? library?.running ?? false;

  const load = useCallback(async () => {
    try {
      // Both, together: the page is only coherent when the library and the
      // chart it feeds are from the same moment, so the store is asked to
      // re-read the chart alongside the library.
      // Skills are best-effort: the editor still composes a team without the
      // picker, it just cannot offer what is installed.
      const [lib, sk] = await Promise.all([
        getTeams(),
        getSkills().catch(() => [] as Skill[]),
        refreshChart(),
      ]);
      setLibrary(lib);
      setSkills(sk);
      // The saved pin is the starting point, not an override: a user
      // mid-selection must not have their unsaved choices replaced by a reload.
      setPinned((prev) => (prev.length ? prev : (lib.pinned ?? [])));
      setLoadError(null);
    } catch (err) {
      setLoadError(err instanceof ApiError ? err.displayMessage : 'Could not load the teams.');
    }
  }, [refreshChart]);

  const loadActivity = useCallback(async () => {
    setActivityLoading(true);
    try {
      setActivity(await getTeamActivity());
    } catch {
      // The connection badge already reports API trouble; the activity panel
      // simply stays as it was.
    } finally {
      setActivityLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
    void loadActivity();
  }, [load, loadActivity]);

  // While a run goes, the activity grows with every manager decision: poll it
  // slowly. The org chart rides the board store, which the stream keeps
  // current. Once the run stops, one last fetch catches the tail.
  const wasRunning = useRef(running);
  useEffect(() => {
    if (!running) {
      if (wasRunning.current) {
        void load();
        void loadActivity();
      }
      wasRunning.current = false;
      return;
    }
    wasRunning.current = true;
    const id = window.setInterval(() => {
      void loadActivity();
    }, ACTIVITY_POLL_MS);
    return () => window.clearInterval(id);
  }, [running, load, loadActivity]);

  const teams = library?.teams ?? [];
  const agents = library?.agents ?? [];
  const managers = library?.managers ?? [];
  const defaultManager = library?.default_manager || 'triage';

  const runProbe = async () => {
    const q = probeQuery.trim();
    if (!q) return;
    setProbing(true);
    try {
      // The composition preview is best-effort and rides alongside: the team
      // answer is the one that must not fail.
      // Both against the SAME pins, or the two panels describe two different
      // runs: the page's pins are local until Activate saves them.
      const [sel, comp] = await Promise.all([
        preselectTeams(q, pinned),
        previewComposition(q, pinned).then((r) => r.composition).catch(() => null),
      ]);
      setPreselect(sel);
      setComposition(comp);
    } catch (err) {
      toast.reportError(err, 'Could not preselect teams');
    } finally {
      setProbing(false);
    }
  };

  const togglePin = (id: string) => {
    setPinned((prev) => (prev.includes(id) ? prev.filter((p) => p !== id) : [...prev, id]));
    // The preview is now stale: it was computed against a different pin set, and
    // a stale preview is worse than none because it reads as current.
    setPreselect(null);
    setComposition(null);
  };

  const saveTeam = async (team: TeamSpec) => {
    setSaving(true);
    try {
      const existing = teams.some((t) => t.id === team.id);
      // A builtin is edited into a project override, which is a PUT to the same
      // id rather than a create — POST would collide with the builtin's id.
      const saved = existing ? await updateTeam(team.id, team) : await createTeam(team);
      toast.success(`Team ${saved.name || saved.id} saved`);
      setEditorOpen(false);
      setEditing(null);
      await load();
    } catch (err) {
      toast.reportError(err, 'Could not save the team');
    } finally {
      setSaving(false);
    }
  };

  const removeTeam = async (team: TeamSpec) => {
    const ok = await confirm({
      title: `Delete ${team.name || team.id}?`,
      description: team.source === 'builtin'
        ? 'This is a builtin and cannot be deleted.'
        : 'The team file is removed. Any pipeline or run that names it will report the id as unknown and run with one fewer team.',
      confirmLabel: 'Delete',
    });
    if (!ok) return;
    try {
      await deleteTeam(team.id);
      toast.success(`Deleted ${team.id}`);
      setPinned((prev) => prev.filter((p) => p !== team.id));
      await load();
    } catch (err) {
      toast.reportError(err, 'Could not delete the team');
    }
  };

  const duplicate = (team: TeamSpec) => {
    // A copy has to differ in the two things that must be unique: its id, and
    // its territory. Ownership is deliberately left EMPTY rather than copied —
    // a duplicate claiming the original's paths can never be selected alongside
    // it, and the user would have to discover that from a refusal.
    setEditing({
      ...team,
      id: nextFreeID(`${team.id}-copy`, teams.map((t) => t.id)),
      name: team.name ? `${team.name} (copy)` : '',
      owns: [],
      source: undefined,
      path: undefined,
      builtin: false,
      effective_manager: undefined,
      manager_default: undefined,
    });
    setEditorOpen(true);
  };

  // A dedicated manager for one team: the server writes a triage-capable
  // agent seeded with the team's charter and points the team at it. Returns
  // the manager id so an open editor can adopt it into its draft.
  const giveManager = async (teamID: string): Promise<string | null> => {
    try {
      const res = await createTeamManager(teamID);
      toast.success(
        res.created ? `${res.manager} created` : `${res.manager} already existed`,
        `${teamID} now answers to its own project manager.`,
      );
      await load();
      return res.manager;
    } catch (err) {
      toast.reportError(err, 'Could not create the manager');
      return null;
    }
  };

  const activate = async (ids: string[]) => {
    setActivating(true);
    try {
      const res = await activateTeams(ids, probeQuery.trim() || undefined);
      toast.success(res.summary || 'Teams activated', 'Pinned — the next run keeps these teams.');
      for (const note of res.staffing ?? []) toast.info(note);
      // Dropped rather than refused, so it has to be said out loud: activating
      // two of the three teams you asked for is a near-miss nobody notices
      // until the run is short a team.
      if ((res.unknown ?? []).length > 0) {
        toast.info(
          `Not in the library, so not activated: ${res.unknown!.join(', ')}`,
        );
      }
      // The server is now the source of truth for the pin, so adopt what it
      // saved rather than keeping the local guess that produced it.
      setPinned(res.teams ?? ids);
      setPreselect(null);
      setComposition(null);
      await load();
    } catch (err) {
      if (err instanceof ApiError && err.problems.length > 0) {
        toast.reportError(err, 'These teams cannot run together');
      } else {
        toast.reportError(err, 'Could not activate the teams');
      }
    } finally {
      setActivating(false);
    }
  };

  // "Send this request to these teams." The run starts with exactly these
  // teams pinned for its duration, and the page hands over to the Live view,
  // which owns the stream. The pin is run-scoped on the server — restored
  // when the run ends — so a one-off choice never governs every later run.
  const run = async (ids: string[]) => {
    const q = probeQuery.trim();
    if (!q || running) return;
    setStarting(true);
    try {
      ctx?.resetLiveEvents();
      await startRun({ query: q, teams: ids.length > 0 ? ids : undefined, skills: ctx?.config?.pinned_skills });
      ctx?.setLiveRunning(true);
      toast.success(
        ids.length > 0 ? `Sent to ${ids.join(', ')}` : 'Run started',
        ids.length > 0 ? 'These teams are pinned for this run only.' : 'The library picks the teams from the request.',
      );
      navigate('/');
    } catch (err) {
      toast.reportError(err, 'Could not start the run');
    } finally {
      setStarting(false);
    }
  };

  if (loadError) {
    return (
      <Shell>
        <p className="text-sm text-red-600 dark:text-red-400">{loadError}</p>
      </Shell>
    );
  }
  if (!library) {
    return (
      <Shell>
        <p className="text-sm text-gray-400">Loading teams…</p>
      </Shell>
    );
  }

  const hasChart = !!squads?.ok && (squads.squads ?? []).length > 0;

  return (
    <Shell>
      <header className="mb-4 flex flex-wrap items-center gap-2">
        <Users size={16} className="shrink-0 text-brand-500" aria-hidden="true" />
        <h1 className="text-base font-bold text-gray-900 dark:text-gray-100">Teams</h1>
        <nav aria-label="Sections" className="ml-auto flex flex-wrap gap-1 text-[11px]">
          {[
            ['#team-library', 'Library'],
            ['#team-composer', 'Send a request'],
            ['#team-chart', 'Org chart'],
            ['#team-activity', 'Activity'],
          ].map(([href, label]) => (
            <a key={href} href={href} className="btn-ghost focus-ring h-7 px-2">
              {label}
            </a>
          ))}
        </nav>
      </header>

      {running && (
        <p className="mb-3 flex items-start gap-1.5 rounded-md bg-brand-50 px-2.5 py-1.5 text-[11px] text-brand-800 dark:bg-brand-950/40 dark:text-brand-200">
          <Info size={12} className="mt-0.5 shrink-0" aria-hidden="true" />
          A run is in flight. The library is read-only until it ends; the org chart and the activity
          below update as the teams work.
        </p>
      )}
      {library.squads_enabled === false && (
        <p className="mb-3 flex items-start gap-1.5 rounded-md bg-amber-50 px-2.5 py-1.5 text-[11px] text-amber-800 dark:bg-amber-950/40 dark:text-amber-200">
          <Info size={12} className="mt-0.5 shrink-0" aria-hidden="true" />
          Teams are turned off for this project (<code className="font-mono">squads: false</code>).
          You can still author them here, and a single pinned team still staffs a run; two or more
          will not run in parallel until it is turned back on in Settings.
        </p>
      )}
      {library.library_enabled === false && (
        <p className="mb-3 flex items-start gap-1.5 rounded-md bg-amber-50 px-2.5 py-1.5 text-[11px] text-amber-800 dark:bg-amber-950/40 dark:text-amber-200">
          <Info size={12} className="mt-0.5 shrink-0" aria-hidden="true" />
          The library is not used to preselect teams for a run
          (<code className="font-mono">team_library: false</code>) — the manager agent assembles them
          from scratch instead. Everything here still works; runs just will not read it.
        </p>
      )}

      <div className="space-y-8">
        <div id="team-library">
          <TeamLibrary
            teams={teams}
            pinned={pinned}
            pipelineTeams={library.pipeline_teams ?? []}
            preselect={preselect}
            defaultManager={defaultManager}
            readOnly={running}
            onTogglePin={togglePin}
            onCreate={() => {
              setEditing(null);
              setEditorOpen(true);
            }}
            onEdit={(t) => {
              setEditing(t);
              setEditorOpen(true);
            }}
            onDuplicate={duplicate}
            onDelete={(t) => void removeTeam(t)}
            onGiveManager={(t) => void giveManager(t.id)}
          />
        </div>

        <div id="team-composer">
          <TeamComposer
            teams={teams}
            pinned={pinned}
            pipelineTeams={library.pipeline_teams ?? []}
            query={probeQuery}
            preselect={preselect}
            composition={composition}
            probing={probing}
            activating={activating}
            starting={starting}
            running={running}
            squadsEnabled={library.squads_enabled !== false}
            dynamicEnabled={library.dynamic_enabled !== false}
            defaultManager={defaultManager}
            onQueryChange={setProbeQuery}
            onProbe={() => void runProbe()}
            onTogglePin={togglePin}
            onActivate={(ids) => void activate(ids)}
            onRun={(ids) => void run(ids)}
          />
        </div>

        <div id="team-chart">
          {hasChart ? (
            <ActiveTeams
              view={squads!}
              library={teams}
              agents={agents}
              managers={managers}
              skills={skills}
              onSaved={() => void load()}
            />
          ) : (
            <section className="rounded-lg border border-dashed border-gray-300 px-6 py-8 text-center dark:border-gray-700">
              <h2 className="text-sm font-semibold text-gray-700 dark:text-gray-200">
                No org chart yet
              </h2>
              <p className="mx-auto mt-1 max-w-xl text-xs text-gray-500 dark:text-gray-400">
                Pick two or more teams above and Activate them, or just send a request — the library
                preselects the teams a request involves from its wording and the files in the
                workspace, with no model call. A single-domain request runs as one stream, staffed by
                the one team that matched.
              </p>
            </section>
          )}
        </div>

        <div id="team-activity">
          <TeamActivity
            activity={activity}
            loading={activityLoading}
            running={running}
            onRefresh={() => void loadActivity()}
          />
        </div>
      </div>

      <TeamEditor
        open={editorOpen}
        team={editing}
        agents={agents}
        managers={managers}
        skills={skills}
        defaultManager={defaultManager}
        takenIds={teams.map((t) => t.id)}
        saving={saving}
        onCancel={() => {
          setEditorOpen(false);
          setEditing(null);
        }}
        onSave={(t) => void saveTeam(t)}
        onCreateManager={giveManager}
      />
    </Shell>
  );
}

function Shell({ children }: { children: ReactNode }) {
  return <div className="mx-auto h-full w-full max-w-[120rem] overflow-auto p-4 2xl:p-8">{children}</div>;
}
