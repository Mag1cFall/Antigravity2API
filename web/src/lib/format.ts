// Locale 是界面语言对应的 Intl 区域
export type Locale = 'zh-CN' | 'zh-TW' | 'en'

// formatters 缓存按区域与选项创建的 Intl 格式化器
const formatters = new Map<string, Intl.NumberFormat | Intl.DateTimeFormat>()

// numberFormat 返回缓存的 Intl.NumberFormat
function numberFormat(locale: Locale, options: Intl.NumberFormatOptions = {}) {
  const key = 'n' + locale + JSON.stringify(options)
  let format = formatters.get(key) as Intl.NumberFormat | undefined
  if (!format) {
    format = new Intl.NumberFormat(locale, options)
    formatters.set(key, format)
  }
  return format
}

// dateFormat 返回缓存的 Intl.DateTimeFormat
function dateFormat(locale: Locale, options: Intl.DateTimeFormatOptions) {
  const key = 'd' + locale + JSON.stringify(options)
  let format = formatters.get(key) as Intl.DateTimeFormat | undefined
  if (!format) {
    format = new Intl.DateTimeFormat(locale, options)
    formatters.set(key, format)
  }
  return format
}

// relativeFormats 缓存各区域的 Intl.RelativeTimeFormat
const relativeFormats = new Map<Locale, Intl.RelativeTimeFormat>()

// formatNumber 按区域输出带千分位的整数
export function formatNumber(value: number, locale: Locale) {
  return numberFormat(locale).format(value)
}

// formatCompact 以紧凑写法输出 token 等大数
export function formatCompact(value: number, locale: Locale) {
  return numberFormat(locale, { notation: 'compact', maximumFractionDigits: 1 }).format(value)
}

// formatTokens 以 K、M 输出 token 上限，1024 的整数倍按二进制换算，例如 65536 为 64K、1048576 为 1M
export function formatTokens(value: number) {
  const binary = value % 1024 === 0
  const unit = binary ? 1024 : 1000
  const scaled = (size: number) => String(Math.round((value / size) * 10) / 10)
  if (value >= unit * unit) return scaled(unit * unit) + 'M'
  if (value >= unit) return scaled(unit) + 'K'
  return String(value)
}

// formatPercent 把 0 到 1 的比例输出为百分比，非整数保留一位小数
export function formatPercent(fraction: number, locale: Locale) {
  return numberFormat(locale, { style: 'percent', maximumFractionDigits: 1 }).format(fraction)
}

// formatCost 以美元输出估算费用，不足 1 美元时保留到 4 位有效数字
export function formatCost(usd: number, locale: Locale) {
  return numberFormat(locale, {
    style: 'currency',
    currency: 'USD',
    currencyDisplay: 'narrowSymbol',
    ...(usd > 0 && usd < 1 ? { maximumSignificantDigits: 4 } : { maximumFractionDigits: 2 }),
  }).format(usd)
}

// formatDuration 把毫秒输出为 ms、s、min、h 中最合适的单位
export function formatDuration(ms: number, locale: Locale) {
  const unit = (value: number, name: string, digits: number) =>
    numberFormat(locale, {
      style: 'unit',
      unit: name,
      unitDisplay: 'narrow',
      maximumFractionDigits: digits,
    }).format(value)
  if (ms < 1000) return unit(Math.round(ms), 'millisecond', 0)
  if (ms < 60_000) return unit(ms / 1000, 'second', ms < 10_000 ? 2 : 1)
  if (ms < 3_600_000) return unit(ms / 60_000, 'minute', 1)
  return unit(ms / 3_600_000, 'hour', 1)
}

// formatBytes 以 1024 进制输出字节数
export function formatBytes(bytes: number, locale: Locale) {
  const units = ['byte', 'kilobyte', 'megabyte', 'gigabyte'] as const
  let value = bytes
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index++
  }
  return numberFormat(locale, {
    style: 'unit',
    unit: units[index],
    unitDisplay: 'short',
    maximumFractionDigits: index === 0 ? 0 : 1,
  }).format(value)
}

// relativeSteps 是相对时间从秒到天的进位
const relativeSteps: [Intl.RelativeTimeFormatUnit, number][] = [
  ['second', 60],
  ['minute', 60],
  ['hour', 24],
  ['day', Number.POSITIVE_INFINITY],
]

// formatRelative 输出目标时间相对 now 的描述，如“3 分钟前”“2 小时后”
export function formatRelative(target: Date, now: Date, locale: Locale) {
  let value = (target.getTime() - now.getTime()) / 1000
  let format = relativeFormats.get(locale)
  if (!format) {
    format = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' })
    relativeFormats.set(locale, format)
  }
  for (const [unit, size] of relativeSteps) {
    if (Math.abs(Math.round(value)) < size) return format.format(Math.round(value), unit)
    value /= size
  }
  return format.format(Math.round(value), 'day')
}

// formatDateTime 输出到秒的本地日期时间，同一天只输出时间
export function formatDateTime(target: Date, now: Date, locale: Locale) {
  const sameDay = target.toDateString() === now.toDateString()
  return dateFormat(locale, {
    ...(sameDay ? {} : { month: '2-digit', day: '2-digit' }),
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(target)
}

// formatFullDateTime 输出完整的本地日期时间
export function formatFullDateTime(target: Date, locale: Locale) {
  return dateFormat(locale, {
    dateStyle: 'medium',
    timeStyle: 'medium',
    hour12: false,
  }).format(target)
}
