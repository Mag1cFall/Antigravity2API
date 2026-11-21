// LogLevel 对应 internal/app 的日志级别
export type LogLevel = 'DEBUG' | 'INFO' | 'WARN' | 'ERROR'

// LogEntry 对应 internal/requestdb 的 Log
export interface LogEntry {
  seq?: number
  time: string
  level: LogLevel
  source: string
  event?: string
  message: string
  request_id?: string
}

// LogPage 对应 GET /api/logs 的一页已保存日志，next_before 为下一页的 before
export interface LogPage {
  items: LogEntry[]
  next_before?: number
}
