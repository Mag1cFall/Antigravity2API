import { keepPreviousData, useQuery } from '@tanstack/react-query'

import type { CustomRange } from '@/features/requests/RangePicker'
import type { UsageReport } from '@/features/usage/types'
import { api } from '@/lib/api'

// UsageBucketSize 是用量分桶：auto 由服务按范围选择，86400 为按本机时区的自然日
type UsageBucketSize = 'auto' | '86400'

// UsageSpan 是用量的时间范围：截止到当前时间的毫秒数，或自定义的起止时刻
type UsageSpan = number | CustomRange

// rangeQuery 是时间范围的 from 与 to，毫秒数时截止到 now
export function rangeQuery(span: UsageSpan, now = Date.now()) {
  if (typeof span !== 'number') {
    return new URLSearchParams({ from: span.from.toISOString(), to: span.to.toISOString() })
  }
  return new URLSearchParams({
    from: new Date(now - span).toISOString(),
    to: new Date(now + 1000).toISOString(),
  })
}

// UsageFilters 是按维度筛选的取值，同一维度的多个取值为或关系
type UsageFilters = Partial<Record<string, string[]>>

// filterQuery 把维度筛选写入查询参数，多个取值以逗号分隔
export function filterQuery(query: URLSearchParams, filters: UsageFilters) {
  for (const [dimension, values] of Object.entries(filters)) {
    if (values?.length) query.set(dimension, values.join(','))
  }
  return query
}

// useUsage 读取一段时间内符合筛选的用量报告，分桶按本机时区对齐
export function useUsage(
  span: UsageSpan,
  bucket: UsageBucketSize = 'auto',
  filters: UsageFilters = {},
) {
  return useQuery({
    queryKey: [
      'usage',
      typeof span === 'number' ? span : [span.from.getTime(), span.to.getTime()],
      bucket,
      filters,
    ],
    queryFn: () => {
      const query = filterQuery(rangeQuery(span), filters)
      query.set('tz', Intl.DateTimeFormat().resolvedOptions().timeZone)
      query.set('bucket', bucket)
      return api<UsageReport>('/api/usage?' + query.toString())
    },
    placeholderData: keepPreviousData,
  })
}
