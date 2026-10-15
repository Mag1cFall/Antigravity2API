import { Meter } from '@/components/ui/meter'
import { Tooltip } from '@/components/ui/tooltip'
import { quotaMessages } from '@/features/quota/messages'
import { formatCountdown, quotaText, quotaTone } from '@/features/quota/quota'
import { formatFullDateTime, formatPercent } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// QuotaLine 是一行额度：窗口名、比例条、剩余百分比与距重置的时间，容器窄于 200px 时倒计时换到比例条下方，悬停倒计时显示完整重置时刻
export function QuotaLine({
  label,
  fraction,
  reset,
  now,
}: {
  label: string
  fraction: number
  reset?: string | undefined
  now: Date
}) {
  const m = useMessages(quotaMessages)
  const { locale } = useI18n()
  const percent = formatPercent(fraction, locale)
  const countdown = reset ? formatCountdown(Date.parse(reset) - now.getTime()) : ''
  return (
    <div className="@container">
      <div className="grid grid-cols-[3rem_minmax(0,1fr)_2.25rem] items-center gap-x-2 gap-y-1 text-xs @min-[200px]:grid-cols-[3rem_minmax(2rem,1fr)_2.5rem_3.5rem]">
        <span className="truncate text-foreground-muted">{label}</span>
        <Meter
          thin
          value={fraction}
          tone={quotaTone(fraction)}
          label={label + ' ' + fill(m.remaining, { percent })}
        />
        <span className={'text-right font-medium tabular-nums ' + quotaText(fraction)}>
          {percent}
        </span>
        {countdown && reset && (
          <Tooltip label={fill(m.resetAt, { time: formatFullDateTime(new Date(reset), locale) })}>
            <span className="col-span-2 col-start-2 truncate font-mono text-foreground-subtle tabular-nums @min-[200px]:col-span-1 @min-[200px]:col-start-auto">
              {countdown}
            </span>
          </Tooltip>
        )}
      </div>
    </div>
  )
}
