import { Popover as Primitive } from 'radix-ui'
import type { ReactNode } from 'react'

export const Popover = Primitive.Root
export const PopoverTrigger = Primitive.Trigger
export const PopoverClose = Primitive.Close

// PopoverContent 是贴着触发控件弹出的白底浮层，label 是读屏名称，超出可用高度时在浮层内滚动
export function PopoverContent({
  label,
  align = 'start',
  children,
}: {
  label: string
  align?: 'start' | 'end'
  children: ReactNode
}) {
  return (
    <Primitive.Portal>
      <Primitive.Content
        aria-label={label}
        align={align}
        sideOffset={4}
        collisionPadding={8}
        className="z-popover max-h-(--radix-popover-content-available-height) max-w-(--radix-popover-content-available-width) overflow-y-auto rounded-md bg-surface p-4 shadow-popover focus:outline-none data-[side=top]:[--drop-from:4px] data-[state=closed]:animate-fade-out data-[state=open]:animate-drop-in"
      >
        {children}
      </Primitive.Content>
    </Primitive.Portal>
  )
}
