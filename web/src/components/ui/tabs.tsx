import { Tabs as Primitive } from 'radix-ui'
import type { ComponentProps } from 'react'
import { twMerge } from 'tailwind-merge'

import { Indicator } from '@/components/ui/indicator'

export const Tabs = Primitive.Root

// TabsContent 是标签页内容，切换时淡入
export function TabsContent({ className, ...props }: ComponentProps<typeof Primitive.Content>) {
  return (
    <Primitive.Content
      className={twMerge('data-[state=active]:animate-fade-in', className)}
      {...props}
    />
  )
}

// TabsList 是标签页的按钮行，外观与分段控件一致，选中背景滑到选中项，放不下时换行
export function TabsList({ className, children, ...props }: ComponentProps<typeof Primitive.List>) {
  return (
    <Primitive.List
      className={twMerge(
        'relative inline-flex w-fit max-w-full flex-wrap gap-1 rounded-lg bg-muted p-1',
        className,
      )}
      {...props}
    >
      <Indicator active="[data-state=active]" className="rounded-md bg-surface shadow-raised" />
      {children}
    </Primitive.List>
  )
}

// TabsTrigger 是单个标签，选中只改变文字颜色
export function TabsTrigger({ className, ...props }: ComponentProps<typeof Primitive.Trigger>) {
  return (
    <Primitive.Trigger
      className={twMerge(
        'relative inline-flex h-control-sm shrink-0 items-center justify-center gap-1 rounded-md px-4 text-sm whitespace-nowrap text-foreground-muted transition-colors duration-200 ease-out hover:text-foreground data-[state=active]:text-foreground data-[state=inactive]:hover:bg-hover pointer-coarse:h-touch',
        className,
      )}
      {...props}
    />
  )
}
