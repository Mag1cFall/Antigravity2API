import { ClipboardPaste, Copy, Scissors, TextSelect } from 'lucide-react'
import { useEffect, useState } from 'react'

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useMessages } from '@/lib/i18n'

// Target 是右键时的输入框与选区，或页面上选中的文字
interface Target {
  x: number
  y: number
  field: HTMLInputElement | HTMLTextAreaElement | undefined
  start: number
  end: number
  text: string
}

// editableField 返回右键位置所在的可编辑输入框或文本框，只读框除外
function editableField(target: EventTarget | null) {
  const element = target instanceof Element ? target.closest('input, textarea') : null
  if (element instanceof HTMLTextAreaElement && !element.readOnly) return element
  if (
    element instanceof HTMLInputElement &&
    !element.readOnly &&
    ['text', 'search', 'url', 'password', ''].includes(element.type)
  )
    return element
  return undefined
}

// replaceSelection 把输入框中 start 到 end 的文字换成 text 并把光标放在其后，经原生写入方法与 input 事件让受控组件收到 onChange
function replaceSelection(
  field: HTMLInputElement | HTMLTextAreaElement,
  start: number,
  end: number,
  text: string,
) {
  const prototype =
    field instanceof HTMLTextAreaElement
      ? HTMLTextAreaElement.prototype
      : HTMLInputElement.prototype
  Object.getOwnPropertyDescriptor(prototype, 'value')?.set?.call(
    field,
    field.value.slice(0, start) + text + field.value.slice(end),
  )
  field.dispatchEvent(new Event('input', { bubbles: true }))
  field.setSelectionRange(start + text.length, start + text.length)
}

// DesktopContextMenu 在桌面版中代替 WebView 的右键菜单：输入框中为剪切、复制、粘贴与全选（密码框不可剪切与复制），页面上有选中文字时为复制，其余位置不弹出菜单
export function DesktopContextMenu() {
  const m = useMessages('app')
  const [target, setTarget] = useState<Target>()
  useEffect(() => {
    const onContextMenu = (event: MouseEvent) => {
      event.preventDefault()
      const field = editableField(event.target)
      const text = field
        ? field.type === 'password'
          ? ''
          : field.value.slice(field.selectionStart ?? 0, field.selectionEnd ?? 0)
        : (window.getSelection()?.toString() ?? '')
      if (!field && !text) {
        setTarget(undefined)
        return
      }
      setTarget({
        x: event.clientX,
        y: event.clientY,
        field,
        start: field?.selectionStart ?? 0,
        end: field?.selectionEnd ?? 0,
        text,
      })
    }
    document.addEventListener('contextmenu', onContextMenu)
    return () => {
      document.removeEventListener('contextmenu', onContextMenu)
    }
  }, [])
  // restore 让输入框重新获得焦点并恢复右键时的选区
  const restore = (current: Target) => {
    current.field?.focus()
    current.field?.setSelectionRange(current.start, current.end)
  }
  const run = (action: (current: Target) => void | Promise<void>) => () => {
    if (!target) return
    const current = target
    setTarget(undefined)
    window.setTimeout(() => {
      restore(current)
      void action(current)
    })
  }
  const field = target?.field
  const selected = Boolean(target?.text)
  return (
    <DropdownMenu
      open={target !== undefined}
      onOpenChange={(open) => {
        if (!open) setTarget(undefined)
      }}
    >
      <DropdownMenuTrigger asChild>
        <span
          aria-hidden
          tabIndex={-1}
          className="pointer-events-none fixed size-0"
          style={{ left: target?.x ?? 0, top: target?.y ?? 0 }}
        />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        {field && (
          <DropdownMenuItem
            disabled={!selected}
            onSelect={run(async (current) => {
              await navigator.clipboard.writeText(current.text)
              if (current.field) replaceSelection(current.field, current.start, current.end, '')
            })}
          >
            <Scissors size={16} aria-hidden className="text-foreground-muted" />
            {m.contextCut}
          </DropdownMenuItem>
        )}
        <DropdownMenuItem
          disabled={!selected}
          onSelect={run((current) => navigator.clipboard.writeText(current.text))}
        >
          <Copy size={16} aria-hidden className="text-foreground-muted" />
          {m.contextCopy}
        </DropdownMenuItem>
        {field && (
          <DropdownMenuItem
            onSelect={run(async (current) => {
              const text = await navigator.clipboard.readText()
              if (current.field) replaceSelection(current.field, current.start, current.end, text)
            })}
          >
            <ClipboardPaste size={16} aria-hidden className="text-foreground-muted" />
            {m.contextPaste}
          </DropdownMenuItem>
        )}
        {field && (
          <DropdownMenuItem
            onSelect={run((current) => {
              current.field?.select()
            })}
          >
            <TextSelect size={16} aria-hidden className="text-foreground-muted" />
            {m.contextSelectAll}
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
