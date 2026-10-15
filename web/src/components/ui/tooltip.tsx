import { Tooltip as Primitive } from 'radix-ui'
import type { ReactNode } from 'react'

// TooltipProvider 统一提示的出现延迟
export function TooltipProvider({ children }: { children: ReactNode }) {
  return (
    <Primitive.Provider delayDuration={400} skipDelayDuration={200}>
      {children}
    </Primitive.Provider>
  )
}

// Tooltip 在悬停与键盘聚焦时显示说明，shortcut 显示对应快捷键；disabledReason 非空时在悬停禁用控件的外层时显示禁用原因
export function Tooltip({
  label,
  shortcut,
  disabledReason,
  side = 'bottom',
  children,
}: {
  label?: ReactNode
  shortcut?: string | undefined
  disabledReason?: string | false | undefined
  side?: 'top' | 'bottom' | 'left' | 'right'
  children: ReactNode
}) {
  const reason = disabledReason === false ? undefined : disabledReason
  if (!reason && !label) return children
  return (
    <Primitive.Root>
      <Primitive.Trigger asChild>
        {reason ? (
          <span className="inline-flex rounded-md *:pointer-events-none">{children}</span>
        ) : (
          children
        )}
      </Primitive.Trigger>
      <Primitive.Portal>
        <Primitive.Content
          side={side}
          sideOffset={8}
          collisionPadding={8}
          className="z-popover flex max-w-prose items-center gap-2 rounded-sm bg-surface px-3 py-2 text-xs text-foreground shadow-popover data-[side=top]:[--drop-from:4px] data-[state=closed]:animate-fade-out data-[state=delayed-open]:animate-drop-in data-[state=instant-open]:animate-drop-in"
        >
          <span className="break-words">{reason ?? label}</span>
          {shortcut && !reason && (
            <kbd className="rounded-sm border border-border px-1 font-mono text-xs text-foreground-muted">
              {shortcut}
            </kbd>
          )}
          <Primitive.Arrow className="fill-surface" width={10} height={5} />
        </Primitive.Content>
      </Primitive.Portal>
    </Primitive.Root>
  )
}
