import { Search, X } from 'lucide-react'
import { type ComponentProps, useRef } from 'react'
import { twMerge } from 'tailwind-merge'

import { Input } from '@/components/ui/input'
import { useFocusShortcut } from '@/lib/hotkeys'

// SearchInput 是带放大镜的搜索框，按 / 聚焦；有内容时右侧为清空按钮，以原生写入方法清空并派发 input 事件使受控组件收到 onChange，clearLabel 是它的读屏名称
export function SearchInput({
  className,
  clearLabel,
  ...props
}: ComponentProps<'input'> & { clearLabel: string }) {
  const ref = useRef<HTMLInputElement>(null)
  useFocusShortcut(ref)
  const filled = typeof props.value === 'string' && props.value !== ''
  return (
    <div className={twMerge('relative min-w-0', className)}>
      <Search
        size={16}
        aria-hidden
        className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-foreground-muted"
      />
      <Input
        ref={ref}
        type="search"
        className={filled ? 'pr-8 pl-8' : 'pr-8 pl-8 max-sm:pr-3 pointer-coarse:pr-3'}
        {...props}
      />
      {filled ? (
        <button
          type="button"
          aria-label={clearLabel}
          onClick={() => {
            const input = ref.current
            if (!input) return
            Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(
              input,
              '',
            )
            input.dispatchEvent(new Event('input', { bubbles: true }))
            input.focus()
          }}
          className="absolute top-1/2 right-1 flex size-6 -translate-y-1/2 items-center justify-center rounded-sm text-foreground-muted hover:bg-hover hover:text-foreground"
        >
          <X size={14} aria-hidden />
        </button>
      ) : (
        <kbd
          aria-hidden
          className="pointer-events-none absolute top-1/2 right-2 -translate-y-1/2 rounded-sm border border-border px-1 font-mono text-xs text-foreground-subtle max-sm:hidden pointer-coarse:hidden"
        >
          /
        </kbd>
      )}
    </div>
  )
}
