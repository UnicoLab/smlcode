import type { EntityKind } from './labels';

// Where each kind of thing lives in the studio, and the query parameter that
// names one. EntityLink renders these; anything that builds a URL by hand
// (a toast, a keyboard shortcut) should build it from here too.
const ROUTE: Record<EntityKind, { path: string; param: string }> = {
  task: { path: '/', param: 'task' },
  agent: { path: '/', param: 'agent' },
  team: { path: '/teams', param: 'team' },
  run: { path: '/runs', param: 'run' },
  file: { path: '/files', param: 'file' },
};

/** The in-app path for an entity, e.g. `/?task=T4`, with any extra parameters. */
export function entityHref(kind: EntityKind, id: string, params?: Record<string, string | undefined>): string {
  const route = ROUTE[kind];
  const qs = new URLSearchParams();
  qs.set(route.param, id);
  for (const [k, v] of Object.entries(params ?? {})) if (v) qs.set(k, v);
  return `${route.path}?${qs.toString()}`;
}
