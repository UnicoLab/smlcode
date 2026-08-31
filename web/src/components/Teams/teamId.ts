/**
 * slugify mirrors the Go side's team-id rule, so the id the form shows is the
 * id that gets saved.
 *
 * If the two ever disagree the failure is silent and confusing: the user types
 * "Backend API", the form says it will create `backend-api`, and the file lands
 * somewhere else — after which "edit" and "delete" both address a team that is
 * not the one on screen.
 */
export function slugify(v: string): string {
  return v
    .toLowerCase()
    .trim()
    .replace(/[\s_]+/g, '-')
    .replace(/[^a-z0-9-]/g, '')
    .replace(/^-+|-+$/g, '');
}

/** nextFreeID finds an unused id for a duplicate rather than failing the save. */
export function nextFreeID(base: string, takenIds: string[]): string {
  const taken = new Set(takenIds);
  const root = slugify(base);
  if (!taken.has(root)) return root;
  for (let n = 2; n < 100; n++) {
    const candidate = `${root}-${n}`;
    if (!taken.has(candidate)) return candidate;
  }
  return root;
}
