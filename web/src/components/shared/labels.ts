import type { SeatKind, TicketState } from '@/components/Live/floor/floorModel';

// ── One vocabulary for the whole studio ──────────────────────────────────
//
// The board, the task rail, the floor and the dossier all name the same
// things, and they used to each carry their own copy of the words: the board
// said "In Progress" where the floor said "in progress", the flat floor and
// the 3D floor each declared the seat glyphs, and a ticket state had three
// label tables. This file is the one place those words live. The API field
// names are untouched — only what a person reads is unified.
//
// The nouns: a TEAM (never "squad" in the UI), its MANAGER, a TASK. The
// floor's own metaphor may call a task a "ticket" while it is lying on a
// table, but a link, a chip or a heading says Task.

/** What each kind of thing is called when it is named in the UI. */
export const ENTITY_LABEL = {
  task: 'Task',
  agent: 'Agent',
  team: 'Team',
  manager: 'Manager',
  run: 'Run',
  file: 'File',
} as const;

export type EntityKind = 'task' | 'agent' | 'team' | 'run' | 'file';

/** Board column id → its heading. */
export const COLUMN_LABELS: Record<string, string> = {
  to_scope: 'To Scope',
  scoped: 'Scoped',
  ready_to_dev: 'Ready',
  in_progress: 'In Progress',
  in_review: 'In Review',
  blocked: 'Blocked',
  done: 'Done',
};

/** The board's columns, left to right. Unknown columns the server adds go after. */
export const COLUMN_ORDER = ['to_scope', 'scoped', 'ready_to_dev', 'in_progress', 'in_review', 'blocked', 'done'];

export function columnLabel(column: string): string {
  return COLUMN_LABELS[column] || column;
}

/** The six states the floor folds a task's column and status into. */
export const TICKET_STATE_LABELS: Record<TicketState, string> = {
  queued: 'queued',
  working: 'in progress',
  review: 'in review',
  blocked: 'blocked',
  done: 'done',
  failed: 'failed',
};

/** The glyph for a seat at a team's table. */
export const SEAT_GLYPHS: Record<SeatKind, string> = {
  manager: '👔',
  worker: '🔧',
  reviewer: '👁️',
  tester: '🧪',
  member: '🤖',
};

/** The glyph for a pipeline role, matched on an agent id's suffix. */
export const ROLE_GLYPHS: Record<string, string> = {
  planner: '📋',
  splitter: '✂️',
  explorer: '🔍',
  architect: '🏗️',
  coordinator: '🎯',
  docs: '📖',
  memory: '💾',
  context: '📝',
  composer: '🎼',
  triage: '👔',
  corrector: '✏️',
  deep: '🧠',
  reviewer: '👁️',
  tester: '🧪',
  worker: '🔧',
};

/** The glyph for a phase or a phase agent on the pipeline's stage. */
export const STAGE_GLYPHS: Record<string, string> = {
  ...ROLE_GLYPHS,
  plan: '📋',
  split: '✂️',
  explore: '🔍',
  coord: '🎯',
  clarify: '💬',
  skills: '🧰',
  learn: '🎓',
  polish: '✨',
  qa: '🧪',
  test: '🧪',
};
