import { apiClient } from '../client'

export type AuditProtocol = 'anthropic' | 'openai'
export type AuditOutcome = 'processing' | 'rejected_pre_upstream' | 'completed' | 'upstream_failed' | 'interrupted'
export type AuditContentState = 'recording' | 'complete' | 'incomplete' | 'expired'

export interface AuditMetadataRecord {
  id: string
  gateway_request_id: string
  subject_user_id?: number
  subject_email_snapshot?: string
  profile_version: string
  protocol: AuditProtocol
  endpoint: string
  method: string
  transport: 'http' | 'sse'
  requested_model?: string
  resolved_model?: string
  request_outcome: AuditOutcome
  content_state: AuditContentState
  downstream_status?: number
  downstream_write_result: string
  admitted_at: string
  completed_at?: string
  expires_at: string
  last_activity_at: string
  request_part_count: number
  response_part_count: number
  safe_error_summary?: string
}

export interface AuditMetadataQuery {
  employee?: string
  from?: string
  to?: string
  protocol?: AuditProtocol | ''
  model?: string
  outcome?: AuditOutcome | ''
  content_state?: AuditContentState | ''
  gateway_request_id?: string
  page?: number
  page_size?: number
}

export interface AuditMetadataPage {
  items: AuditMetadataRecord[]
  total: number
  page: number
  page_size: number
}

export interface RawAuditContentPart {
  direction: 'request' | 'response'
  sequence: number
  content: string
}

export interface AuditDisclosureResult {
  operation_id: string
  metadata: AuditMetadataRecord
  parts: RawAuditContentPart[]
}

export async function listAuditMetadata(params: AuditMetadataQuery): Promise<AuditMetadataPage> {
  const { data } = await apiClient.get<AuditMetadataPage>('/admin/audit/interactions', { params })
  return data
}

export async function discloseAuditContent(interactionId: string): Promise<AuditDisclosureResult> {
  const { data } = await apiClient.post<AuditDisclosureResult>(
    `/admin/audit/interactions/${interactionId}/disclose`
  )
  return data
}

export const adminAuditAPI = {
  list: listAuditMetadata,
  disclose: discloseAuditContent
}

export default adminAuditAPI
