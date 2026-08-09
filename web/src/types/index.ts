// ── SLMCode Studio TypeScript Types ──
// Aligned with Go backend pkg/server/server.go response shapes

// ── Board / Plan ──
export interface Plan {
  summary: string;
  goals: string[];
  assumptions: string[];
  risks: string[];
  steps: string[];
  raw: string;
}

export interface Task {
  id: string;
  title: string;
  description: string;
  role: string;
  assignee: string;
  column: string;
  status: string;
  priority: number;
  depends_on: string[];
  files: string[];
  acceptance: string;
  checklist: ChecklistItem[];
  output: string;
  review: string;
  retries: number;
  error: string;
  updated_at: string;
  notes: string;
}

export interface ChecklistItem {
  id: string;
  text: string;
  done: boolean;
}

export interface Board {
  plan: Plan;
  tasks: Task[];
  columns: string[];
  by_column: Record<string, Task[]>;
}

export interface Column {
  id: string;
  label: string;
}

// ── Config ──
export interface Config {
  provider: string;
  endpoint: string;
  model: string;
  api_key?: string;
  backend: string;
  mode: string;
  specialist: string;
  pinned_skills: string[];
  temperature: number;
  max_tokens: number;
  max_retries: number;
  max_parallel: number;
  max_context_kb: number;
  think_passes: number;
  qa_gate: boolean;
  qa_gate_max_rounds: number;
  post_worker_smoke: boolean;
  permission: string;
  shell_permission: string;
  dry_run: boolean;
  auto_approve: boolean;
  compact_mode: boolean;
  context_compact: boolean;
  wave_snapshots: boolean;
  hooks_enabled: boolean;
  write_guard: boolean;
  read_before_edit: boolean;
  tool_guidance: boolean;
  knowledge_inject: boolean;
  quality_monitor: boolean;
  static_quality: boolean;
  thinking_budget: boolean;
  worker_critique: boolean;
  clarify_mode: string;
  plan_approve: string;
  continue_ask: string;
  escalate_ask: string;
  embedding_enabled: boolean;
  embedding_model: string;
  embedding_endpoint: string;
  embedding_top_k: number;
  price_preset: string;
  price_prompt_per_mtok: number;
  price_completion_per_mtok: number;
  active_stack?: string;
  active_pack?: string;
  active_pipeline?: string;
  qa_gate_command?: string;
  enabled_models?: string[];
  llm_retry_count?: number;
  llm_retry_delay_ms?: number;
  context_compact_engine?: string;
  react_compact?: boolean;
  session_event_log?: boolean;
  auto_refine?: boolean;
  auto_refine_max_rounds?: number;
  root: string;
  skills_dirs: string[];
  listen: string;
}

export type ConfigPatch = Partial<Config>;

// ── Health ──
export interface Health {
  ok: boolean;
  api: string;
  ui: string;
  provider: string;
  model: string;
  backend: string;
  root: string;
  running: boolean;
  events: number;
}

// ── Models / Auth ──
export interface ModelMatch {
  provider: string;
  id: string;
  name?: string;
  selector: string;
}

export interface AuthStatus {
  provider: string;
  configured: boolean;
  required: boolean;
  source?: string;
  env_key?: string;
  has_api_key: boolean;
  message?: string;
  auth_json?: Record<string, boolean>;
  auth_path?: string;
}

export interface ModelCost {
  provider: string;
  model: string;
  prompt_per_mtok: number;
  completion_per_mtok: number;
  source: string;
  known: boolean;
}

export interface ModelsResponse {
  models: string[];
  matches?: ModelMatch[];
  current: string;
  provider?: string;
  endpoint?: string;
  active_stack?: string;
  query?: string;
  auth?: AuthStatus;
  enabled_models?: string[];
  costs?: ModelCost[];
  error?: string;
}

export interface MCPStatus {
  enabled: boolean;
  meta_tool: string;
  pattern?: string;
  servers: Array<{
    name: string;
    connected: boolean;
    transport: string;
    read_only: boolean;
    tools?: string[];
    tool_count: number;
    error?: string;
  }>;
  total_tools: number;
  configured: number;
}

// ── SSE Events ──
export interface RunEvent {
  phase: string;
  kind: string;
  message: string;
  task_id?: string;
  agent?: string;
  scope?: string;
  output?: string;
  time: string;
}

// ── Run Responses ──
export interface StartRunResponse {
  status: string;
  query: string;
}

export interface OrchestratorResult {
  success: boolean;
  summary: string;
  duration: number;       // nanoseconds
  failed_tasks: number;
}

export interface LatestRunResponse {
  running: boolean;
  result: OrchestratorResult | null;
  events: RunEvent[];
}

// ── Run Request ──
export interface RunRequest {
  query: string;
  mode?: string;
  specialist?: string;
  skills?: string[];
}

// ── Pipeline ──
export interface PhaseSpec {
  agent: string;
  when: string;
  label: string;
  tip: string;
  group: string;
  enabled?: boolean;
}

export interface GroupMeta {
  id: string;
  label: string;
  steps: string[];
}

export interface Slot {
  id: string;
  agent: string;
  title?: string;
  before: string;
  after: string;
  replace: string;
  when: string;
  input?: string;
  fail_mode?: string;
  persist_to?: string;
  multipass?: boolean;
  enabled?: boolean;
}

export interface ExecuteLoop {
  default_role: string;
  reviewer: string;
  corrector: string;
  max_waves?: number;
}

export interface PipelineConfig {
  version: number;
  phases: Record<string, PhaseSpec>;
  order: string[];
  groups: GroupMeta[];
  execute: ExecuteLoop;
  slots: Slot[];
}

export interface PipelineView {
  config: PipelineConfig;
  anchors?: string[];
  defaults?: Record<string, string>;
}

// ── Agents ──
export interface AgentSpec {
  id: string;
  title?: string;
  role?: string;
  description?: string;
  system_prompt?: string;
  skills: string[];
  model: string;
  provider: string;
  endpoint: string;
  tools: boolean;
  max_iter: number;
  temperature: number;
  max_tokens: number;
  custom?: boolean;
  builtin?: boolean;
  override?: boolean;
  /** Resolved model after stack/global inheritance */
  effective_model?: string;
  effective_provider?: string;
  inherits_model?: boolean;
  inherits_provider?: boolean;
  active_stack?: string;
}

// ── Skills ──
export interface Skill {
  name: string;
  description: string;
  path: string;
  triggers: string[];
  agents: string[];
  user_invocable: boolean;
  body?: string;
}

// ── Docs ──
export interface DocItem {
  name: string;
  content: string;
}

// ── Archives ──
export interface ArchiveItem {
  name: string;
  size: number;
  modified: string;
}

export interface ArchiveView {
  name: string;
  content: string;
}

// ── Queries ──
export interface QuerySession {
  id: string;
  query: string;
  success: boolean;
  summary: string;
  updated_at: string;
}

export interface QueryView {
  id: string;
  query: string;
  summary_md: string;
  plan_md: string;
  tasks_md: string;
  board: Board;
  summary: string;
}

// ── Stack Presets ──
export interface StackAgentDefault {
  model?: string;
  provider?: string;
  endpoint?: string;
}

export interface StackPreset {
  id: string;
  label: string;
  description: string;
  icon: string;
  provider: string;
  endpoint: string;
  model: string;
  temperature: number;
  max_tokens: number;
  max_parallel: number;
  max_retries: number;
  max_context_kb: number;
  think_passes: number;
  backend: string;
  env_key?: string;
  color: string;
  active: boolean;
  agents?: Record<string, StackAgentDefault>;
}

export interface StacksResponse {
  stacks: StackPreset[];
  active_stack?: string;
  provider: string;
  model: string;
  endpoint: string;
}

export interface StackApplyResult {
  stack_id: string;
  provider: string;
  model: string;
  endpoint: string;
  agents_updated?: string[];
  agents_cleared?: string[];
  conflicting_agents?: string[];
}

export interface StackApplyResponse {
  ok: boolean;
  result: StackApplyResult;
  config: Config;
}

// ── Status ──
export interface AppStatus {
  text: string;
}

// ── HITL (human-in-the-loop) types ──
export interface ClarifyQuestion {
  id: string;
  header: string;
  question: string;
  options: ClarifyOption[];
  recommended: string;
}

export interface ClarifyOption {
  label: string;
  description?: string;
  recommended?: boolean;
}

export interface ClarifyAsk {
  questions: ClarifyQuestion[];
}

export interface PlanAsk {
  summary: string;
  task_count: number;
  tasks: string[];
}

export interface ContinueAsk {
  summary: string;
  reason: string;
  escalated: string[];
  gaps: string[];
}

export interface EscalateAsk {
  task_id: string;
  title: string;
  role: string;
  files: string[];
  detail: string;
  summary: string;
  timeout_sec: number;
  kind: string;
}

export interface ShellAsk {
  command: string;
  task_id: string;
}

// ── Blocks ──
export interface BlockCatalogEntry {
  api_version: string;
  kind: string;
  id: string;
  name: string;
  description?: string;
  version?: string;
  author?: string;
  license?: string;
  tags?: string[];
  language?: string;
  icon?: string;
  shareable?: boolean;
  source?: string;
  path?: string;
  builtin: boolean;
  custom: boolean;
}

export interface BlockView {
  blocks: BlockCatalogEntry[];
  packs: any[];
  pipelines: any[];
  agents: any[];
  quality: any[];
  active_pack?: string;
  active_pipeline?: string;
}

export interface PackApplyResult {
  pack_id: string;
  pipeline_id?: string;
  quality_id?: string;
  qa_gate_command?: string;
  agents_written?: string[];
  skills_pinned?: string[];
  pipeline_path?: string;
}

export interface PackApplyResponse {
  ok: boolean;
  result: PackApplyResult;
  config: Config;
  catalog?: BlockView;
}
