import type { ReactNode } from 'react'

// Stat 是统计条中的一项，detail 是一组以中点分隔的短语，换行只发生在两项之间
export interface Stat {
  key: string
  label: string
  value: ReactNode
  detail?: string[]
}

// dots 把以“ · ”分隔的文案拆成统计卡片的短语，空串不计
export function dots(...texts: string[]) {
  return texts.flatMap((text) => text.split(' · ')).filter(Boolean)
}

// DotList 是以中点分隔的短语，换行优先发生在两项之间，行首的中点落在容器左侧被裁掉
export function DotList({ items }: { items: ReactNode[] }) {
  return (
    <span className="block overflow-hidden">
      <span className="-ml-4 flex flex-wrap">
        {items.map((item, index) => (
          <span
            key={index}
            className="max-w-full pl-4 before:-ml-4 before:inline-block before:w-4 before:text-center before:text-foreground-subtle before:content-['·']"
          >
            {item}
          </span>
        ))}
      </span>
    </span>
  )
}

// StatCards 是一条以竖线分隔的统计条，宽度 ≥1024 时单行，窄屏两列
export function StatCards({ items }: { items: Stat[] }) {
  return (
    <dl className="grid grid-cols-2 overflow-clip rounded-lg border border-card-border bg-surface shadow-card lg:auto-cols-fr lg:grid-flow-col lg:grid-cols-none">
      {items.map((item) => (
        <div
          key={item.key}
          className="-mt-px -ml-px flex min-w-0 flex-col gap-1 border-t border-l border-border px-4 py-3 max-lg:last:odd:col-span-2"
        >
          <dt className="min-w-0 text-xs break-words text-foreground-muted">{item.label}</dt>
          <dd className="text-xl font-semibold break-words tabular-nums">{item.value}</dd>
          {item.detail && item.detail.length > 0 && (
            <dd className="text-xs break-words text-foreground-muted">
              <DotList items={item.detail} />
            </dd>
          )}
        </div>
      ))}
    </dl>
  )
}
