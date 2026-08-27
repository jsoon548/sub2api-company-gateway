import { apiClient } from '../client'

export type WorkSessionReliability = 'reliable' | 'unreliable'
export type ModelTier = 'economy' | 'general' | 'advanced'

export interface WorkSessionStatus {
  mode: string
  schema_ready: boolean
  reliable_ready: boolean
  auto_capability_ready: boolean
  reason_code: string
  tenant_id?: string
  hmac_key_version?: string
  auto_complexity: InternalInferenceProfileStatus
}

export interface InternalInferenceProfileStatus {
  profile: string
  state: 'ready' | 'degraded'
  ready: boolean
  reason_code: string
  backend?: string
  provider?: string
  model?: string
  prompt_version?: string
  schema_version?: string
}

export interface AutoConfig {
  enabled: boolean
  user_whitelist: number[]
  group_whitelist: number[]
  config_version: number
}

export interface ModelCatalogEntry {
  id: string
  generation: number
  logical_model: string
  provider_model: string
  tier: ModelTier
  capabilities: string[]
  valid_from: string
  valid_until?: string
  emergency_disabled: boolean
}

export interface AutoCandidate {
  id: string
  generation: number
  tier: ModelTier
  position: number
  catalog_entry_id: string
  logical_model: string
  valid_from: string
  valid_until?: string
}

export interface WorkSessionRecord {
  id: string
  tenant_id: string
  employee_user_id: number
  profile_version: string
  signal_source: string
  signal_status: string
  hmac_key_version?: string
  reliability: WorkSessionReliability
  routing_mode: 'explicit' | 'auto'
  config_version: number
  analysis_eligible: boolean
  quota_grace_eligible: boolean
  status: 'active' | 'request_scoped'
  selected_logical_model?: string
  selected_tier?: ModelTier
  selected_complexity?: TaskComplexity
  required_capabilities: string[]
  routing_version: number
  first_gateway_request_id: string
  last_gateway_request_id: string
  created_at: string
  last_activity_at: string
}

export type TaskComplexity = 'simple' | 'general' | 'complex'

export interface RouteCandidateEvaluation {
  tier: ModelTier
  position: number
  logical_model: string
  required_capabilities: string[]
  candidate_capabilities: string[]
  status: string
  schedulable_accounts: number
}

export interface GatewayInferenceRun {
  id: string
  purpose: string
  profile: string
  backend: string
  provider: string
  model: string
  prompt_version: string
  schema_version: string
  status: string
  provider_request_id?: string
  input_units?: number
  output_units?: number
  latency_ms: number
  created_at: string
}

export interface RouteDecision {
  id: string
  gateway_request_id: string
  work_session_id: string
  employee_user_id: number
  profile_version: string
  config_version: number
  required_capabilities: string[]
  task_complexity: TaskComplexity
  certainty: 'deterministic' | 'decisive' | 'uncertain'
  explanation: string
  decision_source: 'rule' | 'classifier' | 'fallback'
  rule_version: string
  classifier_run_id?: string
  classifier_version?: string
  classifier_status: string
  classifier_latency_ms: number
  requested_tier: ModelTier
  effective_tier: ModelTier
  candidate_pool: RouteCandidateEvaluation[]
  actual_logical_model?: string
  actual_provider_model?: string
  change_reason: string
  technical_retry_count: number
  technical_retry_reason?: string
  decision_result: 'selected' | 'unavailable' | 'failed'
  routing_latency_ms: number
  audit_linked: boolean
  usage_linked: boolean
  inference_run?: GatewayInferenceRun
  created_at: string
  updated_at: string
}

export interface AutoRoutingMetrics {
  decision_count: number
  classifier_call_count: number
  classifier_timeout_count: number
  classifier_fallback_count: number
  classifier_p95_latency_ms: number
  routing_p95_latency_ms: number
}

export interface WorkSessionConfigVersion {
  config_version: number
  current: boolean
  created_at?: string
  session_count: number
  reliable_session_count: number
  request_scoped_session_count: number
  model_count: number
  candidate_count: number
  auto_snapshot_status: 'current' | 'not_recorded'
  auto?: AutoConfig
  catalog: ModelCatalogEntry[]
  candidate_pools: AutoCandidate[]
}

export interface WorkSessionManagementState {
  status: WorkSessionStatus
  auto: AutoConfig
  catalog: ModelCatalogEntry[]
  candidate_pools: AutoCandidate[]
  config_versions: WorkSessionConfigVersion[]
  recent_sessions: WorkSessionRecord[]
  route_decisions: RouteDecision[]
  routing_metrics: AutoRoutingMetrics
}

export interface ModelCatalogInput {
  logical_model: string
  provider_model: string
  tier: ModelTier
  capabilities: string[]
  valid_from?: string
  valid_until?: string
}

export interface AutoCandidatePoolInput {
  tier: ModelTier
  candidates: string[]
  valid_from?: string
  valid_until?: string
}

export interface WorkSessionManagementUpdate {
  auto_enabled: boolean
  user_whitelist: number[]
  group_whitelist: number[]
  catalog: ModelCatalogInput[]
  candidate_pools: AutoCandidatePoolInput[]
}

async function getManagementState(): Promise<WorkSessionManagementState> {
  const { data } = await apiClient.get<WorkSessionManagementState>('/admin/work-session-auto')
  return data
}

async function replaceManagementConfig(input: WorkSessionManagementUpdate): Promise<WorkSessionManagementState> {
  const { data } = await apiClient.put<WorkSessionManagementState>('/admin/work-session-auto/config', input)
  return data
}

async function setEmergencyDisabled(logicalModel: string, disabled: boolean): Promise<WorkSessionManagementState> {
  const { data } = await apiClient.put<WorkSessionManagementState>(
    `/admin/work-session-auto/models/${encodeURIComponent(logicalModel)}/emergency-disable`,
    { disabled }
  )
  return data
}

export const adminWorkSessionAPI = {
  get: getManagementState,
  replace: replaceManagementConfig,
  setEmergencyDisabled
}

export default adminWorkSessionAPI
