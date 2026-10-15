import { Switch as Primitive } from 'radix-ui'
import type { ComponentProps } from 'react'

// Switch 是 40×20 的开关，状态切换只移动圆钮
export function Switch(props: ComponentProps<typeof Primitive.Root>) {
  return (
    <Primitive.Root
      className="inline-flex h-icon w-control-lg shrink-0 items-center rounded-full border-2 border-transparent bg-border-strong transition-[background-color,opacity] enabled:hover:opacity-80 disabled:cursor-not-allowed disabled:opacity-40 data-[state=checked]:bg-primary data-[state=checked]:enabled:hover:bg-primary-hover data-[state=checked]:enabled:hover:opacity-100"
      {...props}
    >
      <Primitive.Thumb className="block size-4 rounded-full bg-white transition-transform duration-200 ease-out data-[state=checked]:translate-x-icon" />
    </Primitive.Root>
  )
}
