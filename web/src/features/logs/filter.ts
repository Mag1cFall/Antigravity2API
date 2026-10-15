import type { LogEntry, LogLevel } from '@/features/logs/types'

// levelRank 是日志级别的严重程度
const levelRank: Record<LogLevel, number> = { DEBUG: 0, INFO: 1, WARN: 2, ERROR: 3 }

// minimumLevels 是级别筛选的取值，all 显示全部
const minimumLevels = ['all', 'INFO', 'WARN', 'ERROR'] as const

// MinimumLevel 是级别筛选的取值
export type MinimumLevel = (typeof minimumLevels)[number]

// LogFilter 是日志页的筛选条件
export interface LogFilter {
  level: MinimumLevel
  source: string
  q: string
}

// parseLogFilter 从 URL 参数读取日志筛选条件
export function parseLogFilter(params: URLSearchParams): LogFilter {
  const level = params.get('level')
  return {
    level: minimumLevels.find((value) => value === level) ?? 'all',
    source: params.get('source') ?? '',
    q: params.get('q') ?? '',
  }
}

// matchLog 判断日志是否达到最低级别、来源相同并包含搜索词
export function matchLog(entry: LogEntry, filter: LogFilter) {
  if (filter.level !== 'all' && levelRank[entry.level] < levelRank[filter.level]) return false
  if (filter.source && entry.source !== filter.source) return false
  const needle = filter.q.trim().toLowerCase()
  return (
    !needle ||
    entry.message.toLowerCase().includes(needle) ||
    entry.source.toLowerCase().includes(needle)
  )
}
