// RequestState 对应 internal/app 的请求结果
export type RequestState =
  'running' | 'completed' | 'tool_calls' | 'limited' | 'blocked' | 'failed' | 'cancelled'

// Attempt 对应 internal/requestdb 的 Attempt，status 为 0 表示连接失败
export interface Attempt {
  account: string
  url: string
  status: number
  code?: string
  retry_delay?: string
  trace_id?: string
  response_id?: string
  error?: string
  duration_ms: number
}

// RequestRecord 对应 internal/requestdb 的 Row，cost_usd 为按官方单价估算的美元费用，模型没有单价时为 null
export interface RequestRecord {
  id: string
  time: string
  client_request_id: string
  response_id: string
  protocol: string
  path: string
  api_key: string
  client_ip: string
  user_agent: string
  stream: boolean
  model: string
  upstream_model: string
  model_version: string
  request_type: string
  prompt_tier: string
  cascade_id: string
  step_index: number
  account: string
  quota_group: string
  status: number
  state: RequestState
  error_phase: string
  upstream_status: number
  error_code: string
  duration_ms: number
  first_event_ms: number
  queue_ms: number
  input_tokens: number
  cached_tokens: number
  reasoning_tokens: number
  reply_tokens: number
  tool_calls: number
  cost_usd: number | null
  error: string
  attempts: Attempt[] | null
}

// RequestPage 对应 GET /api/usage/records 的一页请求记录
export interface RequestPage {
  items: RequestRecord[]
  next_cursor?: string
}

// Body 对应 internal/requestdb 的 Body，正文截断到 64 KiB，size 为原始字节数
export interface Body {
  request: string
  request_size: number
  response: string
  response_size: number
}

// RequestDetail 对应 internal/requestdb 的 Detail，upstream 与 attempts 按尝试顺序对应，未保存的正文为 null
export interface RequestDetail {
  record: RequestRecord
  body: Body | null
  upstream: Body[] | null
}
