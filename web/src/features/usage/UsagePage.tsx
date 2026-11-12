import { BarChart3, ChevronDown, Download, X } from 'lucide-react'
import { type RefObject, useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router'

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
  dateFormat,
  formatCompact,
  formatCost,
  formatDuration,
  formatNumber,
  formatPercent,
} from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'
import { changeParams } from '@/lib/params'

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

// useWidth 返回元素的内容宽度并随尺寸变化更新，宽度来自尺寸观察的回调，挂载时不读布局
function useWidth(ref: RefObject<HTMLElement | null>) {
  const [width, setWidth] = useState(0)
  useEffect(() => {
    const element = ref.current
    if (!element) return undefined
    const observer = new ResizeObserver(([entry]) => {
      if (entry) setWidth(entry.contentRect.width)
    })
    observer.observe(element)
    return () => {
      observer.disconnect()
    }
  }, [ref])
  return width
}

// TrendCard 是逐桶请求数的柱状图，下方标出起止时间，悬停柱子显示该段的起始时间、时长与请求数，宽度放不下每桶一根柱子时把相邻的桶合并
function TrendCard({ data }: { data: UsageReport }) {
  const m = useMessages('usage')
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
  const axisFormat = dateFormat(locale, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  })
  const interval = formatDuration(data.bucket_seconds * 1000 * per, locale)
  return (
    <Card>
      <CardHeader
        title={m.trend}
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
            <Tooltip
              key={bar.at}
              label={fill(m.trendBar, {
                time: axisFormat.format(new Date(bar.at)),
                bucket: interval,
                requests: formatNumber(bar.requests, locale),
              })}
            >
              <div
                className="min-w-px flex-1 rounded-t-sm bg-primary"
                style={{
                  height: `${String(Math.max(bar.requests ? 2 : 0, (bar.requests / peak) * 100))}%`,
                }}
              />
            </Tooltip>
          ))}
        </div>
        <div className="mt-2 flex justify-between text-xs text-foreground-muted">
          <span>{axisFormat.format(new Date(data.from))}</span>
          <span>{m.trendNow}</span>
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
  const m = useMessages('usage')
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
  const m = useMessages('usage')
  const app = useMessages('app')
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
    changeParams(setParams, changes)
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
  const dayFormat = dateFormat(locale, {
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
                <Tooltip label={row.key}>
                  <Td className="truncate font-mono text-xs max-sm:basis-full">
                    {row.key || m.none}
                  </Td>
                </Tooltip>
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
