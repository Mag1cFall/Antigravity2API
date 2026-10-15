// UsageLatency 对应 internal/requestdb 的 UsageLatency
export interface UsageLatency {
  avg_ms: number
  p50_ms: number
  p95_ms: number
  p99_ms: number
}

// UsageStats 对应 internal/requestdb 的 UsageStats，耗时只统计成功请求，unpriced 是消耗了 token 但模型没有单价的请求数
export interface UsageStats {
  requests: number
  succeeded: number
  failed: number
  canceled: number
  rate_limited: number
  input_tokens: number
  cached_tokens: number
  reasoning_tokens: number
  reply_tokens: number
  total_tokens: number
  cost_usd: number
  unpriced: number
  duration: UsageLatency
  first_event: UsageLatency
  queue_avg_ms: number
  last_at?: string
}

// UsageBucket 对应 internal/requestdb 的 UsageBucket
export interface UsageBucket extends UsageStats {
  at: string
}

// UsageGroup 对应 internal/requestdb 的 UsageGroup
export interface UsageGroup extends UsageStats {
  key: string
}

// UsagePair 对应 internal/requestdb 的 UsagePair
export interface UsagePair extends UsageStats {
  account: string
  model: string
}

// UsageSeries 对应 internal/requestdb 的 UsageSeries
export interface UsageSeries {
  key: string
  other?: boolean
  requests: number[]
  tokens: number[]
}

// UsageRecent 对应 internal/requestdb 的 UsageRecent
export interface UsageRecent {
  minutes: number
  requests: number
  tokens: number
}

// UsageReport 对应 internal/requestdb 的 UsageReport，groups 与 options 以维度名为键，没有取值的 options 为 null
export interface UsageReport {
  from: string
  to: string
  bucket_seconds: number
  generated_at: string
  latest_at?: string
  totals: UsageStats
  previous: UsageStats
  recent: UsageRecent
  buckets: UsageBucket[]
  stack_by: string
  series: UsageSeries[]
  groups: Record<string, UsageGroup[]>
  pairs: UsagePair[]
  options: Record<string, string[] | null>
}
