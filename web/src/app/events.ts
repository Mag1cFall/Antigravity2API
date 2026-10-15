import { type QueryClient, useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'

import type { Account, Login } from '@/features/accounts/types'
import type { LogEntry } from '@/features/logs/types'
import type { RequestRecord } from '@/features/requests/types'

// logsKey 保存事件流推送的服务日志，最多 2000 条
export const logsKey = ['logs'] as const

// runningKey 保存事件流推送的进行中请求
export const runningKey = ['requests-running'] as const

// connectionKey 保存事件流是否连通
export const connectionKey = ['events', 'connected'] as const

// maxLogs 是页面保留的日志条数
const maxLogs = 2000

type AdminEvent =
  | { type: 'accounts'; data: Account[] }
  | { type: 'log'; data: LogEntry }
  | { type: 'request'; data: RequestRecord }
  | { type: 'login'; data: Login }

// apply 把一条管理事件写入对应的查询缓存
function apply(client: QueryClient, event: AdminEvent) {
  switch (event.type) {
    case 'accounts':
      client.setQueryData(['accounts'], { accounts: event.data })
      break
    case 'log':
      client.setQueryData<LogEntry[]>(logsKey, (logs = []) =>
        logs.length >= maxLogs
          ? [...logs.slice(logs.length - maxLogs + 1), event.data]
          : [...logs, event.data],
      )
      break
    case 'request': {
      const record = event.data
      client.setQueryData<RequestRecord[]>(runningKey, (running = []) => {
        const others = running.filter((item) => item.id !== record.id)
        return record.state === 'running' ? [...others, record] : others
      })
      if (record.state !== 'running') void client.invalidateQueries({ queryKey: ['requests'] })
      break
    }
    case 'login':
      client.setQueryData(['login', event.data.id], event.data)
      break
  }
}

// useAdminEvents 订阅管理事件流，断开后由浏览器自动重连并重新收到初始快照
export function useAdminEvents(enabled: boolean) {
  const client = useQueryClient()
  useEffect(() => {
    if (!enabled) return
    const source = new EventSource('/api/events')
    source.onopen = () => {
      client.setQueryData(logsKey, [])
      client.setQueryData(runningKey, [])
      client.setQueryData(connectionKey, true)
    }
    source.onerror = () => {
      client.setQueryData(connectionKey, false)
    }
    source.onmessage = (message: MessageEvent<string>) => {
      apply(client, JSON.parse(message.data) as AdminEvent)
    }
    return () => {
      source.close()
    }
  }, [client, enabled])
}
