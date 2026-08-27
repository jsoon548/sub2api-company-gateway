<template>
  <Teleport to="body">
    <div
      v-if="record"
      class="fixed inset-0 z-50"
      role="dialog"
      aria-modal="true"
      :aria-label="t('admin.gatewayUsage.drawerTitle')"
      data-testid="gateway-request-detail"
    >
      <button
        type="button"
        class="absolute inset-0 cursor-default bg-slate-950/50 backdrop-blur-[1px]"
        :aria-label="t('common.close')"
        @click="emit('close')"
      ></button>

      <aside class="absolute inset-y-0 right-0 flex w-full max-w-3xl flex-col bg-white shadow-2xl dark:bg-dark-900">
        <div class="grid h-1 shrink-0 grid-cols-3" aria-hidden="true">
          <span class="bg-sky-500"></span>
          <span class="bg-indigo-500"></span>
          <span class="bg-emerald-500"></span>
        </div>

        <header class="flex items-start justify-between gap-4 border-b border-gray-200 px-5 py-4 dark:border-dark-700 sm:px-6">
          <div class="min-w-0">
            <p class="text-xs font-semibold uppercase tracking-[0.16em] text-indigo-600 dark:text-indigo-300">
              {{ t('admin.gatewayUsage.evidenceTitle') }}
            </p>
            <h2 class="mt-1 text-xl font-semibold text-gray-950 dark:text-white">
              {{ t('admin.gatewayUsage.drawerTitle') }}
            </h2>
            <code class="mt-2 block truncate text-xs text-gray-500" :title="record.gateway_request_id">
              {{ record.gateway_request_id }}
            </code>
          </div>
          <button type="button" class="btn btn-ghost btn-sm shrink-0" @click="emit('close')">
            {{ t('common.close') }}
          </button>
        </header>

        <div class="flex-1 space-y-5 overflow-y-auto p-5 sm:p-6">
          <section class="relative overflow-hidden rounded-2xl border border-gray-200 bg-gray-50/80 p-4 dark:border-dark-700 dark:bg-dark-800/55">
            <div class="absolute left-[16.66%] right-[16.66%] top-[2.35rem] hidden h-px bg-gray-200 dark:bg-dark-600 sm:block" aria-hidden="true"></div>
            <div class="relative grid gap-4 sm:grid-cols-3">
              <div class="min-w-0">
                <p class="text-xs font-medium text-gray-500">{{ t('admin.gatewayUsage.requestFact') }}</p>
                <div class="mt-2">
                  <AuditStatePair :outcome="record.request_outcome" :content-state="record.content_state" />
                </div>
              </div>
              <div class="min-w-0">
                <p class="text-xs font-medium text-gray-500">{{ t('admin.gatewayUsage.auditFact') }}</p>
                <span :class="[statusBadgeClass, record.audit_present ? linkedTone : unavailableTone]" class="mt-2">
                  <span class="h-1.5 w-1.5 rounded-full bg-current opacity-70" aria-hidden="true"></span>
                  {{ record.audit_present ? t('admin.gatewayUsage.linked') : t('admin.gatewayUsage.unavailable') }}
                </span>
              </div>
              <div class="min-w-0">
                <p class="text-xs font-medium text-gray-500">{{ t('admin.gatewayUsage.usageFact') }}</p>
                <span :class="[statusBadgeClass, record.usage_present ? recordedTone : missingTone]" class="mt-2">
                  <span class="h-1.5 w-1.5 rounded-full bg-current opacity-70" aria-hidden="true"></span>
                  {{ record.usage_present ? t('admin.gatewayUsage.recorded') : t('admin.gatewayUsage.missing') }}
                </span>
              </div>
            </div>
          </section>

          <section class="detail-section">
            <h3 class="detail-heading">{{ t('admin.gatewayUsage.identityTitle') }}</h3>
            <dl class="detail-grid">
              <div>
                <dt>{{ t('admin.gatewayUsage.employee') }}</dt>
                <dd>{{ record.subject_email_snapshot || '—' }}</dd>
              </div>
              <div>
                <dt>{{ t('admin.audit.userId') }}</dt>
                <dd>{{ record.subject_user_id ?? '—' }}</dd>
              </div>
              <div>
                <dt>{{ t('admin.gatewayUsage.profile') }}</dt>
                <dd>{{ record.profile_version || '—' }}</dd>
              </div>
              <div>
                <dt>{{ t('admin.gatewayUsage.protocolTransport') }}</dt>
                <dd>{{ protocolTransport }}</dd>
              </div>
              <div>
                <dt>{{ t('admin.gatewayUsage.requestedModel') }}</dt>
                <dd>{{ record.requested_model || '—' }}</dd>
              </div>
              <div>
                <dt>{{ t('admin.gatewayUsage.resolvedModel') }}</dt>
                <dd>{{ record.resolved_model || '—' }}</dd>
              </div>
            </dl>
          </section>

          <section class="detail-section">
            <h3 class="detail-heading">{{ t('admin.gatewayUsage.usageFactsTitle') }}</h3>
            <dl v-if="record.usage_present" class="detail-grid">
              <div>
                <dt>{{ t('admin.gatewayUsage.inputTokens') }}</dt>
                <dd>{{ formatNumber(record.input_tokens) }}</dd>
              </div>
              <div>
                <dt>{{ t('admin.gatewayUsage.outputTokens') }}</dt>
                <dd>{{ formatNumber(record.output_tokens) }}</dd>
              </div>
              <div>
                <dt>{{ t('admin.gatewayUsage.totalTokens') }}</dt>
                <dd>{{ formatNumber(record.total_tokens) }}</dd>
              </div>
              <div>
                <dt>{{ t('admin.gatewayUsage.actualCost') }}</dt>
                <dd>{{ formatCost(record.actual_cost) }}</dd>
              </div>
              <div>
                <dt>{{ t('admin.gatewayUsage.account') }}</dt>
                <dd>{{ record.account_id ?? '—' }}</dd>
              </div>
              <div>
                <dt>{{ t('admin.gatewayUsage.duration') }}</dt>
                <dd>{{ formatDuration(record.duration_ms) }}</dd>
              </div>
            </dl>
            <div v-else class="rounded-xl border border-amber-200 bg-amber-50/70 p-4 dark:border-amber-800/80 dark:bg-amber-950/30">
              <p class="text-sm font-semibold text-amber-900 dark:text-amber-200">{{ t('admin.gatewayUsage.missing') }}</p>
              <p class="mt-1 text-sm leading-6 text-amber-800 dark:text-amber-300">{{ t('admin.gatewayUsage.noUsageHint') }}</p>
            </div>
          </section>

          <section class="detail-section">
            <div class="flex flex-wrap items-center justify-between gap-3">
              <h3 class="detail-heading">{{ t('admin.gatewayUsage.auditMetadataTitle') }}</h3>
              <button
                v-if="audit && canDisclose"
                type="button"
                class="btn btn-secondary btn-sm"
                :disabled="!isDisclosable"
                data-testid="view-raw-content"
                @click="emit('disclose', audit)"
              >
                {{ t('admin.audit.viewRaw') }}
              </button>
              <span v-else-if="audit" class="text-xs text-gray-500">{{ t('admin.audit.superAdminOnly') }}</span>
            </div>

            <p v-if="auditLoading" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
            <p v-else-if="auditError" class="text-sm text-rose-600 dark:text-rose-300">{{ auditError }}</p>
            <div v-else-if="!audit" class="rounded-xl border border-gray-200 bg-gray-50 p-4 text-sm text-gray-600 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-300">
              {{ t('admin.gatewayUsage.auditUnavailableHint') }}
            </div>
            <dl v-else class="detail-grid">
              <div class="sm:col-span-2">
                <dt>{{ t('admin.gatewayUsage.endpoint') }}</dt>
                <dd class="break-all font-mono text-xs">{{ audit.method }} {{ audit.endpoint }}</dd>
              </div>
              <div>
                <dt>{{ t('admin.gatewayUsage.admittedAt') }}</dt>
                <dd>{{ formatTime(audit.admitted_at) }}</dd>
              </div>
              <div>
                <dt>{{ t('admin.gatewayUsage.completedAt') }}</dt>
                <dd>{{ formatTime(audit.completed_at) }}</dd>
              </div>
              <div>
                <dt>{{ t('admin.audit.expires') }}</dt>
                <dd>{{ formatTime(audit.expires_at) }}</dd>
              </div>
              <div>
                <dt>{{ t('admin.gatewayUsage.parts') }}</dt>
                <dd>{{ t('admin.gatewayUsage.partCounts', { request: audit.request_part_count, response: audit.response_part_count }) }}</dd>
              </div>
              <div>
                <dt>{{ t('admin.gatewayUsage.downstreamStatus') }}</dt>
                <dd>{{ audit.downstream_status ?? '—' }}</dd>
              </div>
              <div>
                <dt>{{ t('admin.gatewayUsage.downstreamWrite') }}</dt>
                <dd>{{ downstreamWriteLabel }}</dd>
              </div>
              <div v-if="audit.safe_error_summary" class="sm:col-span-2">
                <dt>{{ t('admin.gatewayUsage.safeError') }}</dt>
                <dd>{{ audit.safe_error_summary }}</dd>
              </div>
            </dl>
          </section>
        </div>
      </aside>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import AuditStatePair from '@/components/admin/AuditStatePair.vue'
import type { AuditMetadataRecord } from '@/api/admin/audit'
import type { GatewayUsageRecord } from '@/api/admin/gatewayUsage'

const props = defineProps<{
  record: GatewayUsageRecord | null
  audit: AuditMetadataRecord | null
  auditLoading: boolean
  auditError: string
  canDisclose: boolean
}>()

const emit = defineEmits<{
  close: []
  disclose: [record: AuditMetadataRecord]
}>()

const { t } = useI18n()
const statusBadgeClass = 'inline-flex min-h-6 items-center gap-1.5 whitespace-nowrap rounded-full border px-2.5 py-1 text-xs font-medium leading-none'
const linkedTone = 'border-indigo-200 bg-indigo-50 text-indigo-700 dark:border-indigo-800/80 dark:bg-indigo-950/35 dark:text-indigo-300'
const unavailableTone = 'border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-800/80 dark:bg-rose-950/35 dark:text-rose-300'
const recordedTone = 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-800/80 dark:bg-emerald-950/35 dark:text-emerald-300'
const missingTone = 'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-800/80 dark:bg-amber-950/35 dark:text-amber-300'

const protocolTransport = computed(() => {
  if (!props.record) return '—'
  const protocol = props.record.protocol ? props.record.protocol.toUpperCase() : '—'
  const transport = props.record.transport ? t(`admin.audit.transports.${props.record.transport}`) : '—'
  return `${protocol} · ${transport}`
})

const isDisclosable = computed(() => {
  if (!props.audit || !['complete', 'incomplete'].includes(props.audit.content_state)) return false
  return new Date(props.audit.expires_at).getTime() > Date.now()
})

const downstreamWriteLabel = computed(() => {
  if (!props.audit) return '—'
  const key = `admin.gatewayUsage.writeResults.${props.audit.downstream_write_result}`
  const translated = t(key)
  return translated === key ? props.audit.downstream_write_result : translated
})

function formatNumber(value?: number): string {
  return value === undefined ? '—' : new Intl.NumberFormat().format(value)
}

function formatCost(value?: number): string {
  return value === undefined ? '—' : `$${value.toFixed(6)}`
}

function formatDuration(value?: number): string {
  return value === undefined ? '—' : `${new Intl.NumberFormat().format(value)} ms`
}

function formatTime(value?: string): string {
  if (!value) return '—'
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}
</script>

<style scoped>
.detail-section {
  @apply space-y-4 rounded-2xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900 sm:p-5;
}

.detail-heading {
  @apply text-sm font-semibold text-gray-950 dark:text-white;
}

.detail-grid {
  @apply grid gap-x-6 gap-y-4 sm:grid-cols-2;
}

.detail-grid dt {
  @apply text-xs font-medium text-gray-500 dark:text-gray-400;
}

.detail-grid dd {
  @apply mt-1 break-words text-sm text-gray-900 dark:text-gray-100;
}
</style>
