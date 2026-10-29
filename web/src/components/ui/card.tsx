import type { ComponentProps, ReactNode } from 'react'
import { twMerge } from 'tailwind-merge'

// Card 是灰色底上的白色圆角卡片，内容超出圆角时裁切
export function Card({ className, ...props }: ComponentProps<'section'>) {
  return (
    <section
      className={twMerge(
        'flex min-w-0 flex-col overflow-clip rounded-lg border border-card-border bg-surface shadow-card',
        className,
      )}
      {...props}
    />
  )
}

// CardHeader 是卡片顶部的图标、标题、说明与操作
export function CardHeader({
  title,
  description,
  icon,
  actions,
  id,
}: {
  title: ReactNode
  description?: ReactNode
  icon?: ReactNode
  actions?: ReactNode
  id?: string
}) {
  return (
    <header className="flex flex-wrap items-start justify-between gap-x-4 gap-y-2 px-4 pt-3 pb-3">
      <div className="flex min-w-0 flex-[1_1_240px] items-start gap-2">
        {icon && (
          <span className="flex h-6 shrink-0 items-center text-foreground-muted">{icon}</span>
        )}
        <div className="min-w-0">
          <h2 id={id} className="text-md font-semibold break-words">
            {title}
          </h2>
          {description && <p className="text-xs text-foreground-muted">{description}</p>}
        </div>
      </div>
      {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
    </header>
  )
}

// CardBody 是卡片内容区，与标题左右对齐
export function CardBody({ className, ...props }: ComponentProps<'div'>) {
  return <div className={twMerge('px-4 pb-4', className)} {...props} />
}

// CardToolbar 是卡片顶部的筛选与搜索行
export function CardToolbar({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div
      className={twMerge(
        'flex flex-wrap items-center gap-2 border-b border-border px-4 py-3',
        className,
      )}
      {...props}
    />
  )
}
