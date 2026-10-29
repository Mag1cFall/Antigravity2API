import { BarChart3, ChevronDown, Download, X } from 'lucide-react'
import { type RefObject, useLayoutEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { Card, CardBody, CardHeader, CardToolbar } from '@/components/ui/card'
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Meter } from '@/components/ui/meter'
import { Segmented } from '@/components/ui/segmented'
import { EmptyState, ErrorState, Loading } from '@/components/ui/states'
import { dots, StatCards } from '@/components/ui/stats'
import { stackThird, stackTwoThirds, Table, Td, Th } from '@/components/ui/table'
import { Tooltip } from '@/components/ui/tooltip'
import { filterQuery, rangeQuery, useUsage } from '@/features/usage/queries'
import type { UsageReport, UsageStats } from '@/features/usage/types'
import {
  formatCompact,
  formatCost,
  formatDuration,
  formatNumber,
  formatPercent,
} from '@/lib/format'
import { defineMessages, fill, useI18n, useMessages } from '@/lib/i18n'

// usageMessages 是用量页的文案
const usageMessages = defineMessages(
  {
    title: '用量',
    range: '时间范围',
    filters: '筛选',
    filterCount: '{dimension} {count}',
    filterClear: '清除筛选',
    filterNone: '这段时间没有可选项',
    emptyFiltered: '没有符合筛选的请求',
    range24h: '24 小时',
    range7d: '7 天',
    range30d: '30 天',
    range90d: '90 天',
    groupBy: '分组',
    breakdown: '分项',
    model: '模型',
    api_key: 'API key',
    account: '账户',
    day: '日期',
    export: '导出 CSV',
    exportEmpty: '这段时间没有可导出的请求',
    exportFailed: '用量读取失败，无法导出',
    requests: '请求',
    requestsDetail: '成功 {succeeded} · 失败 {failed} · 限流 {limited}',
    change: '较上一周期 {change}',
    cacheRate: '缓存命中率',
    cacheDetail: '{cached} / {input} 输入 token',
    tokens: 'Token',
    tokensDetail: '思考 {reasoning} · 回复 {reply}',
    latency: '耗时 P50',
    latencyDetail: 'P95 {p95} · 首个事件 P50 {first}',
    cost: '估算费用',
    costDetail: '按各模型官方 API 单价估算',
    unpriced: '另有 {count} 个请求的模型没有单价',
    unpricedShort: '未定价 {count}',
    trend: '每 {bucket}的请求数',
    trendLabel: '请求趋势，峰值 {peak} 个请求',
    recent: '近 {minutes} 分钟 {requests} 个请求、{tokens} token',
    share: '请求占比',
    failed: '失败',
    limited: '限流',
    cached: '缓存命中',
    reasoning: '思考 token',
    reply: '回复 token',
    p50: 'P50',
    p95: 'P95',
    none: '（无）',
    loadFailed: '无法读取用量',
    emptyTitle: '这段时间没有请求',
    emptyDescription: '客户端发出请求后，这里按所选维度汇总。',
  },
  {
    title: 'Usage',
    range: 'Time range',
    filters: 'Filters',
    filterCount: '{dimension} {count}',
    filterClear: 'Clear filters',
    filterNone: 'Nothing to choose in this period',
    emptyFiltered: 'No requests match the filters',
    range24h: '24 hours',
    range7d: '7 days',
    range30d: '30 days',
    range90d: '90 days',
    groupBy: 'Group by',
    breakdown: 'Breakdown',
    model: 'Model',
    api_key: 'API key',
    account: 'Account',
    day: 'Day',
    export: 'Export CSV',
    exportEmpty: 'There are no requests to export in this period',
    exportFailed: 'Usage could not be loaded, so there is nothing to export',
    requests: 'Requests',
    requestsDetail: '{succeeded} succeeded · {failed} failed · {limited} rate limited',
    change: '{change} vs previous period',
    cacheRate: 'Cache hit rate',
    cacheDetail: '{cached} of {input} input tokens',
    tokens: 'Tokens',
    tokensDetail: 'thinking {reasoning} · reply {reply}',
    latency: 'Latency P50',
    latencyDetail: 'P95 {p95} · first event P50 {first}',
    cost: 'Estimated cost',
    costDetail: 'Estimated from official API prices per model',
    unpriced: 'Unpriced requests: {count}',
    unpricedShort: '{count} unpriced',
    trend: 'Requests per {bucket}',
    trendLabel: 'Request trend, peak {peak} requests',
    recent: '{requests} requests and {tokens} tokens in the last {minutes} minutes',
    share: 'Share of requests',
    failed: 'Failed',
    limited: 'Rate limited',
    cached: 'Cache hits',
    reasoning: 'Thinking tokens',
    reply: 'Reply tokens',
    p50: 'P50',
    p95: 'P95',
    none: '(none)',
    loadFailed: 'Could not load usage',
    emptyTitle: 'No requests in this period',
    emptyDescription:
      'Once clients send requests, they are summarized here by the selected dimension.',
  },
  {
    title: '用量',
    range: '時間範圍',
    filters: '篩選',
    filterCount: '{dimension} {count}',
    filterClear: '清除篩選',
    filterNone: '這段期間沒有可選項目',
    emptyFiltered: '沒有符合篩選的請求',
    range24h: '24 小時',
    range7d: '7 天',
    range30d: '30 天',
    range90d: '90 天',
    groupBy: '分組',
    breakdown: '明細',
    model: '模型',
    api_key: 'API key',
    account: '帳號',
    day: '日期',
    export: '匯出 CSV',
    exportEmpty: '這段期間沒有可匯出的請求',
    exportFailed: '用量讀取失敗，無法匯出',
    requests: '請求',
    requestsDetail: '成功 {succeeded} · 失敗 {failed} · 速率限制 {limited}',
    change: '較上一期 {change}',
    cacheRate: '快取命中率',
    cacheDetail: '{cached} / {input} 輸入 token',
    tokens: 'Token',
    tokensDetail: '思考 {reasoning} · 回覆 {reply}',
    latency: '耗時 P50',
    latencyDetail: 'P95 {p95} · 首個事件 P50 {first}',
    cost: '估算費用',
    costDetail: '依各模型官方 API 單價估算',
    unpriced: '另有 {count} 個請求的模型沒有單價',
    unpricedShort: '未定價 {count}',
    trend: '每 {bucket}的請求數',
    trendLabel: '請求趨勢，峰值 {peak} 個請求',
    recent: '近 {minutes} 分鐘 {requests} 個請求、{tokens} token',
    share: '請求佔比',
    failed: '失敗',
    limited: '速率限制',
    cached: '快取命中',
    reasoning: '思考 token',
    reply: '回覆 token',
    p50: 'P50',
    p95: 'P95',
    none: '（無）',
    loadFailed: '無法讀取用量',
    emptyTitle: '這段期間沒有請求',
    emptyDescription: '用戶端送出請求後，會在這裡依所選維度彙整。',
  },
)

// ranges 是用量页的时间范围
const ranges = { '24h': 86_400_000, '7d': 604_800_000, '30d': 2_592_000_000, '90d': 7_776_000_000 }
type RangeKey = keyof typeof ranges

// groupings 是用量表的分组，day 按本机时区的自然日分桶
const groupings = ['model', 'api_key', 'account', 'day'] as const
type Grouping = (typeof groupings)[number]

// cacheRate 返回缓存命中 token 占输入 token 的比例
function cacheRate(stats: UsageStats) {
  return stats.input_tokens ? stats.cached_tokens / stats.input_tokens : 0
}

// barWidth 是趋势图每根柱子连同间隔的最小宽度
const barWidth = 6

// useWidth 返回元素的内容宽度并随尺寸变化更新
function useWidth(ref: RefObject<HTMLElement | null>) {
  const [width, setWidth] = useState(0)
  useLayoutEffect(() => {
    const element = ref.current
    if (!element) return undefined
    setWidth(element.clientWidth)
    const observer = new ResizeObserver(() => {
      setWidth(element.clientWidth)
    })
    observer.observe(element)
    return () => {
      observer.disconnect()
    }
  }, [ref])
  return width
}

// TrendCard 是逐桶请求数的柱状图，宽度放不下每桶一根柱子时把相邻的桶合并，副标题随合并后的间隔变化
function TrendCard({ data }: { data: UsageReport }) {
  const m = useMessages(usageMessages)
  const { locale } = useI18n()
  const chart = useRef<HTMLDivElement>(null)
  const width = useWidth(chart)
  const per = width ? Math.max(1, Math.ceil((data.buckets.length * barWidth) / width)) : 1
  const bars: { at: string; requests: number }[] = []
  for (let index = 0; index < data.buckets.length; index += per) {
    const group = data.buckets.slice(index, index + per)
    bars.push({
      at: group[0]?.at ?? '',
      requests: group.reduce((sum, bucket) => sum + bucket.requests, 0),
    })
  }
  const peak = Math.max(1, ...bars.map((bar) => bar.requests))
  return (
    <Card>
      <CardHeader
        title={fill(m.trend, {
          bucket: formatDuration(data.bucket_seconds * 1000 * per, locale),
        })}
        actions={
          <span className="text-sm text-foreground-muted">
            {fill(m.recent, {
              minutes: data.recent.minutes,
              requests: formatNumber(data.recent.requests, locale),
              tokens: formatCompact(data.recent.tokens, locale),
            })}
          </span>
        }
      />
      <CardBody>
        <div
          ref={chart}
          role="img"
          aria-label={fill(m.trendLabel, { peak: formatNumber(peak, locale) })}
          className="flex h-chart items-end gap-px"
        >
          {bars.map((bar) => (
            <div
              key={bar.at}
              className="min-w-px flex-1 rounded-t-sm bg-primary"
              style={{
                height: `${String(Math.max(bar.requests ? 2 : 0, (bar.requests / peak) * 100))}%`,
              }}
            />
          ))}
        </div>
      </CardBody>
    </Card>
  )
}

// filterDimensions 是用量页可筛选的维度，取值写入同名 URL 参数，多个取值以逗号分隔
const filterDimensions = ['model', 'account', 'api_key'] as const
type FilterDimension = (typeof filterDimensions)[number]

// FilterMenu 是一个维度的多选筛选，候选来自报告中这段时间出现过的非空取值
function FilterMenu({
  label,
  options,
  selected,
  onChange,
}: {
  label: string
  options: string[]
  selected: string[]
  onChange: (values: string[]) => void
}) {
  const m = useMessages(usageMessages)
  const values = [...new Set([...selected, ...options.filter(Boolean)])]
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          size="sm"
          className={
            'gap-2 ' +
            (selected.length > 0 ? 'border-primary-border bg-primary-soft text-primary' : '')
          }
        >
          {selected.length > 0
            ? fill(m.filterCount, { dimension: label, count: selected.length })
            : label}
          <ChevronDown size={16} aria-hidden className="text-foreground-muted" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        {values.length === 0 ? (
          <p className="px-4 py-2 text-sm text-foreground-muted">{m.filterNone}</p>
        ) : (
          values.map((value) => (
            <DropdownMenuCheckboxItem
              key={value}
              checked={selected.includes(value)}
              onCheckedChange={(checked) => {
                onChange(checked ? [...selected, value] : selected.filter((item) => item !== value))
              }}
            >
              <span className="font-mono text-xs">{value || m.none}</span>
            </DropdownMenuCheckboxItem>
          ))
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

// UsagePage 是用量统计，范围、分组与筛选保存在 URL
export function UsagePage() {
  const m = useMessages(usageMessages)
  const app = useMessages(appMessages)
  const { locale } = useI18n()
  const [params, setParams] = useSearchParams()
  const range =
    (Object.keys(ranges) as RangeKey[]).find((key) => key === params.get('range')) ?? '7d'
  const grouping: Grouping = groupings.find((key) => key === params.get('by')) ?? 'model'
  const filters: Record<FilterDimension, string[]> = {
    model: (params.get('model') ?? '').split(',').filter(Boolean),
    account: (params.get('account') ?? '').split(',').filter(Boolean),
    api_key: (params.get('api_key') ?? '').split(',').filter(Boolean),
  }
  const filtered = filterDimensions.some((dimension) => filters[dimension].length > 0)
  const usage = useUsage(ranges[range], grouping === 'day' ? '86400' : 'auto', filters)
  const update = (changes: Record<string, string>) => {
    setParams(
      (current) => {
        const next = new URLSearchParams(current)
        for (const [key, value] of Object.entries(changes)) {
          if (value) next.set(key, value)
          else next.delete(key)
        }
        return next
      },
      { replace: true },
    )
  }
  const csv = '/api/usage/records.csv?' + filterQuery(rangeQuery(ranges[range]), filters).toString()
  const rangeLabels: Record<RangeKey, string> = {
    '24h': m.range24h,
    '7d': m.range7d,
    '30d': m.range30d,
    '90d': m.range90d,
  }
  const header = (
    <div className="flex flex-wrap items-center gap-2">
      <Segmented
        label={m.range}
        value={range}
        onValueChange={(value) => {
          update({ range: value })
        }}
        options={(Object.keys(ranges) as RangeKey[]).map((value) => ({
          value,
          label: rangeLabels[value],
        }))}
      />
      <div role="group" aria-label={m.filters} className="flex flex-wrap items-center gap-2">
        {filterDimensions.map((dimension) => (
          <FilterMenu
            key={dimension}
            label={m[dimension]}
            options={usage.data?.options[dimension] ?? []}
            selected={filters[dimension]}
            onChange={(values) => {
              update({ [dimension]: values.join(',') })
            }}
          />
        ))}
        {filtered && (
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              update({ model: '', account: '', api_key: '' })
            }}
          >
            <X size={16} aria-hidden />
            {m.filterClear}
          </Button>
        )}
      </div>
      <span className="flex-1" />
      {usage.data && usage.data.totals.requests > 0 ? (
        <Button size="sm" asChild>
          <a href={csv} download>
            <Download size={16} aria-hidden />
            {m.export}
          </a>
        </Button>
      ) : (
        <Tooltip
          disabledReason={
            usage.isPending ? app.loading : usage.isError ? m.exportFailed : m.exportEmpty
          }
        >
          <Button size="sm" disabled>
            <Download size={16} aria-hidden />
            {m.export}
          </Button>
        </Tooltip>
      )}
    </div>
  )
  if (usage.isPending) {
    return (
      <>
        {header}
        <Card>
          <Loading label={app.loading} />
        </Card>
      </>
    )
  }
  if (usage.isError) {
    return (
      <>
        {header}
        <Card>
          <ErrorState
            title={m.loadFailed}
            error={usage.error}
            retryLabel={app.retry}
            onRetry={() => void usage.refetch()}
          />
        </Card>
      </>
    )
  }
  const totals = usage.data.totals
  if (totals.requests === 0) {
    return (
      <>
        {header}
        <Card>
          <EmptyState
            icon={<BarChart3 size={20} aria-hidden />}
            title={filtered ? m.emptyFiltered : m.emptyTitle}
            description={filtered ? undefined : m.emptyDescription}
          />
        </Card>
      </>
    )
  }
  const data = usage.data
  const dayFormat = new Intl.DateTimeFormat(locale, {
    month: '2-digit',
    day: '2-digit',
    weekday: 'short',
  })
  const rows: (UsageStats & { key: string })[] =
    grouping === 'day'
      ? data.buckets
          .filter((bucket) => bucket.requests > 0)
          .map((bucket) => ({ ...bucket, key: dayFormat.format(new Date(bucket.at)) }))
          .reverse()
      : (data.groups[grouping] ?? [])
  const previous = data.previous.requests
  const change = previous ? (totals.requests - previous) / previous : undefined
  return (
    <>
      {header}
      <StatCards
        items={[
          {
            key: 'requests',
            label: m.requests,
            value: formatNumber(totals.requests, locale),
            detail: dots(
              fill(m.requestsDetail, {
                succeeded: formatNumber(totals.succeeded, locale),
                failed: formatNumber(totals.failed, locale),
                limited: formatNumber(totals.rate_limited, locale),
              }),
              change === undefined
                ? ''
                : fill(m.change, {
                    change: (change > 0 ? '+' : '') + formatPercent(change, locale),
                  }),
            ),
          },
          {
            key: 'cache',
            label: m.cacheRate,
            value: formatPercent(cacheRate(totals), locale),
            detail: [
              fill(m.cacheDetail, {
                cached: formatCompact(totals.cached_tokens, locale),
                input: formatCompact(totals.input_tokens, locale),
              }),
            ],
          },
          {
            key: 'tokens',
            label: m.tokens,
            value: formatCompact(totals.total_tokens, locale),
            detail: dots(
              fill(m.tokensDetail, {
                reasoning: formatCompact(totals.reasoning_tokens, locale),
                reply: formatCompact(totals.reply_tokens, locale),
              }),
            ),
          },
          {
            key: 'latency',
            label: m.latency,
            value: formatDuration(totals.duration.p50_ms, locale),
            detail: dots(
              fill(m.latencyDetail, {
                p95: formatDuration(totals.duration.p95_ms, locale),
                first: formatDuration(totals.first_event.p50_ms, locale),
              }),
            ),
          },
          {
            key: 'cost',
            label: m.cost,
            value: formatCost(totals.cost_usd, locale),
            detail: [
              totals.unpriced
                ? fill(m.unpriced, { count: formatNumber(totals.unpriced, locale) })
                : m.costDetail,
            ],
          },
        ]}
      />
      {grouping !== 'day' && <TrendCard data={data} />}
      <Card>
        <CardToolbar>
          <h2 className="text-md font-semibold">{m.breakdown}</h2>
          <Segmented
            label={m.groupBy}
            value={grouping}
            onValueChange={(value) => {
              update({ by: value })
            }}
            options={groupings.map((value) => ({ value, label: m[value] }))}
          />
        </CardToolbar>
        <Table label={m.title} fixed stack minWidth={760}>
          <colgroup>
            <col />
            <col className="w-[88px]" />
            <col className="w-[120px]" />
            <col className="w-[72px]" />
            <col className="w-[72px] max-xl:hidden" />
            <col className="w-[128px] max-xl:hidden" />
            <col className="w-[96px] max-xl:hidden" />
            <col className="w-[96px]" />
            <col className="w-[80px] max-xl:hidden" />
            <col className="w-[80px]" />
            <col className="w-[104px]" />
          </colgroup>
          <thead>
            <tr>
              <Th>{m[grouping]}</Th>
              <Th className="text-right">{m.requests}</Th>
              <Th>{m.share}</Th>
              <Th className="text-right">{m.failed}</Th>
              <Th className="text-right max-xl:hidden">{m.limited}</Th>
              <Th className="text-right max-xl:hidden">{m.cached}</Th>
              <Th className="text-right max-xl:hidden">{m.reasoning}</Th>
              <Th className="text-right">{m.reply}</Th>
              <Th className="text-right max-xl:hidden">{m.p50}</Th>
              <Th className="text-right">{m.p95}</Th>
              <Th className="text-right">{m.cost}</Th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.key}>
                <Td className="truncate font-mono text-xs max-sm:basis-full" title={row.key}>
                  {row.key || m.none}
                </Td>
                <Td label={m.requests} className={'text-right max-sm:text-left ' + stackThird}>
                  {formatNumber(row.requests, locale)}
                </Td>
                <Td label={m.share} className={stackTwoThirds}>
                  <Meter
                    value={row.requests / totals.requests}
                    tone="info"
                    label={m.share + ' ' + formatPercent(row.requests / totals.requests, locale)}
                  />
                </Td>
                <Td
                  label={m.failed}
                  className={
                    'text-right max-sm:text-left ' +
                    stackThird +
                    (row.failed ? ' text-error-text' : '')
                  }
                >
                  {formatNumber(row.failed, locale)}
                </Td>
                <Td
                  className={
                    'text-right max-xl:hidden ' + (row.rate_limited ? 'text-warning-text' : '')
                  }
                >
                  {formatNumber(row.rate_limited, locale)}
                </Td>
                <Td className="text-right whitespace-nowrap max-xl:hidden">
                  {formatCompact(row.cached_tokens, locale)}
                  <span className="text-foreground-muted">
                    {' · ' + formatPercent(cacheRate(row), locale)}
                  </span>
                </Td>
                <Td className="text-right max-xl:hidden">
                  {formatCompact(row.reasoning_tokens, locale)}
                </Td>
                <Td label={m.reply} className={'text-right max-sm:text-left ' + stackThird}>
                  {formatCompact(row.reply_tokens, locale)}
                </Td>
                <Td className="text-right whitespace-nowrap max-xl:hidden">
                  {formatDuration(row.duration.p50_ms, locale)}
                </Td>
                <Td
                  label={m.p95}
                  className={'text-right whitespace-nowrap max-sm:text-left ' + stackThird}
                >
                  {formatDuration(row.duration.p95_ms, locale)}
                </Td>
                <Td
                  label={m.cost}
                  className={'text-right whitespace-nowrap max-sm:text-left ' + stackThird}
                >
                  {formatCost(row.cost_usd, locale)}
                  {row.unpriced > 0 && (
                    <div className="text-xs text-foreground-muted">
                      {fill(m.unpricedShort, { count: formatNumber(row.unpriced, locale) })}
                    </div>
                  )}
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      </Card>
    </>
  )
}
