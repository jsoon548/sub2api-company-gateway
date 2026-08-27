import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AuditView from '../AuditView.vue'
import zhAdminAudit from '@/i18n/locales/zh/admin/audit'

const { list, disclose, authState, push, routeState } = vi.hoisted(() => ({
  list: vi.fn(),
  disclose: vi.fn(),
  authState: { isSuperAdmin: true },
  push: vi.fn(),
  routeState: { query: {} as Record<string, string> }
}))

vi.mock('@/api/admin', () => ({
  adminAuditAPI: { list, disclose }
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState
}))

vi.mock('vue-router', () => ({
  useRoute: () => routeState,
  useRouter: () => ({ push })
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

const record = {
  id: '11111111-1111-4111-8111-111111111111',
  gateway_request_id: '22222222-2222-4222-8222-222222222222',
  subject_user_id: 42,
  subject_email_snapshot: 'synthetic@example.invalid',
  profile_version: 'codex-openai-v1',
  protocol: 'openai' as const,
  endpoint: '/v1/responses',
  method: 'POST',
  transport: 'http' as const,
  requested_model: 'auditManagement-model',
  resolved_model: 'auditManagement-model',
  request_outcome: 'completed' as const,
  content_state: 'complete' as const,
  downstream_status: 200,
  downstream_write_result: 'succeeded',
  admitted_at: '2026-08-01T00:00:00Z',
  expires_at: '2027-01-28T00:00:00Z',
  last_activity_at: '2026-08-01T00:00:01Z',
  request_part_count: 1,
  response_part_count: 1
}

function mountView() {
  return mount(AuditView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        Pagination: true,
        Teleport: true
      }
    }
  })
}

describe('admin AuditView controlled disclosure', () => {
  beforeEach(() => {
    list.mockReset()
    disclose.mockReset()
    push.mockReset()
    routeState.query = {}
    authState.isSuperAdmin = true
    list.mockResolvedValue({ items: [record], total: 1, page: 1, page_size: 20 })
    disclose.mockResolvedValue({
      operation_id: '33333333-3333-4333-8333-333333333333',
      metadata: record,
      parts: [{ direction: 'request', sequence: 0, content: 'auditManagement synthetic raw content' }]
    })
  })

  it('shows metadata to an ordinary admin without rendering a raw-content action', async () => {
    authState.isSuperAdmin = false
    const wrapper = mountView()
    await flushPromises()

    expect(list).toHaveBeenCalledWith(expect.objectContaining({ page: 1, page_size: 20 }))
    expect(wrapper.text()).toContain('synthetic@example.invalid')
    expect(wrapper.text()).toContain('admin.audit.superAdminOnly')
    expect(wrapper.text()).not.toContain('admin.audit.viewRaw')
    expect(wrapper.text()).toContain('admin.audit.viewUsage')

    const statePair = wrapper.get('[data-testid="audit-state-pair"]')
    expect(statePair.classes()).toContain('gap-1.5')
    expect(statePair.get('[data-testid="audit-outcome-badge"]').classes()).toContain('rounded-full')
    expect(statePair.get('[data-testid="audit-content-state-badge"]').classes()).toContain('rounded-full')
  })

  it('opens the safe usage detail by Gateway ID without invoking disclosure', async () => {
    const wrapper = mountView()
    await flushPromises()
    const usageButton = wrapper.findAll('button').find((button) => button.text().includes('admin.audit.viewUsage'))
    expect(usageButton).toBeTruthy()
    await usageButton!.trigger('click')
    expect(push).toHaveBeenCalledWith({ name: 'AdminGatewayUsage', query: { gateway_request_id: record.gateway_request_id } })
    expect(disclose).not.toHaveBeenCalled()
  })

  it('loads raw content directly after a super admin selects a record', async () => {
    const wrapper = mountView()
    await flushPromises()
    const viewButton = wrapper.findAll('button').find((button) => button.text().includes('admin.audit.viewRaw'))
    expect(viewButton).toBeTruthy()
    await viewButton!.trigger('click')
    await flushPromises()

    expect(disclose).toHaveBeenCalledWith(record.id)
    expect(wrapper.find('textarea').exists()).toBe(false)
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('auditManagement synthetic raw content')
    expect(wrapper.find('a[download]').exists()).toBe(false)
  })

  it('defines Chinese labels for every outcome and content state', () => {
    expect(zhAdminAudit.audit.outcomes).toEqual({
      processing: '处理中',
      rejected_pre_upstream: '上游调用前拒绝',
      completed: '请求已完成',
      upstream_failed: '上游失败',
      interrupted: '请求已中断'
    })
    expect(zhAdminAudit.audit.contentStates).toEqual({
      recording: '原文记录中',
      complete: '原文完整',
      incomplete: '原文不完整',
      expired: '原文已清理'
    })
  })
})
