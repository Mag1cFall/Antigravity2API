import { Check, Copy } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Tooltip } from '@/components/ui/tooltip'

// copyKeys 是当前系统复制选中文字的快捷键
const copyKeys = navigator.userAgent.includes('Mac') ? '⌘ C' : 'Ctrl C'

// CopyButton 是把文本写入剪贴板的图标按钮，成功后图标短暂变为对勾，onCopy 在复制成功后调用；页面不是安全上下文（经 HTTP 从其他设备访问）时浏览器不提供剪贴板，点击后在浮层中选中文本并提示复制快捷键
export function CopyButton({
  value,
  label,
  copiedLabel,
  onCopy,
}: {
  value: string
  label: string
  copiedLabel: string
  onCopy?: (() => void) | undefined
}) {
  const [copied, setCopied] = useState(false)
  const [open, setOpen] = useState(false)
  const field = useRef<HTMLInputElement>(null)
  useEffect(() => {
    if (!copied) return
    const timer = setTimeout(() => {
      setCopied(false)
    }, 1500)
    return () => {
      clearTimeout(timer)
    }
  }, [copied])
  useEffect(() => {
    if (open) field.current?.select()
  }, [open])
  const button = (
    <Button
      variant="ghost"
      size="icon"
      aria-label={label}
      className="size-control-sm shrink-0"
      onClick={
        window.isSecureContext
          ? () => {
              void navigator.clipboard.writeText(value).then(() => {
                setCopied(true)
                onCopy?.()
              })
            }
          : undefined
      }
    >
      {copied ? (
        <Check size={16} aria-hidden className="text-success-text" />
      ) : (
        <Copy size={16} aria-hidden />
      )}
    </Button>
  )
  if (window.isSecureContext) {
    return <Tooltip label={copied ? copiedLabel : label}>{button}</Tooltip>
  }
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <Tooltip label={label}>
        <PopoverTrigger asChild>{button}</PopoverTrigger>
      </Tooltip>
      <PopoverContent label={label} align="end">
        <div className="flex w-[320px] max-w-full items-center gap-2">
          <Input
            ref={field}
            readOnly
            value={value}
            aria-label={label}
            onFocus={(event) => {
              event.currentTarget.select()
            }}
            onCopy={onCopy}
            className="h-control-sm font-mono text-xs"
          />
          <kbd className="shrink-0 rounded-sm border border-border px-1 font-mono text-xs text-foreground-muted">
            {copyKeys}
          </kbd>
        </div>
      </PopoverContent>
    </Popover>
  )
}
