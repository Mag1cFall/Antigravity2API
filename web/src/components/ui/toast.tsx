import { CircleAlert, CircleCheck, X } from 'lucide-react'
import { Toast as Primitive } from 'radix-ui'
import { useSyncExternalStore } from 'react'

// ToastItem 是一条轻提示，description 是标题下的补充说明，action 用于撤销等后续操作，open 为假时正在播放退场动画
interface ToastItem {
  id: number
  title: string
  description?: string
  tone: 'success' | 'error'
  action?: { label: string; onClick: () => void }
  open: boolean
}

// toastDuration 是提示从出现到自动消失的时长，悬停、聚焦与窗口失焦都不延长
const toastDuration = 5000

// exitDuration 是退场动画 toast-out 的时长
const exitDuration = 150

// visibleToasts 是同时显示的提示条数，超出时先移除最早的不带操作的提示，带操作的提示保留到自动消失
const visibleToasts = 3

let items: ToastItem[] = []
let nextId = 1
const listeners = new Set<() => void>()

// emit 通知订阅者提示列表已变化
function emit() {
  for (const listener of listeners) listener()
}

// toast 显示一条提示，toastDuration 后自动消失
export function toast(item: Omit<ToastItem, 'id' | 'open'>) {
  const id = nextId++
  let excess = items.filter((current) => current.open).length + 1 - visibleToasts
  items = [
    ...items.filter((current) => !current.open || current.action !== undefined || excess-- <= 0),
    { ...item, id, open: true },
  ]
  emit()
  window.setTimeout(() => {
    dismiss(id)
  }, toastDuration)
}

// dismiss 关闭指定提示，退场动画结束后移除
function dismiss(id: number) {
  if (!items.some((item) => item.id === id && item.open)) return
  items = items.map((item) => (item.id === id ? { ...item, open: false } : item))
  emit()
  window.setTimeout(() => {
    items = items.filter((item) => item.id !== id)
    emit()
  }, exitDuration)
}

// subscribe 订阅提示列表变化
function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

// Toaster 在页面右下角渲染提示，窄屏时铺满底部；计时由 toast 自己管理，Radix 只负责渲染、读屏与滑动关闭
export function Toaster({ closeLabel }: { closeLabel: string }) {
  const current = useSyncExternalStore(subscribe, () => items)
  return (
    <Primitive.Provider duration={Infinity} swipeDirection="right">
      {current.map((item) => (
        <Primitive.Root
          key={item.id}
          open={item.open}
          onOpenChange={(open) => {
            if (!open) dismiss(item.id)
          }}
          className="flex w-full items-start gap-2 rounded-md border border-card-border bg-surface px-3 py-2 shadow-popover data-[state=closed]:animate-toast-out data-[state=open]:animate-toast-in"
        >
          {item.tone === 'success' ? (
            <CircleCheck aria-hidden size={16} className="mt-px shrink-0 text-primary" />
          ) : (
            <CircleAlert aria-hidden size={16} className="mt-px shrink-0 text-error-text" />
          )}
          <div className="flex min-w-0 flex-1 flex-col">
            <Primitive.Title className="text-sm font-medium break-words">
              {item.title}
            </Primitive.Title>
            {item.description && (
              <Primitive.Description className="text-xs wrap-anywhere text-foreground-muted">
                {item.description}
              </Primitive.Description>
            )}
          </div>
          {item.action && (
            <Primitive.Action
              altText={item.action.label}
              onClick={item.action.onClick}
              className="shrink-0 rounded-sm px-2 text-sm font-semibold text-primary hover:text-primary-hover"
            >
              {item.action.label}
            </Primitive.Action>
          )}
          <Primitive.Close
            aria-label={closeLabel}
            className="flex size-6 shrink-0 items-center justify-center rounded-sm text-foreground-muted hover:bg-hover"
          >
            <X size={16} aria-hidden />
          </Primitive.Close>
        </Primitive.Root>
      ))}
      <Primitive.Viewport className="fixed inset-x-4 bottom-4 z-toast flex flex-col gap-2 outline-none sm:right-6 sm:bottom-6 sm:left-auto sm:w-toast" />
    </Primitive.Provider>
  )
}
