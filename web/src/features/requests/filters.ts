import type { RequestState } from '@/features/requests/types'

// ranges 是时间范围的取值与对应毫秒数
export const ranges = {
  '1h': 3_600_000,
  '24h': 86_400_000,
  '7d': 604_800_000,
  '30d': 2_592_000_000,
} as const

// RangeKey 是时间范围的取值
export type RangeKey = keyof typeof ranges

// states 是请求记录可筛选的结果
export const states: RequestState[] = [
  'completed',
  'tool_calls',
  'limited',
  'blocked',
  'failed',
  'cancelled',
]

// protocols 是可筛选的公开协议
export const protocols = ['openai-chat', 'openai-responses', 'anthropic', 'gemini'] as const

// RequestFilter 是请求记录页的筛选条件，全部来自 URL
export interface RequestFilter {
  range: RangeKey
  state: string
  protocol: string
  model: string
  account: string
  q: string
  cursor: string
}

// parseFilter 从 URL 参数读取筛选条件，非法取值回到默认值
export function parseFilter(params: URLSearchParams): RequestFilter {
  const range = params.get('range')
  const state = params.get('state') ?? ''
  const protocol = params.get('protocol') ?? ''
  return {
    range: range && range in ranges ? (range as RangeKey) : '24h',
    state: (states as string[]).includes(state) ? state : '',
    protocol: (protocols as readonly string[]).includes(protocol) ? protocol : '',
    model: params.get('model') ?? '',
    account: params.get('account') ?? '',
    q: params.get('q') ?? '',
    cursor: params.get('cursor') ?? '',
  }
}

// apiQuery 把筛选条件换成 GET /api/usage/records 与 records.csv 的查询参数，时间范围截止到 now
export function apiQuery(filter: RequestFilter, now: Date) {
  const query = new URLSearchParams({
    from: new Date(now.getTime() - ranges[filter.range]).toISOString(),
    to: new Date(now.getTime() + 1000).toISOString(),
  })
  if (filter.state) query.set('state', filter.state)
  if (filter.protocol) query.set('protocol', filter.protocol)
  if (filter.model) query.set('model', filter.model)
  if (filter.account) query.set('account', filter.account)
  if (filter.q.trim()) query.set('q', filter.q.trim())
  if (filter.cursor) query.set('cursor', filter.cursor)
  return query
}
