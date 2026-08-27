<template>
  <div
    class="flex min-w-0 flex-wrap items-center gap-1.5"
    data-testid="audit-state-pair"
    :aria-label="pairLabel"
  >
    <span
      v-if="outcome"
      :class="[badgeClass, outcomeTone[outcome]]"
      data-testid="audit-outcome-badge"
    >
      <span class="h-1.5 w-1.5 shrink-0 rounded-full bg-current opacity-70" aria-hidden="true"></span>
      {{ outcomeLabel(outcome) }}
    </span>
    <span
      v-if="contentState"
      :class="[badgeClass, contentStateTone[contentState]]"
      data-testid="audit-content-state-badge"
    >
      <span class="h-1.5 w-1.5 shrink-0 rounded-[2px] bg-current opacity-60" aria-hidden="true"></span>
      {{ contentStateLabel(contentState) }}
    </span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AuditContentState, AuditOutcome } from '@/api/admin/audit'

const props = defineProps<{
  outcome?: AuditOutcome
  contentState?: AuditContentState
}>()

const { t } = useI18n()
const badgeClass = 'inline-flex min-h-6 items-center gap-1.5 whitespace-nowrap rounded-full border px-2.5 py-1 text-xs font-medium leading-none'

const outcomeTone: Record<AuditOutcome, string> = {
  processing: 'border-sky-200 bg-sky-50 text-sky-700 dark:border-sky-800/80 dark:bg-sky-950/40 dark:text-sky-300',
  rejected_pre_upstream: 'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-800/80 dark:bg-amber-950/40 dark:text-amber-300',
  completed: 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-800/80 dark:bg-emerald-950/40 dark:text-emerald-300',
  upstream_failed: 'border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-800/80 dark:bg-rose-950/40 dark:text-rose-300',
  interrupted: 'border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-800/80 dark:bg-rose-950/40 dark:text-rose-300'
}

const contentStateTone: Record<AuditContentState, string> = {
  recording: 'border-sky-200 bg-sky-50/50 text-sky-700 dark:border-sky-800/70 dark:bg-sky-950/25 dark:text-sky-300',
  complete: 'border-indigo-200 bg-indigo-50/60 text-indigo-700 dark:border-indigo-800/70 dark:bg-indigo-950/30 dark:text-indigo-300',
  incomplete: 'border-amber-200 bg-amber-50/60 text-amber-800 dark:border-amber-800/70 dark:bg-amber-950/30 dark:text-amber-300',
  expired: 'border-gray-200 bg-gray-50 text-gray-600 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300'
}

function outcomeLabel(value: AuditOutcome): string {
  return t(`admin.audit.outcomes.${value}`)
}

function contentStateLabel(value: AuditContentState): string {
  return t(`admin.audit.contentStates.${value}`)
}

const pairLabel = computed(() => [
  props.outcome ? outcomeLabel(props.outcome) : '',
  props.contentState ? contentStateLabel(props.contentState) : ''
].filter(Boolean).join('，'))
</script>
