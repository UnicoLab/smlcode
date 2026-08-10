// ── Pipeline editor helpers ──
// Pure, immutable helpers for the visual pipeline builder.
// Every function returns a NEW PipelineConfig — never mutates the input.

import type { PipelineConfig, PhaseSpec, GroupMeta, Slot } from '@/types';

/** A selectable agent (merged from /api/agents + agent blocks). */
export interface AgentOption {
  id: string;
  title: string;
}

export const WHEN_OPTIONS = ['always', 'auto', 'never'] as const;
export const PERSIST_OPTIONS = ['none', 'scratch', 'context', 'memory'] as const;
export const FAIL_MODE_OPTIONS = ['continue', 'abort'] as const;
export const SLOT_WHEN_PREFIX = 'query_matches:';

/** Normalized view of a phase — fills defaults so editing is safe. */
export function makePhase(id: string, group = ''): PhaseSpec {
  return { agent: '', when: 'auto', label: id, tip: '', group, enabled: true };
}

export function phaseOrDefault(config: PipelineConfig, id: string): PhaseSpec {
  const p = config.phases[id];
  return { ...makePhase(id, p?.group || ''), ...(p || {}) };
}

/**
 * Archived phases are user-deleted: kept in config.phases as when=never +
 * enabled=false, removed from groups and order. The backend Normalize() merges
 * missing default phase keys back in, so a hard delete can never stick —
 * archiving is the only way to truly remove a phase from the graph.
 */
export function isArchived(config: PipelineConfig, phaseId: string): boolean {
  const p = config.phases[phaseId];
  if (!p) return true; // not in phases at all → treat as archived
  if (p.when === 'never' && p.enabled === false) {
    const inGroup = config.groups.some((g) => g.steps.includes(phaseId));
    return !inGroup;
  }
  return false;
}

/** Ids of archived (user-deleted) phases, in order-position order. */
export function archivedIds(config: PipelineConfig): string[] {
  const orderPos = new Map<string, number>(config.order.map((id, i) => [id, i]));
  return Object.keys(config.phases)
    .filter((id) => isArchived(config, id))
    .sort((a, b) => (orderPos.get(a) ?? 1e9) - (orderPos.get(b) ?? 1e9));
}

/**
 * Keep `order` in sync: flatten groups[].steps in group order (preserving
 * per-group phase order), then append orphan phases (phases not referenced
 * by any group step), preserving their existing relative order. Archived
 * (user-deleted) phases are excluded.
 */
export function syncOrder(config: PipelineConfig): string[] {
  const order: string[] = [];
  const seen = new Set<string>();
  for (const g of config.groups) {
    for (const step of g.steps) {
      if (!seen.has(step)) {
        seen.add(step);
        order.push(step);
      }
    }
  }
  const orderPos = new Map<string, number>(config.order.map((id, i) => [id, i]));
  const orphans = Object.keys(config.phases)
    .filter((id) => !seen.has(id) && !isArchived(config, id))
    .sort((a, b) => (orderPos.get(a) ?? 1e9) - (orderPos.get(b) ?? 1e9));
  return [...order, ...orphans];
}

/** Ids of phases present in config.phases but not in any group's steps. */
export function orphanIds(config: PipelineConfig): string[] {
  const inGroups = new Set<string>();
  for (const g of config.groups) for (const s of g.steps) inGroups.add(s);
  const orderPos = new Map<string, number>(config.order.map((id, i) => [id, i]));
  return Object.keys(config.phases)
    .filter((id) => !inGroups.has(id) && !isArchived(config, id))
    .sort((a, b) => (orderPos.get(a) ?? 1e9) - (orderPos.get(b) ?? 1e9));
}

/** The group id whose steps contain the phase, or null. */
export function groupOf(groups: GroupMeta[], phaseId: string): string | null {
  for (const g of groups) {
    if (g.steps.includes(phaseId)) return g.id;
  }
  return null;
}

/** Apply syncOrder to a config (call after any structural change). */
export function normalizeConfig(config: PipelineConfig): PipelineConfig {
  return { ...config, order: syncOrder(config) };
}

/** Move a whole group up (dir=-1) or down (dir=+1). */
export function reorderGroups(config: PipelineConfig, index: number, dir: -1 | 1): PipelineConfig {
  const target = index + dir;
  if (index < 0 || target < 0 || target >= config.groups.length) return config;
  const groups = [...config.groups];
  const [g] = groups.splice(index, 1);
  groups.splice(target, 0, g);
  return normalizeConfig({ ...config, groups });
}

/** Move a phase up (dir=-1) or down (dir=+1) inside its group. */
export function movePhaseInGroup(
  config: PipelineConfig,
  groupId: string,
  fromIndex: number,
  dir: -1 | 1,
): PipelineConfig {
  const groups = config.groups.map((g) => {
    if (g.id !== groupId) return g;
    const target = fromIndex + dir;
    if (fromIndex < 0 || target < 0 || target >= g.steps.length) return g;
    const steps = [...g.steps];
    const [step] = steps.splice(fromIndex, 1);
    steps.splice(target, 0, step);
    return { ...g, steps };
  });
  return normalizeConfig({ ...config, groups });
}

/** Add a phase to a group. Returns null when id is empty/duplicate. */
export function addPhase(config: PipelineConfig, groupId: string, id: string): PipelineConfig | null {
  const clean = id.trim();
  if (!clean || config.phases[clean]) return null;
  const phases = { ...config.phases, [clean]: makePhase(clean, groupId) };
  const groups = config.groups.map((g) =>
    g.id === groupId ? { ...g, steps: [...g.steps, clean] } : g,
  );
  return normalizeConfig({ ...config, phases, groups });
}

/**
 * Delete a phase: archive it (when=never, enabled=false) and remove it from
 * every group's steps + order. It stays in config.phases so the backend
 * Normalize() does not resurrect it from defaults; it shows in the
 * "Archived" section where it can be restored.
 */
export function removePhase(config: PipelineConfig, phaseId: string): PipelineConfig {
  const phases = { ...config.phases };
  const cur = phases[phaseId];
  if (cur) {
    phases[phaseId] = { ...cur, when: 'never', enabled: false, group: '' };
  }
  const groups = config.groups.map((g) => ({
    ...g,
    steps: g.steps.filter((s) => s !== phaseId),
  }));
  return normalizeConfig({ ...config, phases, groups });
}

/** Restore an archived phase back into the graph (prefers its old group). */
export function restorePhase(config: PipelineConfig, phaseId: string): PipelineConfig {
  const phases = { ...config.phases };
  const cur = phases[phaseId];
  if (!cur) return config;
  const targetGroup = config.groups.find((g) => g.id === cur.group)?.id || config.groups[0]?.id || '';
  phases[phaseId] = { ...cur, when: 'auto', enabled: true, group: targetGroup };
  const groups = config.groups.map((g) =>
    g.id === targetGroup && !g.steps.includes(phaseId) ? { ...g, steps: [...g.steps, phaseId] } : g,
  );
  return normalizeConfig({ ...config, phases, groups });
}

/**
 * Assign a phase to a group (or null to orphan it). Updates group steps,
 * the phase.group field, and re-syncs order.
 */
export function movePhaseToGroup(
  config: PipelineConfig,
  phaseId: string,
  toGroup: string | null,
): PipelineConfig {
  const phases = { ...config.phases };
  if (phases[phaseId]) {
    phases[phaseId] = { ...phases[phaseId], group: toGroup || '' };
  }
  const groups = config.groups.map((g) => {
    const has = g.steps.includes(phaseId);
    if (g.id === toGroup) {
      return has ? g : { ...g, steps: [...g.steps, phaseId] };
    }
    return has ? { ...g, steps: g.steps.filter((s) => s !== phaseId) } : g;
  });
  return normalizeConfig({ ...config, phases, groups });
}

export function updatePhase(
  config: PipelineConfig,
  phaseId: string,
  patch: Partial<PhaseSpec>,
): PipelineConfig {
  const phases = {
    ...config.phases,
    [phaseId]: { ...phaseOrDefault(config, phaseId), ...patch },
  };
  return { ...config, phases };
}

export function updateGroup(
  config: PipelineConfig,
  index: number,
  patch: Partial<{ label: string }>,
): PipelineConfig {
  const groups = config.groups.map((g, i) => (i === index ? { ...g, ...patch } : g));
  return { ...config, groups };
}

/**
 * Delete a group: removes it and its steps from `order`; phases stay in
 * config.phases (they surface in the Unassigned section).
 */
export function deleteGroup(config: PipelineConfig, index: number): PipelineConfig {
  const group = config.groups[index];
  if (!group) return config;
  const groups = config.groups.filter((_, i) => i !== index);
  const phases = { ...config.phases };
  for (const step of group.steps) {
    if (phases[step]) phases[step] = { ...phases[step], group: '' };
  }
  return normalizeConfig({ ...config, groups, phases });
}

export function makeSlot(): Slot {
  return {
    id: `slot-${Date.now()}`,
    agent: '',
    title: '',
    before: '',
    after: '',
    replace: '',
    when: 'always',
    input: '',
    fail_mode: 'continue',
    persist_to: 'none',
    enabled: true,
  };
}

export function addSlot(config: PipelineConfig): PipelineConfig {
  return { ...config, slots: [...(config.slots || []), makeSlot()] };
}

export function updateSlot(
  config: PipelineConfig,
  index: number,
  patch: Partial<Slot>,
): PipelineConfig {
  const slots = (config.slots || []).map((s, i) => (i === index ? { ...s, ...patch } : s));
  return { ...config, slots };
}

export function removeSlot(config: PipelineConfig, index: number): PipelineConfig {
  return { ...config, slots: (config.slots || []).filter((_, i) => i !== index) };
}

/** Merge /api/agents + agent blocks into one deduped, sorted list. */
export function mergeAgents(
  agents: Array<{ id: string; title?: string }>,
  blockAgents: any[],
): AgentOption[] {
  const map = new Map<string, AgentOption>();
  for (const a of agents) {
    if (!a?.id) continue;
    map.set(a.id, { id: a.id, title: a.title || a.id });
  }
  for (const b of blockAgents || []) {
    const spec = b?.spec;
    const id = spec?.id || b?.id;
    if (!id) continue;
    map.set(id, { id, title: spec?.title || b?.name || id });
  }
  return [...map.values()].sort((a, b) => a.id.localeCompare(b.id));
}
