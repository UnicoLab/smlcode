import { useState } from 'react';
import type { ReactNode } from 'react';
import { ChevronRight, Bot, Layers, AlertTriangle, Cpu } from 'lucide-react';
import type { AgentSpec, DynamicComposition } from '@/types';
import clsx from 'clsx';

/**
 * Everything about HOW a run is configured, behind one disclosure.
 *
 * These panels — the composition, the team, the handoff contract, SLM-fit
 * hints, recent agents — used to sit permanently expanded above the event log,
 * roughly 400px of them. They are genuinely useful, and they are useful at a
 * different moment than the log: you read them while deciding what to run, and
 * you stop reading them the instant the run starts and the log becomes the
 * thing worth watching.
 *
 * So the disclosure defaults to OPEN while idle and CLOSED while running, and
 * the summary line keeps the one fact you still want mid-run — which pipeline
 * shape is active — visible either way.
 */

function Section({
  title,
  children,
  accent,
}: {
  title: string;
  children: ReactNode;
  accent?: string;
}) {
  return (
    <div className="min-w-0">
      <div
        className={clsx(
          'mb-1.5 text-[10px] font-bold uppercase tracking-[0.12em]',
          accent ?? 'text-gray-400 dark:text-gray-500',
        )}
      >
        {title}
      </div>
      {children}
    </div>
  );
}

function Chip({ children, title }: { children: ReactNode; title?: string }) {
  return (
    <span
      title={title}
      className="inline-flex max-w-full items-center gap-1.5 rounded-md border border-gray-200 bg-white px-2 py-1 text-[11px] text-gray-700 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-200"
    >
      {children}
    </span>
  );
}

export default function RunSetup({
  composition,
  mode,
  fit,
  agents,
  running,
  compositionError,
}: {
  composition: DynamicComposition | null;
  mode: '' | 'runtime' | 'preview';
  fit: string[];
  agents: AgentSpec[];
  running: boolean;
  compositionError: string;
}) {
  // Three states, not two, because a preview and a real composition are not
  // the same thing:
  //
  //   preview            collapsed. This is a DETERMINISTIC GUESS made from the
  //                      query text while you are still typing it. Unfolding a
  //                      twelve-phase wall on the third keystroke is noise, and
  //                      worse, it reads as a decision that has been made.
  //   running            collapsed. You are watching the log.
  //   runtime, finished  open. This is what actually ran, and it is worth
  //                      reading.
  //
  // The user can override either way and their choice sticks for the session.
  const [open, setOpen] = useState(mode === 'runtime' && !running);

  if (!composition && !compositionError) return null;

  const phases = (composition?.phases ?? []).filter((p) => p.enabled && p.when !== 'never');
  const exec = composition?.execute;

  return (
    <div className="shrink-0 border-b border-gray-200 bg-gray-50/80 dark:border-gray-800 dark:bg-gray-900/40">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="focus-ring flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-gray-100/70 dark:hover:bg-gray-800/50 sm:px-4"
      >
        <ChevronRight
          size={14}
          className={clsx('shrink-0 text-gray-400 transition-transform', open && 'rotate-90')}
        />
        <Layers size={14} className="shrink-0 text-brand-500" />
        <span className="text-xs font-bold text-gray-700 dark:text-gray-200">
          {mode === 'preview' ? 'Provisional pipeline' : 'Active pipeline'}
        </span>
        {mode === 'preview' && (
          <span
            className="badge-neutral text-[10px]"
            title="A deterministic guess from your query text. The composer agent decides the real pipeline when the run starts."
          >
            guess
          </span>
        )}
        {composition?.complexity && (
          <span
            className="badge-brand text-[10px]"
            title="Budget class: how much of the run's budget this request is worth"
          >
            {composition.complexity}
            {composition.kind ? `:${composition.kind}` : ''}
          </span>
        )}
        <span className="hidden min-w-0 flex-1 truncate text-[11px] text-gray-500 dark:text-gray-400 sm:block">
          {composition?.summary}
        </span>
        <span className="ml-auto shrink-0 font-mono text-[10px] text-gray-400 sm:ml-0">
          {phases.length} phases
        </span>
      </button>

      {/* OUTSIDE the disclosure. An error is not optional detail: hiding it
          behind a collapsed panel means the one state the user has to act on is
          the one state they cannot see. */}
      {compositionError && (
        <div className="flex items-start gap-2 border-t border-amber-200 bg-amber-50 px-3 py-2 text-[11px] text-amber-900 dark:border-amber-900/70 dark:bg-amber-950/30 dark:text-amber-100 sm:px-4">
          <AlertTriangle size={13} className="mt-0.5 shrink-0" />
          <span className="min-w-0 break-words">
            Saved composition could not be read: {compositionError}
          </span>
        </div>
      )}

      {open && (
        <div className="space-y-3 border-t border-gray-200 px-3 py-3 dark:border-gray-800 sm:px-4">
          {mode === 'preview' && (
            <p className="text-[11px] text-gray-500 dark:text-gray-400">
              Assembled from your query text alone, without reading the repository. The composer
              agent picks the real phases, team and skills when the run starts — expect this to
              change.
            </p>
          )}

          {composition && (
            <div className="grid gap-3 lg:grid-cols-2 xl:grid-cols-3">
              <Section title="Phases">
                <div className="flex flex-wrap gap-1.5">
                  {phases.map((p, i) => (
                    <Chip key={`${p.id}-${i}`} title={p.when ? `${p.id} (${p.when})` : p.id}>
                      <span className="font-semibold">{p.id}</span>
                      {p.agent && <span className="text-gray-400">@{p.agent}</span>}
                    </Chip>
                  ))}
                </div>
              </Section>

              <Section title="Loop">
                <div className="flex flex-wrap gap-1.5">
                  <Chip title="Implements each task">worker: {exec?.default_role || 'worker'}</Chip>
                  <Chip title="Judges each task from disk evidence">
                    reviewer: {exec?.reviewer || 'reviewer'}
                  </Chip>
                  <Chip title="Fixes what the reviewer rejects">
                    corrector: {exec?.corrector || 'corrector'}
                  </Chip>
                  {exec?.max_waves ? <Chip title="Corrective wave budget">waves: {exec.max_waves}</Chip> : null}
                </div>
              </Section>

              {composition.team && composition.team.length > 0 && (
                <Section title="Team">
                  <div className="flex flex-wrap gap-1.5">
                    {composition.team.map((member) => {
                      const spec = agents.find((a) => a.id === member.role);
                      return (
                        <Chip
                          key={member.role}
                          title={`${spec?.title || member.role}${
                            member.skills?.length ? ` — skills: ${member.skills.join(', ')}` : ''
                          }`}
                        >
                          <Bot size={11} className="shrink-0 text-brand-500" />
                          <span className="truncate font-semibold">{spec?.title || member.role}</span>
                        </Chip>
                      );
                    })}
                  </div>
                </Section>
              )}

              {composition.handoff && composition.handoff.length > 0 && (
                <Section title="Handoff contract">
                  <ul className="space-y-1">
                    {composition.handoff.slice(0, 6).map((h, i) => (
                      <li
                        key={`${h}-${i}`}
                        className="flex gap-2 text-[11px] text-gray-600 dark:text-gray-300"
                      >
                        <span className="mt-1.5 h-1 w-1 shrink-0 rounded-full bg-gray-300 dark:bg-gray-600" />
                        <span className="min-w-0 break-words">{h}</span>
                      </li>
                    ))}
                  </ul>
                </Section>
              )}

              {fit.length > 0 && (
                <Section title="Small-model fit" accent="text-amber-600 dark:text-amber-400">
                  <ul className="space-y-1">
                    {fit.slice(0, 4).map((hint, i) => (
                      <li
                        key={`${hint}-${i}`}
                        className="flex gap-2 text-[11px] text-amber-800 dark:text-amber-200"
                      >
                        <AlertTriangle size={11} className="mt-0.5 shrink-0" />
                        <span className="min-w-0 break-words">{hint}</span>
                      </li>
                    ))}
                  </ul>
                </Section>
              )}

              {composition.slots && composition.slots.length > 0 && (
                <Section title="Slots">
                  <div className="flex flex-wrap gap-1.5">
                    {composition.slots.slice(0, 8).map((slot) => (
                      <Chip key={slot.id}>
                        <Cpu size={11} className="shrink-0 text-gray-400" />
                        <span className="font-semibold">{slot.id}</span>
                        <span className="text-gray-400">
                          {slot.before
                            ? `before ${slot.before}`
                            : slot.after
                              ? `after ${slot.after}`
                              : slot.replace
                                ? `replace ${slot.replace}`
                                : ''}
                        </span>
                      </Chip>
                    ))}
                  </div>
                </Section>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
