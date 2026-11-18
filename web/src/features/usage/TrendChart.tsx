import {
  type KeyboardEvent,
  type PointerEvent,
  type RefObject,
  useEffect,
  useRef,
  useState,
} from 'react'

import type { CustomRange } from '@/features/requests/RangePicker'
import type { UsageReport } from '@/features/usage/types'
import { dateFormat, formatCompact, formatCost, formatNumber } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// barWidth 是每根柱子连同间隔的最小宽度
const barWidth = 6

// Bar 是图上的一根柱子：起止时刻与其中各桶的请求结果、token 与费用之和
interface Bar {
  from: number
  to: number
  requests: number
  succeeded: number
  failed: number
  canceled: number
  limited: number
  tokens: number
  cost: number
}

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

// niceCeil 返回不小于 value 的 1、2、2.5、5 乘 10 的整数次幂，作为纵轴上限
function niceCeil(value: number) {
  if (value <= 1) return 1
  const power = 10 ** Math.floor(Math.log10(value))
  return ([1, 2, 2.5, 5, 10].find((step) => value <= step * power) ?? 10) * power
}

// mergeBars 把逐桶用量合并成柱子，宽度放不下每桶一根时把相邻的 per 个桶合为一根；柱子止于下一个桶的起点，首尾限制在报告的查询范围内
function mergeBars(data: UsageReport, per: number): Bar[] {
  const from = Date.parse(data.from)
  const to = Date.parse(data.to)
  const bars: Bar[] = []
  for (let index = 0; index < data.buckets.length; index += per) {
    const group = data.buckets.slice(index, index + per)
    const next = data.buckets[index + per]
    const bar: Bar = {
      from: Math.max(from, Date.parse(group[0]?.at ?? data.from)),
      to: next ? Date.parse(next.at) : to,
      requests: 0,
      succeeded: 0,
      failed: 0,
      canceled: 0,
      limited: 0,
      tokens: 0,
      cost: 0,
    }
    for (const bucket of group) {
      bar.requests += bucket.requests
      bar.succeeded += bucket.succeeded
      bar.failed += bucket.failed
      bar.canceled += bucket.canceled
      bar.limited += bucket.rate_limited
      bar.tokens += bucket.total_tokens
      bar.cost += bucket.cost_usd
    }
    bars.push(bar)
  }
  return bars
}

// TrendChart 是逐段请求数的堆叠柱状图（成功、失败、已取消），左侧为请求数刻度；鼠标悬停或聚焦后用方向键选中一段时高亮该列并显示该段的时间、请求结果、token 与费用；在图上拖动选出一段时间后以 onZoom 切换到该范围
export function TrendChart({
  data,
  onZoom,
}: {
  data: UsageReport
  onZoom: (range: CustomRange) => void
}) {
  const m = useMessages('usage')
  const { locale } = useI18n()
  const chart = useRef<HTMLDivElement>(null)
  const width = useWidth(chart)
  const [active, setActive] = useState<number>()
  const [drag, setDrag] = useState<{ start: number; end: number }>()
  const per = width ? Math.max(1, Math.ceil((data.buckets.length * barWidth) / width)) : 1
  const bars = mergeBars(data, per)
  const peak = niceCeil(Math.max(0, ...bars.map((bar) => bar.requests)))
  const daily = data.bucket_seconds * per >= 86_400
  const timeFormat = dateFormat(
    locale,
    daily
      ? { month: '2-digit', day: '2-digit', weekday: 'short' }
      : { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false },
  )
  const clockFormat = dateFormat(locale, { hour: '2-digit', minute: '2-digit', hour12: false })
  const span = (bar: Bar) =>
    daily
      ? per === 1
        ? timeFormat.format(bar.from)
        : timeFormat.format(bar.from) + ' – ' + timeFormat.format(bar.to - 1)
      : timeFormat.format(bar.from) +
        ' – ' +
        (new Date(bar.from).toDateString() === new Date(bar.to).toDateString()
          ? clockFormat.format(bar.to)
          : timeFormat.format(bar.to))
  const indexAt = (event: PointerEvent) => {
    const rect = event.currentTarget.getBoundingClientRect()
    const ratio = (event.clientX - rect.left) / rect.width
    return Math.min(bars.length - 1, Math.max(0, Math.floor(ratio * bars.length)))
  }
  const finish = () => {
    if (drag && drag.start !== drag.end) {
      const low = bars[Math.min(drag.start, drag.end)]
      const high = bars[Math.max(drag.start, drag.end)]
      if (low && high) onZoom({ from: new Date(low.from), to: new Date(high.to) })
    }
    setDrag(undefined)
  }
  const onKeyDown = (event: KeyboardEvent) => {
    const last = bars.length - 1
    const current = active ?? last
    const next =
      event.key === 'ArrowLeft'
        ? Math.max(0, current - 1)
        : event.key === 'ArrowRight'
          ? Math.min(last, current + 1)
          : event.key === 'Home'
            ? 0
            : event.key === 'End'
              ? last
              : undefined
    if (event.key === 'Escape') setActive(undefined)
    if (next === undefined) return
    event.preventDefault()
    setActive(next)
  }
  const shown = active === undefined ? undefined : bars[active]
  const endsNow = Math.abs(Date.parse(data.to) - Date.parse(data.generated_at)) < 120_000
  const axisFormat = dateFormat(locale, {
    month: '2-digit',
    day: '2-digit',
    ...(daily ? {} : { hour: '2-digit', minute: '2-digit', hour12: false }),
  })
  const middle = (Date.parse(data.from) + Date.parse(data.to)) / 2
  const summary = (bar: Bar | undefined) =>
    bar &&
    fill(m.trendBar, {
      time: span(bar),
      requests: formatNumber(bar.requests, locale),
      succeeded: formatNumber(bar.succeeded, locale),
      failed: formatNumber(bar.failed, locale),
    })
  return (
    <div className="flex flex-col gap-2">
      <div className="flex gap-2">
        <div
          aria-hidden
          className="flex h-chart w-8 shrink-0 flex-col justify-between text-right text-xs leading-none text-foreground-muted tabular-nums"
        >
          <span>{formatCompact(peak, locale)}</span>
          <span>{formatCompact(peak / 2, locale)}</span>
          <span>0</span>
        </div>
        <div
          ref={chart}
          role="slider"
          tabIndex={0}
          aria-label={fill(m.trendLabel, { peak: formatNumber(peak, locale) })}
          aria-describedby="trend-hint"
          aria-valuemin={0}
          aria-valuemax={Math.max(0, bars.length - 1)}
          aria-valuenow={active ?? Math.max(0, bars.length - 1)}
          aria-valuetext={summary(bars[active ?? bars.length - 1] ?? bars[0])}
          onPointerMove={(event) => {
            const index = indexAt(event)
            setActive(index)
            if (drag) setDrag({ ...drag, end: index })
          }}
          onPointerLeave={() => {
            if (!drag) setActive(undefined)
          }}
          onPointerDown={(event) => {
            if (event.button !== 0) return
            event.currentTarget.setPointerCapture(event.pointerId)
            const index = indexAt(event)
            setDrag({ start: index, end: index })
          }}
          onPointerUp={finish}
          onPointerCancel={() => {
            setDrag(undefined)
          }}
          onKeyDown={onKeyDown}
          onBlur={() => {
            setActive(undefined)
          }}
          className="relative h-chart min-w-0 flex-1 cursor-crosshair touch-none rounded-sm outline-offset-2 select-none"
        >
          <div aria-hidden className="absolute inset-0 flex flex-col justify-between">
            <span className="border-t border-dashed border-border" />
            <span className="border-t border-dashed border-border" />
            <span className="border-t border-border" />
          </div>
          <div aria-hidden className="absolute inset-0 flex items-end gap-px">
            {bars.map((bar, index) => {
              const selected =
                drag !== undefined &&
                index >= Math.min(drag.start, drag.end) &&
                index <= Math.max(drag.start, drag.end)
              return (
                <div
                  key={bar.from}
                  className={
                    'relative flex h-full min-w-px flex-1 flex-col justify-end rounded-t-sm ' +
                    (selected ? 'bg-primary-soft-strong' : active === index ? 'bg-hover' : '')
                  }
                >
                  {bar.requests > 0 && (
                    <div
                      className="flex flex-col justify-end overflow-hidden rounded-t-sm"
                      style={{ height: `${String(Math.max(2, (bar.requests / peak) * 100))}%` }}
                    >
                      {bar.canceled > 0 && (
                        <span className="bg-foreground-subtle" style={{ flexGrow: bar.canceled }} />
                      )}
                      {bar.failed > 0 && (
                        <span className="bg-error" style={{ flexGrow: bar.failed }} />
                      )}
                      {bar.succeeded > 0 && (
                        <span className="bg-primary" style={{ flexGrow: bar.succeeded }} />
                      )}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
          {shown && active !== undefined && !drag && (
            <div
              aria-hidden
              className="pointer-events-none absolute top-0 z-popover flex w-max max-w-[260px] flex-col gap-1 rounded-sm bg-surface px-3 py-2 text-xs text-foreground shadow-popover"
              style={
                (active + 0.5) / bars.length < 0.5
                  ? { left: `calc(${String(((active + 1) / bars.length) * 100)}% + 8px)` }
                  : {
                      right: `calc(${String(((bars.length - active) / bars.length) * 100)}% + 8px)`,
                    }
              }
            >
              <span className="font-medium">{span(shown)}</span>
              <span className="flex flex-col gap-1">
                <span className="flex items-center gap-2">
                  <span className="text-foreground-muted">{m.requests}</span>
                  <span className="ml-auto font-medium tabular-nums">
                    {formatNumber(shown.requests, locale)}
                  </span>
                </span>
                {(
                  [
                    ['bg-primary', m.trendSucceeded, shown.succeeded],
                    ['bg-error', m.trendFailed, shown.failed],
                    ['bg-warning', m.trendLimited, shown.limited],
                    ['bg-foreground-subtle', m.trendCanceled, shown.canceled],
                  ] as const
                )
                  .filter(([, , value], index) => index === 0 || value > 0)
                  .map(([color, label, value]) => (
                    <span key={label} className="flex items-center gap-2">
                      <span className={'size-2 rounded-full ' + color} />
                      <span className="text-foreground-muted">{label}</span>
                      <span className="ml-auto tabular-nums">{formatNumber(value, locale)}</span>
                    </span>
                  ))}
                <span className="flex items-center gap-2">
                  <span className="text-foreground-muted">{m.tokens}</span>
                  <span className="ml-auto tabular-nums">
                    {formatCompact(shown.tokens, locale)}
                  </span>
                </span>
                <span className="flex items-center gap-2">
                  <span className="text-foreground-muted">{m.cost}</span>
                  <span className="ml-auto tabular-nums">{formatCost(shown.cost, locale)}</span>
                </span>
              </span>
            </div>
          )}
        </div>
      </div>
      <div className="ml-10 flex justify-between gap-2 text-xs text-foreground-muted tabular-nums">
        <span>{axisFormat.format(Date.parse(data.from))}</span>
        <span className="max-sm:hidden">{axisFormat.format(middle)}</span>
        <span>{endsNow ? m.trendNow : axisFormat.format(Date.parse(data.to))}</span>
      </div>
      <p id="trend-hint" className="ml-10 text-xs text-foreground-subtle">
        {m.trendHint}
      </p>
    </div>
  )
}
