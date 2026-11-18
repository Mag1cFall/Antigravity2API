import { type QueryClient, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'

import { invalidate, requestDependents } from '@/app/sync'
import { accountsKey, loginFailuresKey, loginKey, loginsKey } from '@/features/accounts/queries'
import type { Account, Login } from '@/features/accounts/types'
import type { LogEntry } from '@/features/logs/types'
import type { RequestRecord } from '@/features/requests/types'
import type { Status } from '@/features/service/types'
import { api } from '@/lib/api'

// logsKey 保存事件流推送的服务日志，最多 2000 条，写入时不做结构共享，没有页面订阅时也保留
export const logsKey = ['logs'] as const

// runningKey 保存事件流推送的进行中请求
const runningKey = ['requests-running'] as const

// connectionKey 保存事件流的连接状态：connecting 为尚未收完初始快照，open 为已收完初始快照，closed 为已断开
const connectionKey = ['events', 'connection'] as const

// Connection 是事件流的连接状态
type Connection = 'connecting' | 'open' | 'closed'

// reconnectDelay 是事件流被服务拒绝后重新连接的间隔
const reconnectDelay = 3000

// maxLogs 是页面保留的日志条数
const maxLogs = 2000

// refreshWindow 是请求结束后重新读取请求记录与用量的最短间隔
const refreshWindow = 1000

// AdminEvent 是事件流推送的一条管理事件
type AdminEvent =
  | { type: 'accounts'; data: Account[] }
  | { type: 'log'; data: LogEntry }
  | { type: 'request'; data: RequestRecord }
  | { type: 'login'; data: Login }
  | { type: 'ready' }

// Snapshot 是连接建立后到 ready 之间收到的初始快照：最近日志与进行中的请求
interface Snapshot {
  logs: LogEntry[]
  running: RequestRecord[]
}

// mergeLogs 把初始快照的最近日志接在页面已有的日志之后：快照的第一条在已有日志中时保留它之前的日志，否则（首次连接、服务重启或日志已清空）以快照为准
function mergeLogs(current: LogEntry[], incoming: LogEntry[]) {
  const first = incoming[0]
  const at = first
    ? current.findIndex((entry) => entry.time === first.time && entry.message === first.message)
    : -1
  return at < 0 ? incoming : [...current.slice(0, at), ...incoming].slice(-maxLogs)
}

// refreshing 是请求结束后重新读取的节流状态：timer 为间隔计时，pending 为间隔内又有请求结束
const refreshing: { timer: ReturnType<typeof setTimeout> | undefined; pending: boolean } = {
  timer: undefined,
  pending: false,
}

// refreshRequests 在请求结束后重新读取请求记录与用量：间隔内的第一次立即读取，其余合并为间隔结束时的一次
function refreshRequests(client: QueryClient) {
  if (refreshing.timer) {
    refreshing.pending = true
    return
  }
  invalidate(client, requestDependents)
  refreshing.timer = setTimeout(() => {
    refreshing.timer = undefined
    if (!refreshing.pending) return
    refreshing.pending = false
    refreshRequests(client)
  }, refreshWindow)
}

// apply 把一条管理事件写入对应的查询缓存；snapshot 存在时日志与进行中的请求先收进初始快照，收到 ready 时一次写入
function apply(client: QueryClient, event: AdminEvent, snapshot: Snapshot | undefined) {
  switch (event.type) {
    case 'accounts':
      client.setQueryData(accountsKey, { accounts: event.data })
      break
    case 'log':
      if (snapshot) {
        snapshot.logs.push(event.data)
        break
      }
      client.setQueryData<LogEntry[]>(logsKey, (logs = []) =>
        logs.length >= maxLogs
          ? [...logs.slice(logs.length - maxLogs + 1), event.data]
          : [...logs, event.data],
      )
      break
    case 'request': {
      const record = event.data
      if (snapshot) {
        snapshot.running.push(record)
        break
      }
      client.setQueryData<RequestRecord[]>(runningKey, (running = []) => {
        const others = running.filter((item) => item.id !== record.id)
        return record.state === 'running' ? [...others, record] : others
      })
      if (record.state !== 'running') refreshRequests(client)
      break
    }
    case 'login': {
      const login = event.data
      const known =
        client.getQueryData<Login>(loginKey(login.id))?.state === 'pending' ||
        client.getQueryData<Login[]>(loginsKey)?.some((item) => item.id === login.id)
      client.setQueryData(loginKey(login.id), login)
      if (login.state === 'failed' && known) {
        client.setQueryData<Login[]>(loginFailuresKey, (failures = []) => [...failures, login])
      }
      void client.invalidateQueries({ queryKey: loginsKey })
      break
    }
    case 'ready':
      if (snapshot) {
        client.setQueryData<LogEntry[]>(logsKey, (logs = []) => mergeLogs(logs, snapshot.logs))
        client.setQueryData(runningKey, snapshot.running)
      }
      client.setQueryData<Connection>(connectionKey, 'open')
      break
  }
}

// useAdminEvents 订阅管理事件流，收到 ready 时把初始快照写入缓存（日志接在页面已有的日志之后），断开后由浏览器自动重连并重新收到初始快照，服务以错误状态拒绝时 3 秒后重新连接；重连后服务版本变化时（在线更新重启）刷新页面
export function useAdminEvents(enabled: boolean) {
  const client = useQueryClient()
  useEffect(() => {
    if (!enabled) return
    let version = ''
    let snapshot: Snapshot | undefined
    let source: EventSource
    let timer: ReturnType<typeof setTimeout> | undefined
    const connect = () => {
      source = new EventSource('/api/events')
      source.onopen = () => {
        snapshot = { logs: [], running: [] }
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
        const event = JSON.parse(message.data) as AdminEvent
        apply(client, event, snapshot)
        if (event.type === 'ready') snapshot = undefined
      }
    }
    connect()
    return () => {
      clearTimeout(timer)
      source.close()
    }
  }, [client, enabled])
}

// useRunning 返回事件流推送的进行中请求
export function useRunning() {
  const client = useQueryClient()
  return (
    useQuery({
      queryKey: runningKey,
      queryFn: () => client.getQueryData<RequestRecord[]>(runningKey) ?? [],
      staleTime: Number.POSITIVE_INFINITY,
    }).data ?? []
  )
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
