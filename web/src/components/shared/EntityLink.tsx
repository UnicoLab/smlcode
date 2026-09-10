import type { ReactNode, MouseEvent } from 'react';
import { Link, useInRouterContext } from 'react-router-dom';
import { Archive, Bot, FileCode, ListTodo, Users } from 'lucide-react';
import clsx from 'clsx';
import { ENTITY_LABEL, type EntityKind } from './labels';
import { entityHref } from './entityHref';

// ── One chip for every identifier ────────────────────────────────────────
//
// A task id in the log, an agent name in the ticker, a team on a card: they
// used to be inert text in five different styles. Each is now the same chip,
// and each goes somewhere:
//
//   task  → Live  ?task=ID     opens the ticket's dossier, flags it in Tasks
//   agent → Live  ?agent=ID    opens the person's dossier
//   team  → Teams ?team=ID
//   run   → Runs  ?run=ID
//   file  → Files ?file=path
//
// The Live page reads ?task/?agent on mount and writes its selection back, so
// a dossier survives navigation and a reload — and a link to one can be
// pasted.

export interface EntityLinkProps {
  kind: EntityKind;
  id: string;
  /** Shown instead of the id. */
  label?: ReactNode;
  /** Extra query parameters, e.g. the team a task belongs to. */
  params?: Record<string, string | undefined>;
  title?: string;
  className?: string;
  /** Drop the glyph; the surrounding text already says what this is. */
  bare?: boolean;
  /** Stop the click from reaching a card or row that is itself clickable. */
  stopPropagation?: boolean;
  onClick?: (e: MouseEvent<HTMLAnchorElement>) => void;
}

const TONE: Record<EntityKind, string> = {
  task: 'border-gray-200 bg-gray-50 text-gray-700 hover:border-gray-300 hover:bg-gray-100 dark:border-gray-700 dark:bg-gray-800/70 dark:text-gray-200 dark:hover:border-gray-600',
  agent: 'border-brand-200 bg-brand-50 text-brand-700 hover:border-brand-300 hover:bg-brand-100 dark:border-brand-800 dark:bg-brand-950/40 dark:text-brand-300 dark:hover:border-brand-700',
  team: 'border-teal-200 bg-teal-50 text-teal-800 hover:border-teal-300 hover:bg-teal-100 dark:border-teal-800 dark:bg-teal-950/40 dark:text-teal-200 dark:hover:border-teal-700',
  run: 'border-sky-200 bg-sky-50 text-sky-700 hover:border-sky-300 hover:bg-sky-100 dark:border-sky-800 dark:bg-sky-950/40 dark:text-sky-300 dark:hover:border-sky-700',
  file: 'border-violet-200 bg-violet-50 text-violet-700 hover:border-violet-300 hover:bg-violet-100 dark:border-violet-800 dark:bg-violet-950/40 dark:text-violet-300 dark:hover:border-violet-700',
};

const GLYPH: Record<EntityKind, ReactNode> = {
  task: <ListTodo size={10} aria-hidden="true" />,
  agent: <Bot size={10} aria-hidden="true" />,
  team: <Users size={10} aria-hidden="true" />,
  run: <Archive size={10} aria-hidden="true" />,
  file: <FileCode size={10} aria-hidden="true" />,
};

export default function EntityLink({ kind, id, label, params, title, className, bare, stopPropagation, onClick }: EntityLinkProps) {
  // Rendered inside the router in the app, but the components that use it are
  // also rendered bare in tests and could be embedded anywhere: a plain anchor
  // to the same path is the honest fallback, not a crash.
  const inRouter = useInRouterContext();
  const href = entityHref(kind, id, params);
  const classes = clsx(
    'focus-ring inline-flex max-w-full items-center gap-1 rounded border px-1.5 py-px font-mono text-[10px] font-semibold leading-4 no-underline transition-colors',
    TONE[kind],
    className,
  );
  const text = (
    <>
      {!bare && GLYPH[kind]}
      <span className="truncate">{label ?? id}</span>
    </>
  );
  const tip = title ?? `${ENTITY_LABEL[kind]} ${id} — open`;
  const handleClick = (e: MouseEvent<HTMLAnchorElement>) => {
    if (stopPropagation) e.stopPropagation();
    onClick?.(e);
  };
  if (!inRouter) {
    return (
      <a href={href} className={classes} title={tip} data-entity={kind} data-entity-id={id} onClick={handleClick}>
        {text}
      </a>
    );
  }
  return (
    <Link to={href} className={classes} title={tip} data-entity={kind} data-entity-id={id} onClick={handleClick}>
      {text}
    </Link>
  );
}
