import { ChevronLeft, ChevronRight } from 'lucide-react'
import { type KeyboardEvent, useEffect, useRef, useState } from 'react'

import { Button } from '@/components/ui/button'
import { dateFormat, type Locale } from '@/lib/format'

// sundayFirst 是一周从星期日开始的界面语言
const sundayFirst = new Set<Locale>(['en', 'ja', 'ko', 'zh-TW'])

// addDays 返回 date 之后第 days 天的本地零点
export function addDays(date: Date, days: number) {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate() + days)
}

// startOfDay 返回 date 当天的本地零点
export function startOfDay(date: Date) {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate())
}

// sameDay 判断两个时刻在同一个本地日期
function sameDay(a: Date | undefined, b: Date | undefined) {
  return a !== undefined && b !== undefined && startOfDay(a).getTime() === startOfDay(b).getTime()
}

// dayKey 是日期按钮的 data-date 取值
function dayKey(date: Date) {
  return (
    String(date.getFullYear()) + '-' + String(date.getMonth() + 1) + '-' + String(date.getDate())
  )
}

// keySteps 是方向键与 Home、End 移动焦点的天数，Home 与 End 在 move 中按周计算
const keySteps: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 }

// Calendar 是按月显示的日期网格，点两次选出日期范围：第一次为开始日，第二次为结束日（早于开始日时两者互换），选择结束日前悬停的日期预览范围；晚于 max 的日期不可选。方向键移动焦点，Home 与 End 到本周首尾，PageUp 与 PageDown 翻月，Enter 与空格选择
export function Calendar({
  start,
  end,
  max,
  locale,
  previousLabel,
  nextLabel,
  onChange,
}: {
  start: Date | undefined
  end: Date | undefined
  max: Date
  locale: Locale
  previousLabel: string
  nextLabel: string
  onChange: (start: Date, end: Date | undefined) => void
}) {
  const [focus, setFocus] = useState(() => startOfDay(end ?? start ?? max))
  const [month, setMonth] = useState(() => new Date(focus.getFullYear(), focus.getMonth(), 1))
  const [hover, setHover] = useState<Date>()
  const keyboard = useRef(false)
  const grid = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!keyboard.current) return
    keyboard.current = false
    grid.current?.querySelector<HTMLButtonElement>(`[data-date="${dayKey(focus)}"]`)?.focus()
  }, [focus])
  const weekStart = sundayFirst.has(locale) ? 0 : 1
  const first = addDays(month, -((month.getDay() - weekStart + 7) % 7))
  const days = Array.from({ length: 42 }, (_, index) => addDays(first, index))
  const weekdays = dateFormat(locale, { weekday: 'narrow' })
  const fullDay = dateFormat(locale, {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
    weekday: 'long',
  })
  const last = startOfDay(max)
  const choosing = start !== undefined && end === undefined
  const other = start && (end ?? (choosing ? hover : undefined))
  const [from, to] =
    start && other ? (other < start ? [other, start] : [start, other]) : [start, start]
  const move = (target: Date) => {
    const next = target > last ? last : target
    keyboard.current = true
    setFocus(next)
    if (next.getMonth() !== month.getMonth() || next.getFullYear() !== month.getFullYear()) {
      setMonth(new Date(next.getFullYear(), next.getMonth(), 1))
    }
  }
  const pick = (date: Date) => {
    if (choosing) {
      onChange(date < start ? date : start, date < start ? start : date)
    } else {
      onChange(date, undefined)
    }
    setFocus(date)
  }
  const onKeyDown = (event: KeyboardEvent) => {
    const step = keySteps[event.key]
    let target: Date | undefined
    if (step !== undefined) target = addDays(focus, step)
    else if (event.key === 'Home') target = addDays(focus, -((focus.getDay() - weekStart + 7) % 7))
    else if (event.key === 'End')
      target = addDays(focus, 6 - ((focus.getDay() - weekStart + 7) % 7))
    else if (event.key === 'PageUp' || event.key === 'PageDown') {
      const delta = event.key === 'PageUp' ? -1 : 1
      target = new Date(focus.getFullYear(), focus.getMonth() + delta, focus.getDate())
    }
    if (!target) return
    event.preventDefault()
    move(target)
  }
  const shift = (delta: number) => {
    const next = new Date(month.getFullYear(), month.getMonth() + delta, 1)
    setMonth(next)
    setFocus(next > last ? last : next)
  }
  return (
    <div className="flex w-[272px] flex-col gap-2">
      <div className="flex items-center justify-between">
        <Button
          variant="ghost"
          size="icon"
          aria-label={previousLabel}
          className="size-control-sm"
          onClick={() => {
            shift(-1)
          }}
        >
          <ChevronLeft size={16} aria-hidden />
        </Button>
        <span aria-live="polite" className="text-sm font-medium">
          {dateFormat(locale, { year: 'numeric', month: 'long' }).format(month)}
        </span>
        <Button
          variant="ghost"
          size="icon"
          aria-label={nextLabel}
          className="size-control-sm"
          disabled={new Date(month.getFullYear(), month.getMonth() + 1, 1) > last}
          onClick={() => {
            shift(1)
          }}
        >
          <ChevronRight size={16} aria-hidden />
        </Button>
      </div>
      <div
        ref={grid}
        role="grid"
        tabIndex={-1}
        className="flex flex-col gap-1"
        onKeyDown={onKeyDown}
      >
        <div role="row" className="grid grid-cols-7">
          {days.slice(0, 7).map((day) => (
            <span
              key={day.getDay()}
              role="columnheader"
              aria-label={dateFormat(locale, { weekday: 'long' }).format(day)}
              className="flex h-6 items-center justify-center text-xs text-foreground-muted"
            >
              {weekdays.format(day)}
            </span>
          ))}
        </div>
        {Array.from({ length: 6 }, (_, row) => (
          <div key={row} role="row" className="grid grid-cols-7">
            {days.slice(row * 7, row * 7 + 7).map((day) => {
              const outside = day.getMonth() !== month.getMonth()
              const edge = sameDay(from, day) || sameDay(to, day)
              const inside = from !== undefined && to !== undefined && day > from && day < to
              return (
                <div
                  key={dayKey(day)}
                  role="gridcell"
                  aria-selected={edge || inside}
                  className={
                    'flex justify-center ' +
                    (inside ? 'bg-primary-soft ' : '') +
                    (edge && from && to && !sameDay(from, to)
                      ? sameDay(from, day)
                        ? 'rounded-l-md bg-primary-soft'
                        : 'rounded-r-md bg-primary-soft'
                      : '')
                  }
                >
                  <button
                    type="button"
                    data-date={dayKey(day)}
                    tabIndex={sameDay(focus, day) ? 0 : -1}
                    disabled={day > last}
                    aria-label={fullDay.format(day)}
                    aria-current={sameDay(last, day) ? 'date' : undefined}
                    onMouseEnter={() => {
                      setHover(day)
                    }}
                    onFocus={() => {
                      setHover(day)
                    }}
                    onClick={() => {
                      pick(day)
                    }}
                    className={
                      'flex size-control-sm items-center justify-center rounded-md text-sm tabular-nums transition-colors disabled:cursor-not-allowed disabled:text-foreground-subtle disabled:opacity-40 ' +
                      (edge
                        ? 'bg-primary font-medium text-primary-foreground'
                        : outside
                          ? 'text-foreground-subtle enabled:hover:bg-hover'
                          : 'text-foreground enabled:hover:bg-hover') +
                      (sameDay(last, day) && !edge ? ' font-semibold text-primary' : '')
                    }
                  >
                    {day.getDate()}
                  </button>
                </div>
              )
            })}
          </div>
        ))}
      </div>
    </div>
  )
}
