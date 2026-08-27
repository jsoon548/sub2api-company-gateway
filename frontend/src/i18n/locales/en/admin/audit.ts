export default {
  audit: {
    title: 'Gateway Audit',
    description: 'Search authoritative interaction metadata and perform individually governed raw-content disclosure.',
    filters: 'Structured metadata filters',
    employee: 'Employee',
    employeePlaceholder: 'Employee ID or email',
    gatewayId: 'Gateway Request ID',
    gatewayIdPlaceholder: 'Exact UUID',
    protocol: 'Protocol',
    model: 'Model',
    modelPlaceholder: 'Exact requested or resolved model',
    outcome: 'Request outcome',
    contentState: 'Content state',
    from: 'Admitted from',
    to: 'Admitted to',
    time: 'Time',
    userId: 'User ID',
    state: 'Request outcome / content state',
    expires: 'Expires',
    empty: 'No audit interactions match these filters.',
    loadFailed: 'Failed to load audit metadata.',
    viewRaw: 'View raw content',
    viewUsage: 'Usage reconciliation',
    superAdminOnly: 'Super admin only',
    disclosureTitle: 'Raw-content view',
    disclosureWarning: 'This action reveals exact employee request or response content. Permanent governance events are recorded automatically before decryption and again on completion.',
    loadingRaw: 'Recording the disclosure event and decrypting content…',
    retryView: 'Retry view',
    disclosureFailed: 'Raw content was not released. Authorization, retention, or governance recording may have failed.',
    disclosureRecorded: 'Disclosure recorded. Operation ID: {id}',
    outcomes: {
      processing: 'Processing',
      rejected_pre_upstream: 'Rejected before upstream',
      completed: 'Request completed',
      upstream_failed: 'Upstream failed',
      interrupted: 'Request interrupted'
    },
    contentStates: {
      recording: 'Content recording',
      complete: 'Content complete',
      incomplete: 'Content incomplete',
      expired: 'Content removed'
    },
    transports: {
      http: 'HTTP',
      sse: 'Streaming SSE'
    },
    directions: {
      request: 'Request',
      response: 'Response'
    }
  }
}
