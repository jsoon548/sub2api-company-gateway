import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import GatewayUsageView from '../GatewayUsageView.vue'
import zhAuditMessages from '@/i18n/locales/zh/admin/audit'
import zhGatewayUsageMessages from '@/i18n/locales/zh/admin/gatewayUsage'

const { usageList, summary, auditList, disclose, authState, replace, routeState } = vi.hoisted(() => ({
  usageList: vi.fn(),
  summary: vi.fn(),
  auditList: vi.fn(),
  disclose: vi.fn(),
  authState: { isSuperAdmin: true },
  replace: vi.fn(),
  routeState: { query: {} as Record<string, string> }
}))

vi.mock('@/api/admin', () => ({
  adminGatewayUsageAPI: { list: usageList, summary },
  adminAuditAPI: { list: auditList, disclose }
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState
}))

vi.mock('vue-router', () => ({
  useRoute: () => routeState,
  useRouter: () => ({ replace })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string>) => params?.id ? `${key}:${params.id}` : key
    })
  }
})

const normal = {
  gateway_request_id: '11111111-1111-4111-8111-111111111111',
  audit_interaction_id: '22222222-2222-4222-8222-222222222222',
  usage_log_id: 9,
  usage_record_count: 1,
  audit_present: true,
  usage_present: true,
  result: 'normal_usage' as const,
  event_time: '2026-08-01T00:00:00Z',
  subject_user_id: 42,
  subject_email_snapshot: 'historical@example.invalid',
  profile_version: 'codex-openai-v1',
  protocol: 'openai' as const,
  transport: 'http' as const,
  requested_model: 'gatewayUsage-model',
  resolved_model: 'gatewayUsage-model',
  request_outcome: 'completed' as const,
  content_state: 'complete' as const,
  account_id: 17,
  input_tokens: 120,
  output_tokens: 30,
  total_tokens: 150,
  actual_cost: 0.25,
  duration_ms: 250
}

const noUsage = {
  ...normal,
  gateway_request_id: '33333333-3333-4333-8333-333333333333',
  audit_interaction_id: '33333333-3333-4333-8333-333333333334',
  usage_log_id: undefined,
  usage_record_count: 0,
  usage_present: false,
  result: 'no_usage' as const,
  total_tokens: undefined,
  actual_cost: undefined
}

const upstreamFailed = {
  ...normal,
  gateway_request_id: '44444444-4444-4444-8444-444444444444',
  result: 'audit_failed' as const,
  request_outcome: 'upstream_failed' as const,
  content_state: 'incomplete' as const
}

const auditRecord = {
  id: normal.audit_interaction_id,
  gateway_request_id: normal.gateway_request_id,
  subject_user_id: normal.subject_user_id,
  subject_email_snapshot: normal.subject_email_snapshot,
  profile_version: normal.profile_version,
  protocol: 'openai' as const,
  endpoint: '/v1/responses',
  method: 'POST',
  transport: 'http' as const,
  requested_model: normal.requested_model,
  resolved_model: normal.resolved_model,
  request_outcome: 'completed' as const,
  content_state: 'complete' as const,
  downstream_status: 200,
  downstream_write_result: 'succeeded',
  admitted_at: '2026-08-01T00:00:00Z',
  completed_at: '2026-08-01T00:00:01Z',
  expires_at: '2027-01-28T00:00:00Z',
  last_activity_at: '2026-08-01T00:00:01Z',
  request_part_count: 1,
  response_part_count: 1
}

function mountView() {
  return mount(GatewayUsageView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        Pagination: true,
        Teleport: true
      }
    }
  })
}

describe('admin GatewayUsageView unified gateway reconciliation', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'scrollX', { configurable: true, value: 0 })
    Object.defineProperty(window, 'scrollY', { configurable: true, value: 0 })
    usageList.mockReset()
    summary.mockReset()
    auditList.mockReset()
    disclose.mockReset()
    replace.mockReset()
    routeState.query = {}
    authState.isSuperAdmin = true
    usageList.mockResolvedValue({ items: [normal, noUsage], total: 2, page: 1, page_size: 20 })
    auditList.mockResolvedValue({ items: [auditRecord], total: 1, page: 1, page_size: 1 })
    disclose.mockResolvedValue({
      operation_id: '55555555-5555-4555-8555-555555555555',
      metadata: auditRecord,
      parts: [{ direction: 'request', sequence: 0, content: 'gatewayUsage synthetic raw content' }]
    })
    summary.mockResolvedValue({
      group_by: 'time',
      totals: {
        requests: 2, usage_records: 1, normal_usage_requests: 1, no_usage_requests: 1,
        audit_failed_requests: 0, rejected_pre_upstream_requests: 0,
        input_tokens: 120, output_tokens: 30, total_cost: 0.25, actual_cost: 0.25
      },
      items: [{ key: '2026-08-01', requests: 2, usage_records: 1, input_tokens: 120, output_tokens: 30, total_cost: 0.25, actual_cost: 0.25 }]
    })
  })

  it('shows one request list and keeps missing usage distinct from zero', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('h1').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('admin.gatewayUsage.description')
    expect(wrapper.text()).not.toContain('admin.audit.retentionNotice')
    expect(wrapper.text()).toContain('historical@example.invalid')
    expect(wrapper.text()).toContain('admin.gatewayUsage.noUsage')
    const missingUsageHint = wrapper.get('[data-testid="gateway-usage-missing-hint"]')
    expect(wrapper.get('[role="tooltip"]').isVisible()).toBe(false)
    expect(missingUsageHint.get('[role="img"]').attributes('aria-label')).toBe('admin.gatewayUsage.noUsageHint')
    await missingUsageHint.trigger('mouseenter')
    await flushPromises()
    const tooltip = wrapper.get('[role="tooltip"]')
    expect(tooltip.isVisible()).toBe(true)
    expect(tooltip.text()).toBe('admin.gatewayUsage.noUsageHint')
    expect(wrapper.text()).toContain('150')
    expect(wrapper.text()).toContain('OpenAI')
    expect(wrapper.text()).not.toContain('admin.audit.transports.http')
    expect(wrapper.text()).not.toContain('nonce')
    expect(wrapper.text()).not.toContain('ciphertext')
    expect(wrapper.find('a[download]').exists()).toBe(false)
    expect(wrapper.findAll('[data-testid="open-request-detail"]')).toHaveLength(2)
  })

  it('anchors the fixed tooltip to the viewport even after the page scrolls', async () => {
    Object.defineProperty(window, 'scrollX', { configurable: true, value: 320 })
    Object.defineProperty(window, 'scrollY', { configurable: true, value: 640 })
    const wrapper = mountView()
    await flushPromises()

    const missingUsageHint = wrapper.get('[data-testid="gateway-usage-missing-hint"]')
    vi.spyOn(missingUsageHint.element, 'getBoundingClientRect').mockReturnValue({
      x: 300,
      y: 200,
      top: 200,
      left: 300,
      right: 332,
      bottom: 224,
      width: 32,
      height: 24,
      toJSON: () => ({})
    })

    await missingUsageHint.trigger('mouseenter')
    await flushPromises()

    const style = wrapper.get('[role="tooltip"]').attributes('style')
    expect(style).toContain('top: calc(192px)')
    expect(style).toContain('left: 316px')
    expect(style).not.toContain('840px')
    expect(style).not.toContain('636px')
  })

  it('shows the UTC day grouping rule and only calls out exceptional streaming transport', async () => {
    usageList.mockResolvedValue({ items: [{ ...normal, transport: 'sse' as const }], total: 1, page: 1, page_size: 20 })
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('admin.gatewayUsage.groupDescriptions.time')
    expect(wrapper.text()).toContain('admin.gatewayUsage.groupHeadings.time')
    expect(wrapper.text()).toContain('OpenAI · admin.audit.transports.sse')
    expect(summary).toHaveBeenCalledWith(expect.objectContaining({ page: 1, page_size: 20 }), 'time')
  })

  it('uses concise reconciliation labels instead of rating the amount of usage', () => {
    expect(zhGatewayUsageMessages.gatewayUsage.results).toEqual({
      normal_usage: '核验通过',
      no_usage: '缺少用量记录',
      audit_failed: '审计异常',
      rejected_pre_upstream: '上游调用前拒绝'
    })
    expect(zhGatewayUsageMessages.gatewayUsage.groupDescriptions.time).toContain('同一天的请求合并为一行')
    expect(zhAuditMessages.audit.transports).toEqual({ http: 'HTTP', sse: 'SSE 流式' })
  })

  it('localizes request outcomes and content states instead of exposing enum values', async () => {
    usageList.mockResolvedValue({ items: [normal, noUsage, upstreamFailed], total: 3, page: 1, page_size: 20 })
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('admin.audit.outcomes.completed')
    expect(wrapper.text()).toContain('admin.audit.outcomes.upstream_failed')
    expect(wrapper.text()).toContain('admin.audit.contentStates.complete')
    expect(wrapper.text()).toContain('admin.audit.contentStates.incomplete')
    expect(wrapper.text()).not.toContain('completed / complete')
    expect(wrapper.text()).not.toContain('upstream_failed / incomplete')

    const statePairs = wrapper.findAll('[data-testid="audit-state-pair"]')
    expect(statePairs).toHaveLength(3)
    for (const pair of statePairs) {
      expect(pair.classes()).toContain('gap-1.5')
      expect(pair.get('[data-testid="audit-outcome-badge"]').classes()).toContain('rounded-full')
      expect(pair.get('[data-testid="audit-content-state-badge"]').classes()).toContain('rounded-full')
    }
  })

  it('opens one linked detail drawer without disclosing raw content to an ordinary admin', async () => {
    authState.isSuperAdmin = false
    const wrapper = mountView()
    await flushPromises()

    await wrapper.findAll('[data-testid="open-request-detail"]')[0].trigger('click')
    await flushPromises()

    expect(auditList).toHaveBeenCalledWith({ gateway_request_id: normal.gateway_request_id, page: 1, page_size: 1 })
    expect(wrapper.get('[data-testid="gateway-request-detail"]').text()).toContain('/v1/responses')
    expect(wrapper.text()).toContain('admin.audit.superAdminOnly')
    expect(wrapper.find('[data-testid="view-raw-content"]').exists()).toBe(false)
    expect(disclose).not.toHaveBeenCalled()
  })

  it('keeps raw disclosure behind the existing super-admin action', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.findAll('[data-testid="open-request-detail"]')[0].trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="view-raw-content"]').trigger('click')
    await flushPromises()

    expect(disclose).toHaveBeenCalledWith(auditRecord.id)
    expect(wrapper.text()).toContain('gatewayUsage synthetic raw content')
    expect(wrapper.find('textarea').exists()).toBe(false)
    expect(wrapper.find('a[download]').exists()).toBe(false)
  })

  it('preserves the old Gateway ID deep link and opens the unified detail', async () => {
    routeState.query = { gateway_request_id: normal.gateway_request_id }
    const wrapper = mountView()
    await flushPromises()

    expect(usageList).toHaveBeenCalledWith(expect.objectContaining({ gateway_request_id: normal.gateway_request_id }))
    expect(auditList).toHaveBeenCalledWith({ gateway_request_id: normal.gateway_request_id, page: 1, page_size: 1 })
    expect(wrapper.find('[data-testid="gateway-request-detail"]').exists()).toBe(true)
  })
})
