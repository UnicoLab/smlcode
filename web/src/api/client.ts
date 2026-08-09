// ── SLMCode API Client ──
// Typed fetch wrapper aligned with Go backend pkg/server/server.go
// All request/response shapes match backend handlers exactly.

import type {
  Config,
  ConfigPatch,
  Health,
  ModelsResponse,
  Board,
  Column,
  Task,
  RunRequest,
  StartRunResponse,
  LatestRunResponse,
  RunEvent,
  Skill,
  AgentSpec,
  PipelineView,
  PipelineConfig,
  DocItem,
  ArchiveItem,
  ArchiveView,
  QuerySession,
  QueryView,
  AppStatus,
  ClarifyAsk,
  PlanAsk,
  ContinueAsk,
  EscalateAsk,
  ShellAsk,
  StackApplyResponse,
  StacksResponse,
  AuthStatus,
} from '@/types';

const BASE = '/api';

async function request<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const url = `${BASE}${path}`;
  const res = await fetch(url, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...options.headers,
    },
    credentials: 'same-origin',
  });

  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText);
    throw new Error(`${res.status}: ${text || res.statusText}`);
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

// GET /api/runs/latest → {running, result, events}
export async function getLatestRun(): Promise<LatestRunResponse> {
  return request<LatestRunResponse>('/runs/latest');
}

// SSE stream — use EventSource directly, not fetch
export function createEventSource(): EventSource {
  return new EventSource(`${BASE}/events`);
}

// ── Pipeline ──
// GET /api/pipeline → {config, anchors?, defaults?}
export async function getPipeline(): Promise<PipelineView> {
  return request<PipelineView>('/pipeline');
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

// Clarify: GET /api/clarify/pending → {pending, ask: ClarifyAsk} | {pending: false}
//          POST /api/clarify/answer → body {answers: [{question_id, selected}, …]} | {use_all_recommended: true}
export async function getClarifyPending(): Promise<{ pending: boolean; ask?: ClarifyAsk }> {
  return request('/clarify/pending');
}

export async function answerClarify(
  answers: { question_id: string; selected: string[] }[],
): Promise<{ ok: string }> {
  return request('/clarify/answer', {
    method: 'POST',
    body: JSON.stringify({ answers }),
  });
}

export async function clarifyUseRecommended(): Promise<{ ok: string }> {
  return request('/clarify/answer', {
    method: 'POST',
    body: JSON.stringify({ use_all_recommended: true }),
  });
}

// Plan: GET /api/plan/pending → {pending, ask: PlanAsk} | {pending: false}
//       POST /api/plan/approve → body {decision: "approve"|"replan"}
export async function getPlanPending(): Promise<{ pending: boolean; ask?: PlanAsk }> {
  return request('/plan/pending');
}

export async function approvePlan(decision: 'approve' | 'replan'): Promise<{ ok: string }> {
  return request('/plan/approve', {
    method: 'POST',
    body: JSON.stringify({ decision }),
  });
}

// Continue: GET /api/continue/pending → {pending, ask: ContinueAsk} | {pending: false}
//           POST /api/continue/answer → body {action: "continue"|"stop"|"flag_only"}
export async function getContinuePending(): Promise<{ pending: boolean; ask?: ContinueAsk }> {
  return request('/continue/pending');
}

export async function answerContinue(action: 'continue' | 'stop' | 'flag_only'): Promise<{ ok: string }> {
  return request('/continue/answer', {
    method: 'POST',
    body: JSON.stringify({ action }),
  });
}

// Escalate: GET /api/escalate/pending → {pending, ask: EscalateAsk} | {pending: false}
//           POST /api/escalate/answer → body {action: "retry"|"re_scope"|"mark_done"|"abort"}
export async function getEscalatePending(): Promise<{ pending: boolean; ask?: EscalateAsk }> {
  return request('/escalate/pending');
}

export async function answerEscalate(
  action: 'retry' | 're_scope' | 'mark_done' | 'abort',
): Promise<{ ok: string }> {
  return request('/escalate/answer', {
    method: 'POST',
    body: JSON.stringify({ action }),
  });
}

// Shell: GET /api/shell/pending → {pending, ask: ShellAsk} | {pending: false}
//        POST /api/shell/approve → body {decision: "approve"|"deny"}
export async function getShellPending(): Promise<{ pending: boolean; ask?: ShellAsk }> {
  return request('/shell/pending');
}

export async function approveShell(decision: 'approve' | 'deny'): Promise<{ ok: string }> {
  return request('/shell/approve', {
    method: 'POST',
    body: JSON.stringify({ decision }),
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
