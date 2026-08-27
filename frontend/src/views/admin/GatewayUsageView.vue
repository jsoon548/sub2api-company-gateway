<template>
  <AppLayout>
    <div class="mx-auto max-w-[1600px] space-y-6">
      <section class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4" :aria-label="t('admin.gatewayUsage.resultTotals')">
        <article v-for="card in resultCards" :key="card.key" class="card relative overflow-hidden p-5">
          <span :class="card.tone" class="absolute inset-y-0 left-0 w-1" aria-hidden="true"></span>
          <p class="text-xs font-semibold text-gray-500 dark:text-gray-400">{{ card.label }}</p>
          <p class="mt-2 text-2xl font-bold tabular-nums text-gray-900 dark:text-white">{{ card.value }}</p>
        </article>
      </section>

      <section class="card p-5" aria-labelledby="gateway-verification-filters-title">
        <div class="mb-4 flex items-center justify-between gap-3">
          <h2 id="gateway-verification-filters-title" class="font-semibold text-gray-900 dark:text-white">{{ t('admin.gatewayUsage.filters') }}</h2>
          <button type="button" class="btn btn-ghost btn-sm" @click="resetFilters">{{ t('common.reset') }}</button>
        </div>
        <form class="grid gap-4 md:grid-cols-2 xl:grid-cols-4" @submit.prevent="applyFilters">
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-gray-300">
            <span>{{ t('admin.gatewayUsage.employee') }}</span>
            <input v-model.trim="filters.employee" class="input w-full" :placeholder="t('admin.gatewayUsage.employeePlaceholder')" />
          </label>
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-gray-300">
            <span>{{ t('admin.gatewayUsage.profile') }}</span>
            <input v-model.trim="filters.profile" class="input w-full" :placeholder="t('admin.gatewayUsage.profilePlaceholder')" />
          </label>
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-gray-300">
            <span>{{ t('admin.gatewayUsage.model') }}</span>
            <input v-model.trim="filters.model" class="input w-full" :placeholder="t('admin.gatewayUsage.modelPlaceholder')" />
          </label>
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-gray-300">
            <span>{{ t('admin.gatewayUsage.gatewayId') }}</span>
            <input v-model.trim="filters.gateway_request_id" class="input w-full font-mono text-xs" :placeholder="t('admin.gatewayUsage.gatewayIdPlaceholder')" />
          </label>
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-gray-300">
            <span>{{ t('admin.gatewayUsage.result') }}</span>
            <select v-model="filters.result" class="input w-full">
              <option value="">{{ t('common.all') }}</option>
              <option v-for="value in results" :key="value" :value="value">{{ resultLabel(value) }}</option>
            </select>
          </label>
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-gray-300">
            <span>{{ t('admin.audit.protocol') }}</span>
            <select v-model="filters.protocol" class="input w-full">
              <option value="">{{ t('common.all') }}</option>
              <option value="anthropic">Anthropic</option>
              <option value="openai">OpenAI</option>
            </select>
          </label>
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-gray-300">
            <span>{{ t('admin.audit.outcome') }}</span>
            <select v-model="filters.outcome" class="input w-full">
              <option value="">{{ t('common.all') }}</option>
              <option v-for="value in outcomes" :key="value" :value="value">{{ outcomeLabel(value) }}</option>
            </select>
          </label>
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-gray-300">
            <span>{{ t('admin.audit.contentState') }}</span>
            <select v-model="filters.content_state" class="input w-full">
              <option value="">{{ t('common.all') }}</option>
              <option v-for="value in contentStates" :key="value" :value="value">{{ contentStateLabel(value) }}</option>
            </select>
          </label>
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-gray-300">
            <span>{{ t('admin.gatewayUsage.from') }}</span>
            <input v-model="filters.from" type="datetime-local" class="input w-full" />
          </label>
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-gray-300">
            <span>{{ t('admin.gatewayUsage.to') }}</span>
            <input v-model="filters.to" type="datetime-local" class="input w-full" />
          </label>
          <div class="flex items-end md:col-span-2">
            <button type="submit" class="btn btn-primary" :disabled="loading">{{ loading ? t('common.loading') : t('common.search') }}</button>
          </div>
        </form>
      </section>

      <section class="card overflow-hidden">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 px-5 py-4 dark:border-dark-700">
          <div>
            <h2 class="font-semibold text-gray-900 dark:text-white">{{ t('admin.gatewayUsage.aggregateTitle') }}</h2>
            <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ groupDescription }}</p>
          </div>
          <label class="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-300">
            <span class="whitespace-nowrap">{{ t('admin.gatewayUsage.groupBy') }}</span>
            <select v-model="groupBy" class="input w-48" @change="loadSummary">
              <option v-for="value in groups" :key="value" :value="value">{{ t(`admin.gatewayUsage.groups.${value}`) }}</option>
            </select>
          </label>
        </div>
        <div class="overflow-x-auto">
          <table class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
            <thead class="bg-gray-50 dark:bg-dark-800"><tr>
              <th class="px-4 py-3 text-left text-xs font-semibold text-gray-500">{{ groupHeading }}</th>
              <th v-for="heading in aggregateHeadings" :key="heading" class="px-4 py-3 text-right text-xs font-semibold text-gray-500">{{ heading }}</th>
            </tr></thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
              <tr v-for="item in summary?.items || []" :key="item.key">
                <td class="px-4 py-3 text-sm text-gray-800 dark:text-gray-200">{{ groupLabel(item.key) }}</td>
                <td class="px-4 py-3 text-right text-sm tabular-nums">{{ formatNumber(item.requests) }}</td>
                <td class="px-4 py-3 text-right text-sm tabular-nums">{{ formatNumber(item.usage_records) }}</td>
                <td class="px-4 py-3 text-right text-sm tabular-nums">{{ formatNumber(item.input_tokens + item.output_tokens) }}</td>
                <td class="px-4 py-3 text-right text-sm tabular-nums">{{ formatCost(item.actual_cost) }}</td>
              </tr>
              <tr v-if="!loading && (summary?.items.length || 0) === 0"><td colspan="5" class="px-4 py-8 text-center text-sm text-gray-500">{{ t('admin.gatewayUsage.empty') }}</td></tr>
            </tbody>
          </table>
        </div>
      </section>

      <section class="card overflow-hidden" aria-live="polite">
        <div class="border-b border-gray-200 px-5 py-4 dark:border-dark-700">
          <h2 class="font-semibold text-gray-900 dark:text-white">{{ t('admin.gatewayUsage.detailsTitle') }}</h2>
        </div>
        <div v-if="errorMessage" class="border-b border-red-200 bg-red-50 px-5 py-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300">{{ errorMessage }}</div>
        <div class="overflow-x-auto">
          <table class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
            <thead class="bg-gray-50 dark:bg-dark-800"><tr>
              <th v-for="heading in detailHeadings" :key="heading" class="px-4 py-3 text-left text-xs font-semibold text-gray-500">{{ heading }}</th>
            </tr></thead>
            <tbody class="divide-y divide-gray-100 bg-white dark:divide-dark-800 dark:bg-dark-900">
              <tr v-if="loading"><td colspan="7" class="px-4 py-10 text-center text-sm text-gray-500">{{ t('common.loading') }}</td></tr>
              <tr v-else-if="records.length === 0"><td colspan="7" class="px-4 py-10 text-center text-sm text-gray-500">{{ t('admin.gatewayUsage.empty') }}</td></tr>
              <tr v-for="record in records" v-else :key="record.gateway_request_id" class="align-top hover:bg-gray-50/80 dark:hover:bg-dark-800/60">
                <td class="whitespace-nowrap px-4 py-4 text-sm">{{ formatTime(record.event_time) }}</td>
                <td class="px-4 py-4 text-sm">
                  <div class="font-medium">{{ employeeLabel(record) }}</div>
                  <div class="mt-1 text-xs text-gray-500">{{ record.profile_version || t('admin.gatewayUsage.auditUnavailable') }}</div>
                </td>
                <td class="px-4 py-4 text-sm">
                  <div>{{ record.requested_model || '—' }}</div>
                  <div v-if="record.resolved_model && record.resolved_model !== record.requested_model" class="mt-1 text-xs text-gray-500">→ {{ record.resolved_model }}</div>
                  <div v-if="record.protocol" class="mt-1 text-xs text-gray-500">{{ protocolSummary(record) }}</div>
                </td>
                <td class="min-w-[260px] px-4 py-4 text-xs">
                  <AuditStatePair v-if="record.request_outcome || record.content_state" :outcome="record.request_outcome" :content-state="record.content_state" />
                  <span v-else class="inline-flex min-h-6 items-center gap-1.5 whitespace-nowrap rounded-full border border-rose-200 bg-rose-50 px-2.5 py-1 font-medium leading-none text-rose-700 dark:border-rose-800/80 dark:bg-rose-950/40 dark:text-rose-300">
                    <span class="h-1.5 w-1.5 rounded-full bg-current opacity-70" aria-hidden="true"></span>
                    {{ t('admin.gatewayUsage.auditUnavailable') }}
                  </span>
                </td>
                <td class="px-4 py-4 text-sm">
                  <template v-if="record.usage_present">
                    <div class="tabular-nums">{{ formatNumber(record.total_tokens || 0) }} {{ t('admin.gatewayUsage.tokens') }}</div>
                    <div class="mt-1 text-xs tabular-nums text-gray-500">{{ formatCost(record.actual_cost || 0) }} · {{ t('admin.gatewayUsage.account') }} {{ record.account_id ?? '—' }}</div>
                  </template>
                  <span v-else class="inline-flex min-h-6 items-center gap-1.5 whitespace-nowrap rounded-full border border-amber-200 bg-amber-50 px-2.5 py-1 text-xs font-medium leading-none text-amber-800 dark:border-amber-800/80 dark:bg-amber-950/40 dark:text-amber-300">
                    {{ t('admin.gatewayUsage.noUsage') }}
                    <HelpTooltip :content="t('admin.gatewayUsage.noUsageHint')" width-class="w-72" data-testid="gateway-usage-missing-hint">
                      <template #trigger>
                        <span class="inline-flex size-4 cursor-help items-center justify-center rounded-full border border-current text-[10px] leading-none" role="img" tabindex="0" :aria-label="t('admin.gatewayUsage.noUsageHint')">i</span>
                      </template>
                    </HelpTooltip>
                  </span>
                </td>
                <td class="px-4 py-4"><code class="block max-w-[220px] truncate text-xs text-gray-600 dark:text-gray-400" :title="record.gateway_request_id">{{ record.gateway_request_id }}</code></td>
                <td class="whitespace-nowrap px-4 py-4 text-right text-sm">
                  <button type="button" class="btn btn-secondary btn-sm" data-testid="open-request-detail" @click="openDetails(record)">{{ t('admin.gatewayUsage.viewDetails') }}</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <Pagination v-if="total > 0" :total="total" :page="page" :page-size="pageSize" @update:page="changePage" @update:page-size="changePageSize" />
      </section>
    </div>

    <GatewayRequestDetailDrawer
      :record="selectedRecord"
      :audit="selectedAudit"
      :audit-loading="auditLoading"
      :audit-error="auditError"
      :can-disclose="canDisclose"
      @close="closeDetails"
      @disclose="openDisclosure"
    />

    <Teleport to="body">
      <div v-if="disclosureRecord" class="fixed inset-0 z-[60] flex items-center justify-center p-4" role="dialog" aria-modal="true" :aria-label="t('admin.audit.disclosureTitle')">
        <div class="absolute inset-0 bg-black/65" @click="closeDisclosure"></div>
        <div class="relative max-h-[92vh] w-full max-w-5xl overflow-y-auto rounded-2xl bg-white shadow-2xl dark:bg-dark-900">
          <div class="sticky top-0 z-10 flex items-start justify-between border-b border-gray-200 bg-white px-6 py-5 dark:border-dark-700 dark:bg-dark-900">
            <div>
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.audit.disclosureTitle') }}</h2>
              <p class="mt-1 font-mono text-xs text-gray-500">{{ disclosureRecord.gateway_request_id }}</p>
            </div>
            <button type="button" class="btn btn-ghost btn-sm" @click="closeDisclosure">{{ t('common.close') }}</button>
          </div>
          <div v-if="!disclosure" class="space-y-5 p-6">
            <div class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-800 dark:border-red-900 dark:bg-red-950/30 dark:text-red-200">{{ t('admin.audit.disclosureWarning') }}</div>
            <p v-if="disclosing" class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.audit.loadingRaw') }}</p>
            <p v-if="disclosureError" class="text-sm text-red-600 dark:text-red-400">{{ disclosureError }}</p>
            <div class="flex justify-end gap-3">
              <button type="button" class="btn btn-secondary" @click="closeDisclosure">{{ t('common.close') }}</button>
              <button v-if="disclosureError" type="button" class="btn btn-primary" :disabled="disclosing" @click="submitDisclosure">{{ t('admin.audit.retryView') }}</button>
            </div>
          </div>
          <div v-else class="space-y-5 p-6">
            <div class="rounded-xl border border-emerald-200 bg-emerald-50 p-4 text-sm text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950/30 dark:text-emerald-200">{{ t('admin.audit.disclosureRecorded', { id: disclosure.operation_id }) }}</div>
            <article v-for="part in disclosure.parts" :key="`${part.direction}-${part.sequence}`" class="overflow-hidden rounded-xl border border-gray-200 dark:border-dark-700">
              <header class="flex items-center justify-between bg-gray-50 px-4 py-3 text-xs font-semibold text-gray-600 dark:bg-dark-800 dark:text-gray-300">
                <span>{{ directionLabel(part.direction) }}</span>
                <span>#{{ part.sequence }}</span>
              </header>
              <pre class="max-h-[420px] overflow-auto whitespace-pre-wrap break-words bg-white p-4 text-xs leading-6 text-gray-900 dark:bg-dark-950 dark:text-gray-100">{{ part.content }}</pre>
            </article>
            <div class="flex justify-end"><button type="button" class="btn btn-primary" @click="closeDisclosure">{{ t('common.close') }}</button></div>
          </div>
        </div>
      </div>
    </Teleport>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import AuditStatePair from '@/components/admin/AuditStatePair.vue'
import GatewayRequestDetailDrawer from '@/components/admin/GatewayRequestDetailDrawer.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import Pagination from '@/components/common/Pagination.vue'
import { adminAuditAPI, adminGatewayUsageAPI } from '@/api/admin'
import { useAuthStore } from '@/stores/auth'
import type { AuditContentState, AuditDisclosureResult, AuditMetadataRecord, AuditOutcome, AuditProtocol } from '@/api/admin/audit'
import type { GatewayUsageGroup, GatewayUsageQuery, GatewayUsageRecord, GatewayUsageResult, GatewayUsageSummary } from '@/api/admin/gatewayUsage'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const canDisclose = computed(() => authStore.isSuperAdmin)
const results: GatewayUsageResult[] = ['normal_usage', 'no_usage', 'audit_failed', 'rejected_pre_upstream']
const outcomes: AuditOutcome[] = ['processing', 'rejected_pre_upstream', 'completed', 'upstream_failed', 'interrupted']
const contentStates: AuditContentState[] = ['recording', 'complete', 'incomplete', 'expired']
const groups: GatewayUsageGroup[] = ['time', 'employee', 'profile', 'model', 'result']
const groupBy = ref<GatewayUsageGroup>('time')
const groupDescription = computed(() => t(`admin.gatewayUsage.groupDescriptions.${groupBy.value}`))
const groupHeading = computed(() => t(`admin.gatewayUsage.groupHeadings.${groupBy.value}`))
const aggregateHeadings = computed(() => [t('admin.gatewayUsage.requests'), t('admin.gatewayUsage.usageRecords'), t('admin.gatewayUsage.tokens'), t('admin.gatewayUsage.actualCost')])
const detailHeadings = computed(() => [t('admin.gatewayUsage.time'), t('admin.gatewayUsage.employeeProfile'), t('admin.gatewayUsage.modelProtocol'), t('admin.gatewayUsage.requestState'), t('admin.gatewayUsage.usage'), t('admin.gatewayUsage.gatewayId'), t('common.actions')])

type FilterForm = {
  employee: string
  profile: string
  protocol: AuditProtocol | ''
  model: string
  result: GatewayUsageResult | ''
  outcome: AuditOutcome | ''
  content_state: AuditContentState | ''
  from: string
  to: string
  gateway_request_id: string
}

const emptyFilters = (): FilterForm => ({ employee: '', profile: '', protocol: '', model: '', result: '', outcome: '', content_state: '', from: '', to: '', gateway_request_id: '' })
const filters = reactive<FilterForm>(emptyFilters())
const records = ref<GatewayUsageRecord[]>([])
const summary = ref<GatewayUsageSummary | null>(null)
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const loading = ref(false)
const errorMessage = ref('')

const selectedRecord = ref<GatewayUsageRecord | null>(null)
const selectedAudit = ref<AuditMetadataRecord | null>(null)
const auditLoading = ref(false)
const auditError = ref('')
let detailAttempt = 0
let autoOpenedGatewayID = ''

const disclosureRecord = ref<AuditMetadataRecord | null>(null)
const disclosing = ref(false)
const disclosureError = ref('')
const disclosure = ref<AuditDisclosureResult | null>(null)
let disclosureAttempt = 0

const resultTones: Record<GatewayUsageResult, string> = {
  normal_usage: 'bg-emerald-500',
  no_usage: 'bg-amber-500',
  audit_failed: 'bg-rose-500',
  rejected_pre_upstream: 'bg-sky-500'
}

const resultCards = computed(() => results.map((key) => ({
  key,
  label: resultLabel(key),
  tone: resultTones[key],
  value: formatNumber(summary.value?.totals[`${key}_requests` as keyof GatewayUsageSummary['totals']] as number || 0)
})))

function toISO(value: string): string | undefined {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString()
}

function buildQuery(): GatewayUsageQuery {
  return {
    employee: filters.employee || undefined,
    profile: filters.profile || undefined,
    protocol: filters.protocol || undefined,
    model: filters.model || undefined,
    result: filters.result || undefined,
    outcome: filters.outcome || undefined,
    content_state: filters.content_state || undefined,
    from: toISO(filters.from),
    to: toISO(filters.to),
    gateway_request_id: filters.gateway_request_id || undefined,
    page: page.value,
    page_size: pageSize.value
  }
}

async function loadRecords() {
  loading.value = true
  errorMessage.value = ''
  try {
    const query = buildQuery()
    const [pageResult, summaryResult] = await Promise.all([
      adminGatewayUsageAPI.list(query),
      adminGatewayUsageAPI.summary(query, groupBy.value)
    ])
    records.value = pageResult.items
    total.value = pageResult.total
    summary.value = summaryResult
    if (filters.gateway_request_id && autoOpenedGatewayID !== filters.gateway_request_id) {
      const exact = pageResult.items.find((item) => item.gateway_request_id === filters.gateway_request_id)
      if (exact) {
        autoOpenedGatewayID = filters.gateway_request_id
        void openDetails(exact)
      }
    }
  } catch (error) {
    records.value = []
    total.value = 0
    summary.value = null
    errorMessage.value = (error as { message?: string })?.message || t('admin.gatewayUsage.loadFailed')
  } finally {
    loading.value = false
  }
}

async function loadSummary() {
  try {
    summary.value = await adminGatewayUsageAPI.summary(buildQuery(), groupBy.value)
  } catch (error) {
    errorMessage.value = (error as { message?: string })?.message || t('admin.gatewayUsage.loadFailed')
  }
}

function applyFilters() {
  page.value = 1
  closeDetails()
  autoOpenedGatewayID = ''
  void router.replace({ query: filters.gateway_request_id ? { gateway_request_id: filters.gateway_request_id } : {} })
  void loadRecords()
}

function resetFilters() {
  Object.assign(filters, emptyFilters())
  autoOpenedGatewayID = ''
  closeDetails()
  void router.replace({ query: {} })
  void loadRecords()
}

function changePage(value: number) { page.value = value; closeDetails(); void loadRecords() }
function changePageSize(value: number) { pageSize.value = value; page.value = 1; closeDetails(); void loadRecords() }
function resultLabel(value: GatewayUsageResult): string { return t(`admin.gatewayUsage.results.${value}`) }
function outcomeLabel(value: AuditOutcome): string { return t(`admin.audit.outcomes.${value}`) }
function contentStateLabel(value: AuditContentState): string { return t(`admin.audit.contentStates.${value}`) }
function directionLabel(value: 'request' | 'response'): string { return t(`admin.audit.directions.${value}`) }
function groupLabel(value: string): string { return groupBy.value === 'result' && results.includes(value as GatewayUsageResult) ? resultLabel(value as GatewayUsageResult) : value }
function employeeLabel(record: GatewayUsageRecord): string { return record.subject_email_snapshot || (record.subject_user_id ? `#${record.subject_user_id}` : '—') }
function protocolSummary(record: GatewayUsageRecord): string {
  const protocol = record.protocol === 'openai' ? 'OpenAI' : record.protocol === 'anthropic' ? 'Anthropic' : ''
  if (record.transport !== 'sse') return protocol
  return `${protocol} · ${t('admin.audit.transports.sse')}`
}
function formatTime(value: string): string { return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value)) }
function formatNumber(value: number): string { return new Intl.NumberFormat().format(value) }
function formatCost(value: number): string { return `$${value.toFixed(6)}` }

async function openDetails(record: GatewayUsageRecord) {
  const attempt = ++detailAttempt
  selectedRecord.value = record
  selectedAudit.value = null
  auditError.value = ''
  auditLoading.value = record.audit_present
  if (!record.audit_present) return
  try {
    const result = await adminAuditAPI.list({ gateway_request_id: record.gateway_request_id, page: 1, page_size: 1 })
    if (attempt !== detailAttempt || selectedRecord.value?.gateway_request_id !== record.gateway_request_id) return
    selectedAudit.value = result.items.find((item) => item.id === record.audit_interaction_id) || result.items[0] || null
    if (!selectedAudit.value) auditError.value = t('admin.gatewayUsage.auditLoadFailed')
  } catch (error) {
    if (attempt === detailAttempt) auditError.value = (error as { message?: string })?.message || t('admin.gatewayUsage.auditLoadFailed')
  } finally {
    if (attempt === detailAttempt) auditLoading.value = false
  }
}

function closeDetails() {
  detailAttempt += 1
  selectedRecord.value = null
  selectedAudit.value = null
  auditLoading.value = false
  auditError.value = ''
}

function openDisclosure(record: AuditMetadataRecord) {
  disclosureRecord.value = record
  disclosureError.value = ''
  disclosure.value = null
  void submitDisclosure()
}

function closeDisclosure() {
  if (disclosure.value) disclosure.value.parts.forEach((part) => { part.content = '' })
  disclosure.value = null
  disclosureRecord.value = null
  disclosureAttempt += 1
  disclosing.value = false
  disclosureError.value = ''
}

async function submitDisclosure() {
  const interactionID = disclosureRecord.value?.id
  if (!interactionID || disclosing.value) return
  const attempt = ++disclosureAttempt
  disclosing.value = true
  disclosureError.value = ''
  try {
    const result = await adminAuditAPI.disclose(interactionID)
    if (attempt !== disclosureAttempt || disclosureRecord.value?.id !== interactionID) {
      result.parts.forEach((part) => { part.content = '' })
      return
    }
    disclosure.value = result
  } catch (error) {
    if (attempt === disclosureAttempt) disclosureError.value = (error as { message?: string })?.message || t('admin.audit.disclosureFailed')
  } finally {
    if (attempt === disclosureAttempt) disclosing.value = false
  }
}

onMounted(() => {
  filters.gateway_request_id = typeof route.query.gateway_request_id === 'string' ? route.query.gateway_request_id : ''
  void loadRecords()
})
</script>
