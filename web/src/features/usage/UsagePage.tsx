import { BarChart3, ChevronDown, X } from 'lucide-react'
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
import { useKeyName } from '@/features/connect/ApiKeys'
import {
  customParams,
  type CustomRange,
  parseCustom,
  RangePicker,
} from '@/features/requests/RangePicker'
import { RecordsExport } from '@/features/requests/RecordsExport'
import { filterQuery, rangeQuery, useUsage } from '@/features/usage/queries'
import { TrendChart } from '@/features/usage/TrendChart'
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

// TrendCard 是请求趋势卡片：标题、最近几分钟的请求与 token，以及可悬停查看、拖动放大的逐段请求图
function TrendCard({ data, onZoom }: { data: UsageReport; onZoom: (range: CustomRange) => void }) {
  const m = useMessages('usage')
  const { locale } = useI18n()
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
        <TrendChart data={data} onZoom={onZoom} />
      </CardBody>
    </Card>
  )
}

// filterDimensions 是用量页可筛选的维度，取值写入同名 URL 参数，多个取值以逗号分隔
const filterDimensions = ['model', 'account', 'api_key'] as const
type FilterDimension = (typeof filterDimensions)[number]

// FilterMenu 是一个维度的多选筛选，候选来自报告中这段时间出现过的取值，空值显示为“（无）”
function FilterMenu({
  label,
  options,
  selected,
  onChange,
  format,
}: {
  label: string
  options: string[]
  selected: string[]
  onChange: (values: string[]) => void
  format: (value: string) => string
}) {
  const m = useMessages('usage')
  const values = [...new Set([...selected, ...options])]
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
              <span className="font-mono text-xs">{value ? format(value) : m.none}</span>
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
  const custom = parseCustom(params)
  const grouping: Grouping = groupings.find((key) => key === params.get('by')) ?? 'model'
  const filters = Object.fromEntries(
    filterDimensions.map((dimension) => [
      dimension,
      params.has(dimension) ? (params.get(dimension) ?? '').split(',') : [],
    ]),
  ) as Record<FilterDimension, string[]>
  const filtered = filterDimensions.some((dimension) => filters[dimension].length > 0)
  const span = custom ?? ranges[range]
  const usage = useUsage(span, grouping === 'day' ? '86400' : 'auto', filters)
  const keyName = useKeyName()
  const update = (changes: Record<string, string>) => {
    changeParams(setParams, changes)
  }
  const zoom = (next: CustomRange) => {
    update(customParams(next))
  }
  const rangeLabels: Record<RangeKey, string> = {
    '24h': m.range24h,
    '7d': m.range7d,
    '30d': m.range30d,
    '90d': m.range90d,
  }
  const header = (
    <div className="flex flex-wrap items-center gap-2">
      <RangePicker
        label={m.range}
        presets={(Object.keys(ranges) as RangeKey[]).map((value) => ({
          value,
          label: rangeLabels[value],
        }))}
        preset={custom ? undefined : range}
        presetMs={ranges[range]}
        custom={custom}
        onPreset={(value) => {
          update({ range: value, from: '', to: '' })
        }}
        onCustom={zoom}
      />
      <div role="group" aria-label={m.filters} className="flex flex-wrap items-center gap-2">
        {filterDimensions.map((dimension) => (
          <FilterMenu
            key={dimension}
            label={m[dimension]}
            options={usage.data?.options[dimension] ?? []}
            selected={filters[dimension]}
            format={dimension === 'api_key' ? keyName : (value: string) => value}
            onChange={(values) => {
              setParams(
                (current) => {
                  const next = new URLSearchParams(current)
                  if (values.length > 0) next.set(dimension, values.join(','))
                  else next.delete(dimension)
                  return next
                },
                { replace: true },
              )
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
      <RecordsExport
        label={m.export}
        query={filterQuery(rangeQuery(span), filters)}
        disabledReason={
          !(usage.data && usage.data.totals.requests > 0) &&
          (usage.isPending ? app.loading : usage.isError ? m.exportFailed : m.exportEmpty)
        }
      />
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
      {grouping !== 'day' && <TrendCard data={data} onZoom={zoom} />}
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
                    {row.key ? (grouping === 'api_key' ? keyName(row.key) : row.key) : m.none}
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
