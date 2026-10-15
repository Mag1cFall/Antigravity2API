import type { ComponentProps } from 'react'
import { twMerge } from 'tailwind-merge'

// inputClass 是单行与多行输入框共用的外观
const inputClass =
  'w-full min-w-0 rounded-md border border-border bg-surface px-3 text-sm transition-colors text-foreground placeholder:text-foreground-subtle hover:border-primary-border focus:border-primary-border focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-40 aria-invalid:border-destructive-border'

// Input 是单行输入框，保留浏览器的自动填充与原生选择行为
export function Input({ className, ...props }: ComponentProps<'input'>) {
  return (
    <input
      className={twMerge(inputClass, 'h-control pointer-coarse:h-touch', className)}
      {...props}
    />
  )
}

// Textarea 是多行输入框
export function Textarea({ className, ...props }: ComponentProps<'textarea'>) {
  return <textarea className={twMerge(inputClass, 'py-2', className)} {...props} />
}
