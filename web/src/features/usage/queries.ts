import { keepPreviousData, useQuery } from '@tanstack/react-query'

import type { UsageReport } from '@/features/usage/types'
import { api } from '@/lib/api'

// UsageBucketSize 是用量分桶：auto 由服务按范围选择，86400 为按本机时区的自然日
export type UsageBucketSize = 'auto' | '86400'

// rangeQuery 是截止到当前时间的 from 与 to
export function rangeQuery(rangeMs: number) {
  const now = Date.now()
  return new URLSearchParams({
    from: new Date(now - rangeMs).toISOString(),
    to: new Date(now + 1000).toISOString(),
  })
}

// useUsage 读取一段时间内的用量报告，分桶按本机时区对齐
export function useUsage(rangeMs: number, bucket: UsageBucketSize = 'auto') {
  return useQuery({
    queryKey: ['usage', rangeMs, bucket],
    queryFn: () => {
      const query = rangeQuery(rangeMs)
      query.set('tz', Intl.DateTimeFormat().resolvedOptions().timeZone)
      query.set('bucket', bucket)
      return api<UsageReport>('/api/usage?' + query.toString())
    },
    placeholderData: keepPreviousData,
  })
}
