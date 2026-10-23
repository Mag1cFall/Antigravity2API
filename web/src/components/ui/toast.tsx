import { CircleAlert, CircleCheck, X } from 'lucide-react'
import { Toast as Primitive } from 'radix-ui'
import { useSyncExternalStore } from 'react'

// ToastItem 是一条轻提示，description 是标题下的补充说明，action 用于撤销等后续操作
interface ToastItem {
  id: number
  title: string
  description?: string
  tone: 'success' | 'error'
  action?: { label: string; onClick: () => void }
}

// visibleToasts 是同时显示的提示条数，超出时先移除最早的不带操作的提示，带操作的提示保留到自动消失
const visibleToasts = 3

let items: ToastItem[] = []
let nextId = 1
const listeners = new Set<() => void>()

// emit 通知订阅者提示列表已变化
function emit() {
  for (const listener of listeners) listener()
}

// toast 显示一条提示，5 秒后自动消失
export function toast(item: Omit<ToastItem, 'id'>) {
  let excess = items.length + 1 - visibleToasts
  items = [
    ...items.filter((current) => current.action !== undefined || excess-- <= 0),
    { ...item, id: nextId++ },
  ]
  emit()
}

// dismiss 移除指定提示
function dismiss(id: number) {
  items = items.filter((item) => item.id !== id)
  emit()
}

// subscribe 订阅提示列表变化
function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

// Toaster 在页面右下角渲染提示，窄屏时铺满底部
export function Toaster({ closeLabel }: { closeLabel: string }) {
  const current = useSyncExternalStore(subscribe, () => items)
  return (
    <Primitive.Provider duration={5000} swipeDirection="right">
      {current.map((item) => (
        <Primitive.Root
          key={item.id}
          onOpenChange={(open) => {
            if (!open) dismiss(item.id)
          }}
          className="flex w-full items-start gap-3 rounded-md border border-card-border bg-surface px-4 py-3 shadow-popover data-[state=closed]:animate-toast-out data-[state=open]:animate-toast-in"
        >
          {item.tone === 'success' ? (
            <CircleCheck aria-hidden size={20} className="shrink-0 text-primary" />
          ) : (
            <CircleAlert aria-hidden size={20} className="shrink-0 text-error-text" />
          )}
          <div className="flex min-w-0 flex-1 flex-col gap-1">
            <Primitive.Title className="text-base break-words">{item.title}</Primitive.Title>
            {item.description && (
              <Primitive.Description
                title={item.description}
                className="line-clamp-3 text-xs break-words text-foreground-muted"
              >
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
