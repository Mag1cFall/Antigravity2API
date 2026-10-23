import { Check, Minus } from 'lucide-react'
import { Checkbox as Primitive } from 'radix-ui'
import type { ComponentProps } from 'react'
import { twMerge } from 'tailwind-merge'

// Checkbox 是 16×16 的复选框，checked 为 indeterminate 时显示横线
export function Checkbox({ className, ...props }: ComponentProps<typeof Primitive.Root>) {
  return (
    <Primitive.Root
      className={twMerge(
        'relative inline-flex size-4 shrink-0 items-center justify-center rounded-sm border border-border-strong bg-surface text-primary-foreground transition-colors before:absolute before:-inset-2 enabled:hover:border-primary disabled:cursor-not-allowed disabled:opacity-40 data-[state=checked]:border-primary data-[state=checked]:bg-primary data-[state=indeterminate]:border-primary data-[state=indeterminate]:bg-primary',
        className,
      )}
      {...props}
    >
      <Primitive.Indicator>
        {props.checked === 'indeterminate' ? (
          <Minus size={12} strokeWidth={3} aria-hidden />
        ) : (
          <Check size={12} strokeWidth={3} aria-hidden />
        )}
      </Primitive.Indicator>
    </Primitive.Root>
  )
}
