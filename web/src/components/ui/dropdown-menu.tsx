import { Check } from 'lucide-react'
import { DropdownMenu as Primitive } from 'radix-ui'
import type { ComponentProps, ReactNode } from 'react'

import { ignoreSecondaryRelease } from '@/lib/pointer'

export const DropdownMenu = Primitive.Root
export const DropdownMenuTrigger = Primitive.Trigger

// DropdownMenuContent 是白底浮层菜单，选项超出可用高度时在菜单内滚动
export function DropdownMenuContent({
  children,
  align = 'end',
}: {
  children: ReactNode
  align?: 'start' | 'end'
}) {
  return (
    <Primitive.Portal>
      <Primitive.Content
        align={align}
        sideOffset={4}
        collisionPadding={8}
        className="z-popover max-h-(--radix-dropdown-menu-content-available-height) max-w-(--radix-dropdown-menu-content-available-width) min-w-menu overflow-y-auto rounded-sm bg-surface py-1 shadow-popover data-[side=top]:[--drop-from:4px] data-[state=closed]:animate-fade-out data-[state=open]:animate-drop-in"
      >
        {children}
      </Primitive.Content>
    </Primitive.Portal>
  )
}

export const DropdownMenuRadioGroup = Primitive.RadioGroup

// DropdownMenuRadioItem 是菜单中的单选项，选中项为蓝字
export function DropdownMenuRadioItem(props: ComponentProps<typeof Primitive.RadioItem>) {
  return (
    <Primitive.RadioItem
      onPointerUp={ignoreSecondaryRelease}
      className="flex h-control-sm cursor-pointer items-center px-4 text-sm text-foreground outline-none select-none data-highlighted:bg-hover data-[state=checked]:text-primary pointer-coarse:h-touch"
      {...props}
    />
  )
}

// DropdownMenuItem 是菜单中的操作项，danger 时为红字
export function DropdownMenuItem({
  danger = false,
  ...props
}: ComponentProps<typeof Primitive.Item> & { danger?: boolean }) {
  return (
    <Primitive.Item
      onPointerUp={ignoreSecondaryRelease}
      className={
        'flex h-control-sm cursor-pointer items-center gap-2 px-4 text-sm outline-none select-none data-highlighted:bg-hover pointer-coarse:h-touch ' +
        (danger ? 'text-error-text' : 'text-foreground')
      }
      {...props}
    />
  )
}

// DropdownMenuCheckboxItem 是菜单中的多选项，选中后菜单保持打开，选中项为蓝字带勾
export function DropdownMenuCheckboxItem({
  children,
  ...props
}: ComponentProps<typeof Primitive.CheckboxItem>) {
  return (
    <Primitive.CheckboxItem
      onPointerUp={ignoreSecondaryRelease}
      onSelect={(event) => {
        event.preventDefault()
      }}
      className="flex min-h-control-sm cursor-pointer items-center justify-between gap-4 px-4 py-1 text-sm wrap-anywhere text-foreground outline-none select-none data-highlighted:bg-hover data-[state=checked]:text-primary pointer-coarse:min-h-touch"
      {...props}
    >
      {children}
      <Primitive.ItemIndicator>
        <Check size={16} aria-hidden />
      </Primitive.ItemIndicator>
    </Primitive.CheckboxItem>
  )
}
