import { cva } from 'class-variance-authority'

// fillVariants 按语气给进度填充上色
const fillVariants = cva('h-full rounded-full transition-[width] duration-200 ease-out', {
  variants: {
    tone: {
      info: 'bg-primary',
      success: 'bg-success',
      warning: 'bg-warning',
      error: 'bg-error',
    },
  },
})

// MeterTone 是比例条的语气
export type MeterTone = 'info' | 'success' | 'warning' | 'error'

// Meter 是全圆角比例条，value 为 0 到 1，thin 时高 6px，否则 8px
export function Meter({
  value,
  tone,
  label,
  thin = false,
}: {
  value: number
  tone: MeterTone
  label: string
  thin?: boolean
}) {
  const clamped = Math.min(1, Math.max(0, value))
  return (
    <div
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(clamped * 100)}
      className={
        'w-full min-w-0 overflow-hidden rounded-full bg-hover ' + (thin ? 'h-[6px]' : 'h-2')
      }
    >
      <div className={fillVariants({ tone })} style={{ width: `${clamped * 100}%` }} />
    </div>
  )
}
