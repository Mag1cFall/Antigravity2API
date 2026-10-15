import { Check, Copy } from 'lucide-react'
import { useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Tooltip } from '@/components/ui/tooltip'

// CopyButton 把文本写入剪贴板，成功后图标短暂变为对勾；text 非空时显示文字按钮，onCopy 在复制成功后调用
export function CopyButton({
  value,
  label,
  copiedLabel,
  text,
  onCopy,
}: {
  value: string
  label: string
  copiedLabel: string
  text?: string | undefined
  onCopy?: (() => void) | undefined
}) {
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    if (!copied) return
    const timer = setTimeout(() => {
      setCopied(false)
    }, 1500)
    return () => {
      clearTimeout(timer)
    }
  }, [copied])
  const copy = () => {
    void navigator.clipboard.writeText(value).then(() => {
      setCopied(true)
      onCopy?.()
    })
  }
  const icon = copied ? (
    <Check size={16} aria-hidden className="text-success-text" />
  ) : (
    <Copy size={16} aria-hidden />
  )
  if (text) {
    return (
      <Button size="sm" aria-label={label} onClick={copy}>
        {icon}
        {copied ? copiedLabel : text}
      </Button>
    )
  }
  return (
    <Tooltip label={copied ? copiedLabel : label}>
      <Button
        variant="ghost"
        size="icon"
        aria-label={label}
        className="size-control-sm shrink-0"
        onClick={copy}
      >
        {icon}
      </Button>
    </Tooltip>
  )
}
