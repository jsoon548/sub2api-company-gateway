<template>
  <AppLayout>
    <div class="mx-auto max-w-[1500px] space-y-6">
      <div v-if="message" class="rounded-xl border px-4 py-3 text-sm" :class="messageKind === 'error' ? 'border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300' : 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/30 dark:text-emerald-300'">
        {{ message }}
      </div>

      <section class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4" :aria-label="t('admin.workSession.statusArea')">
        <div v-for="item in statusItems" :key="item.label" class="card p-4">
          <div class="flex items-center gap-1.5">
            <div class="text-xs font-medium uppercase tracking-wide text-gray-500">{{ item.label }}</div>
            <HelpTooltip v-if="item.help" :content="item.help" width-class="w-80">
              <template #trigger>
                <button
                  type="button"
                  class="rounded-full text-gray-400 outline-none transition-colors hover:text-primary-600 focus-visible:ring-2 focus-visible:ring-primary-500 focus-visible:ring-offset-2 dark:text-gray-500 dark:hover:text-primary-400 dark:focus-visible:ring-offset-dark-900"
                  :aria-label="item.helpLabel || item.label"
                  :data-test="item.helpTest"
                >
                  <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" aria-hidden="true">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                  </svg>
                </button>
              </template>
            </HelpTooltip>
          </div>
          <div class="mt-2 break-words font-mono text-sm font-semibold text-gray-900 dark:text-white">{{ item.value }}</div>
        </div>
      </section>

      <form class="card space-y-5 p-5" @submit.prevent="save">
        <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div>
            <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.workSession.autoTitle') }}</h2>
            <p class="mt-1 text-sm text-gray-500">{{ t('admin.workSession.autoBoundaryNote') }}</p>
          </div>
          <label
            class="flex min-w-[260px] cursor-pointer items-center gap-3 rounded-xl border px-3.5 py-2.5 transition-colors"
            :class="[
              form.auto_enabled
                ? 'border-emerald-200 bg-emerald-50 dark:border-emerald-800 dark:bg-emerald-950/30'
                : 'border-gray-200 bg-gray-50 dark:border-dark-600 dark:bg-dark-800',
              loading ? 'cursor-not-allowed opacity-60' : ''
            ]"
            data-test="auto-control"
          >
            <input
              v-model="form.auto_enabled"
              type="checkbox"
              role="switch"
              class="sr-only"
              :disabled="loading"
              :aria-label="t('admin.workSession.autoEnabled')"
              data-test="auto-enabled"
            />
            <span class="h-2.5 w-2.5 shrink-0 rounded-full" :class="form.auto_enabled ? 'bg-emerald-500' : 'bg-gray-400 dark:bg-gray-500'"></span>
            <span class="min-w-0 flex-1">
              <span class="block text-sm font-semibold text-gray-900 dark:text-white" data-test="auto-status">
                {{ form.auto_enabled ? t('admin.workSession.autoOn') : t('admin.workSession.autoOff') }}
              </span>
              <span class="mt-0.5 block text-xs text-gray-500 dark:text-gray-400">
                {{ form.auto_enabled ? t('admin.workSession.autoOnNote') : t('admin.workSession.autoOffNote') }}
              </span>
            </span>
            <span class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors" :class="form.auto_enabled ? 'bg-emerald-500' : 'bg-gray-300 dark:bg-gray-600'" aria-hidden="true">
              <span class="inline-block h-5 w-5 rounded-full bg-white shadow-sm transition-transform" :class="form.auto_enabled ? 'translate-x-5' : 'translate-x-0.5'"></span>
            </span>
          </label>
        </div>

        <div class="grid gap-4 md:grid-cols-2">
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-gray-300">
            <span>{{ t('admin.workSession.userWhitelist') }}</span>
            <input v-model="userWhitelist" class="input w-full" placeholder="12, 42" data-test="user-whitelist" />
          </label>
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-gray-300">
            <span>{{ t('admin.workSession.groupWhitelist') }}</span>
            <input v-model="groupWhitelist" class="input w-full" placeholder="3, 9" data-test="group-whitelist" />
          </label>
        </div>

        <div class="grid gap-4 xl:grid-cols-2">
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-gray-300">
            <span>{{ t('admin.workSession.catalogJson') }}</span>
            <textarea v-model="catalogJSON" rows="13" class="input w-full font-mono text-xs" data-test="catalog-json"></textarea>
          </label>
          <label class="space-y-1.5 text-sm text-gray-700 dark:text-gray-300">
            <span>{{ t('admin.workSession.poolsJson') }}</span>
            <textarea v-model="poolsJSON" rows="13" class="input w-full font-mono text-xs" data-test="pools-json"></textarea>
          </label>
        </div>

        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" :disabled="loading" @click="load">{{ t('common.refresh') }}</button>
          <button type="submit" class="btn btn-primary" :disabled="loading" data-test="save-config">{{ loading ? t('common.saving') : t('common.save') }}</button>
        </div>
      </form>

      <section class="card overflow-hidden" data-test="emergency-models">
        <div class="border-b border-gray-200 px-5 py-4 dark:border-dark-700">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.workSession.emergencyTitle') }}</h2>
          <p class="mt-1 text-sm text-gray-500">{{ t('admin.workSession.emergencyNote') }}</p>
        </div>
        <div class="overflow-x-auto">
          <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
            <thead class="bg-gray-50 text-left text-xs uppercase tracking-wide text-gray-500 dark:bg-dark-800">
              <tr><th class="px-4 py-3">{{ t('admin.workSession.model') }}</th><th class="px-4 py-3">{{ t('admin.workSession.tier') }}</th><th class="px-4 py-3">{{ t('admin.workSession.capabilities') }}</th><th class="px-4 py-3 text-right">{{ t('common.actions') }}</th></tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
              <tr v-for="entry in state?.catalog || []" :key="entry.id">
                <td class="px-4 py-3"><div class="font-medium text-gray-900 dark:text-white">{{ entry.logical_model }}</div><div class="text-xs text-gray-500">{{ entry.provider_model }}</div></td>
                <td class="px-4 py-3 text-gray-700 dark:text-gray-300">{{ tierLabel(entry.tier) }}</td>
                <td class="px-4 py-3 text-gray-700 dark:text-gray-300">{{ entry.capabilities.join(', ') || '—' }}</td>
                <td class="px-4 py-3 text-right"><button type="button" class="btn btn-sm" :class="entry.emergency_disabled ? 'btn-secondary' : 'btn-danger'" :disabled="loading" :data-test="`emergency-${entry.logical_model}`" @click="setEmergency(entry.logical_model, !entry.emergency_disabled)">{{ entry.emergency_disabled ? t('admin.workSession.restoreModel') : t('admin.workSession.disableModel') }}</button></td>
              </tr>
              <tr v-if="!state?.catalog.length"><td colspan="4" class="px-4 py-10 text-center text-gray-500">{{ t('common.noData') }}</td></tr>
            </tbody>
          </table>
        </div>
      </section>

      <section class="card overflow-hidden" data-test="recent-sessions">
        <div class="border-b border-gray-200 px-5 py-4 dark:border-dark-700">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.workSession.recentSessions') }}</h2>
          <p class="mt-1 text-sm text-gray-500">{{ t('admin.workSession.noRawSignal') }}</p>
        </div>
        <div class="overflow-x-auto">
          <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
            <thead class="bg-gray-50 text-left text-xs uppercase tracking-wide text-gray-500 dark:bg-dark-800"><tr><th class="px-4 py-3">ID</th><th class="px-4 py-3">{{ t('admin.workSession.employee') }}</th><th class="px-4 py-3">{{ t('admin.workSession.profileSignal') }}</th><th class="px-4 py-3">{{ t('admin.workSession.reliability') }}</th><th class="px-4 py-3">{{ t('admin.workSession.sessionConfig') }}</th><th class="px-4 py-3">{{ t('admin.workSession.lastActivity') }}</th></tr></thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
              <tr v-for="session in state?.recent_sessions || []" :key="session.id">
                <td class="px-4 py-3 font-mono text-xs text-gray-600 dark:text-gray-400">{{ session.id }}</td>
                <td class="px-4 py-3">{{ session.employee_user_id }}</td>
                <td class="px-4 py-3"><div>{{ session.profile_version }}</div><div class="text-xs text-gray-500">{{ session.signal_source }} · {{ signalStatusLabel(session.signal_status) }}</div></td>
                <td class="px-4 py-3"><span class="rounded-full px-2 py-1 text-xs font-medium" :class="session.reliability === 'reliable' ? 'bg-emerald-100 text-emerald-700' : 'bg-amber-100 text-amber-700'">{{ reliabilityLabel(session.reliability) }}</span></td>
                <td class="px-4 py-3">v{{ session.config_version }} · {{ routingModeLabel(session.routing_mode) }}</td>
                <td class="whitespace-nowrap px-4 py-3">{{ formatTime(session.last_activity_at) }}</td>
              </tr>
              <tr v-if="!state?.recent_sessions.length"><td colspan="6" class="px-4 py-10 text-center text-gray-500">{{ t('common.noData') }}</td></tr>
            </tbody>
          </table>
        </div>
      </section>

      <section class="space-y-3" :aria-label="t('admin.workSession.routingMetrics')" data-test="routing-metrics">
        <div>
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.workSession.routingMetrics') }}</h2>
          <p class="mt-1 text-sm text-gray-500">{{ t('admin.workSession.routingMetricsNote') }}</p>
        </div>
        <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          <div v-for="item in routingMetricItems" :key="item.label" class="card p-4">
            <div class="flex items-center gap-1.5">
              <div class="text-xs font-medium uppercase tracking-wide text-gray-500">{{ item.label }}</div>
              <HelpTooltip v-if="item.help" :content="item.help" width-class="w-80">
                <template #trigger>
                  <button
                    type="button"
                    class="rounded-full text-gray-400 outline-none transition-colors hover:text-primary-600 focus-visible:ring-2 focus-visible:ring-primary-500 focus-visible:ring-offset-2 dark:text-gray-500 dark:hover:text-primary-400 dark:focus-visible:ring-offset-dark-900"
                    :aria-label="item.helpLabel || item.label"
                    :data-test="item.helpTest"
                  >
                    <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" aria-hidden="true">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                    </svg>
                  </button>
                </template>
              </HelpTooltip>
            </div>
            <div class="mt-2 font-mono text-lg font-semibold text-gray-900 dark:text-white">{{ item.value }}</div>
          </div>
        </div>
      </section>

      <section class="card overflow-hidden" data-test="route-decisions">
        <div class="border-b border-gray-200 px-5 py-4 dark:border-dark-700">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.workSession.routeDecisions') }}</h2>
          <p class="mt-1 text-sm text-gray-500">{{ t('admin.workSession.routeDecisionsNote') }}</p>
        </div>
        <div class="overflow-x-auto">
          <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
            <thead class="bg-gray-50 text-left text-xs uppercase tracking-wide text-gray-500 dark:bg-dark-800">
              <tr>
                <th class="px-4 py-3">{{ t('admin.workSession.requestAndSession') }}</th>
                <th class="px-4 py-3">
                  <span class="inline-flex items-center gap-1.5 whitespace-nowrap">
                    {{ t('admin.workSession.complexityDecision') }}
                    <HelpTooltip :content="t('admin.workSession.complexityDecisionHelp')" width-class="w-96">
                      <template #trigger>
                        <button
                          type="button"
                          class="rounded-full text-gray-400 outline-none transition-colors hover:text-primary-600 focus-visible:ring-2 focus-visible:ring-primary-500 focus-visible:ring-offset-2 dark:text-gray-500 dark:hover:text-primary-400 dark:focus-visible:ring-offset-dark-800"
                          :aria-label="t('admin.workSession.complexityDecisionHelpLabel')"
                          data-test="complexity-decision-help"
                        >
                          <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" aria-hidden="true">
                            <path stroke-linecap="round" stroke-linejoin="round" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                          </svg>
                        </button>
                      </template>
                    </HelpTooltip>
                  </span>
                </th>
                <th class="px-4 py-3">
                  <span class="inline-flex items-center gap-1.5 whitespace-nowrap">
                    {{ t('admin.workSession.classifier') }}
                    <HelpTooltip :content="t('admin.workSession.classifierHelp')" width-class="w-96">
                      <template #trigger>
                        <button
                          type="button"
                          class="rounded-full text-gray-400 outline-none transition-colors hover:text-primary-600 focus-visible:ring-2 focus-visible:ring-primary-500 focus-visible:ring-offset-2 dark:text-gray-500 dark:hover:text-primary-400 dark:focus-visible:ring-offset-dark-800"
                          :aria-label="t('admin.workSession.classifierHelpLabel')"
                          data-test="classifier-help"
                        >
                          <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" aria-hidden="true">
                            <path stroke-linecap="round" stroke-linejoin="round" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                          </svg>
                        </button>
                      </template>
                    </HelpTooltip>
                  </span>
                </th>
                <th class="px-4 py-3">{{ t('admin.workSession.actualRoute') }}</th>
                <th class="px-4 py-3">{{ t('admin.workSession.associations') }}</th>
                <th class="px-4 py-3 text-left" data-test="route-actions-header">{{ t('common.actions') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
              <template v-for="decision in state?.route_decisions || []" :key="decision.id">
              <tr :data-test="`route-decision-${decision.id}`">
                <td class="px-4 py-3">
                  <div class="font-mono text-xs text-gray-700 dark:text-gray-300">{{ decision.gateway_request_id }}</div>
                  <div class="mt-1 font-mono text-xs text-gray-500">{{ decision.work_session_id }}</div>
                  <div class="mt-1 text-xs text-gray-500">{{ formatTime(decision.created_at) }}</div>
                </td>
                <td class="px-4 py-3">
                  <span class="inline-flex whitespace-nowrap rounded-full px-2.5 py-1 text-xs font-semibold" :class="complexityClass(decision.task_complexity)">
                    {{ complexityLabel(decision.task_complexity) }}
                  </span>
                  <div class="mt-1 text-xs text-gray-500">{{ decisionSourceLabel(decision.decision_source) }} · {{ decision.rule_version }}</div>
                  <div class="mt-1 max-w-[20rem] text-xs leading-5 text-gray-500 dark:text-gray-400">{{ decisionExplanation(decision) }}</div>
                </td>
                <td class="px-4 py-3">
                  <div class="font-medium text-gray-900 dark:text-white">{{ classifierResultLabel(decision) }}</div>
                  <div v-if="decision.classifier_status !== 'not_called'" class="mt-1 flex flex-wrap items-center gap-1.5 text-xs text-gray-500">
                    <span>{{ decision.classifier_version || '—' }} · {{ decision.classifier_latency_ms }} ms</span>
                    <span v-if="isSyntheticClassifier(decision.classifier_version)" class="whitespace-nowrap rounded bg-violet-50 px-1.5 py-0.5 font-medium text-violet-700 dark:bg-violet-950/40 dark:text-violet-300">{{ t('admin.workSession.syntheticClassifier') }}</span>
                  </div>
                  <div v-else class="mt-1 text-xs text-gray-500">{{ t('admin.workSession.classifierNotCalledNote') }}</div>
                </td>
                <td class="px-4 py-3">
                  <div class="font-medium text-gray-900 dark:text-white">{{ decision.actual_logical_model || t('admin.workSession.noModelSelected') }}</div>
                  <div class="mt-1 text-xs text-gray-500">{{ tierLabel(decision.effective_tier) }} · {{ decision.change_reason }}</div>
                  <div v-if="decision.technical_retry_count" class="mt-1 text-xs text-amber-700 dark:text-amber-300">{{ t('admin.workSession.technicalRetry', { count: decision.technical_retry_count, reason: decision.technical_retry_reason || '—' }) }}</div>
                </td>
                <td class="px-4 py-3">
                  <div class="flex min-w-max flex-nowrap items-center gap-1.5" data-test="decision-associations">
                    <span class="inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded px-2 py-1 text-xs" :class="decision.audit_linked ? 'bg-emerald-50 text-emerald-700' : 'bg-red-50 text-red-700'" data-test="route-audit-association">
                      <span>audit</span><span aria-hidden="true">{{ decision.audit_linked ? '✓' : '×' }}</span>
                    </span>
                    <span class="inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded px-2 py-1 text-xs" :class="decision.usage_linked ? 'bg-emerald-50 text-emerald-700' : 'bg-gray-100 text-gray-600'" data-test="route-usage-association">
                      <span>usage</span><span aria-hidden="true">{{ decision.usage_linked ? '✓' : '—' }}</span>
                    </span>
                  </div>
                </td>
                <td class="px-4 py-3 text-left align-top">
                  <button
                    type="button"
                    class="btn btn-sm btn-secondary w-max whitespace-nowrap"
                    data-test="full-decision-toggle"
                    :aria-expanded="expandedDecision === decision.id"
                    :aria-controls="`full-decision-${decision.id}`"
                    @click="toggleDecision(decision.id)"
                  >
                    {{ expandedDecision === decision.id ? t('admin.workSession.hideDetails') : t('admin.workSession.fullDecision') }}
                  </button>
                </td>
              </tr>
              <tr
                v-if="expandedDecision === decision.id"
                :id="`full-decision-${decision.id}`"
                :data-test="`expanded-route-decision-${decision.id}`"
              >
                <td colspan="6" class="bg-gray-50/70 px-5 py-5 dark:bg-dark-800/50">
                  <div class="rounded-xl border border-gray-200 bg-white p-4 text-left dark:border-dark-600 dark:bg-dark-900" data-test="full-decision-panel">
                      <div class="grid gap-3 sm:grid-cols-2">
                        <div>
                          <div class="text-[11px] font-medium uppercase tracking-wide text-gray-500">{{ t('admin.workSession.associations') }}</div>
                          <div class="mt-2 flex flex-nowrap items-center gap-1.5" data-test="expanded-decision-associations">
                            <span class="inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded bg-white px-2 py-1 text-xs text-gray-700 ring-1 ring-inset ring-gray-200 dark:bg-dark-900 dark:text-gray-300 dark:ring-dark-600" data-test="expanded-audit-association">audit <span aria-hidden="true">{{ decision.audit_linked ? '✓' : '×' }}</span></span>
                            <span class="inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded bg-white px-2 py-1 text-xs text-gray-700 ring-1 ring-inset ring-gray-200 dark:bg-dark-900 dark:text-gray-300 dark:ring-dark-600" data-test="expanded-usage-association">usage <span aria-hidden="true">{{ decision.usage_linked ? '✓' : '—' }}</span></span>
                          </div>
                        </div>
                        <div>
                          <div class="text-[11px] font-medium uppercase tracking-wide text-gray-500">{{ t('admin.workSession.decisionExplanation') }}</div>
                          <p class="mt-2 text-xs leading-5 text-gray-700 dark:text-gray-300">{{ decisionExplanation(decision) }}</p>
                        </div>
                      </div>
                      <section class="mt-4 rounded-lg border border-gray-200 bg-gray-50/70 p-4 dark:border-dark-700 dark:bg-dark-800/60" data-test="classification-evidence">
                        <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                          <div>
                            <div class="text-[11px] font-medium uppercase tracking-wide text-gray-500">{{ t('admin.workSession.classificationEvidence') }}</div>
                            <p class="mt-1 max-w-3xl text-xs leading-5 text-gray-600 dark:text-gray-400">{{ t('admin.workSession.classificationEvidenceNote') }}</p>
                          </div>
                          <button
                            v-if="decision.audit_linked && canDisclose && classificationEvidence?.decisionId !== decision.id"
                            type="button"
                            class="btn btn-sm btn-secondary w-max shrink-0 whitespace-nowrap"
                            :disabled="classificationEvidenceLoading"
                            data-test="view-classification-evidence"
                            @click="loadClassificationEvidence(decision)"
                          >
                            {{ classificationEvidenceLoading ? t('common.loading') : t('admin.workSession.viewClassificationEvidence') }}
                          </button>
                        </div>
                        <p v-if="!decision.audit_linked" class="mt-3 text-xs text-amber-700 dark:text-amber-300" data-test="classification-evidence-unavailable">{{ t('admin.workSession.classificationEvidenceUnavailable') }}</p>
                        <p v-else-if="!canDisclose" class="mt-3 text-xs text-gray-500" data-test="classification-evidence-forbidden">{{ t('admin.workSession.classificationEvidenceSuperAdminOnly') }}</p>
                        <p v-if="classificationEvidenceError" class="mt-3 text-xs text-red-600 dark:text-red-400" data-test="classification-evidence-error">{{ classificationEvidenceError }}</p>
                        <div v-if="classificationEvidence?.decisionId === decision.id" class="mt-4 space-y-3" data-test="classification-evidence-content">
                          <div class="rounded-md border border-emerald-200 bg-emerald-50 px-3 py-2 text-xs text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950/30 dark:text-emerald-200">
                            {{ t('admin.workSession.classificationEvidenceRecorded', { id: classificationEvidence.operationId }) }}
                          </div>
                          <article v-for="(messageItem, index) in classificationEvidence.messages" :key="`${messageItem.role}-${index}`" class="overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
                            <header class="flex items-center justify-between border-b border-gray-100 bg-gray-50 px-3 py-2 text-xs font-medium text-gray-600 dark:border-dark-800 dark:bg-dark-800 dark:text-gray-300">
                              <span>{{ messageRoleLabel(messageItem.role) }}</span>
                              <span>#{{ index + 1 }}</span>
                            </header>
                            <pre class="max-h-72 overflow-auto whitespace-pre-wrap break-words p-3 text-xs leading-6 text-gray-900 dark:text-gray-100">{{ messageItem.text }}</pre>
                          </article>
                          <p v-if="classificationEvidence.messages.length === 0" class="text-xs text-gray-500">{{ t('admin.workSession.noClassificationMessages') }}</p>
                          <details class="rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
                            <summary class="cursor-pointer px-3 py-2 text-xs font-medium text-gray-700 dark:text-gray-300">{{ t('admin.workSession.fullClassificationRequest') }}</summary>
                            <pre class="max-h-80 overflow-auto whitespace-pre-wrap break-words border-t border-gray-100 p-3 text-xs leading-5 text-gray-900 dark:border-dark-800 dark:text-gray-100">{{ classificationEvidence.prettyRequest }}</pre>
                          </details>
                        </div>
                      </section>
                      <pre class="mt-4 max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-950 p-4 text-xs leading-5 text-gray-100">{{ JSON.stringify(displayDecision(decision), null, 2) }}</pre>
                  </div>
                </td>
              </tr>
              </template>
              <tr v-if="!state?.route_decisions.length"><td colspan="6" class="px-4 py-10 text-center text-gray-500">{{ t('common.noData') }}</td></tr>
            </tbody>
          </table>
        </div>
      </section>

      <section class="card overflow-hidden" data-test="config-version-history">
        <div class="border-b border-gray-200 px-5 py-4 dark:border-dark-700">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.workSession.versionHistory') }}</h2>
          <p class="mt-1 max-w-4xl text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('admin.workSession.versionHistoryNote') }}</p>
        </div>
        <div class="overflow-x-auto">
          <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700">
            <thead class="bg-gray-50 text-left text-xs uppercase tracking-wide text-gray-500 dark:bg-dark-800">
              <tr>
                <th class="px-4 py-3">{{ t('admin.workSession.version') }}</th>
                <th class="px-4 py-3">{{ t('admin.workSession.createdAt') }}</th>
                <th class="px-4 py-3">{{ t('admin.workSession.sessionReferences') }}</th>
                <th class="px-4 py-3">{{ t('admin.workSession.savedContent') }}</th>
                <th class="px-4 py-3">{{ t('admin.workSession.autoSnapshot') }}</th>
                <th class="px-4 py-3 text-right">{{ t('common.actions') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
              <template v-for="version in state?.config_versions || []" :key="version.config_version">
                <tr :data-test="`config-version-${version.config_version}`">
                  <td class="px-4 py-3">
                    <div class="flex items-center gap-2">
                      <span class="font-mono font-semibold text-gray-900 dark:text-white">v{{ version.config_version }}</span>
                      <span
                        class="rounded-full px-2 py-0.5 text-xs font-medium"
                        :class="version.current ? 'bg-primary-100 text-primary-700 dark:bg-primary-950/40 dark:text-primary-300' : version.session_count > 0 ? 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300' : 'bg-gray-50 text-gray-500 ring-1 ring-inset ring-gray-200 dark:bg-dark-800 dark:text-gray-400 dark:ring-dark-600'"
                      >
                        {{ version.current ? t('admin.workSession.versionCurrent') : version.session_count > 0 ? t('admin.workSession.versionInUse') : t('admin.workSession.versionUnused') }}
                      </span>
                    </div>
                  </td>
                  <td class="whitespace-nowrap px-4 py-3 text-gray-600 dark:text-gray-400">{{ version.created_at ? formatTime(version.created_at) : '—' }}</td>
                  <td class="px-4 py-3">
                    <div class="text-gray-900 dark:text-white">{{ t('admin.workSession.sessionCount', { count: version.session_count }) }}</div>
                    <div class="mt-0.5 text-xs text-gray-500">{{ t('admin.workSession.sessionBreakdown', { reliable: version.reliable_session_count, requestScoped: version.request_scoped_session_count }) }}</div>
                  </td>
                  <td class="px-4 py-3 text-gray-700 dark:text-gray-300">{{ t('admin.workSession.contentCounts', { models: version.model_count, candidates: version.candidate_count }) }}</td>
                  <td class="px-4 py-3">
                    <span v-if="version.auto_snapshot_status === 'current'" class="text-gray-700 dark:text-gray-300">{{ version.auto?.enabled ? t('admin.workSession.autoOn') : t('admin.workSession.autoOff') }}</span>
                    <span v-else class="text-amber-700 dark:text-amber-300">{{ t('admin.workSession.autoHistoryMissing') }}</span>
                  </td>
                  <td class="px-4 py-3 text-right">
                    <button type="button" class="btn btn-sm btn-secondary" :data-test="`version-details-${version.config_version}`" @click="toggleVersion(version.config_version)">
                      {{ expandedVersion === version.config_version ? t('admin.workSession.hideDetails') : t('admin.workSession.viewDetails') }}
                    </button>
                  </td>
                </tr>
                <tr v-if="expandedVersion === version.config_version" :data-test="`version-detail-${version.config_version}`">
                  <td colspan="6" class="bg-gray-50/70 px-5 py-5 dark:bg-dark-800/50">
                    <div v-if="version.auto_snapshot_status !== 'current'" class="mb-4 rounded-lg border border-amber-200 bg-amber-50 px-3.5 py-3 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-300">
                      {{ t('admin.workSession.historicalAutoUnavailable') }}
                    </div>
                    <div v-else class="mb-4 rounded-lg border border-gray-200 bg-white px-3.5 py-3 text-sm text-gray-700 dark:border-dark-600 dark:bg-dark-900 dark:text-gray-300">
                      <div class="font-medium text-gray-900 dark:text-white">{{ t('admin.workSession.currentAutoSnapshot') }}</div>
                      <div class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
                        {{ t('admin.workSession.currentAutoDetails', { users: formatIDs(version.auto?.user_whitelist), groups: formatIDs(version.auto?.group_whitelist) }) }}
                      </div>
                    </div>
                    <div class="grid gap-4 lg:grid-cols-2">
                      <div class="rounded-lg border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-900">
                        <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.workSession.modelCatalogSnapshot') }}</h3>
                        <div v-if="version.catalog.length" class="mt-3 divide-y divide-gray-100 dark:divide-dark-700">
                          <div v-for="entry in version.catalog" :key="entry.id" class="py-3 first:pt-0 last:pb-0">
                            <div class="flex flex-wrap items-center gap-2">
                              <span class="font-medium text-gray-900 dark:text-white">{{ entry.logical_model }}</span>
                              <span class="rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600 dark:bg-dark-700 dark:text-gray-300">{{ tierLabel(entry.tier) }}</span>
                              <span v-if="entry.emergency_disabled" class="rounded bg-red-50 px-1.5 py-0.5 text-xs text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ t('admin.workSession.emergencyDisabled') }}</span>
                            </div>
                            <div class="mt-1 font-mono text-xs text-gray-500">{{ entry.provider_model }}</div>
                            <div class="mt-1 text-xs text-gray-500">{{ entry.capabilities.join(', ') || '—' }} · {{ validityLabel(entry.valid_from, entry.valid_until) }}</div>
                          </div>
                        </div>
                        <p v-else class="mt-3 text-sm text-gray-500">{{ t('common.noData') }}</p>
                      </div>
                      <div class="rounded-lg border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-900">
                        <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.workSession.candidatePoolSnapshot') }}</h3>
                        <div v-if="version.candidate_pools.length" class="mt-3 space-y-2">
                          <div v-for="candidate in version.candidate_pools" :key="candidate.id" class="flex items-center gap-3 rounded-md bg-gray-50 px-3 py-2 dark:bg-dark-800">
                            <span class="w-20 shrink-0 text-xs font-medium text-gray-500">{{ tierLabel(candidate.tier) }} #{{ candidate.position }}</span>
                            <span class="font-mono text-xs text-gray-800 dark:text-gray-200">{{ candidate.logical_model }}</span>
                          </div>
                        </div>
                        <p v-else class="mt-3 text-sm text-gray-500">{{ t('admin.workSession.noCandidates') }}</p>
                      </div>
                    </div>
                  </td>
                </tr>
              </template>
              <tr v-if="!state?.config_versions.length"><td colspan="6" class="px-4 py-10 text-center text-gray-500">{{ t('common.noData') }}</td></tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import { adminAuditAPI, adminWorkSessionAPI } from '@/api/admin'
import { useAuthStore } from '@/stores/auth'
import type { AutoCandidatePoolInput, ModelCatalogInput, RouteDecision, TaskComplexity, WorkSessionManagementState, WorkSessionManagementUpdate } from '@/api/admin/workSession'

const { t } = useI18n()
const authStore = useAuthStore()
const canDisclose = computed(() => authStore.isSuperAdmin)
const state = ref<WorkSessionManagementState>()
const loading = ref(false)
const message = ref('')
const messageKind = ref<'success' | 'error'>('success')
const form = reactive({ auto_enabled: false })
const userWhitelist = ref('')
const groupWhitelist = ref('')
const catalogJSON = ref('[]')
const poolsJSON = ref('[]')
const expandedDecision = ref<string>()
const expandedVersion = ref<number>()
type ClassificationEvidenceMessage = { role: string; text: string }
type ClassificationEvidence = {
  decisionId: string
  operationId: string
  messages: ClassificationEvidenceMessage[]
  prettyRequest: string
}
const classificationEvidence = ref<ClassificationEvidence>()
const classificationEvidenceLoading = ref(false)
const classificationEvidenceError = ref('')
let classificationEvidenceAttempt = 0

const statusReasonKeys: Record<string, string> = {
  ready: 'admin.workSession.statusReady',
  disabled: 'admin.workSession.statusDisabled',
  not_started: 'admin.workSession.statusStarting',
  schema_not_ready: 'admin.workSession.statusSchemaUnavailable',
  hmac_key_unavailable: 'admin.workSession.statusKeyUnavailable',
  identity_config_incomplete: 'admin.workSession.statusConfigIncomplete',
  invalid_mode_or_repository: 'admin.workSession.statusUnavailable',
  service_unavailable: 'admin.workSession.statusUnavailable'
}

const modeKeys: Record<string, string> = {
  required: 'admin.workSession.modeRequired',
  disabled: 'admin.workSession.modeDisabled'
}

const statusItems = computed(() => [
  { label: t('admin.workSession.foundation'), value: valueLabel(statusReasonKeys, state.value?.status.reason_code), help: t('admin.workSession.associationHelp'), helpLabel: t('admin.workSession.associationHelpLabel'), helpTest: 'association-help' },
  { label: t('admin.workSession.mode'), value: valueLabel(modeKeys, state.value?.status.mode), help: t('admin.workSession.modeHelp'), helpLabel: t('admin.workSession.modeHelpLabel'), helpTest: 'association-method-help' },
  { label: t('admin.workSession.keyVersion'), value: state.value?.status.hmac_key_version || '—' },
  { label: t('admin.workSession.configVersion'), value: state.value ? `v${state.value.auto.config_version}` : '—', help: t('admin.workSession.configVersionHelp'), helpLabel: t('admin.workSession.configVersionHelpLabel'), helpTest: 'config-version-help' }
])

const routingMetricItems = computed(() => [
  { label: t('admin.workSession.routeDecisionCount'), value: state.value?.routing_metrics.decision_count ?? 0 },
  { label: t('admin.workSession.classifierFallbackCount'), value: state.value?.routing_metrics.classifier_fallback_count ?? 0 },
  {
    label: t('admin.workSession.classifierP95'),
    value: `${state.value?.routing_metrics.classifier_p95_latency_ms ?? 0} ms`,
    help: t('admin.workSession.classifierP95Help'),
    helpLabel: t('admin.workSession.classifierP95HelpLabel'),
    helpTest: 'classifier-p95-help'
  },
  {
    label: t('admin.workSession.routingP95'),
    value: `${state.value?.routing_metrics.routing_p95_latency_ms ?? 0} ms`,
    help: t('admin.workSession.routingP95Help'),
    helpLabel: t('admin.workSession.routingP95HelpLabel'),
    helpTest: 'routing-p95-help'
  }
])

function valueLabel(keys: Record<string, string>, value?: string): string {
  if (!value) return '—'
  return keys[value] ? t(keys[value]) : value
}

function tierLabel(value: string): string {
  return valueLabel({ economy: 'admin.workSession.tierEconomy', general: 'admin.workSession.tierGeneral', advanced: 'admin.workSession.tierAdvanced' }, value)
}

function reliabilityLabel(value: string): string {
  return valueLabel({ reliable: 'admin.workSession.reliable', unreliable: 'admin.workSession.unreliable' }, value)
}

function complexityLabel(value: string): string {
  return valueLabel({ simple: 'admin.workSession.complexitySimple', general: 'admin.workSession.complexityGeneral', complex: 'admin.workSession.complexityComplex' }, value)
}

function decisionSourceLabel(value: string): string {
  return valueLabel({
    rule: 'admin.workSession.decisionSourceRule',
    classifier: 'admin.workSession.decisionSourceClassifier',
    fallback: 'admin.workSession.decisionSourceFallback'
  }, value)
}

function classifierResultLabel(decision: RouteDecision): string {
  if (decision.classifier_status === 'completed') {
    return t(decision.certainty === 'decisive'
      ? 'admin.workSession.classifierCertaintyDecisive'
      : 'admin.workSession.classifierCertaintyUncertain')
  }
  return valueLabel({
    not_called: 'admin.workSession.classifierStatusNotCalled',
    timeout: 'admin.workSession.classifierStatusTimeout',
    invalid: 'admin.workSession.classifierStatusInvalid',
    unavailable: 'admin.workSession.classifierStatusUnavailable'
  }, decision.classifier_status)
}

function isSyntheticClassifier(version?: string): boolean {
  return (version || '').toLowerCase().includes('stub')
}

function complexityClass(value: TaskComplexity): string {
  if (value === 'simple') return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300'
  if (value === 'complex') return 'bg-amber-100 text-amber-800 dark:bg-amber-950/40 dark:text-amber-300'
  return 'bg-sky-100 text-sky-700 dark:bg-sky-950/40 dark:text-sky-300'
}

function displayDecision(decision: RouteDecision) {
  return decision
}

function decisionExplanation(decision: RouteDecision): string {
  if (decision.decision_source === 'rule') {
    return t(decision.task_complexity === 'complex' ? 'admin.workSession.ruleComplexExplanation' : 'admin.workSession.ruleSimpleExplanation')
  }
  const fallbackKeys: Record<string, string> = {
    timeout: 'admin.workSession.classifierTimeoutExplanation',
    invalid: 'admin.workSession.classifierInvalidExplanation',
    unavailable: 'admin.workSession.classifierUnavailableExplanation'
  }
  if (decision.classifier_status === 'completed' && decision.certainty === 'uncertain') {
    return t('admin.workSession.classifierUncertainExplanation')
  }
  return fallbackKeys[decision.classifier_status] ? t(fallbackKeys[decision.classifier_status]) : decision.explanation
}

function signalStatusLabel(value: string): string {
  return valueLabel({ verified: 'admin.workSession.signalVerified', missing: 'admin.workSession.signalMissing', malformed: 'admin.workSession.signalMalformed' }, value)
}

function routingModeLabel(value: string): string {
  return valueLabel({ explicit: 'admin.workSession.routingExplicit', auto: 'admin.workSession.routingAuto' }, value)
}

function toggleDecision(id: string) {
  clearClassificationEvidence()
  expandedDecision.value = expandedDecision.value === id ? undefined : id
}

function messageRoleLabel(role: string): string {
  const normalized = role.toLowerCase()
  if (normalized === 'user') return t('admin.workSession.messageRoleUser')
  if (normalized === 'assistant') return t('admin.workSession.messageRoleAssistant')
  if (normalized === 'system' || normalized === 'developer') return t('admin.workSession.messageRoleInstruction')
  if (normalized === 'tool') return t('admin.workSession.messageRoleTool')
  return role || t('admin.workSession.messageRoleUnknown')
}

function textFromContent(value: unknown): string {
  if (typeof value === 'string') return value.trim()
  if (!Array.isArray(value)) return ''
  return value.map((part) => {
    if (typeof part === 'string') return part
    if (!part || typeof part !== 'object') return ''
    const item = part as Record<string, unknown>
    if (typeof item.text === 'string') return item.text
    if (typeof item.content === 'string') return item.content
    if (typeof item.input === 'string') return item.input
    return ''
  }).filter(Boolean).join('\n').trim()
}

function extractClassificationMessages(request: unknown): ClassificationEvidenceMessage[] {
  if (!request || typeof request !== 'object') return []
  const root = request as Record<string, unknown>
  const messages: ClassificationEvidenceMessage[] = []
  const append = (role: string, content: unknown) => {
    const text = textFromContent(content)
    if (text) messages.push({ role, text })
  }
  if (typeof root.instructions === 'string' && root.instructions.trim()) {
    messages.push({ role: 'developer', text: root.instructions.trim() })
  }
  if (typeof root.system === 'string' && root.system.trim()) {
    messages.push({ role: 'system', text: root.system.trim() })
  } else if (Array.isArray(root.system)) {
    append('system', root.system)
  }
  if (Array.isArray(root.messages)) {
    for (const raw of root.messages) {
      if (!raw || typeof raw !== 'object') continue
      const item = raw as Record<string, unknown>
      append(typeof item.role === 'string' ? item.role : 'unknown', item.content)
    }
  }
  if (typeof root.input === 'string') {
    append('user', root.input)
  } else if (Array.isArray(root.input)) {
    for (const raw of root.input) {
      if (typeof raw === 'string') {
        append('user', raw)
        continue
      }
      if (!raw || typeof raw !== 'object') continue
      const item = raw as Record<string, unknown>
      append(typeof item.role === 'string' ? item.role : 'user', item.content ?? item.text ?? item.input)
    }
  }
  if (typeof root.prompt === 'string') append('user', root.prompt)
  return messages
}

function decodeBase64UTF8(value: string): string {
  const binary = atob(value)
  const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0))
  return new TextDecoder().decode(bytes)
}

function requestBodyFromDisclosure(parts: Array<{ direction: string; sequence: number; content: string }>): string {
  const requestParts = parts.filter((part) => part.direction === 'request').sort((left, right) => left.sequence - right.sequence)
  for (const part of requestParts) {
    try {
      const envelope = JSON.parse(part.content) as { version?: string; body?: string }
      if (envelope.version === 'core-gateway-request-v1' && typeof envelope.body === 'string') {
        return decodeBase64UTF8(envelope.body)
      }
    } catch {
      // Continue to the next governed request part.
    }
  }
  throw new Error(t('admin.workSession.classificationEvidenceInvalid'))
}

function clearClassificationEvidence() {
  classificationEvidenceAttempt += 1
  if (classificationEvidence.value) {
    classificationEvidence.value.messages.forEach((item) => { item.text = '' })
    classificationEvidence.value.prettyRequest = ''
  }
  classificationEvidence.value = undefined
  classificationEvidenceLoading.value = false
  classificationEvidenceError.value = ''
}

async function loadClassificationEvidence(decision: RouteDecision) {
  if (!decision.audit_linked || !canDisclose.value || classificationEvidenceLoading.value) return
  clearClassificationEvidence()
  const attempt = ++classificationEvidenceAttempt
  classificationEvidenceLoading.value = true
  try {
    const page = await adminAuditAPI.list({ gateway_request_id: decision.gateway_request_id, page: 1, page_size: 1 })
    const interaction = page.items.find((item) => item.gateway_request_id === decision.gateway_request_id)
    if (!interaction) throw new Error(t('admin.workSession.classificationEvidenceUnavailable'))
    const disclosure = await adminAuditAPI.disclose(interaction.id)
    if (attempt !== classificationEvidenceAttempt || expandedDecision.value !== decision.id) {
      disclosure.parts.forEach((part) => { part.content = '' })
      return
    }
    const requestBody = requestBodyFromDisclosure(disclosure.parts)
    disclosure.parts.forEach((part) => { part.content = '' })
    let parsed: unknown
    try {
      parsed = JSON.parse(requestBody)
    } catch {
      parsed = requestBody
    }
    classificationEvidence.value = {
      decisionId: decision.id,
      operationId: disclosure.operation_id,
      messages: extractClassificationMessages(parsed),
      prettyRequest: typeof parsed === 'string' ? parsed : JSON.stringify(parsed, null, 2)
    }
  } catch (error) {
    if (attempt === classificationEvidenceAttempt) {
      classificationEvidenceError.value = (error as { message?: string })?.message || t('admin.workSession.classificationEvidenceLoadFailed')
    }
  } finally {
    if (attempt === classificationEvidenceAttempt) classificationEvidenceLoading.value = false
  }
}

function toggleVersion(version: number) {
  expandedVersion.value = expandedVersion.value === version ? undefined : version
}

function formatIDs(values?: number[]): string {
  return values?.length ? values.join(', ') : t('admin.workSession.none')
}

function validityLabel(validFrom: string, validUntil?: string): string {
  return validUntil
    ? t('admin.workSession.validityRange', { from: formatTime(validFrom), until: formatTime(validUntil) })
    : t('admin.workSession.validityFrom', { from: formatTime(validFrom) })
}

function parseIDs(value: string): number[] {
  return [...new Set(value.split(',').map((part) => Number(part.trim())).filter((id) => Number.isSafeInteger(id) && id > 0))].sort((a, b) => a - b)
}

function applyState(next: WorkSessionManagementState) {
  state.value = next
  form.auto_enabled = next.auto.enabled
  userWhitelist.value = next.auto.user_whitelist.join(', ')
  groupWhitelist.value = next.auto.group_whitelist.join(', ')
  catalogJSON.value = JSON.stringify(next.catalog.map(({ logical_model, provider_model, tier, capabilities, valid_from, valid_until }) => ({ logical_model, provider_model, tier, capabilities, valid_from, ...(valid_until ? { valid_until } : {}) })), null, 2)
  const grouped = new Map<string, AutoCandidatePoolInput>()
  for (const item of next.candidate_pools) {
    const pool = grouped.get(item.tier) || { tier: item.tier, candidates: [], valid_from: item.valid_from, ...(item.valid_until ? { valid_until: item.valid_until } : {}) }
    pool.candidates.push(item.logical_model)
    grouped.set(item.tier, pool)
  }
  poolsJSON.value = JSON.stringify([...grouped.values()], null, 2)
}

function errorText(error: unknown): string {
  if (error instanceof Error) return error.message
  return t('admin.workSession.failed')
}

async function load() {
  loading.value = true
  message.value = ''
  try {
    applyState(await adminWorkSessionAPI.get())
  } catch (error) {
    messageKind.value = 'error'
    message.value = errorText(error)
  } finally {
    loading.value = false
  }
}

async function save() {
  loading.value = true
  message.value = ''
  try {
    const input: WorkSessionManagementUpdate = {
      auto_enabled: form.auto_enabled,
      user_whitelist: parseIDs(userWhitelist.value),
      group_whitelist: parseIDs(groupWhitelist.value),
      catalog: JSON.parse(catalogJSON.value) as ModelCatalogInput[],
      candidate_pools: JSON.parse(poolsJSON.value) as AutoCandidatePoolInput[]
    }
    applyState(await adminWorkSessionAPI.replace(input))
    messageKind.value = 'success'
    message.value = t('admin.workSession.saved')
  } catch (error) {
    messageKind.value = 'error'
    message.value = errorText(error)
  } finally {
    loading.value = false
  }
}

async function setEmergency(model: string, disabled: boolean) {
  loading.value = true
  message.value = ''
  try {
    applyState(await adminWorkSessionAPI.setEmergencyDisabled(model, disabled))
    messageKind.value = 'success'
    message.value = t('admin.workSession.emergencySaved')
  } catch (error) {
    messageKind.value = 'error'
    message.value = errorText(error)
  } finally {
    loading.value = false
  }
}

function formatTime(value: string) {
  return new Date(value).toLocaleString()
}

onMounted(load)
onBeforeUnmount(clearClassificationEvidence)
</script>
