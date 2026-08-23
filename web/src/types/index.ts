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
  dynamic_pipeline: boolean;
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
  shell_whitelist: boolean;
  dry_run: boolean;
  auto_approve: boolean;
  compact_mode: boolean;
  context_compact: boolean;
  react_compact: boolean;
  react_compact_at_percent: number;
  wave_snapshots: boolean;
  file_checkpoints: boolean;
  hooks_enabled: boolean;
  write_guard: boolean;
  read_before_edit: boolean;
  shell_write_guard: boolean;
  tool_guidance: boolean;
  knowledge_inject: boolean;
  quality_monitor: boolean;
  static_quality: boolean;
  thinking_budget: boolean;
  thinking_budget_tokens: number;
  finalize_warn: boolean;
  require_smoke: boolean;
  claims_gate: boolean;
  worker_critique: boolean;
  over_edit_guard: boolean;
  read_head_lines: number;
  auto_text_tools: boolean;
  clarify_mode: string;
  clarify_timeout?: number;
  clarify_timeout_sec?: number;
  plan_approve: string;
  plan_approve_timeout?: number;
  plan_approve_timeout_sec?: number;
  continue_ask: string;
  continue_ask_timeout?: number;
  continue_ask_timeout_sec?: number;
  escalate_ask: string;
  escalate_ask_timeout?: number;
  escalate_ask_timeout_sec?: number;
  shell_ask_timeout?: number;
  shell_ask_timeout_sec?: number;
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
  /** Highest SSE sequence number the server has emitted. */
  last_seq?: number;
  /** Whether a session token is required for /api/* calls. */
  auth?: boolean;
  /** Number of queued review-mode changes awaiting apply/reject. */
  pending?: number;
  permission?: string;
}

export interface ReadinessCheck {
  id: string;
  label: string;
  ok: boolean;
  severity: 'critical' | 'warning' | string;
  message: string;
  fix_label?: string;
  fix_hint?: string;
  fix_patch?: ConfigPatch;
  endpoint?: string;
  latency_ms?: number;
  details?: Record<string, unknown>;
}

export interface ModelProfile {
  context_limit: number;
  max_tokens: number;
  thinking_budget_tokens: number;
  skill_token_budget: number;
  knowledge_token_budget: number;
  temperature: number;
  max_turns: number;
}

export interface ReadinessReport {
  ok: boolean;
  score: number;
  status: string;
  provider: string;
  model: string;
  endpoint: string;
  backend: string;
  active_stack?: string;
  active_pack?: string;
  active_pipeline?: string;
  guards: Record<string, boolean>;
  model_profile: ModelProfile;
  checks: ReadinessCheck[];
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
  level?: 'info' | 'warning' | 'error' | 'success' | 'problem' | string;
  message: string;
  task_id?: string;
  agent?: string;
  scope?: string;
  output?: string;
  data?: DynamicComposition;
  model?: string;
  tokens?: number;
  cost_usd?: number;
  time: string;
}

export interface RunEventCount {
  name: string;
  count: number;
}

export interface RunInsight {
  severity: 'info' | 'warning' | 'error' | 'success' | 'problem' | string;
  title: string;
  detail?: string;
  phase?: string;
  task_id?: string;
  agent?: string;
  time?: string;
}

export interface RunAction {
  title: string;
  detail?: string;
  command?: string;
}

export interface RunEventSummary {
  total_events: number;
  started_at?: string;
  last_at?: string;
  duration_ms?: number;
  final_phase?: string;
  final_kind?: string;
  last_message?: string;
  phases?: RunEventCount[];
  agents?: RunEventCount[];
  models?: RunEventCount[];
  tasks: number;
  retries: number;
  replans: number;
  failures: number;
  warnings: number;
  errors: number;
  tool_calls: number;
  shell_calls: number;
  tokens?: number;
  cost_usd?: number;
  insights?: RunInsight[];
  actions?: RunAction[];
}

export interface DynamicPhaseChoice {
  id: string;
  agent?: string;
  enabled: boolean;
  when?: string;
}

export interface DynamicExecuteChoice {
  default_role?: string;
  reviewer?: string;
  corrector?: string;
  max_waves?: number;
}

export interface DynamicTeamMember {
  role: string;
  skills?: string[];
}

export interface DynamicComposition {
  summary: string;
  strategy?: string;
  handoff?: string[];
  slm_fit?: string[];
  phases?: DynamicPhaseChoice[] | null;
  execute?: DynamicExecuteChoice | null;
  team?: DynamicTeamMember[];
  slots?: Slot[];
}

export interface CompositionPreviewResponse {
  ok: boolean;
  dynamic_enabled: boolean;
  composition: DynamicComposition;
  slm_fit?: string[];
}

export interface CompositionGetResponse {
  ok: boolean;
  composition: DynamicComposition | null;
  composition_error?: string;
}

// ── Run Responses ──
export interface StartRunResponse {
  status: string;
  query?: string;
  id?: string;
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
  /** event_seqs[i] is the SSE id of events[i] — used to seed Last-Event-ID. */
  event_seqs?: number[];
  last_seq?: number;
}

export interface InterruptedRun {
  id: string;
  query: string;
  updated_at: string;
  phase?: string;
  resume_from?: string;
  tasks: number;
  done: number;
  blocked: number;
  react_resume: boolean;
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
  interrupted?: boolean;
  phase?: string;
  resume_from?: string;
}

export interface QueryView {
  id: string;
  query: string;
  success: boolean;
  updated_at: string;
  interrupted?: boolean;
  phase?: string;
  resume_from?: string;
  summary_md: string;
  plan_md: string;
  tasks_md: string;
  board: Board;
  summary: string;
  composition?: DynamicComposition | null;
  composition_error?: string;
}

export interface QueryEventsResponse {
  id: string;
  events: RunEvent[];
  summary?: RunEventSummary;
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
  running?: boolean;
  readiness?: ReadinessReport;
  composition?: DynamicComposition | null;
  composition_error?: string;
  plan_pending?: boolean;
}

// ── HITL (human-in-the-loop) types ──
export interface ClarifyQuestion {
  id: string;
  header: string;
  question: string;
  options: ClarifyOption[];
  multi_select?: boolean;
  allow_freeform?: boolean;
  recommended: string;
}

export interface ClarifyOption {
  label: string;
  description?: string;
  recommended?: boolean;
}

export interface ClarifyAsk {
  id?: string;
  kind?: string;
  query?: string;
  questions: ClarifyQuestion[];
  prd_draft?: {
    summary?: string;
    goals?: string[];
    non_goals?: string[];
    acceptance?: string[];
    constraints?: string[];
    language?: string;
    entrypoint?: string;
  };
  timeout_sec?: number;
  on_timeout?: string;
  created_at?: string;
}

export interface PlanAsk {
  id?: string;
  kind?: string;
  query?: string;
  summary: string;
  goals?: string[];
  assumptions?: string[];
  task_count: number;
  tasks: string[];
  task_details?: PlanApprovalTask[];
  composition?: DynamicComposition | null;
  validation?: PlanValidation;
  options?: string[];
  timeout_sec?: number;
  on_timeout?: string;
  created_at?: string;
}

export interface PlanApprovalTask {
  id: string;
  title: string;
  description?: string;
  role?: string;
  column?: string;
  priority?: number;
  depends_on?: string[];
  files?: string[];
  acceptance?: string;
}

export interface PlanValidation {
  ok: boolean;
  issues?: string[];
  hints?: string[];
  weak_task_ids?: string[];
}

export interface ContinueAsk {
  id?: string;
  kind?: string;
  query?: string;
  summary: string;
  reason: string;
  escalated?: string[];
  gaps?: string[];
  options?: string[];
  timeout_sec?: number;
  on_timeout?: string;
  created_at?: string;
}

export interface EscalateAsk {
  id?: string;
  task_id: string;
  title: string;
  role: string;
  files?: string[];
  detail: string;
  summary: string;
  options?: string[];
  timeout_sec: number;
  on_timeout?: string;
  kind: string;
  created_at?: string;
}

export interface ShellAsk {
  id?: string;
  kind?: string;
  command: string;
  task_id?: string;
  timeout_sec?: number;
  on_timeout?: string;
  created_at?: string;
}

export interface PendingResponse<T> {
  pending: boolean;
  ask?: T;
  expired?: boolean;
  answered?: boolean;
  stale?: boolean;
  message?: string;
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

// ── Block CRUD (create/edit/delete via Studio) ──
// A block as authored in YAML: shared meta fields + kind-specific `spec`.
// Mirrors the backend block schema (Meta inlined with `spec`).
export interface BlockMeta {
  api_version?: string;
  kind: string;
  id: string;
  name?: string;
  description?: string;
  version?: string;
  author?: string;
  license?: string;
  tags?: string[];
  language?: string;
  icon?: string;
  shareable?: boolean;
}

// Kind-specific spec payloads (loosely typed — mirrors backend schema).
export interface AgentBlockSpec {
  id?: string;
  title?: string;
  description?: string;
  system_prompt?: string;
  tools?: boolean;
  max_iter?: number;
  temperature?: number;
  max_tokens?: number;
  model?: string;
  provider?: string;
  endpoint?: string;
  skills?: string[];
}

export interface QualityCheckCmd {
  cmd: string;
  optional?: boolean;
  label?: string;
}

export interface QualityBlockSpec {
  detect?: { files?: string[]; extensions?: string[]; priority?: number };
  format?: QualityCheckCmd[];
  lint?: QualityCheckCmd[];
  typecheck?: QualityCheckCmd[];
  test?: QualityCheckCmd[];
  build?: QualityCheckCmd[];
  smoke?: string;
  qa_gate?: string;
  safe_prefixes?: string[];
  tester_hints?: string;
}

export interface PackBlockSpec {
  pipeline?: string;
  quality?: string;
  agents?: string[];
  skills?: string[];
  pin_skills?: boolean;
  override_tester?: string;
  override_worker?: string;
  defer_plan_approve?: boolean;
  defer_clarify?: boolean;
}

export interface BlockPayload extends BlockMeta {
  spec?: any; // AgentBlockSpec | PipelineConfig | QualityBlockSpec | PackBlockSpec
}

export interface BlockCrudResponse {
  ok: boolean;
  block?: BlockPayload;
  path?: string;
  error?: string;
}

// ── Live Feedback ──
// GET /api/feedback → current active feedback (injected into the next agent prompt)
// POST /api/feedback → {text} → {ok, text}
// DELETE /api/feedback → {ok: "true"}
export interface FeedbackState {
  text: string;
  set_at?: string;
}

export interface FeedbackResponse {
  ok: boolean;
  text?: string;
}

// ── Version update ──
export interface UpdateInfo {
  current: string;
  latest: string;
  update_available: boolean;
  release_url?: string;
  checked_at?: string;
  error?: string;
}

// ── Workspace tree ──

export interface WorkspaceEntry {
  name: string;
  path: string;
  is_dir: boolean;
  size?: number;
  /** True for dot-entries such as `.slmcode` or `.github`. */
  hidden?: boolean;
}

export interface WorkspaceTree {
  path: string;
  entries: WorkspaceEntry[];
  hidden_shown?: boolean;
  hidden_count?: number;
}

// ── Review queue (.slmcode/pending/*.patch.json) ──

export interface DiffOp {
  type: 'equal' | 'insert' | 'delete';
  old_line?: number;
  new_line?: number;
  text: string;
}

export interface DiffHunk {
  old_start: number;
  old_lines: number;
  new_start: number;
  new_lines: number;
  ops: DiffOp[];
}

export interface DiffStat {
  added: number;
  removed: number;
  binary?: boolean;
}

export interface PendingChange {
  id: string;
  path: string;
  kind: string;
  created_at?: string;
  exists: boolean;
  is_new: boolean;
  before: string;
  after: string;
  bytes: number;
  stat: DiffStat;
  hunks?: DiffHunk[];
  truncated?: boolean;
  error?: string;
}

export interface ReviewQueue {
  count: number;
  items: PendingChange[];
  dir: string;
  permission: string;
  stat: DiffStat;
}

/** Target selector for apply/reject — one id, several ids, or everything. */
export interface ReviewTarget {
  id?: string;
  ids?: string[];
  all?: boolean;
}

export interface ReviewFailure {
  id: string;
  path?: string;
  error: string;
}

export interface ReviewApplyResult {
  ok: boolean;
  applied: string[];
  failed: ReviewFailure[];
  remaining: number;
}

export interface ReviewRejectResult {
  ok: boolean;
  rejected: string[];
  failed: ReviewFailure[];
  remaining: number;
}

// ── Run trace ──

export interface TracePhase {
  phase: string;
  started_at?: string;
  ended_at?: string;
  duration_ms: number;
  events: number;
  tokens?: number;
  cost_usd?: number;
  agents?: string[];
  models?: string[];
  tools?: number;
  errors?: number;
  warnings?: number;
  message?: string;
}

export interface TraceTotals {
  duration_ms: number;
  events: number;
  tokens: number;
  cost_usd: number;
  phases: number;
  errors: number;
  warnings: number;
}

export interface RunTrace {
  id: string;
  query?: string;
  success?: boolean;
  updated_at?: string;
  interrupted?: boolean;
  phases: TracePhase[];
  totals: TraceTotals;
  summary?: RunEventSummary;
}

// ── Live stream ──

/** Connection state derived from EventSource.readyState plus the health poll. */
export type ConnectionState = 'connecting' | 'live' | 'reconnecting' | 'down';
