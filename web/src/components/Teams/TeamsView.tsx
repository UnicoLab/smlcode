import { useCallback, useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { Users, Info } from 'lucide-react';
import {
  getSquads,
  getTeams,
  createTeam,
  updateTeam,
  deleteTeam,
  preselectTeams,
  activateTeams,
  getSkills,
  ApiError,
} from '@/api/client';
import { useToast } from '@/components/ui/Toast';
import { useConfirm } from '@/components/ui/Modal';
import TeamLibrary from './TeamLibrary';
import TeamEditor from './TeamEditor';
import { nextFreeID } from './teamId';
import ActiveTeams from './ActiveTeams';
import type { Skill, SquadsView, TeamPreselect, TeamSpec, TeamsLibrary } from '@/types';

// ── The Teams page ───────────────────────────────────────────────────────
//
// This page used to show one thing — the teams of the CURRENT RUN — which meant
// it was empty whenever nothing was running, which is nearly always. It was
// reporting on something with the lifetime of a single run.
//
// It now shows both halves, in the order the decisions are made in:
//
//   1. the LIBRARY — teams the user authored, composed from existing agents.
//      Create, edit, duplicate, delete, pin. Try a request against it and see
//      which teams it would get AND why, before starting anything.
//   2. the ORG CHART — what this project currently runs with, editable down to
//      the frozen contract, present whether or not a run is in flight.

export default function TeamsView() {
  const toast = useToast();
  const confirm = useConfirm();

  const [library, setLibrary] = useState<TeamsLibrary | null>(null);
  const [squads, setSquads] = useState<SquadsView | null>(null);
  const [skills, setSkills] = useState<Skill[]>([]);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [editing, setEditing] = useState<TeamSpec | null>(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const [saving, setSaving] = useState(false);

  const [pinned, setPinned] = useState<string[]>([]);
  const [probeQuery, setProbeQuery] = useState('');
  const [preselect, setPreselect] = useState<TeamPreselect | null>(null);
  const [probing, setProbing] = useState(false);
  const [activating, setActivating] = useState(false);

  const load = useCallback(async () => {
    try {
      // Both, together: the page is only coherent when the library and the
      // chart it feeds are from the same moment. Squads is allowed to fail —
      // "no org chart yet" is the normal state, not an error.
      // Skills are best-effort: the editor still composes a team without the
      // picker, it just cannot offer what is installed.
      const [lib, sq, sk] = await Promise.all([
        getTeams(),
        getSquads().catch(() => null),
        getSkills().catch(() => [] as Skill[]),
      ]);
      setLibrary(lib);
      setSquads(sq);
      setSkills(sk);
      // The saved pin is the starting point, not an override: a user
      // mid-selection must not have their unsaved choices replaced by a reload.
      setPinned((prev) => (prev.length ? prev : (lib.pinned ?? [])));
      setLoadError(null);
    } catch (err) {
      setLoadError(err instanceof ApiError ? err.displayMessage : 'Could not load the teams.');
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const teams = library?.teams ?? [];
  const agents = library?.agents ?? [];
  const managers = library?.managers ?? [];

  const runProbe = async () => {
    const q = probeQuery.trim();
    if (!q) return;
    setProbing(true);
    try {
      setPreselect(await preselectTeams(q, pinned));
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
    });
    setEditorOpen(true);
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
      </header>

      {library.squads_enabled === false && (
        <p className="mb-3 flex items-start gap-1.5 rounded-md bg-amber-50 px-2.5 py-1.5 text-[11px] text-amber-800 dark:bg-amber-950/40 dark:text-amber-200">
          <Info size={12} className="mt-0.5 shrink-0" aria-hidden="true" />
          Teams are turned off for this project (<code className="font-mono">squads: false</code>).
          You can still author them here; runs will use a single stream until it is turned back on
          in Settings.
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

      <div className="space-y-6">
        <TeamLibrary
          teams={teams}
          pinned={pinned}
          pipelineTeams={library.pipeline_teams ?? []}
          preselect={preselect}
          probeQuery={probeQuery}
          probing={probing}
          activating={activating}
          onProbeQueryChange={setProbeQuery}
          onProbe={() => void runProbe()}
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
          onActivate={(ids) => void activate(ids)}
        />

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
              Pick two or more teams above and Activate them, or just start a run — the library
              preselects the teams a request involves from its wording and the files in the
              workspace, with no model call. A single-domain request runs as one stream, which is
              most of them.
            </p>
          </section>
        )}
      </div>

      <TeamEditor
        open={editorOpen}
        team={editing}
        agents={agents}
        managers={managers}
        skills={skills}
        takenIds={teams.map((t) => t.id)}
        saving={saving}
        onCancel={() => {
          setEditorOpen(false);
          setEditing(null);
        }}
        onSave={(t) => void saveTeam(t)}
      />
    </Shell>
  );
}

function Shell({ children }: { children: ReactNode }) {
  return <div className="mx-auto h-full w-full max-w-[120rem] overflow-auto p-4 2xl:p-8">{children}</div>;
}
