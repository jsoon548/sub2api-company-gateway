import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const mocks = vi.hoisted(() => ({
  changeLifecycle: vi.fn(),
  transferSuperAdmin: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    users: {
      changeLifecycle: mocks.changeLifecycle,
      transferSuperAdmin: mocks.transferSuperAdmin,
    },
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: mocks.showError, showSuccess: mocks.showSuccess }),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

vi.mock('@/components/common/BaseDialog.vue', () => ({
  default: {
    props: ['show', 'title', 'width'],
    template: '<div v-if="show"><slot /><slot name="footer" /></div>',
  },
}))

import UserGovernanceDialog from '../UserGovernanceDialog.vue'

const user = {
  id: 42,
  email: 'synthetic@example.invalid',
  role: 'user',
  status: 'active',
} as any

describe('UserGovernanceDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.changeLifecycle.mockResolvedValue({ user_id: 42, status: 'disabled' })
    mocks.transferSuperAdmin.mockResolvedValue({ previous_user_id: 1, current_user_id: 42, seat_version: 8 })
  })

  it('requires a reason and calls the explicit lifecycle endpoint', async () => {
    const wrapper = mount(UserGovernanceDialog, {
      props: { show: true, user, mode: 'deactivate', seatVersion: 7 },
      global: { stubs: { Icon: true } },
    })

    expect(wrapper.get('[data-test="governance-confirm"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="governance-reason"]').setValue('synthetic offboarding')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.changeLifecycle).toHaveBeenCalledWith(42, 'deactivate', 'synthetic offboarding')
    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  it('passes the observed seat version to compare-and-swap transfer', async () => {
    const admin = { ...user, role: 'admin' }
    const wrapper = mount(UserGovernanceDialog, {
      props: { show: true, user: admin, mode: 'transfer', seatVersion: 7 },
      global: { stubs: { Icon: true } },
    })

    await wrapper.get('[data-test="governance-reason"]').setValue('synthetic planned rotation')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(mocks.transferSuperAdmin).toHaveBeenCalledWith(42, 7, 'synthetic planned rotation')
  })
})
