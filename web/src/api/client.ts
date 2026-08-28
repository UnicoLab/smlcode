// ── SLMCode API Client ──
// Typed fetch wrapper aligned with Go backend pkg/server/server.go
// All request/response shapes match backend handlers exactly.

import type {
  Config,
  ConfigPatch,
  Health,
  ReadinessReport,
  CalibrationView,
  ModelsResponse,
  Board,
  Column,
  Task,
  RunRequest,
  StartRunResponse,
  LatestRunResponse,
  InterruptedRun,
  RunEvent,
  Skill,
  AgentSpec,
  PipelineView,
  PipelineConfig,
  DynamicComposition,
  CompositionGetResponse,
  CompositionPreviewResponse,
  DocItem,
  ArchiveItem,
  ArchiveView,
  QuerySession,
  QueryView,
  QueryEventsResponse,
  AppStatus,
  ClarifyAsk,
  PlanAsk,
  ContinueAsk,
  EscalateAsk,
  ShellAsk,
  PendingResponse,
  StackApplyResponse,
  StacksResponse,
  AuthStatus,
  FeedbackState,
  FeedbackResponse,
  UpdateInfo,
  WorkspaceTree,
  ReviewQueue,
  ReviewTarget,
  ReviewApplyResult,
  ReviewRejectResult,
  PendingChange,
  RunTrace,
  PlanEdits,
  SquadsView,
} from '@/types';

import { planEditsEmpty } from '@/types';
import { authHeaders, withToken } from './session';

const BASE = '/api';

/**
 * ApiError preserves the HTTP status so callers can react to it — a 409 from
 * `PUT /api/config` means "a run is active", which used to vanish into a bare
 * `catch {}` and leave the user staring at an unchanged dropdown.
 */
export class ApiError extends Error {
  readonly status: number;
  readonly body: string;

  constructor(status: number, body: string, statusText: string) {
    super(`${status}: ${body || statusText}`);
    this.name = 'ApiError';
    this.status = status;
    this.body = body;
  }

  /** True when the server refused because a run is in progress. */
  get isConflict(): boolean {
    return this.status === 409;
  }

  /** True when the studio session token is missing or stale. */
  get isUnauthorized(): boolean {
    return this.status === 401;
  }

  /** A short, human-readable line suitable for a toast. */
  get displayMessage(): string {
    const body = this.body.trim();
    if (this.isUnauthorized) {
      return 'Studio session expired — reopen the URL printed by the CLI.';
    }
    if (this.status === 403) {
      return body || 'Rejected by the Studio security policy.';
    }
    return body || `Request failed (${this.status})`;
  }
}

/** Normalise any thrown value into a readable message. */
export function errorText(err: unknown, fallback = 'Request failed'): string {
  if (err instanceof ApiError) return err.displayMessage;
  if (err instanceof Error && err.message) return err.message;
  if (typeof err === 'string' && err) return err;
  return fallback;
}

async function request<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const url = `${BASE}${path}`;
  const res = await fetch(url, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...authHeaders(),
      ...options.headers,
    },
    credentials: 'same-origin',
  });

  if (!res.ok) {
    const text = await res.text().catch(() => '');
    throw new ApiError(res.status, text, res.statusText);
  }

  const contentType = res.headers.get('content-type');
  if (contentType && contentType.includes('application/json')) {
    return res.json();
  }
  return res.text() as unknown as T;
}

// ── Health ──
export async function getHealth(): Promise<Health> {
  return request<Health>('/health');
}

export async function getReadiness(opts?: { probe?: boolean }): Promise<ReadinessReport> {
  const qs = opts?.probe === false ? '' : '?probe=1';
  return request<ReadinessReport>(`/readiness${qs}`);
}

// Read-only by construction: the endpoint never probes, so a polling UI cannot
// spend a minute of GPU on a cold model. Measurement happens at studio startup
// and before a run.
export async function getCalibration(): Promise<CalibrationView> {
  return request<CalibrationView>('/calibration');
}

// ── Config ──
// Backend returns Config.Public() which is the full config with api_key redacted.
export async function getConfig(): Promise<Config> {
  return request<Config>('/config');
}

// Backend accepts config.Patch and returns Config.Public().
export async function updateConfig(patch: ConfigPatch): Promise<Config> {
  return request<Config>('/config', {
    method: 'PUT',
    body: JSON.stringify(patch),
  });
}

// ── Stacks ──
export async function getStacks(): Promise<StacksResponse> {
  return request<StacksResponse>('/stacks');
}

export async function applyStack(
  id: string,
  opts?: { apply_agent_defaults?: boolean; force_agents?: boolean; clear_agent_llm?: boolean },
): Promise<StackApplyResponse> {
  return request<StackApplyResponse>(`/stacks/${encodeURIComponent(id)}/apply`, {
    method: 'POST',
    body: JSON.stringify(opts || {}),
  });
}

// ── Models / Auth ──
export async function getModels(opts?: { q?: string; limit?: number }): Promise<ModelsResponse> {
  const params = new URLSearchParams();
  if (opts?.q) params.set('q', opts.q);
  if (opts?.limit) params.set('limit', String(opts.limit));
  const qs = params.toString();
  return request<ModelsResponse>(`/models${qs ? `?${qs}` : ''}`);
}

export async function getAuthStatus(): Promise<AuthStatus> {
  return request<AuthStatus>('/auth');
}

export async function putAuthKey(apiKey: string, provider?: string): Promise<{ ok: boolean; auth: AuthStatus }> {
  return request('/auth', {
    method: 'PUT',
    body: JSON.stringify({ api_key: apiKey, provider: provider || '' }),
  });
}

export async function getMCPStatus(): Promise<import('@/types').MCPStatus> {
  return request('/mcp');
}

export async function getConfigSchema(): Promise<{ fields: Array<{ key: string; type: string; label: string; group: string; enum?: string[]; patchable: boolean; description?: string }>; slash: string[] }> {
  return request('/config/schema');
}

// ── Board ──
// GET /api/board → {plan, tasks, columns, by_column}
export async function getBoard(): Promise<Board> {
  return request<Board>('/board');
}

// GET /api/columns → [{id, label}, …]
export async function getColumns(): Promise<Column[]> {
  return request<Column[]>('/columns');
}

// GET /api/tasks → same shape as board (list of tasks)
export async function getTasks(): Promise<Board> {
  return request<Board>('/tasks');
}

// PUT /api/tasks — body is the full board
export async function updateTasks(board: Board): Promise<Board> {
  return request<Board>('/tasks', {
    method: 'PUT',
    body: JSON.stringify(board),
  });
}

// POST /api/tasks → creates a task, returns it
export async function addTask(task: Partial<Task>): Promise<Task> {
  return request<Task>('/tasks', {
    method: 'POST',
    body: JSON.stringify(task),
  });
}

// PATCH /api/tasks/{id} → partial update, returns updated task
export async function patchTask(id: string, patch: Partial<Task>): Promise<Task> {
  return request<Task>(`/tasks/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: JSON.stringify(patch),
  });
}

// DELETE /api/tasks/{id} → {ok: "true"}
export async function deleteTask(id: string): Promise<{ ok: string }> {
  return request(`/tasks/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
}

// ── Docs ──
// GET /api/docs → ["CONTEXT.md", "PLAN.md", …]
export async function listDocs(): Promise<string[]> {
  return request<string[]>('/docs');
}

// GET /api/docs/{name} → {name, content}
export async function getDoc(name: string): Promise<DocItem> {
  return request<DocItem>(`/docs/${encodeURIComponent(name)}`);
}

// PUT /api/docs/{name} → body {content} → {ok: "true"}
export async function updateDoc(name: string, content: string): Promise<{ ok: string }> {
  return request(`/docs/${encodeURIComponent(name)}`, {
    method: 'PUT',
    body: JSON.stringify({ content }),
  });
}

// ── Skills ──
export async function getSkills(): Promise<Skill[]> {
  return request<Skill[]>('/skills');
}

// GET /api/skills/{name} → full skill with body
export async function getSkill(name: string): Promise<Skill> {
  return request<Skill>(`/skills/${encodeURIComponent(name)}`);
}

// POST /api/skills → create a skill
export async function createSkill(skill: {
  name: string;
  description?: string;
  agents?: string[];
  body?: string;
  user_invocable?: boolean;
}): Promise<Skill> {
  return request<Skill>('/skills', {
    method: 'POST',
    body: JSON.stringify(skill),
  });
}

// PUT /api/skills/{name} → update a skill
export async function updateSkill(name: string, skill: Partial<Skill>): Promise<Skill> {
  return request<Skill>(`/skills/${encodeURIComponent(name)}`, {
    method: 'PUT',
    body: JSON.stringify(skill),
  });
}

// DELETE /api/skills/{name}
export async function deleteSkill(name: string): Promise<{ ok: string }> {
  return request(`/skills/${encodeURIComponent(name)}`, {
    method: 'DELETE',
  });
}

// ── Agents ──
// GET /api/agents → list of agents (builtin + custom)
export async function getAgents(): Promise<AgentSpec[]> {
  return request<AgentSpec[]>('/agents');
}

// GET /api/agents/{id} → full agent spec
export async function getAgent(id: string): Promise<AgentSpec> {
  return request<AgentSpec>(`/agents/${encodeURIComponent(id)}`);
}

// POST /api/agents → create custom agent
export async function createAgent(agent: Partial<AgentSpec> & { id: string }): Promise<AgentSpec> {
  return request<AgentSpec>('/agents', {
    method: 'POST',
    body: JSON.stringify(agent),
  });
}

// PUT /api/agents/{id} → update agent (or create override)
export async function updateAgent(id: string, agent: Partial<AgentSpec>): Promise<AgentSpec> {
  return request<AgentSpec>(`/agents/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify(agent),
  });
}

// DELETE /api/agents/{id} → delete custom agent or reset override
export async function deleteAgent(id: string): Promise<{ ok: string }> {
  return request(`/agents/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
}

// ── Runs ──
// POST /api/runs → body {query, mode?, specialist?, skills?} → {status, query}
export async function startRun(req: RunRequest): Promise<StartRunResponse> {
  return request<StartRunResponse>('/runs', {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

// POST /api/runs/stop → {ok: "true"}
export async function stopRun(): Promise<{ ok: string }> {
  return request<{ ok: string }>('/runs/stop', { method: 'POST' });
}

// GET /api/runs/interrupted → resumable interrupted turns, newest first
export async function getInterruptedRuns(): Promise<InterruptedRun[]> {
  return request<InterruptedRun[]>('/runs/interrupted');
}

// POST /api/runs/resume → resume latest or selected interrupted turn
export async function resumeRun(id?: string): Promise<StartRunResponse> {
  return request<StartRunResponse>('/runs/resume', {
    method: 'POST',
    body: JSON.stringify({ id: id || '' }),
  });
}

// GET /api/runs/latest → {running, result, events}
export async function getLatestRun(): Promise<LatestRunResponse> {
  return request<LatestRunResponse>('/runs/latest');
}

// SSE stream — use EventSource directly, not fetch.
// EventSource is same-origin, so the HttpOnly session cookie authenticates it;
// withToken() only appends a `t=` parameter in the cookie-less fallback,
// because EventSource cannot set headers. `lastEventId` asks the server to
// replay only what was missed.
export function createEventSource(lastEventId?: number): EventSource {
  let url = `${BASE}/events`;
  if (lastEventId && lastEventId > 0) {
    url += `?last_event_id=${encodeURIComponent(String(lastEventId))}`;
  }
  return new EventSource(withToken(url));
}

// ── Pipeline ──
// GET /api/pipeline → {config, anchors?, defaults?}
export async function getPipeline(): Promise<PipelineView> {
  return request<PipelineView>('/pipeline');
}

// GET /api/composition → latest persisted dynamic composition, when available
export async function getComposition(): Promise<CompositionGetResponse> {
  return request<CompositionGetResponse>('/composition');
}

export async function previewComposition(query: string): Promise<CompositionPreviewResponse> {
  return request<CompositionPreviewResponse>('/composition/preview', {
    method: 'POST',
    body: JSON.stringify({ query }),
  });
}

// PUT /api/pipeline → body {config} → updated PipelineView
export async function updatePipeline(config: PipelineConfig): Promise<PipelineView> {
  return request<PipelineView>('/pipeline', {
    method: 'PUT',
    body: JSON.stringify({ config }),
  });
}

// POST /api/pipeline/reset → reset to defaults
export async function resetPipeline(): Promise<PipelineView> {
  return request<PipelineView>('/pipeline/reset', { method: 'POST' });
}

// ── Archives ──
// GET /api/archives → [{name, size, modified}, …]
export async function getArchives(): Promise<ArchiveItem[]> {
  return request<ArchiveItem[]>('/archives');
}

// GET /api/archives/{name} → {name, content}
export async function getArchive(name: string): Promise<ArchiveView> {
  return request<ArchiveView>(`/archives/${encodeURIComponent(name)}`);
}

// ── Queries (sessions) ──
// GET /api/queries → [{id, query, success, summary, updated_at}, …]
export async function getQueries(): Promise<QuerySession[]> {
  return request<QuerySession[]>('/queries');
}

// GET /api/queries/{id} → full query view with summary_md, plan_md, tasks_md, board
export async function getQuery(id: string): Promise<QueryView> {
  return request<QueryView>(`/queries/${encodeURIComponent(id)}`);
}

// GET /api/queries/{id}/events → archived query event log
export async function getQueryEvents(id: string, limit = 1000): Promise<QueryEventsResponse> {
  const params = new URLSearchParams();
  if (limit > 0) params.set('limit', String(limit));
  return request<QueryEventsResponse>(`/queries/${encodeURIComponent(id)}/events?${params.toString()}`);
}

// ── Status ──
// GET /api/status → {text}
export async function getStatus(): Promise<AppStatus> {
  return request<AppStatus>('/status');
}

// ── Rewind ──
export async function getRewindList(): Promise<{ names: string[] }> {
  return request('/rewind');
}

// POST /api/rewind/{id} → restore a rewind snapshot
export async function restoreRewind(id: string): Promise<{ ok: string }> {
  return request(`/rewind/${encodeURIComponent(id)}`, { method: 'POST' });
}

// ── Compact ──
// POST /api/compact → trigger context compaction
export async function compact(): Promise<{ ok: string }> {
  return request('/compact', { method: 'POST' });
}

// ── HITL (human-in-the-loop) — aligned with backend handlers ──

// Clarify: GET /api/clarify/pending → {pending, ask: ClarifyAsk} | {pending: false, expired?: true}
//          POST /api/clarify/answer → body {ask_id, answers: [{question_id, selected}, …]} | {ask_id, use_all_recommended: true}
export async function getClarifyPending(): Promise<PendingResponse<ClarifyAsk>> {
  return request('/clarify/pending');
}

export async function answerClarify(
  answers: { question_id: string; selected: string[]; freeform?: string; comment?: string }[],
  notes?: string,
  askId?: string,
): Promise<{ ok: boolean }> {
  return request('/clarify/answer', {
    method: 'POST',
    body: JSON.stringify({ answers, notes: notes || undefined, ask_id: askId || undefined }),
  });
}

export async function clarifyUseRecommended(notes?: string, askId?: string): Promise<{ ok: boolean }> {
  return request('/clarify/answer', {
    method: 'POST',
    body: JSON.stringify({ use_all_recommended: true, notes: notes || undefined, ask_id: askId || undefined }),
  });
}

// Plan: GET /api/plan/pending → {pending, ask: PlanAsk} | {pending: false, expired?: true}
//       POST /api/plan/approve → body {ask_id, decision: "approve"|"replan"}
export async function getPlanPending(): Promise<PendingResponse<PlanAsk>> {
  return request('/plan/pending');
}

export async function approvePlan(
  decision: 'approve' | 'replan',
  notes?: string,
  askId?: string,
  edits?: PlanEdits,
): Promise<{ ok: boolean }> {
  // Edits only travel with an approval. A replan discards the board they refer
  // to, and the API rejects the pair rather than promising a change that is
  // about to be thrown away.
  const withEdits = decision === 'approve' && !planEditsEmpty(edits) ? edits : undefined;
  return request('/plan/approve', {
    method: 'POST',
    body: JSON.stringify({
      decision,
      notes: notes || undefined,
      ask_id: askId || undefined,
      edits: withEdits,
    }),
  });
}

// Continue: GET /api/continue/pending → {pending, ask: ContinueAsk} | {pending: false, expired?: true}
//           POST /api/continue/answer → body {ask_id, action: "continue"|"stop"|"flag_only"}
export async function getContinuePending(): Promise<PendingResponse<ContinueAsk>> {
  return request('/continue/pending');
}

export async function answerContinue(
  action: 'continue' | 'stop' | 'flag_only',
  askId?: string,
  notes?: string,
): Promise<{ ok: boolean }> {
  return request('/continue/answer', {
    method: 'POST',
    body: JSON.stringify({ action, ask_id: askId || undefined, notes: notes || undefined }),
  });
}

// Escalate: GET /api/escalate/pending → {pending, ask: EscalateAsk} | {pending: false, expired?: true}
//           POST /api/escalate/answer → body {ask_id, action: "retry"|"re_scope"|"mark_done"|"abort"}
export async function getEscalatePending(): Promise<PendingResponse<EscalateAsk>> {
  return request('/escalate/pending');
}

export async function answerEscalate(
  action: 'retry' | 're_scope' | 'mark_done' | 'abort',
  askId?: string,
  notes?: string,
): Promise<{ ok: boolean }> {
  return request('/escalate/answer', {
    method: 'POST',
    body: JSON.stringify({ action, ask_id: askId || undefined, notes: notes || undefined }),
  });
}

// Shell: GET /api/shell/pending → {pending, ask: ShellAsk} | {pending: false, expired?: true}
//        POST /api/shell/approve → body {ask_id, decision: "approve"|"deny"}
export async function getShellPending(): Promise<PendingResponse<ShellAsk>> {
  return request('/shell/pending');
}

export async function approveShell(decision: 'approve' | 'deny', askId?: string): Promise<{ ok: boolean }> {
  return request('/shell/approve', {
    method: 'POST',
    body: JSON.stringify({ decision, ask_id: askId || undefined }),
  });
}

// ── Blocks ──
import type { BlockCatalogEntry, BlockView, PackApplyResponse } from '@/types';

export async function getBlocks(kind?: string): Promise<{ blocks: BlockCatalogEntry[]; kind?: string } & BlockView> {
  const params = kind ? `?kind=${encodeURIComponent(kind)}` : '';
  return request(`/blocks${params}`);
}

export async function getBlock(kind: string, id: string): Promise<any> {
  return request(`/blocks/${encodeURIComponent(kind)}/${encodeURIComponent(id)}`);
}

export async function applyPack(id: string, opts?: { materialize_agents?: boolean; force_agents?: boolean }): Promise<PackApplyResponse> {
  return request(`/packs/${encodeURIComponent(id)}/apply`, {
    method: 'POST',
    body: JSON.stringify(opts || { materialize_agents: true }),
  });
}

export async function applyPipelinePreset(id: string): Promise<{ ok: boolean; result: { pipeline_id: string } }> {
  return request(`/pipeline-presets/${encodeURIComponent(id)}/apply`, { method: 'POST' });
}

// ── Block CRUD (Studio editing of pipelines/agents/quality/packs) ──
// POST /api/blocks/{kind} → create a custom block in .slmcode/blocks/
// PUT /api/blocks/{kind}/{id} → update (or create override of a builtin)
// DELETE /api/blocks/{kind}/{id} → delete a custom block (or reset override)
import type { BlockPayload, BlockCrudResponse } from '@/types';

export async function createBlock(kind: string, block: BlockPayload): Promise<BlockCrudResponse> {
  return request<BlockCrudResponse>(`/blocks/${encodeURIComponent(kind)}`, {
    method: 'POST',
    body: JSON.stringify(block),
  });
}

export async function updateBlock(kind: string, id: string, block: BlockPayload): Promise<BlockCrudResponse> {
  return request<BlockCrudResponse>(`/blocks/${encodeURIComponent(kind)}/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify(block),
  });
}

export async function deleteBlock(kind: string, id: string): Promise<{ ok: string }> {
  return request(`/blocks/${encodeURIComponent(kind)}/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
}

// ── Live Feedback ──
// GET /api/feedback → {text, set_at} — active feedback injected into the next agent prompt
// POST /api/feedback → body {text} → {ok, text}
// DELETE /api/feedback → {ok: "true"}
export async function getFeedback(): Promise<FeedbackState> {
  return request<FeedbackState>('/feedback');
}

export async function postFeedback(text: string): Promise<FeedbackResponse> {
  return request<FeedbackResponse>('/feedback', {
    method: 'POST',
    body: JSON.stringify({ text }),
  });
}

export async function clearFeedback(): Promise<{ ok: string }> {
  return request<{ ok: string }>('/feedback', { method: 'DELETE' });
}

// ── Workspace files ──
export async function getWorkspaceFile(path: string): Promise<{ path: string; content: string; size: number }> {
  return request(`/workspace/file?path=${encodeURIComponent(path)}`);
}

export async function getWorkspaceTree(
  path?: string,
  opts?: { hidden?: boolean },
): Promise<WorkspaceTree> {
  const params = new URLSearchParams();
  if (path) params.set('path', path);
  if (opts?.hidden === false) params.set('hidden', 'false');
  const qs = params.toString();
  return request(`/workspace/tree${qs ? `?${qs}` : ''}`);
}

// ── Review queue (permission mode "review") ──
// GET  /api/review/pending          → {count, items:[PendingChange], stat}
// GET  /api/review/pending/{id}     → PendingChange (always with hunks)
// POST /api/review/apply            → body {ids?|id?|all?} → {ok, applied, failed, remaining}
// POST /api/review/reject           → body {ids?|id?|all?} → {ok, rejected, failed, remaining}
export async function getPendingReview(opts?: { hunks?: boolean; context?: number }): Promise<ReviewQueue> {
  const params = new URLSearchParams();
  if (opts?.hunks === false) params.set('hunks', 'false');
  if (opts?.context !== undefined) params.set('context', String(opts.context));
  const qs = params.toString();
  return request<ReviewQueue>(`/review/pending${qs ? `?${qs}` : ''}`);
}

export async function getPendingChange(id: string, context = 3): Promise<PendingChange> {
  return request<PendingChange>(`/review/pending/${encodeURIComponent(id)}?context=${context}`);
}

export async function applyPendingChanges(target: ReviewTarget): Promise<ReviewApplyResult> {
  return request<ReviewApplyResult>('/review/apply', {
    method: 'POST',
    body: JSON.stringify(target),
  });
}

export async function rejectPendingChanges(target: ReviewTarget): Promise<ReviewRejectResult> {
  return request<ReviewRejectResult>('/review/reject', {
    method: 'POST',
    body: JSON.stringify(target),
  });
}

// ── Run trace ──
// GET /api/queries/{id}/trace → per-phase timings + token/cost attribution
export async function getQueryTrace(id: string): Promise<RunTrace> {
  return request<RunTrace>(`/queries/${encodeURIComponent(id)}/trace`);
}

// ── Version update ──
export async function getUpdateInfo(): Promise<UpdateInfo> {
  return request<UpdateInfo>('/update');
}

// Squads: GET /api/squads → the virtual-team org chart plus live progress.
// `ok:false` simply means this run is single-stream, which is most of them.
export async function getSquads(): Promise<SquadsView> {
  return request('/squads');
}
