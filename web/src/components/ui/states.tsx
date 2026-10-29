import { CircleAlert } from 'lucide-react'
import { type ReactNode, useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'

// useDelayedFlag 让加载指示延迟 200ms 出现，出现后至少保持 300ms
function useDelayedFlag(active: boolean) {
  const [visible, setVisible] = useState(false)
  const [shownAt, setShownAt] = useState(0)
  useEffect(() => {
    if (active && !visible) {
      const timer = setTimeout(() => {
        setVisible(true)
        setShownAt(Date.now())
      }, 200)
      return () => {
        clearTimeout(timer)
      }
    }
    if (!active && visible) {
      const timer = setTimeout(
        () => {
          setVisible(false)
        },
        Math.max(0, 300 - (Date.now() - shownAt)),
      )
      return () => {
        clearTimeout(timer)
      }
    }
    return undefined
  }, [active, visible, shownAt])
  return visible
}

// Loading 是数据区域加载时的骨架，rows 行 48px 高的占位与加载后的表格行同高，200ms 后才显示占位条
export function Loading({ label, rows = 4 }: { label: string; rows?: number }) {
  const visible = useDelayedFlag(true)
  return (
    <div role="status" aria-busy="true" aria-label={label} className="flex flex-col">
      {Array.from({ length: rows }, (_, index) => (
        <div
          key={index}
          className="flex h-12 items-center gap-6 border-t border-border px-4 first:border-t-0"
        >
          {visible && (
            <>
              <span className="h-3 w-1/4 animate-pulse rounded-full bg-muted" />
              <span className="h-3 w-1/3 animate-pulse rounded-full bg-muted" />
              <span className="h-3 flex-1 animate-pulse rounded-full bg-muted" />
            </>
          )}
        </div>
      ))}
    </div>
  )
}

// EmptyState 是数据区域的空状态，action 给出下一步操作
export function EmptyState({
  icon,
  title,
  description,
  action,
}: {
  icon: ReactNode
  title: string
  description?: ReactNode
  action?: ReactNode
}) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 px-4 py-8 text-center">
      <span className="flex size-12 items-center justify-center rounded-lg bg-muted text-foreground-muted">
        {icon}
      </span>
      <div className="flex max-w-prose flex-col gap-1">
        <p className="text-md font-semibold">{title}</p>
        {description && <p className="text-sm text-foreground-muted">{description}</p>}
      </div>
      {action}
    </div>
  )
}

// ErrorState 是数据区域的错误状态，说明原因并提供重试
export function ErrorState({
  title,
  error,
  retryLabel,
  onRetry,
}: {
  title: string
  error: unknown
  retryLabel: string
  onRetry?: () => void
}) {
  return (
    <div
      role="alert"
      className="flex flex-col items-center justify-center gap-3 px-4 py-12 text-center"
    >
      <span className="flex size-12 items-center justify-center rounded-lg bg-error-soft text-error-text">
        <CircleAlert size={20} aria-hidden />
      </span>
      <div className="flex max-w-prose flex-col gap-1">
        <p className="text-md font-semibold">{title}</p>
        <p className="text-sm break-words text-foreground-muted">
          {error instanceof Error ? error.message : String(error)}
        </p>
      </div>
      {onRetry && <Button onClick={onRetry}>{retryLabel}</Button>}
    </div>
  )
}
