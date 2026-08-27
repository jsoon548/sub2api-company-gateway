import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import WorkSessionAutoView from '../WorkSessionAutoView.vue'
import zhWorkSession from '@/i18n/locales/zh/admin/workSession'

const { getState, replace, setEmergencyDisabled, auditList, auditDisclose, authState } = vi.hoisted(() => ({
  getState: vi.fn(),
  replace: vi.fn(),
  setEmergencyDisabled: vi.fn(),
  auditList: vi.fn(),
  auditDisclose: vi.fn(),
  authState: { isSuperAdmin: true }
}))

vi.mock('@/api/admin', () => ({
  adminWorkSessionAPI: { get: getState, replace, setEmergencyDisabled },
  adminAuditAPI: { list: auditList, disclose: auditDisclose }
}))

vi.mock('@/stores/auth', () => ({ useAuthStore: () => authState }))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const state = {
  status: {
    mode: 'required', schema_ready: true, reliable_ready: true,
    auto_capability_ready: true, reason_code: 'ready', tenant_id: 'tenant-test',
    hmac_key_version: 'workSession-v1',
    auto_complexity: {
      profile: 'auto_complexity', state: 'ready', ready: true, reason_code: 'ready',
      backend: 'stub', provider: 'openai-compatible', model: 'classifier-small',
      prompt_version: 'auto-complexity-v1', schema_version: 'auto-complexity-v1'
    }
  },
  auto: { enabled: false, user_whitelist: [42], group_whitelist: [], config_version: 2 },
  catalog: [{
    id: '11111111-1111-4111-8111-111111111111', generation: 2,
    logical_model: 'general-model', provider_model: 'provider-general', tier: 'general' as const,
    capabilities: ['tools'], valid_from: '2026-08-09T00:00:00Z', emergency_disabled: false
  }],
  candidate_pools: [{
    id: '22222222-2222-4222-8222-222222222222', generation: 2, tier: 'general' as const,
    position: 1, catalog_entry_id: '11111111-1111-4111-8111-111111111111',
    logical_model: 'general-model', valid_from: '2026-08-09T00:00:00Z'
  }],
  config_versions: [{
    config_version: 2, current: true, created_at: '2026-08-09T00:00:00Z',
    session_count: 1, reliable_session_count: 1, request_scoped_session_count: 0,
    model_count: 1, candidate_count: 1, auto_snapshot_status: 'current' as const,
    auto: { enabled: false, user_whitelist: [42], group_whitelist: [], config_version: 2 },
    catalog: [{
      id: '11111111-1111-4111-8111-111111111111', generation: 2,
      logical_model: 'general-model', provider_model: 'provider-general', tier: 'general' as const,
      capabilities: ['tools'], valid_from: '2026-08-09T00:00:00Z', emergency_disabled: false
    }],
    candidate_pools: [{
      id: '22222222-2222-4222-8222-222222222222', generation: 2, tier: 'general' as const,
      position: 1, catalog_entry_id: '11111111-1111-4111-8111-111111111111',
      logical_model: 'general-model', valid_from: '2026-08-09T00:00:00Z'
    }]
  }, {
    config_version: 1, current: false, created_at: '2026-08-08T00:00:00Z',
    session_count: 0, reliable_session_count: 0, request_scoped_session_count: 0,
    model_count: 1, candidate_count: 0, auto_snapshot_status: 'not_recorded' as const,
    catalog: [{
      id: '55555555-5555-4555-8555-555555555555', generation: 1,
      logical_model: 'legacy-model', provider_model: 'provider-legacy', tier: 'economy' as const,
      capabilities: [], valid_from: '2026-08-08T00:00:00Z', emergency_disabled: false
    }],
    candidate_pools: []
  }],
  recent_sessions: [{
    id: '33333333-3333-4333-8333-333333333333', tenant_id: 'tenant-test', employee_user_id: 42,
    profile_version: 'codex-openai-v1', signal_source: 'codex_session_header_v1', signal_status: 'verified',
    hmac_key_version: 'workSession-v1', reliability: 'reliable' as const, routing_mode: 'explicit' as const,
    config_version: 2, analysis_eligible: true, quota_grace_eligible: false, status: 'active' as const,
    required_capabilities: [], routing_version: 0,
    first_gateway_request_id: '44444444-4444-4444-8444-444444444444',
    last_gateway_request_id: '44444444-4444-4444-8444-444444444444',
    created_at: '2026-08-09T00:00:00Z', last_activity_at: '2026-08-09T00:01:00Z'
  }],
  routing_metrics: {
    decision_count: 1, classifier_call_count: 1, classifier_timeout_count: 0,
    classifier_fallback_count: 0, classifier_p95_latency_ms: 17, routing_p95_latency_ms: 22
  },
  route_decisions: [{
    id: '66666666-6666-4666-8666-666666666666',
    gateway_request_id: '77777777-7777-4777-8777-777777777777',
    work_session_id: '33333333-3333-4333-8333-333333333333', employee_user_id: 42,
    profile_version: 'openai-responses-v1', config_version: 2,
    required_capabilities: ['tool_use'], task_complexity: 'general' as const, certainty: 'decisive' as const,
    explanation: 'Synthetic general classification.', decision_source: 'classifier' as const,
    rule_version: 'autoRouting-rules-v1', classifier_version: 'autoRouting-runtime-stub-v1',
    classifier_status: 'completed', classifier_latency_ms: 17,
    requested_tier: 'general' as const, effective_tier: 'general' as const,
    candidate_pool: [{
      tier: 'general' as const, position: 1, logical_model: 'general-model',
      required_capabilities: ['tool_use'], candidate_capabilities: ['tool_use'],
      status: 'eligible', schedulable_accounts: 2
    }],
    actual_logical_model: 'general-model', actual_provider_model: 'provider-general',
    change_reason: 'initial_selection', technical_retry_count: 1,
    technical_retry_reason: 'account_switch', decision_result: 'selected' as const,
    routing_latency_ms: 22, audit_linked: true, usage_linked: true,
    classifier_run_id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    inference_run: {
      id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', purpose: 'auto_complexity_classification',
      profile: 'auto_complexity', backend: 'stub', provider: 'openai-compatible',
      model: 'classifier-small', prompt_version: 'auto-complexity-v1', schema_version: 'auto-complexity-v1',
      status: 'completed', provider_request_id: 'provider-request-synthetic',
      input_units: 12, output_units: 3, latency_ms: 17, created_at: '2026-08-09T00:00:00Z'
    },
    created_at: '2026-08-09T00:00:00Z', updated_at: '2026-08-09T00:00:01Z'
  }, {
    id: '88888888-8888-4888-8888-888888888888',
    gateway_request_id: '99999999-9999-4999-8999-999999999999',
    work_session_id: '33333333-3333-4333-8333-333333333333', employee_user_id: 42,
    profile_version: 'openai-responses-v1', config_version: 2,
    required_capabilities: [], task_complexity: 'simple' as const, certainty: 'deterministic' as const,
    explanation: 'A deterministic rule matched a bounded rewrite.', decision_source: 'rule' as const,
    rule_version: 'autoRouting-rules-v1', classifier_status: 'not_called', classifier_latency_ms: 0,
    requested_tier: 'economy' as const, effective_tier: 'economy' as const,
    candidate_pool: [], actual_logical_model: 'economy-model', actual_provider_model: 'provider-economy',
    change_reason: 'initial_selection', technical_retry_count: 0,
    decision_result: 'selected' as const, routing_latency_ms: 1, audit_linked: true, usage_linked: false,
    created_at: '2026-08-09T00:02:00Z', updated_at: '2026-08-09T00:02:00Z'
  }]
}

function mountView() {
  return mount(WorkSessionAutoView, {
    global: { stubs: { AppLayout: { template: '<div><slot /></div>' } } }
  })
}

describe('WorkSessionAutoView work-session implementation-10 boundary', () => {
  beforeEach(() => {
    authState.isSuperAdmin = true
    getState.mockReset().mockResolvedValue(structuredClone(state))
    replace.mockReset().mockResolvedValue(structuredClone({ ...state, auto: { ...state.auto, enabled: true, config_version: 3 } }))
    setEmergencyDisabled.mockReset().mockResolvedValue(structuredClone({ ...state, catalog: [{ ...state.catalog[0], emergency_disabled: true }] }))
    const requestBody = JSON.stringify({
      model: 'auto',
      input: [{ type: 'message', role: 'user', content: [{ type: 'input_text', text: '请跨文件重构认证流程，并解释并发安全性。' }] }]
    })
    const requestBodyBase64 = btoa(String.fromCharCode(...new TextEncoder().encode(requestBody)))
    auditList.mockReset().mockResolvedValue({
      items: [{
        id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
        gateway_request_id: '77777777-7777-4777-8777-777777777777',
        profile_version: 'openai-responses-v1', protocol: 'openai', endpoint: '/v1/responses', method: 'POST', transport: 'http',
        request_outcome: 'completed', content_state: 'complete', downstream_write_result: 'succeeded',
        admitted_at: '2026-08-09T00:00:00Z', expires_at: '2027-02-05T00:00:00Z', last_activity_at: '2026-08-09T00:00:01Z',
        request_part_count: 1, response_part_count: 1
      }], total: 1, page: 1, page_size: 1
    })
    auditDisclose.mockReset().mockResolvedValue({
      operation_id: 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
      metadata: {},
      parts: [{
        direction: 'request', sequence: 0,
        content: JSON.stringify({ version: 'core-gateway-request-v1', body: requestBodyBase64 })
      }]
    })
  })

  it('describes cross-request association separately from explicit Auto admission', () => {
    expect(zhWorkSession.workSession.foundation).toBe('归属服务')
    expect(zhWorkSession.workSession.mode).toBe('关联方式')
    expect(zhWorkSession.workSession.modeRequired).toBe('HMAC 关联')
    expect(zhWorkSession.workSession.reliable).toBe('跨请求')
    expect(zhWorkSession.workSession.unreliable).toBe('单次请求')
    expect(zhWorkSession.workSession.associationHelp).toBe('会话标识有效时，同一客户端任务的多次请求归入同一会话；标识缺失或无效时按单次请求处理。')
    expect(zhWorkSession.workSession.modeHelp).toContain('独立 HMAC 密钥')
    expect(zhWorkSession.workSession.modeHelp).toContain('不保存原始会话标识')
    expect(zhWorkSession.workSession.autoOn).toBe('Auto 路由已开启')
    expect(zhWorkSession.workSession.autoOnNote).toContain('model=auto')
    expect(zhWorkSession.workSession.autoBoundaryNote).toContain('能力硬约束检查')
    expect(zhWorkSession.workSession.routingExplicit).toBe('显式模型')
    expect(zhWorkSession.workSession.routingAuto).toBe('Auto 请求')
    expect(zhWorkSession.workSession.versionHistoryNote).toBe('每个版本保留其模型目录、候选顺序和引用它的会话。')
  })

  it('keeps Auto disabled by default and never exposes raw session values', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect((wrapper.get('[data-test="auto-enabled"]').element as HTMLInputElement).checked).toBe(false)
    expect(wrapper.get('[data-test="auto-status"]').text()).toBe('admin.workSession.autoOff')
    expect(wrapper.get('[data-test="association-help"]').attributes('aria-label')).toBe('admin.workSession.associationHelpLabel')
    expect(wrapper.get('[data-test="association-method-help"]').attributes('aria-label')).toBe('admin.workSession.modeHelpLabel')
    expect(wrapper.get('[data-test="config-version-help"]').attributes('aria-label')).toBe('admin.workSession.configVersionHelpLabel')
    expect(wrapper.find('h1').exists()).toBe(false)
    expect(wrapper.text()).toContain('codex_session_header_v1')
    expect(wrapper.text()).not.toContain('client session value')
    expect(wrapper.text()).not.toContain('prompt similarity')
  })

  it('shows what every retained config version contains without inventing historical Auto settings', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-test="config-version-history"]').exists()).toBe(true)
    const sections = wrapper.findAll('section')
    expect(sections.at(-1)?.attributes('data-test')).toBe('config-version-history')
    expect(wrapper.get('[data-test="config-version-1"]').text()).toContain('admin.workSession.autoHistoryMissing')
    await wrapper.get('[data-test="version-details-1"]').trigger('click')
    expect(wrapper.get('[data-test="version-detail-1"]').text()).toContain('provider-legacy')
    expect(wrapper.get('[data-test="version-detail-1"]').text()).toContain('admin.workSession.historicalAutoUnavailable')
  })

  it('shows complete Route Decision facts and routing p95 without model-output injection', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-test="routing-metrics"]').text()).toContain('17 ms')
    const decision = wrapper.get('[data-test="route-decision-66666666-6666-4666-8666-666666666666"]')
    expect(decision.text()).toContain('general-model')
    expect(decision.text()).toContain('initial_selection')
    expect(decision.text()).toContain('Synthetic general classification.')
    expect(decision.get('[data-test="decision-associations"]').classes()).toContain('flex-nowrap')
    expect(decision.get('[data-test="route-audit-association"]').classes()).toContain('whitespace-nowrap')
    expect(decision.get('[data-test="route-usage-association"]').classes()).toContain('whitespace-nowrap')
    expect(wrapper.get('[data-test="route-actions-header"]').classes()).toContain('text-left')
    expect(decision.get('[data-test="full-decision-toggle"]').classes()).toContain('whitespace-nowrap')
    expect(decision.text()).toContain('admin.workSession.complexityGeneral')
    expect(decision.text()).toContain('admin.workSession.decisionSourceClassifier')
    expect(decision.text()).toContain('admin.workSession.classifierCertaintyDecisive')
    expect(decision.text()).toContain('admin.workSession.syntheticClassifier')
    expect(decision.text()).not.toContain('completed')
    expect(decision.text()).not.toContain('91%')
    expect(wrapper.get('[data-test="route-decisions"]').text()).toContain('admin.workSession.ruleSimpleExplanation')
    expect(wrapper.get('[data-test="route-decisions"]').text()).not.toContain('high-confidence')
    expect(wrapper.get('[data-test="complexity-decision-help"]').attributes('aria-label')).toBe('admin.workSession.complexityDecisionHelpLabel')
    expect(wrapper.get('[data-test="classifier-help"]').attributes('aria-label')).toBe('admin.workSession.classifierHelpLabel')
    expect(wrapper.get('[data-test="classifier-p95-help"]').attributes('aria-label')).toBe('admin.workSession.classifierP95HelpLabel')
    expect(wrapper.get('[data-test="routing-p95-help"]').attributes('aria-label')).toBe('admin.workSession.routingP95HelpLabel')
    await decision.get('[data-test="full-decision-toggle"]').trigger('click')
    const expandedDecision = wrapper.get('[data-test="expanded-route-decision-66666666-6666-4666-8666-666666666666"]')
    expect(expandedDecision.get('[data-test="expanded-decision-associations"]').classes()).toContain('flex-nowrap')
    expect(expandedDecision.get('[data-test="expanded-audit-association"]').classes()).toContain('whitespace-nowrap')
    expect(expandedDecision.get('[data-test="expanded-usage-association"]').classes()).toContain('whitespace-nowrap')
    expect(expandedDecision.get('[data-test="full-decision-panel"]').text()).toContain('"certainty": "decisive"')
    expect(expandedDecision.get('[data-test="full-decision-panel"]').text()).not.toContain('confidence')
    await expandedDecision.get('[data-test="view-classification-evidence"]').trigger('click')
    await flushPromises()
    const evidence = expandedDecision.get('[data-test="classification-evidence-content"]')
    expect(auditList).toHaveBeenCalledWith(expect.objectContaining({ gateway_request_id: '77777777-7777-4777-8777-777777777777' }))
    expect(auditDisclose).toHaveBeenCalledWith('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')
    expect(evidence.text()).toContain('admin.workSession.messageRoleUser')
    expect(evidence.text()).toContain('请跨文件重构认证流程，并解释并发安全性。')
    expect(evidence.text()).toContain('admin.workSession.fullClassificationRequest')
    expect(wrapper.get('[data-test="route-decisions"]').text()).toContain('autoRouting-runtime-stub-v1')
  })

  it('keeps governed classification plaintext unavailable to ordinary administrators', async () => {
    authState.isSuperAdmin = false
    const wrapper = mountView()
    await flushPromises()
    const decision = wrapper.get('[data-test="route-decision-66666666-6666-4666-8666-666666666666"]')
    await decision.get('[data-test="full-decision-toggle"]').trigger('click')
    const expandedDecision = wrapper.get('[data-test="expanded-route-decision-66666666-6666-4666-8666-666666666666"]')
    expect(expandedDecision.find('[data-test="view-classification-evidence"]').exists()).toBe(false)
    expect(expandedDecision.get('[data-test="classification-evidence-forbidden"]').text()).toBe('admin.workSession.classificationEvidenceSuperAdminOnly')
    expect(auditList).not.toHaveBeenCalled()
    expect(auditDisclose).not.toHaveBeenCalled()
  })

  it('documents discrete complexity labels and discrete classifier certainty without percentages', () => {
    expect(zhWorkSession.workSession.complexityDecision).toBe('复杂度')
    expect(zhWorkSession.workSession.complexityDecisionHelp).toContain('简单、通用、复杂三档')
    expect(zhWorkSession.workSession.complexityDecisionHelp).toContain('只返回「明确 / 不明确」')
    expect(zhWorkSession.workSession.complexityDecisionHelp).toContain('不输出分数')
    expect(zhWorkSession.workSession.complexityDecisionHelp).not.toContain('94%')
    expect(zhWorkSession.workSession.complexityDecisionHelp).not.toContain('96%')
    expect(zhWorkSession.workSession.classifier).toBe('分类器')
    expect(zhWorkSession.workSession.classifierHelp).toContain('简单、通用、复杂')
    expect(zhWorkSession.workSession.classifierHelp).toContain('明确时网关采用其判断')
    expect(zhWorkSession.workSession.classifierHelp).toContain('按通用档处理')
    expect(zhWorkSession.workSession.classifierHelp).toContain('合成验收')
    expect(zhWorkSession.workSession.classifierHelp).not.toContain('80%')
    expect(zhWorkSession.workSession.classifierP95Help).toContain('规则直接判断')
    expect(zhWorkSession.workSession.routingP95Help).toContain('不含模型响应时间')
    expect(zhWorkSession.workSession.classificationEvidenceRecorded).toContain('{id}')
  })

  it('saves allowlists/catalog/pool order and performs immediate emergency disable', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="auto-enabled"]').setValue(true)
    expect(wrapper.get('[data-test="auto-status"]').text()).toBe('admin.workSession.autoOn')
    await wrapper.get('[data-test="user-whitelist"]').setValue('42, 42, 7')
    await wrapper.get('[data-test="group-whitelist"]').setValue('9')
    await wrapper.get('[data-test="save-config"]').trigger('submit')
    await flushPromises()

    expect(replace).toHaveBeenCalledWith(expect.objectContaining({
      auto_enabled: true,
      user_whitelist: [7, 42],
      group_whitelist: [9],
      candidate_pools: [expect.objectContaining({ tier: 'general', candidates: ['general-model'] })]
    }))

    await wrapper.get('[data-test="emergency-general-model"]').trigger('click')
    await flushPromises()
    expect(setEmergencyDisabled).toHaveBeenCalledWith('general-model', true)
  })
})
