export default {
  audit: {
    title: '网关审计',
    description: '查询权威交互元数据，并查看仍在保留期内的原文。',
    filters: '结构化元数据筛选',
    employee: '员工',
    employeePlaceholder: '员工 ID 或邮箱',
    gatewayId: '网关请求 ID',
    gatewayIdPlaceholder: '精确 UUID',
    protocol: '协议',
    model: '模型',
    modelPlaceholder: '精确请求或解析模型',
    outcome: '请求结果',
    contentState: '原文状态',
    from: '准入开始时间',
    to: '准入结束时间',
    time: '时间',
    userId: '用户 ID',
    state: '请求结果 / 原文状态',
    expires: '到期',
    empty: '没有符合条件的审计交互。',
    loadFailed: '加载审计元数据失败。',
    viewRaw: '查看原文',
    viewUsage: '用量核验',
    superAdminOnly: '仅超级管理员',
    disclosureTitle: '原文查看',
    disclosureWarning: '此操作会显示员工请求或响应的精确原文。系统会自动在解密前及操作完成时分别写入永久治理事件。',
    loadingRaw: '正在记录查看事件并解密原文…',
    retryView: '重试查看',
    disclosureFailed: '系统未释放原文。授权、留存状态或治理事件写入可能失败。',
    disclosureRecorded: '查看事件已记录。操作 ID：{id}',
    outcomes: {
      processing: '处理中',
      rejected_pre_upstream: '上游调用前拒绝',
      completed: '请求已完成',
      upstream_failed: '上游失败',
      interrupted: '请求已中断'
    },
    contentStates: {
      recording: '原文记录中',
      complete: '原文完整',
      incomplete: '原文不完整',
      expired: '原文已清理'
    },
    transports: {
      http: 'HTTP',
      sse: 'SSE 流式'
    },
    directions: {
      request: '请求',
      response: '响应'
    }
  }
}
