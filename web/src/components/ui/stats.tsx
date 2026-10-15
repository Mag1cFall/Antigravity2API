import { cva } from 'class-variance-authority'
import type { ReactNode } from 'react'

// iconVariants 是统计卡片图标底色
const iconVariants = cva('flex size-control-sm shrink-0 items-center justify-center rounded-md', {
  variants: {
    tone: {
      info: 'bg-info-soft text-primary',
      success: 'bg-success-soft text-success-text',
      warning: 'bg-warning-soft text-warning-text',
      error: 'bg-error-soft text-error-text',
      violet: 'bg-violet-soft text-violet',
    },
  },
})

// StatTone 是统计卡片图标的语气
export type StatTone = 'info' | 'success' | 'warning' | 'error' | 'violet'

// Stat 是一张统计卡片
export interface Stat {
  key: string
  label: string
  value: ReactNode
  detail?: ReactNode
  icon: ReactNode
  tone: StatTone
}

// StatCards 是一行等宽统计卡片，宽度 ≥1024 时单行，窄屏两列
export function StatCards({ items }: { items: Stat[] }) {
  return (
    <dl className="grid grid-cols-2 gap-4 max-sm:gap-3 lg:auto-cols-fr lg:grid-flow-col lg:grid-cols-none">
      {items.map((item) => (
        <div
          key={item.key}
          className="flex min-w-0 flex-col gap-3 rounded-lg border border-card-border bg-surface p-6 shadow-card max-lg:last:odd:col-span-2 max-sm:p-4"
        >
          <div className="flex items-center gap-2">
            <span aria-hidden className={iconVariants({ tone: item.tone })}>
              {item.icon}
            </span>
            <dt title={item.label} className="min-w-0 truncate text-sm text-foreground-muted">
              {item.label}
            </dt>
          </div>
          <dd className="text-2xl font-semibold break-words tabular-nums">{item.value}</dd>
          {item.detail && (
            <dd className="-mt-2 text-xs break-words text-foreground-muted">{item.detail}</dd>
          )}
        </div>
      ))}
    </dl>
  )
}
