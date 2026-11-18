import { CalendarRange } from 'lucide-react'
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { addDays, Calendar, startOfDay } from '@/components/ui/calendar'
import { Input } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Segmented } from '@/components/ui/segmented'
import { dateFormat } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// CustomRange 是自定义时间范围 [from, to) 的起止时刻
export interface CustomRange {
  from: Date
  to: Date
}

// parseCustom 读取 URL 中 range=custom 时的 from 与 to，缺少、无法解析或结束不晚于开始时为 undefined
export function parseCustom(params: URLSearchParams): CustomRange | undefined {
  if (params.get('range') !== 'custom') return undefined
  const from = new Date(params.get('from') ?? '')
  const to = new Date(params.get('to') ?? '')
  if (Number.isNaN(from.getTime()) || Number.isNaN(to.getTime()) || to <= from) return undefined
  return { from, to }
}

// customParams 是自定义时间范围写入 URL 的参数
export function customParams(range: CustomRange) {
  return { range: 'custom', from: range.from.toISOString(), to: range.to.toISOString() }
}

// clock 是时刻输入框的格式：小时与分钟
const clock = /^([01]?\d|2[0-3]):([0-5]\d)$/

// timeText 返回时刻的 HH:mm 文字
function timeText(date: Date) {
  return String(date.getHours()).padStart(2, '0') + ':' + String(date.getMinutes()).padStart(2, '0')
}

// atTime 返回 day 当天 text 所写的时刻加 seconds 秒；text 不是 HH:mm 时为 undefined
function atTime(day: Date, text: string, seconds: number) {
  const match = clock.exec(text.trim())
  if (!match) return undefined
  return new Date(
    day.getFullYear(),
    day.getMonth(),
    day.getDate(),
    Number(match[1]),
    Number(match[2]),
    seconds,
  )
}

// lastMinute 返回排他终点之前的最后一刻，用于显示与编辑范围所含的最后一分钟
function lastMinute(range: CustomRange) {
  return new Date(range.to.getTime() - 1)
}

// useRangeText 返回时间范围的简短文字，结束写范围所含的最后一分钟，与今年同年时省略年份
export function useRangeText() {
  const { locale } = useI18n()
  return (range: CustomRange) => {
    const sameYear =
      range.from.getFullYear() === new Date().getFullYear() &&
      range.to.getFullYear() === range.from.getFullYear()
    const format = dateFormat(locale, {
      ...(sameYear ? {} : { year: 'numeric' }),
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false,
    })
    return format.format(range.from) + ' – ' + format.format(lastMinute(range))
  }
}

// RangePicker 是时间范围：预设范围的分段按钮与自定义范围按钮；自定义范围在浮层中以日历点选起止日期（点选后时刻为全天）并填写起止时刻，选中时按钮显示所选范围。preset 为当前预设，自定义时为 undefined；presetMs 是当前预设的时长，用作打开自定义浮层时的初始范围
export function RangePicker<T extends string>({
  label,
  presets,
  preset,
  presetMs,
  custom,
  onPreset,
  onCustom,
}: {
  label: string
  presets: { value: T; label: string }[]
  preset: T | undefined
  presetMs: number
  custom: CustomRange | undefined
  onPreset: (value: T) => void
  onCustom: (range: CustomRange) => void
}) {
  const m = useMessages('range')
  const { locale } = useI18n()
  const rangeText = useRangeText()
  const [open, setOpen] = useState(false)
  const [start, setStart] = useState<Date>()
  const [end, setEnd] = useState<Date>()
  const [startTime, setStartTime] = useState('00:00')
  const [endTime, setEndTime] = useState('23:59')
  const [error, setError] = useState('')
  const reset = () => {
    const now = new Date()
    const range = custom ?? { from: new Date(now.getTime() - presetMs), to: now }
    setStart(startOfDay(range.from))
    setEnd(startOfDay(lastMinute(range)))
    setStartTime(timeText(range.from))
    setEndTime(timeText(lastMinute(range)))
    setError('')
  }
  const apply = () => {
    if (!start) return
    const from = atTime(start, startTime, 0)
    const to = atTime(end ?? start, endTime, 60)
    if (!from || !to) {
      setError(m.invalidTime)
      return
    }
    if (to <= from) {
      setError(m.endBeforeStart)
      return
    }
    onCustom({ from, to })
    setOpen(false)
  }
  const dayText = dateFormat(locale, { year: 'numeric', month: '2-digit', day: '2-digit' })
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Segmented
        label={label}
        value={preset ?? ('' as T)}
        onValueChange={onPreset}
        options={presets}
      />
      <Popover
        open={open}
        onOpenChange={(next) => {
          if (next) reset()
          setOpen(next)
        }}
      >
        <PopoverTrigger asChild>
          <Button
            size="sm"
            aria-label={custom ? fill(m.customOf, { range: rangeText(custom) }) : m.custom}
            className={custom ? 'border-primary-border bg-primary-soft text-primary' : undefined}
          >
            <CalendarRange size={16} aria-hidden />
            {custom ? rangeText(custom) : m.custom}
          </Button>
        </PopoverTrigger>
        <PopoverContent label={m.custom}>
          <form
            className="flex flex-col gap-3"
            onSubmit={(event) => {
              event.preventDefault()
              apply()
            }}
          >
            <Calendar
              start={start}
              end={end}
              max={new Date()}
              locale={locale}
              previousLabel={m.previousMonth}
              nextLabel={m.nextMonth}
              onChange={(nextStart, nextEnd) => {
                setStart(nextStart)
                setEnd(nextEnd)
                setStartTime('00:00')
                setEndTime('23:59')
                setError('')
              }}
            />
            <div className="grid grid-cols-[auto_minmax(0,1fr)_88px] items-center gap-x-2 gap-y-2 text-sm">
              <span className="text-foreground-muted">{m.start}</span>
              <span className="tabular-nums">{start ? dayText.format(start) : '—'}</span>
              <Input
                aria-label={m.startTime}
                value={startTime}
                inputMode="numeric"
                autoComplete="off"
                spellCheck={false}
                onChange={(event) => {
                  setStartTime(event.target.value)
                  setError('')
                }}
                className="h-control-sm text-center tabular-nums"
              />
              <span className="text-foreground-muted">{m.end}</span>
              <span className="tabular-nums">
                {end ? dayText.format(end) : start ? m.pickEnd : '—'}
              </span>
              <Input
                aria-label={m.endTime}
                value={endTime}
                inputMode="numeric"
                autoComplete="off"
                spellCheck={false}
                onChange={(event) => {
                  setEndTime(event.target.value)
                  setError('')
                }}
                className="h-control-sm text-center tabular-nums"
              />
            </div>
            <div className="flex flex-wrap gap-1">
              {[1, 7, 30].map((days) => (
                <Button
                  key={days}
                  size="sm"
                  variant="ghost"
                  onClick={() => {
                    const today = startOfDay(new Date())
                    setStart(addDays(today, 1 - days))
                    setEnd(today)
                    setStartTime('00:00')
                    setEndTime('23:59')
                    setError('')
                  }}
                >
                  {days === 1 ? m.today : fill(m.lastDays, { days })}
                </Button>
              ))}
            </div>
            {error && (
              <p role="alert" className="text-sm text-error-text">
                {error}
              </p>
            )}
            <div className="flex justify-end gap-2">
              <Button
                size="sm"
                onClick={() => {
                  setOpen(false)
                }}
              >
                {m.cancel}
              </Button>
              <Button type="submit" size="sm" variant="primary" disabled={!start}>
                {m.apply}
              </Button>
            </div>
          </form>
        </PopoverContent>
      </Popover>
    </div>
  )
}
