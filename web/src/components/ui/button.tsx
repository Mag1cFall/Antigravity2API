import { cva, type VariantProps } from 'class-variance-authority'
import { LoaderCircle } from 'lucide-react'
import { Slot } from 'radix-ui'
import type { ComponentProps } from 'react'
import { twMerge } from 'tailwind-merge'

// buttonVariants 定义按钮的外观档位，按下只改变颜色
const buttonVariants = cva(
  'relative inline-flex shrink-0 items-center justify-center gap-1 rounded-md border text-sm whitespace-nowrap transition-[color,background-color,border-color,opacity] select-none disabled:cursor-not-allowed disabled:opacity-40 aria-disabled:cursor-not-allowed aria-disabled:opacity-40',
  {
    variants: {
      variant: {
        primary:
          'border-primary bg-primary text-primary-foreground enabled:hover:border-primary-hover enabled:hover:bg-primary-hover enabled:active:border-primary-active enabled:active:bg-primary-active',
        secondary:
          'border-border bg-surface text-foreground enabled:hover:border-primary-border enabled:hover:text-primary enabled:active:bg-primary-soft-strong enabled:active:text-primary',
        outline:
          'border-primary-border bg-transparent text-primary enabled:hover:bg-primary-soft enabled:active:bg-primary-soft-strong',
        ghost:
          'border-transparent bg-transparent text-foreground enabled:hover:bg-hover enabled:active:bg-nav-active',
        destructive:
          'border-destructive bg-destructive text-white enabled:hover:border-destructive-hover enabled:hover:bg-destructive-hover enabled:active:border-destructive-active enabled:active:bg-destructive-active',
        link: 'h-auto border-transparent px-0 text-primary enabled:hover:text-primary-hover enabled:active:text-primary-active',
      },
      size: {
        sm: 'h-control-sm px-3 pointer-coarse:h-touch',
        md: 'h-control px-4 pointer-coarse:h-touch',
        lg: 'h-control-lg px-6 pointer-coarse:h-touch',
        icon: 'size-control text-foreground-muted pointer-coarse:size-touch',
      },
    },
    compoundVariants: [{ variant: 'link', className: 'h-auto px-0' }],
    defaultVariants: { variant: 'secondary', size: 'md' },
  },
)

// ButtonProps 是按钮的属性，loading 时保持宽度并禁用重复提交
interface ButtonProps extends ComponentProps<'button'>, VariantProps<typeof buttonVariants> {
  asChild?: boolean
  loading?: boolean
}

// Button 是全站唯一的按钮实现
export function Button({
  className,
  variant,
  size,
  asChild = false,
  loading = false,
  disabled,
  children,
  type = 'button',
  ...props
}: ButtonProps) {
  const classes = twMerge(buttonVariants({ variant, size }), className)
  if (asChild) {
    return (
      <Slot.Root className={classes} {...props}>
        {children}
      </Slot.Root>
    )
  }
  return (
    <button
      type={type}
      className={classes}
      disabled={disabled === true || loading}
      aria-busy={loading || undefined}
      {...props}
    >
      {loading ? (
        <>
          <span className="invisible inline-flex items-center gap-1">{children}</span>
          <LoaderCircle aria-hidden size={16} className="absolute animate-spin" />
        </>
      ) : (
        children
      )}
    </button>
  )
}
