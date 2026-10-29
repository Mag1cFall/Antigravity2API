import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { FormRow } from '@/components/ui/form-row'
import { toast } from '@/components/ui/toast'
import { settingsMessages } from '@/features/settings/messages'
import { api } from '@/lib/api'
import { fill, useMessages } from '@/lib/i18n'

// ProxyStatus 对应 internal/proxypool 的 Status
interface ProxyStatus {
  url: string
  healthy: boolean
  checked_at?: string
  latency_ms: number
  error?: string
  accounts: string[]
}

// proxiesKey 是代理池状态的查询键
const proxiesKey = ['proxies'] as const

// ProxyPool 是设置中代理池的状态：每个代理是否可用、延迟与使用它的账户，可立即检测；有未检测的代理时每 2 秒读取一次，否则每 30 秒
export function ProxyPool() {
  const m = useMessages(settingsMessages)
  const client = useQueryClient()
  const proxies = useQuery({
    queryKey: proxiesKey,
    queryFn: () => api<{ proxies: ProxyStatus[] }>('/api/proxies'),
    refetchInterval: (query) =>
      query.state.data?.proxies.some((proxy) => !proxy.checked_at) ? 2000 : 30_000,
  })
  const check = useMutation({
    mutationFn: () => api<{ proxies: ProxyStatus[] }>('/api/proxies/check', { method: 'POST' }),
    onSuccess: (data) => {
      client.setQueryData(proxiesKey, data)
    },
    onError: (error) => {
      toast({ tone: 'error', title: fill(m.poolCheckFailed, { message: error.message }) })
    },
  })
  const list = proxies.data?.proxies ?? []
  if (list.length === 0) return null
  return (
    <FormRow id="proxy-pool" label={m.pool}>
      <ul
        aria-label={m.pool}
        className="flex flex-col divide-y divide-border rounded-md border border-border"
      >
        {list.map((proxy) => (
          <li key={proxy.url} className="flex flex-col gap-1 px-3 py-2 text-sm">
            <span className="font-mono break-all">{proxy.url}</span>
            <span className="flex flex-wrap gap-x-2 text-foreground-muted">
              <span
                className={
                  !proxy.checked_at
                    ? undefined
                    : proxy.healthy
                      ? 'text-success-text'
                      : 'text-error-text'
                }
                title={proxy.error}
              >
                {!proxy.checked_at
                  ? m.poolUnchecked
                  : proxy.healthy
                    ? fill(m.poolHealthy, { latency: proxy.latency_ms })
                    : m.poolUnhealthy}
              </span>
              <span title={proxy.accounts.join('\n')}>
                · {fill(m.poolAccounts, { count: proxy.accounts.length })}
              </span>
            </span>
          </li>
        ))}
      </ul>
      <Button
        id="proxy-pool"
        type="button"
        size="sm"
        className="self-start"
        loading={check.isPending}
        onClick={() => {
          check.mutate()
        }}
      >
        <RefreshCw size={16} aria-hidden />
        {m.poolCheck}
      </Button>
    </FormRow>
  )
}
