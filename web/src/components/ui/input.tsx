import { Eye, EyeOff } from 'lucide-react'
import { type ComponentProps, useState } from 'react'
import { twMerge } from 'tailwind-merge'

// inputClass 是单行与多行输入框共用的外观
const inputClass =
  'w-full min-w-0 rounded-md border border-border bg-surface px-3 text-sm text-ellipsis transition-colors text-foreground placeholder:text-foreground-subtle enabled:hover:border-primary-border focus:border-primary-border focus-visible:outline-none disabled:cursor-not-allowed disabled:bg-muted disabled:text-foreground-subtle aria-invalid:border-destructive-border'

// Input 是单行输入框，保留浏览器的自动填充与原生选择行为
export function Input({ className, ...props }: ComponentProps<'input'>) {
  return (
    <input
      className={twMerge(inputClass, 'h-control pointer-coarse:h-touch', className)}
      {...props}
    />
  )
}

// PasswordInput 是带显示与隐藏按钮的密码输入框，showLabel 与 hideLabel 是按钮的读屏名称；浏览器自带的显示密码按钮由全局样式隐藏
export function PasswordInput({
  showLabel,
  hideLabel,
  className,
  ...props
}: ComponentProps<'input'> & { showLabel: string; hideLabel: string }) {
  const [shown, setShown] = useState(false)
  return (
    <div className="relative min-w-0">
      <Input
        {...props}
        type={shown ? 'text' : 'password'}
        className={twMerge('pr-control', className)}
      />
      <button
        type="button"
        aria-label={shown ? hideLabel : showLabel}
        aria-pressed={shown}
        disabled={props.disabled}
        onClick={() => {
          setShown(!shown)
        }}
        className="absolute top-1/2 right-1 flex size-control-sm -translate-y-1/2 items-center justify-center rounded-sm text-foreground-muted enabled:hover:bg-hover enabled:hover:text-foreground disabled:opacity-40"
      >
        {shown ? <EyeOff size={16} aria-hidden /> : <Eye size={16} aria-hidden />}
      </button>
    </div>
  )
}

// Textarea 是多行输入框
export function Textarea({ className, ...props }: ComponentProps<'textarea'>) {
  return <textarea className={twMerge(inputClass, 'py-2', className)} {...props} />
}
