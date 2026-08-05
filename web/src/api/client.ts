// ── SLMCode API Client ──
// Typed fetch wrapper for the Go backend at :7420

import type {
  Config,
  ConfigPatch,
  Health,
  ModelsResponse,
  Board,
  Column,
  Task,
  RunRequest,
  RunResult,
  Skill,
  AgentSpec,
  PipelineView,
  PipelineConfig,
  DocItem,
  ArchiveItem,
  QuerySession,
  AppStatus,
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
    throw new Error(`${res.status}: ${text}`);
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
export async function getConfig(): Promise<Config> {
  return request<Config>('/config');
}

export async function updateConfig(patch: ConfigPatch): Promise<Config> {
  return request<Config>('/config', {
    method: 'PUT',
    body: JSON.stringify(patch),
  });
}

// ── Models ──
export async function getModels(): Promise<ModelsResponse> {
  return request<ModelsResponse>('/models');
}

// ── Board ──
export async function getBoard(): Promise<Board> {
  return request<Board>('/board');
}

export async function getColumns(): Promise<Column[]> {
  return request<Column[]>('/columns');
}

export async function getTasks(): Promise<Board> {
  return request<Board>('/tasks');
}

export async function updateTasks(board: Board): Promise<Board> {
  return request<Board>('/tasks', {
    method: 'PUT',
    body: JSON.stringify(board),
  });
}

export async function addTask(task: Partial<Task>): Promise<Task> {
  return request<Task>('/tasks', {
    method: 'POST',
    body: JSON.stringify(task),
  });
}

export async function patchTask(id: string, patch: Partial<Task>): Promise<Task> {
  return request<Task>(`/tasks/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: JSON.stringify(patch),
  });
}

export async function deleteTask(id: string): Promise<{ ok: string }> {
  return request(`/tasks/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
}

// ── Docs ──
export async function listDocs(): Promise<string[]> {
  return request<string[]>('/docs');
}

export async function getDoc(name: string): Promise<DocItem> {
  return request<DocItem>(`/docs/${encodeURIComponent(name)}`);
}

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

export async function getSkill(name: string): Promise<Skill> {
  return request<Skill>(`/skills/${encodeURIComponent(name)}`);
}

export async function createSkill(skill: Partial<Skill>): Promise<Skill> {
  return request<Skill>('/skills', {
    method: 'POST',
    body: JSON.stringify(skill),
  });
}

export async function updateSkill(name: string, skill: Partial<Skill>): Promise<Skill> {
  return request<Skill>(`/skills/${encodeURIComponent(name)}`, {
    method: 'PUT',
    body: JSON.stringify(skill),
  });
}

export async function deleteSkill(name: string): Promise<{ ok: string; deleted: string }> {
  return request(`/skills/${encodeURIComponent(name)}`, {
    method: 'DELETE',
  });
}

// ── Agents ──
export async function getAgents(): Promise<AgentSpec[]> {
  return request<AgentSpec[]>('/agents');
}

export async function getAgent(id: string): Promise<AgentSpec> {
  return request<AgentSpec>(`/agents/${encodeURIComponent(id)}`);
}

export async function createAgent(agent: AgentSpec): Promise<AgentSpec> {
  return request<AgentSpec>('/agents', {
    method: 'POST',
    body: JSON.stringify(agent),
  });
}

export async function updateAgent(id: string, agent: AgentSpec): Promise<AgentSpec> {
  return request<AgentSpec>(`/agents/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify(agent),
  });
}

export async function deleteAgent(id: string): Promise<{ ok: string; deleted: string }> {
  return request(`/agents/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
}

// ── Runs ──
export async function startRun(req: RunRequest): Promise<RunResult> {
  return request<RunResult>('/runs', {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

export async function stopRun(): Promise<{ ok: string }> {
  return request('/runs/stop', { method: 'POST' });
}

export async function getLatestRun(): Promise<RunResult> {
  return request<RunResult>('/runs/latest');
}

// ── Pipeline ──
export async function getPipeline(): Promise<PipelineView> {
  return request<PipelineView>('/pipeline');
}

export async function updatePipeline(config: PipelineConfig): Promise<PipelineView> {
  return request<PipelineView>('/pipeline', {
    method: 'PUT',
    body: JSON.stringify({ config }),
  });
}

export async function resetPipeline(): Promise<PipelineView> {
  return request<PipelineView>('/pipeline/reset', { method: 'POST' });
}

// ── Archives ──
export async function getArchives(): Promise<ArchiveItem[]> {
  return request<ArchiveItem[]>('/archives');
}

export async function getArchive(name: string): Promise<Board> {
  return request<Board>(`/archives/${encodeURIComponent(name)}`);
}

// ── Queries (sessions) ──
export async function getQueries(): Promise<QuerySession[]> {
  return request<QuerySession[]>('/queries');
}

export async function getQuery(id: string): Promise<RunResult> {
  return request<RunResult>(`/queries/${encodeURIComponent(id)}`);
}

// ── Status ──
export async function getStatus(): Promise<AppStatus> {
  return request<AppStatus>('/status');
}

// ── Rewind ──
export async function getRewindList(): Promise<{ names: string[] }> {
  return request('/rewind');
}

export async function restoreRewind(id: string): Promise<{ ok: string }> {
  return request(`/rewind/${encodeURIComponent(id)}`, { method: 'POST' });
}

// ── Compact ──
export async function compact(): Promise<Record<string, unknown>> {
  return request('/compact', { method: 'POST' });
}

// ── HITL (human-in-the-loop) ──
export async function getClarifyPending(): Promise<{ id: string; message: string }> {
  return request('/clarify/pending');
}

export async function answerClarify(id: string, answer: string): Promise<{ ok: string }> {
  return request('/clarify/answer', {
    method: 'POST',
    body: JSON.stringify({ id, answer }),
  });
}

export async function getPlanPending(): Promise<{ id: string; message: string; plan: Record<string, unknown> }> {
  return request('/plan/pending');
}

export async function approvePlan(id: string, approved: boolean): Promise<{ ok: string }> {
  return request('/plan/approve', {
    method: 'POST',
    body: JSON.stringify({ id, approved }),
  });
}

export async function getContinuePending(): Promise<{ id: string; message: string }> {
  return request('/continue/pending');
}

export async function answerContinue(id: string, answer: string): Promise<{ ok: string }> {
  return request('/continue/answer', {
    method: 'POST',
    body: JSON.stringify({ id, answer }),
  });
}

export async function getEscalatePending(): Promise<{ id: string; message: string; task_id: string; detail: string }> {
  return request('/escalate/pending');
}

export async function answerEscalate(
  id: string,
  action: 'retry' | 're_scope' | 'mark_done' | 'abort',
): Promise<{ ok: string }> {
  return request('/escalate/answer', {
    method: 'POST',
    body: JSON.stringify({ id, action }),
  });
}

export async function getShellPending(): Promise<{ id: string; message: string; command: string }> {
  return request('/shell/pending');
}

export async function approveShell(
  id: string,
  approved: boolean,
): Promise<{ ok: string }> {
  return request('/shell/approve', {
    method: 'POST',
    body: JSON.stringify({ id, approved }),
  });
}
