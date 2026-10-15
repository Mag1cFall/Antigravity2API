import type { RequestRecord } from '@/features/requests/types'

// LogLevel 对应 internal/app 的日志级别
export type LogLevel = 'DEBUG' | 'INFO' | 'WARN' | 'ERROR'

// LogEntry 对应 internal/requestdb 的 Log，request 是请求开始与结束日志附带的请求记录
export interface LogEntry {
  seq?: number
  time: string
  level: LogLevel
  source: string
  event?: string
  message: string
  request_id?: string
  model?: string
  request?: RequestRecord
  attrs?: Record<string, unknown>
}

// LogPage 对应 GET /api/logs 的一页已保存日志，next_before 为下一页的 before
export interface LogPage {
  items: LogEntry[]
  next_before?: number
}
