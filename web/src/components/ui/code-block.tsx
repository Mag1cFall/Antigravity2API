import type { ReactNode } from 'react'

import { CopyButton } from '@/components/ui/copy-button'

// CodeBlock 是带文件名与复制按钮的代码片段，wrap 时长行折行而不横向滚动，onCopy 在复制成功后调用
export function CodeBlock({
  code,
  title,
  copyLabel,
  copiedLabel,
  wrap = false,
  onCopy,
}: {
  code: string
  title: ReactNode
  copyLabel: string
  copiedLabel: string
  wrap?: boolean
  onCopy?: () => void
}) {
  return (
    <figure className="flex min-w-0 flex-col overflow-hidden rounded-md border border-border">
      <figcaption className="flex h-control items-center justify-between gap-3 border-b border-border bg-muted pr-1 pl-4 text-xs text-foreground-muted">
        <span className="min-w-0 truncate font-mono">{title}</span>
        <CopyButton value={code} label={copyLabel} copiedLabel={copiedLabel} onCopy={onCopy} />
      </figcaption>
      <pre
        className={
          'bg-surface px-4 py-3 font-mono text-xs leading-[20px] select-text ' +
          (wrap ? 'break-words whitespace-pre-wrap' : 'overflow-x-auto whitespace-pre')
        }
      >
        {code}
      </pre>
    </figure>
  )
}
