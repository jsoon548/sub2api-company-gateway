<template>
  <AppLayout>
    <div class="mx-auto max-w-[1600px] space-y-6">
      <header class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ t('admin.audit.title') }}</h1>
          <p class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ t('admin.audit.description') }}</p>
        </div>
      </header>

      <section class="card p-5" aria-labelledby="audit-filters-title">
        <div class="mb-4 flex items-center justify-between gap-3">
          <h2 id="audit-filters-title" class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.audit.filters') }}
          </h2>
          <button type="button" class="btn btn-ghost btn-sm" @click="resetFilters">
            {{ t('common.reset') }}
          </button>
        </div>
        <form class="grid gap-4 md:grid-cols-2 xl:grid-cols-4" @submit.prevent="applyFilters">
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-gray-300">
            <span>{{ t('admin.audit.employee') }}</span>
            <input v-model.trim="filters.employee" class="input w-full" :placeholder="t('admin.audit.employeePlaceholder')" />
          </label>
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-gray-300">
            <span>{{ t('admin.audit.gatewayId') }}</span>
            <input v-model.trim="filters.gateway_request_id" class="input w-full font-mono text-xs" :placeholder="t('admin.audit.gatewayIdPlaceholder')" />
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
            <span>{{ t('admin.audit.model') }}</span>
            <input v-model.trim="filters.model" class="input w-full" :placeholder="t('admin.audit.modelPlaceholder')" />
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
            <span>{{ t('admin.audit.from') }}</span>
            <input v-model="filters.from" type="datetime-local" class="input w-full" />
          </label>
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-gray-300">
            <span>{{ t('admin.audit.to') }}</span>
            <input v-model="filters.to" type="datetime-local" class="input w-full" />
          </label>
          <div class="md:col-span-2 xl:col-span-4">
            <button type="submit" class="btn btn-primary" :disabled="loading">
              {{ loading ? t('common.loading') : t('common.search') }}
            </button>
          </div>
        </form>
      </section>

      <section class="card overflow-hidden" aria-live="polite">
        <div v-if="errorMessage" class="border-b border-red-200 bg-red-50 px-5 py-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300">
          {{ errorMessage }}
        </div>
        <div class="overflow-x-auto">
          <table class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
            <thead class="bg-gray-50 dark:bg-dark-800">
              <tr>
                <th v-for="heading in headings" :key="heading" class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
                  {{ heading }}
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 bg-white dark:divide-dark-800 dark:bg-dark-900">
              <tr v-if="loading">
                <td colspan="7" class="px-4 py-12 text-center text-sm text-gray-500">{{ t('common.loading') }}</td>
              </tr>
              <tr v-else-if="records.length === 0">
                <td colspan="7" class="px-4 py-12 text-center text-sm text-gray-500">{{ t('admin.audit.empty') }}</td>
              </tr>
              <tr v-for="record in records" v-else :key="record.id" class="align-top hover:bg-gray-50/80 dark:hover:bg-dark-800/60">
                <td class="whitespace-nowrap px-4 py-4 text-sm text-gray-700 dark:text-gray-300">
                  <div>{{ formatTime(record.admitted_at) }}</div>
                  <div class="mt-1 text-xs text-gray-500">{{ t('admin.audit.expires') }} {{ formatTime(record.expires_at) }}</div>
                </td>
                <td class="px-4 py-4 text-sm">
                  <div class="font-medium text-gray-900 dark:text-white">{{ record.subject_email_snapshot || '—' }}</div>
                  <div class="mt-1 text-xs text-gray-500">{{ t('admin.audit.userId') }} {{ record.subject_user_id ?? '—' }}</div>
                </td>
                <td class="px-4 py-4 text-sm text-gray-700 dark:text-gray-300">
                  <div class="font-medium">{{ record.protocol }} · {{ transportLabel(record.transport) }}</div>
                  <div class="mt-1 max-w-[260px] truncate font-mono text-xs text-gray-500" :title="record.endpoint">{{ record.endpoint }}</div>
                </td>
                <td class="px-4 py-4 text-sm text-gray-700 dark:text-gray-300">
                  <div>{{ record.requested_model || '—' }}</div>
                  <div v-if="record.resolved_model && record.resolved_model !== record.requested_model" class="mt-1 text-xs text-gray-500">
                    → {{ record.resolved_model }}
                  </div>
                </td>
                <td class="min-w-[260px] px-4 py-4 text-xs">
                  <AuditStatePair :outcome="record.request_outcome" :content-state="record.content_state" />
                </td>
                <td class="px-4 py-4">
                  <code class="block max-w-[220px] truncate text-xs text-gray-600 dark:text-gray-400" :title="record.gateway_request_id">{{ record.gateway_request_id }}</code>
                </td>
                <td class="whitespace-nowrap px-4 py-4 text-right text-sm">
                  <div class="flex justify-end gap-2">
                    <button type="button" class="btn btn-secondary btn-sm" @click="openUsage(record.gateway_request_id)">{{ t('admin.audit.viewUsage') }}</button>
                    <button v-if="canDisclose" type="button" class="btn btn-secondary btn-sm" :disabled="!isDisclosable(record)" @click="openDisclosure(record)">
                      {{ t('admin.audit.viewRaw') }}
                    </button>
                    <span v-else class="self-center text-xs text-gray-500">{{ t('admin.audit.superAdminOnly') }}</span>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <Pagination
          v-if="total > 0"
          :total="total"
          :page="page"
          :page-size="pageSize"
          @update:page="changePage"
          @update:page-size="changePageSize"
        />
      </section>
    </div>

    <Teleport to="body">
      <div v-if="selectedRecord" class="fixed inset-0 z-50 flex items-center justify-center p-4" role="dialog" aria-modal="true" :aria-label="t('admin.audit.disclosureTitle')">
        <div class="absolute inset-0 bg-black/60" @click="closeDisclosure"></div>
        <div class="relative max-h-[92vh] w-full max-w-5xl overflow-y-auto rounded-2xl bg-white shadow-2xl dark:bg-dark-900">
          <div class="sticky top-0 z-10 flex items-start justify-between border-b border-gray-200 bg-white px-6 py-5 dark:border-dark-700 dark:bg-dark-900">
            <div>
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.audit.disclosureTitle') }}</h2>
              <p class="mt-1 font-mono text-xs text-gray-500">{{ selectedRecord.gateway_request_id }}</p>
            </div>
            <button type="button" class="btn btn-ghost btn-sm" @click="closeDisclosure">{{ t('common.close') }}</button>
          </div>

          <div v-if="!disclosure" class="space-y-5 p-6">
            <div class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-800 dark:border-red-900 dark:bg-red-950/30 dark:text-red-200">
              {{ t('admin.audit.disclosureWarning') }}
            </div>
            <p v-if="disclosing" class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.audit.loadingRaw') }}</p>
            <p v-if="disclosureError" class="text-sm text-red-600 dark:text-red-400">{{ disclosureError }}</p>
            <div class="flex justify-end gap-3">
              <button type="button" class="btn btn-secondary" @click="closeDisclosure">{{ t('common.close') }}</button>
              <button v-if="disclosureError" type="button" class="btn btn-primary" :disabled="disclosing" @click="submitDisclosure">
                {{ t('admin.audit.retryView') }}
              </button>
            </div>
          </div>

          <div v-else class="space-y-5 p-6">
            <div class="rounded-xl border border-emerald-200 bg-emerald-50 p-4 text-sm text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950/30 dark:text-emerald-200">
              {{ t('admin.audit.disclosureRecorded', { id: disclosure.operation_id }) }}
            </div>
            <article v-for="part in disclosure.parts" :key="`${part.direction}-${part.sequence}`" class="overflow-hidden rounded-xl border border-gray-200 dark:border-dark-700">
              <header class="flex items-center justify-between bg-gray-50 px-4 py-3 text-xs font-semibold uppercase tracking-wide text-gray-600 dark:bg-dark-800 dark:text-gray-300">
                <span>{{ directionLabel(part.direction) }}</span>
                <span>#{{ part.sequence }}</span>
              </header>
              <pre class="max-h-[420px] overflow-auto whitespace-pre-wrap break-words bg-white p-4 text-xs leading-6 text-gray-900 dark:bg-dark-950 dark:text-gray-100">{{ part.content }}</pre>
            </article>
            <div class="flex justify-end">
              <button type="button" class="btn btn-primary" @click="closeDisclosure">{{ t('common.close') }}</button>
            </div>
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
import AuditStatePair from '@/components/admin/AuditStatePair.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import { useAuthStore } from '@/stores/auth'
import { adminAuditAPI } from '@/api/admin'
import type {
  AuditContentState,
  AuditDisclosureResult,
  AuditMetadataQuery,
  AuditMetadataRecord,
  AuditOutcome,
  AuditProtocol
} from '@/api/admin/audit'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const canDisclose = computed(() => authStore.isSuperAdmin)

const outcomes: AuditOutcome[] = ['processing', 'rejected_pre_upstream', 'completed', 'upstream_failed', 'interrupted']
const contentStates: AuditContentState[] = ['recording', 'complete', 'incomplete', 'expired']
const headings = computed(() => [
  t('admin.audit.time'),
  t('admin.audit.employee'),
  t('admin.audit.protocol'),
  t('admin.audit.model'),
  t('admin.audit.state'),
  t('admin.audit.gatewayId'),
  t('common.actions')
])

type FilterForm = {
  employee: string
  gateway_request_id: string
  protocol: AuditProtocol | ''
  model: string
  outcome: AuditOutcome | ''
  content_state: AuditContentState | ''
  from: string
  to: string
}

const emptyFilters = (): FilterForm => ({
  employee: '', gateway_request_id: '', protocol: '', model: '', outcome: '', content_state: '', from: '', to: ''
})
const filters = reactive<FilterForm>(emptyFilters())
const records = ref<AuditMetadataRecord[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const loading = ref(false)
const errorMessage = ref('')

const selectedRecord = ref<AuditMetadataRecord | null>(null)
const disclosing = ref(false)
const disclosureError = ref('')
const disclosure = ref<AuditDisclosureResult | null>(null)
let disclosureAttempt = 0

function toISO(value: string): string | undefined {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString()
}

function buildQuery(): AuditMetadataQuery {
  return {
    employee: filters.employee || undefined,
    gateway_request_id: filters.gateway_request_id || undefined,
    protocol: filters.protocol || undefined,
    model: filters.model || undefined,
    outcome: filters.outcome || undefined,
    content_state: filters.content_state || undefined,
    from: toISO(filters.from),
    to: toISO(filters.to),
    page: page.value,
    page_size: pageSize.value
  }
}

async function loadRecords() {
  loading.value = true
  errorMessage.value = ''
  try {
    const result = await adminAuditAPI.list(buildQuery())
    records.value = result.items
    total.value = result.total
  } catch (error) {
    records.value = []
    total.value = 0
    errorMessage.value = (error as { message?: string })?.message || t('admin.audit.loadFailed')
  } finally {
    loading.value = false
  }
}

function applyFilters() {
  page.value = 1
  void loadRecords()
}

function resetFilters() {
  Object.assign(filters, emptyFilters())
  applyFilters()
}

function changePage(value: number) {
  page.value = value
  void loadRecords()
}

function changePageSize(value: number) {
  pageSize.value = value
  page.value = 1
  void loadRecords()
}

function formatTime(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

function outcomeLabel(value: AuditOutcome): string {
  return t(`admin.audit.outcomes.${value}`)
}

function contentStateLabel(value: AuditContentState): string {
  return t(`admin.audit.contentStates.${value}`)
}

function transportLabel(value: AuditMetadataRecord['transport']): string {
  return t(`admin.audit.transports.${value}`)
}

function directionLabel(value: 'request' | 'response'): string {
  return t(`admin.audit.directions.${value}`)
}

function isDisclosable(record: AuditMetadataRecord): boolean {
  return ['complete', 'incomplete'].includes(record.content_state) && new Date(record.expires_at).getTime() > Date.now()
}

function openDisclosure(record: AuditMetadataRecord) {
  selectedRecord.value = record
  disclosureError.value = ''
  disclosure.value = null
  void submitDisclosure()
}

function openUsage(gatewayRequestId: string) {
  void router.push({ name: 'AdminGatewayUsage', query: { gateway_request_id: gatewayRequestId } })
}

function closeDisclosure() {
  if (disclosure.value) {
    disclosure.value.parts.forEach((part) => { part.content = '' })
  }
  disclosure.value = null
  selectedRecord.value = null
  disclosureAttempt += 1
  disclosing.value = false
  disclosureError.value = ''
}

async function submitDisclosure() {
  const interactionID = selectedRecord.value?.id
  if (!interactionID || disclosing.value) return
  const attempt = ++disclosureAttempt
  disclosing.value = true
  disclosureError.value = ''
  try {
    const result = await adminAuditAPI.disclose(interactionID)
    if (attempt !== disclosureAttempt || selectedRecord.value?.id !== interactionID) {
      result.parts.forEach((part) => { part.content = '' })
      return
    }
    disclosure.value = result
  } catch (error) {
    if (attempt === disclosureAttempt) {
      disclosureError.value = (error as { message?: string })?.message || t('admin.audit.disclosureFailed')
    }
  } finally {
    if (attempt === disclosureAttempt) {
      disclosing.value = false
    }
  }
}

onMounted(() => {
  filters.gateway_request_id = typeof route.query.gateway_request_id === 'string' ? route.query.gateway_request_id : ''
  void loadRecords()
})
</script>
