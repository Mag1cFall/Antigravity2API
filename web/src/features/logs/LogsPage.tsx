import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query'
import { useVirtualizer } from '@tanstack/react-virtual'
import { ScrollText, X } from 'lucide-react'
import { type ReactNode, useEffect, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router'

import { logsKey, useConnection } from '@/app/events'
import { Button } from '@/components/ui/button'
import { Card, CardToolbar } from '@/components/ui/card'
import { SearchInput } from '@/components/ui/search-input'
import { Segmented } from '@/components/ui/segmented'
import { Select } from '@/components/ui/select'
import { EmptyState, ErrorState, Loading } from '@/components/ui/states'
import { Switch } from '@/components/ui/switch'
import { toast } from '@/components/ui/toast'
import { Tooltip } from '@/components/ui/tooltip'
import { matchLog, type MinimumLevel, parseLogFilter } from '@/features/logs/filter'
import { logMessages } from '@/features/logs/messages'
import type { LogEntry, LogLevel, LogPage } from '@/features/logs/types'
import { api } from '@/lib/api'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// levelClass 是日志级别文字的颜色
const levelClass: Record<LogLevel, string> = {
  DEBUG: 'text-foreground-subtle',
  INFO: 'text-primary',
  WARN: 'text-warning-text',
  ERROR: 'text-error-text',
}

// LogLine 是一条日志：时间、级别、来源与消息
function LogLine({ entry, timeFormat }: { entry: LogEntry; timeFormat: Intl.DateTimeFormat }) {
  const m = useMessages(logMessages)
  return (
    <div
      className={
        'grid grid-cols-1 gap-x-3 border-b border-border px-6 py-2 font-mono text-xs max-sm:px-4 sm:grid-cols-log ' +
        (entry.level === 'ERROR'
          ? 'bg-error-soft'
          : entry.level === 'WARN'
            ? 'bg-warning-soft'
            : '')
      }
    >
      <span className="text-foreground-subtle tabular-nums">
        {timeFormat.format(new Date(entry.time))}
      </span>
      <span className={'font-semibold ' + levelClass[entry.level]}>{entry.level}</span>
      <Tooltip label={entry.source}>
        <span className="truncate text-foreground-muted">{entry.source}</span>
      </Tooltip>
      <span className="min-w-0 break-words whitespace-pre-wrap text-foreground select-text">
        {entry.message}
        {entry.request_id && (
          <>
            {' '}
            <Link
              to={'/requests?id=' + encodeURIComponent(entry.request_id)}
              className="font-sans text-primary hover:text-primary-hover"
            >
              {m.viewRequest}
            </Link>
          </>
        )}
      </span>
    </div>
  )
}

// savedRanges 是已保存日志的时间范围
const savedRanges = { '24h': 86_400_000, '7d': 604_800_000, '30d': 2_592_000_000 }
type SavedRange = keyof typeof savedRanges

// savedQuery 是 GET /api/logs 的查询参数，范围截止到当前时间，before 为 0 时从最新一条开始
function savedQuery(
  range: SavedRange,
  errorsOnly: boolean,
  q: string,
  requestId: string,
  before: number,
) {
  const now = Date.now()
  const query = new URLSearchParams({
    from: new Date(now - savedRanges[range]).toISOString(),
    to: new Date(now + 1000).toISOString(),
    limit: '100',
  })
  if (errorsOnly) query.set('level', 'ERROR')
  if (q.trim()) query.set('q', q.trim())
  if (requestId) query.set('request_id', requestId)
  if (before) query.set('before', String(before))
  return query
}

// SavedLogs 检索写入账本的 WARN 以上日志，按时间范围、级别、请求 ID 与关键词筛选，条件保存在 URL；toolbar 是视图切换等通用控件
function SavedLogs({ toolbar }: { toolbar: ReactNode }) {
  const m = useMessages(logMessages)
  const { locale } = useI18n()
  const [params, setParams] = useSearchParams()
  const range =
    (Object.keys(savedRanges) as SavedRange[]).find((key) => key === params.get('range')) ?? '24h'
  const errorsOnly = params.get('level') === 'ERROR'
  const q = params.get('q') ?? ''
  const requestId = params.get('request_id') ?? ''
  const logs = useInfiniteQuery({
    queryKey: ['saved-logs', range, errorsOnly, q, requestId],
    queryFn: ({ pageParam }) =>
      api<LogPage>(
        '/api/logs?' + savedQuery(range, errorsOnly, q, requestId, pageParam).toString(),
      ),
    initialPageParam: 0,
    getNextPageParam: (last) => last.next_before,
  })
  const update = (key: string, value: string) => {
    setParams(
      (current) => {
        const next = new URLSearchParams(current)
        if (value) next.set(key, value)
        else next.delete(key)
        return next
      },
      { replace: true },
    )
  }
  const timeFormat = new Intl.DateTimeFormat(locale, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
  const entries = logs.data?.pages.flatMap((page) => page.items) ?? []
  return (
    <Card>
      <CardToolbar>
        {toolbar}
        <Segmented
          label={m.range}
          value={range}
          onValueChange={(value) => {
            update('range', value === '24h' ? '' : value)
          }}
          options={[
            { value: '24h', label: m.range24h },
            { value: '7d', label: m.range7d },
            { value: '30d', label: m.range30d },
          ]}
        />
        <Segmented
          label={m.filterLevel}
          value={errorsOnly ? 'ERROR' : 'WARN'}
          onValueChange={(value) => {
            update('level', value === 'ERROR' ? 'ERROR' : '')
          }}
          options={[
            { value: 'WARN', label: m.warn },
            { value: 'ERROR', label: m.error },
          ]}
        />
        {requestId && (
          <Button
            size="sm"
            aria-label={fill(m.clearRequest, { id: requestId })}
            onClick={() => {
              update('request_id', '')
            }}
          >
            <span className="font-mono">{requestId}</span>
            <X size={16} aria-hidden />
          </Button>
        )}
        <SearchInput
          aria-label={m.searchSaved}
          placeholder={m.searchSaved}
          value={q}
          onChange={(event) => {
            update('q', event.target.value)
          }}
          className="max-w-field min-w-[200px] flex-[1_1_240px]"
        />
      </CardToolbar>
      {logs.isPending ? (
        <Loading label={m.loading} />
      ) : logs.isError ? (
        <ErrorState
          title={m.loadFailed}
          error={logs.error}
          retryLabel={m.retry}
          onRetry={() => void logs.refetch()}
        />
      ) : entries.length === 0 ? (
        <EmptyState
          icon={<ScrollText size={20} aria-hidden />}
          title={m.savedEmptyTitle}
          description={m.savedEmptyDescription}
        />
      ) : (
        <div className="flex flex-col">
          {entries.map((entry, index) => (
            <LogLine key={entry.seq ?? index} entry={entry} timeFormat={timeFormat} />
          ))}
          {logs.hasNextPage && (
            <div className="px-6 py-3">
              <Button
                size="sm"
                loading={logs.isFetchingNextPage}
                onClick={() => {
                  void logs.fetchNextPage()
                }}
              >
                {m.older}
              </Button>
            </div>
          )}
        </div>
      )}
    </Card>
  )
}

// LogsPage 是服务日志：实时尾随与已保存日志检索，视图与筛选条件保存在 URL；实时日志区占满页面剩余高度
export function LogsPage() {
  const m = useMessages(logMessages)
  const { locale } = useI18n()
  const connection = useConnection()
  const client = useQueryClient()
  const lastTop = useRef(0)
  const [params, setParams] = useSearchParams()
  const filter = parseLogFilter(params)
  const [follow, setFollow] = useState(true)
  const scrollRef = useRef<HTMLDivElement>(null)
  const logs = useQuery({
    queryKey: logsKey,
    queryFn: () => client.getQueryData<LogEntry[]>(logsKey) ?? [],
    staleTime: Number.POSITIVE_INFINITY,
  })
  const all = logs.data ?? []
  const visible = all.filter((entry) => matchLog(entry, filter))
  const sources = [...new Set(all.map((entry) => entry.source))].sort()
  const virtualizer = useVirtualizer({
    count: visible.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 33,
    overscan: 16,
  })
  const timeFormat = new Intl.DateTimeFormat(locale, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    fractionalSecondDigits: 3,
    hour12: false,
  })

  const totalSize = virtualizer.getTotalSize()
  useEffect(() => {
    const element = scrollRef.current
    if (follow && element) element.scrollTop = element.scrollHeight
  }, [follow, visible.length, totalSize])

  const update = (key: string, value: string) => {
    setParams(
      (current) => {
        const next = new URLSearchParams(current)
        if (value && value !== 'all') next.set(key, value)
        else next.delete(key)
        return next
      },
      { replace: true },
    )
  }
  const levelLabels: Record<MinimumLevel, string> = {
    all: m.all,
    INFO: m.info,
    WARN: m.warn,
    ERROR: m.error,
  }
  const view = params.get('view') === 'saved' ? 'saved' : 'live'
  const viewSwitch = (
    <Segmented
      label={m.view}
      value={view}
      onValueChange={(value) => {
        setParams(value === 'saved' ? { view: 'saved' } : {}, { replace: true })
      }}
      options={[
        { value: 'live', label: m.live },
        { value: 'saved', label: m.saved },
      ]}
    />
  )
  const settingsLink = (
    <Button size="sm" asChild>
      <Link to="/settings/logging">{m.levelSettings}</Link>
    </Button>
  )
  const clear = () => {
    const previous = all
    client.setQueryData<LogEntry[]>(logsKey, [])
    const timer = setTimeout(() => {
      api('/api/logs', { method: 'DELETE' }).catch((error: unknown) => {
        client.setQueryData<LogEntry[]>(logsKey, (current) => [...previous, ...(current ?? [])])
        toast({
          tone: 'error',
          title: fill(m.clearFailed, {
            message: error instanceof Error ? error.message : String(error),
          }),
        })
      })
    }, 5000)
    toast({
      tone: 'success',
      title: m.cleared,
      action: {
        label: m.undo,
        onClick: () => {
          clearTimeout(timer)
          client.setQueryData<LogEntry[]>(logsKey, (current) => [...previous, ...(current ?? [])])
        },
      },
    })
  }

  if (view === 'saved') {
    return (
      <>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <p className="text-sm text-foreground-muted">{m.savedDescription}</p>
          {settingsLink}
        </div>
        <SavedLogs toolbar={viewSwitch} />
      </>
    )
  }

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-foreground-muted">{m.description}</p>
        <div className="flex flex-wrap items-center gap-3">
          <span className="text-sm text-foreground-muted tabular-nums">
            {fill(m.count, { shown: visible.length, total: all.length })}
          </span>
          <label className="flex items-center gap-2 text-sm">
            <Switch checked={follow} onCheckedChange={setFollow} />
            {m.follow}
          </label>
          <Tooltip disabledReason={all.length === 0 && m.clearEmpty}>
            <Button size="sm" disabled={all.length === 0} onClick={clear}>
              {m.clear}
            </Button>
          </Tooltip>
          {settingsLink}
        </div>
      </div>
      <Card className={visible.length > 0 ? 'flex-1' : undefined}>
        <CardToolbar>
          {viewSwitch}
          <Segmented
            label={m.filterLevel}
            value={filter.level}
            onValueChange={(value) => {
              update('level', value)
            }}
            options={(['all', 'INFO', 'WARN', 'ERROR'] as const).map((value) => ({
              value,
              label: levelLabels[value],
            }))}
          />
          <Select
            label={m.source}
            size="sm"
            className="max-w-field"
            value={filter.source || 'all'}
            onValueChange={(value) => {
              update('source', value)
            }}
            options={[
              { value: 'all', label: m.allSources },
              ...sources.map((source) => ({ value: source, label: source })),
            ]}
          />
          <SearchInput
            aria-label={m.search}
            placeholder={m.search}
            value={filter.q}
            onChange={(event) => {
              update('q', event.target.value)
            }}
            className="max-w-field min-w-[200px] flex-[1_1_240px]"
          />
        </CardToolbar>
        {visible.length === 0 ? (
          all.length === 0 && connection === 'connecting' ? (
            <Loading label={m.loading} />
          ) : all.length === 0 && connection === 'closed' ? (
            <ErrorState title={m.liveFailed} error={m.liveFailedDescription} retryLabel={m.retry} />
          ) : all.length === 0 ? (
            <EmptyState
              icon={<ScrollText size={20} aria-hidden />}
              title={m.emptyTitle}
              description={m.emptyDescription}
            />
          ) : (
            <EmptyState
              icon={<ScrollText size={20} aria-hidden />}
              title={m.noMatchTitle}
              description={m.noMatchDescription}
              action={
                <Button
                  onClick={() => {
                    setParams({}, { replace: true })
                  }}
                >
                  {m.clearFilters}
                </Button>
              }
            />
          )
        ) : (
          <div
            ref={scrollRef}
            role="log"
            aria-label={m.title}
            tabIndex={0}
            onScroll={(event) => {
              const element = event.currentTarget
              const atBottom = element.scrollHeight - element.scrollTop - element.clientHeight < 48
              if (element.scrollTop < lastTop.current && !atBottom && follow) setFollow(false)
              lastTop.current = element.scrollTop
            }}
            className="min-h-[320px] flex-1 [scrollbar-gutter:stable] overflow-y-auto contain-size"
          >
            <div className="relative w-full" style={{ height: totalSize }}>
              {virtualizer.getVirtualItems().map((item) => {
                const entry = visible[item.index]
                if (!entry) return null
                return (
                  <div
                    key={item.key}
                    data-index={item.index}
                    ref={virtualizer.measureElement}
                    className="absolute inset-x-0 top-0"
                    style={{ transform: `translateY(${String(item.start)}px)` }}
                  >
                    <LogLine entry={entry} timeFormat={timeFormat} />
                  </div>
                )
              })}
            </div>
          </div>
        )}
      </Card>
    </>
  )
}
