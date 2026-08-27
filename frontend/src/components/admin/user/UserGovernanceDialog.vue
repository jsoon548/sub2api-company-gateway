<template>
  <BaseDialog
    :show="show"
    :title="title"
    width="normal"
    @close="close"
  >
    <form id="user-governance-form" class="space-y-4" @submit.prevent="submit">
      <div class="rounded-xl border border-amber-200 bg-amber-50/70 p-4 dark:border-amber-900/60 dark:bg-amber-950/20">
        <div class="flex items-start gap-3">
          <Icon name="shield" size="md" class="mt-0.5 shrink-0 text-amber-600 dark:text-amber-400" />
          <div>
            <p class="font-medium text-gray-900 dark:text-white">{{ user?.email }}</p>
            <p class="mt-1 text-sm leading-5 text-gray-600 dark:text-gray-300">{{ description }}</p>
          </div>
        </div>
      </div>

      <div>
        <label class="input-label" for="governance-reason">{{ t('admin.users.governance.reason') }}</label>
        <textarea
          id="governance-reason"
          v-model="reason"
          required
          maxlength="512"
          rows="3"
          class="input"
          :placeholder="t('admin.users.governance.reasonPlaceholder')"
          data-test="governance-reason"
        ></textarea>
        <p class="input-hint">{{ t('admin.users.governance.reasonHint') }}</p>
      </div>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="close">{{ t('common.cancel') }}</button>
        <button
          type="submit"
          form="user-governance-form"
          class="btn"
          :class="mode === 'reactivate' ? 'btn-primary' : 'btn-danger'"
          :disabled="submitting || !reason.trim()"
          data-test="governance-confirm"
        >
          {{ submitting ? t('admin.users.governance.working') : actionLabel }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type { AdminUser } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

type GovernanceMode = 'deactivate' | 'reactivate' | 'transfer'

const props = defineProps<{
  show: boolean
  user: AdminUser | null
  mode: GovernanceMode
  seatVersion?: number | null
}>()
const emit = defineEmits(['close', 'success'])
const { t } = useI18n()
const appStore = useAppStore()
const reason = ref('')
const submitting = ref(false)

const title = computed(() => t(`admin.users.governance.${props.mode}Title`))
const description = computed(() => t(`admin.users.governance.${props.mode}Description`))
const actionLabel = computed(() => t(`admin.users.governance.${props.mode}Action`))

watch(() => props.show, (visible) => {
  if (visible) reason.value = ''
})

const close = () => {
  if (!submitting.value) emit('close')
}

const submit = async () => {
  if (!props.user || !reason.value.trim()) return
  submitting.value = true
  try {
    if (props.mode === 'transfer') {
      if (!props.seatVersion) throw new Error(t('admin.users.governance.seatUnavailable'))
      await adminAPI.users.transferSuperAdmin(props.user.id, props.seatVersion, reason.value.trim())
    } else {
      await adminAPI.users.changeLifecycle(props.user.id, props.mode, reason.value.trim())
    }
    appStore.showSuccess(t(`admin.users.governance.${props.mode}Success`))
    emit('success')
    emit('close')
  } catch (error: any) {
    appStore.showError(error.response?.data?.message || error.response?.data?.detail || error.message || t('admin.users.governance.failed'))
  } finally {
    submitting.value = false
  }
}
</script>
