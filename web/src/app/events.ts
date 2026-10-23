import { type QueryClient, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'

import type { Account, Login } from '@/features/accounts/types'
import type { LogEntry } from '@/features/logs/types'
import type { RequestRecord } from '@/features/requests/types'
import type { Status } from '@/features/service/types'
import { api } from '@/lib/api'

// logsKey 保存事件流推送的服务日志，最多 2000 条
export const logsKey = ['logs'] as const

// runningKey 保存事件流推送的进行中请求
export const runningKey = ['requests-running'] as const

// connectionKey 保存事件流的连接状态：connecting 为尚未连通，open 为已连通，closed 为已断开
export const connectionKey = ['events', 'connection'] as const

// Connection 是事件流的连接状态
export type Connection = 'connecting' | 'open' | 'closed'

// reconnectDelay 是事件流被服务拒绝后重新连接的间隔
const reconnectDelay = 3000

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

// useAdminEvents 订阅管理事件流，断开后由浏览器自动重连并重新收到初始快照，服务以错误状态拒绝时 3 秒后重新连接；重连后服务版本变化时（在线更新重启）刷新页面
export function useAdminEvents(enabled: boolean) {
  const client = useQueryClient()
  useEffect(() => {
    if (!enabled) return
    let version = ''
    let source: EventSource
    let timer: ReturnType<typeof setTimeout> | undefined
    const connect = () => {
      source = new EventSource('/api/events')
      source.onopen = () => {
        client.setQueryData(logsKey, [])
        client.setQueryData(runningKey, [])
        client.setQueryData<Connection>(connectionKey, 'open')
        void api<Status>('/api/status').then((status) => {
          if (version && status.version !== version) window.location.reload()
          version = status.version
        })
      }
      source.onerror = () => {
        client.setQueryData<Connection>(connectionKey, 'closed')
        if (source.readyState === EventSource.CLOSED) timer = setTimeout(connect, reconnectDelay)
      }
      source.onmessage = (message: MessageEvent<string>) => {
        apply(client, JSON.parse(message.data) as AdminEvent)
      }
    }
    connect()
    return () => {
      clearTimeout(timer)
      source.close()
    }
  }, [client, enabled])
}

// useConnection 返回事件流的连接状态
export function useConnection() {
  return useQuery<Connection>({
    queryKey: connectionKey,
    queryFn: () => 'connecting',
    enabled: false,
    initialData: 'connecting',
  }).data
}
