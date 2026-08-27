import { apiClient } from '../client'
import type { AuditContentState, AuditOutcome, AuditProtocol } from './audit'

export type GatewayUsageResult = 'normal_usage' | 'no_usage' | 'audit_failed' | 'rejected_pre_upstream'
export type GatewayUsageGroup = 'time' | 'employee' | 'profile' | 'model' | 'result'

export interface GatewayUsageRecord {
  gateway_request_id: string
  audit_interaction_id?: string
  usage_log_id?: number
  usage_record_count: number
  audit_present: boolean
  usage_present: boolean
  result: GatewayUsageResult
  event_time: string
  subject_user_id?: number
  subject_email_snapshot?: string
  profile_version?: string
  protocol?: 'anthropic' | 'openai'
  transport?: 'http' | 'sse'
  requested_model?: string
  resolved_model?: string
  request_outcome?: AuditOutcome
  content_state?: AuditContentState
  account_id?: number
  input_tokens?: number
  output_tokens?: number
  total_tokens?: number
  total_cost?: number
  actual_cost?: number
  duration_ms?: number
  usage_created_at?: string
}

export interface GatewayUsageQuery {
  employee?: string
  profile?: string
  protocol?: AuditProtocol | ''
  model?: string
  result?: GatewayUsageResult | ''
  outcome?: AuditOutcome | ''
  content_state?: AuditContentState | ''
  from?: string
  to?: string
  gateway_request_id?: string
  page?: number
  page_size?: number
}

export interface GatewayUsagePage {
  items: GatewayUsageRecord[]
  total: number
  page: number
  page_size: number
}

export interface GatewayUsageAggregate {
  key: string
  requests: number
  usage_records: number
  input_tokens: number
  output_tokens: number
  total_cost: number
  actual_cost: number
}

export interface GatewayUsageTotals extends Omit<GatewayUsageAggregate, 'key'> {
  normal_usage_requests: number
  no_usage_requests: number
  audit_failed_requests: number
  rejected_pre_upstream_requests: number
}

export interface GatewayUsageSummary {
  group_by: GatewayUsageGroup
  totals: GatewayUsageTotals
  items: GatewayUsageAggregate[]
}

export async function listGatewayUsage(params: GatewayUsageQuery): Promise<GatewayUsagePage> {
  const { data } = await apiClient.get<GatewayUsagePage>('/admin/gateway-usage', { params })
  return data
}

export async function summarizeGatewayUsage(params: GatewayUsageQuery, groupBy: GatewayUsageGroup): Promise<GatewayUsageSummary> {
  const { data } = await apiClient.get<GatewayUsageSummary>('/admin/gateway-usage/summary', {
    params: { ...params, page: undefined, page_size: undefined, group_by: groupBy }
  })
  return data
}

export async function getGatewayUsage(gatewayRequestId: string): Promise<GatewayUsageRecord> {
  const { data } = await apiClient.get<GatewayUsageRecord>(`/admin/gateway-usage/${gatewayRequestId}`)
  return data
}

export const adminGatewayUsageAPI = {
  list: listGatewayUsage,
  summary: summarizeGatewayUsage,
  get: getGatewayUsage
}

export default adminGatewayUsageAPI
