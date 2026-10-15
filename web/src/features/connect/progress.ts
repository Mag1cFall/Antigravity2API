import { useSyncExternalStore } from 'react'

// Progress 是首次使用引导中后两步的完成记录与是否隐藏引导
export interface Progress {
  endpoint: boolean
  client: boolean
  dismissed: boolean
}

// progressKey 是本地存储中保存引导进度的键
const progressKey = 'antigravity2api.setup'

// empty 是没有记录时的进度
const empty: Progress = { endpoint: false, client: false, dismissed: false }

let current = read()
const listeners = new Set<() => void>()

// read 读取保存的进度，本地存储不可用时返回空进度
function read(): Progress {
  try {
    const saved = localStorage.getItem(progressKey)
    return saved ? { ...empty, ...(JSON.parse(saved) as Partial<Progress>) } : empty
  } catch {
    return empty
  }
}

// markProgress 记录一步完成或隐藏引导
export function markProgress(change: Partial<Progress>) {
  if (Object.entries(change).every(([key, value]) => current[key as keyof Progress] === value)) {
    return
  }
  current = { ...current, ...change }
  try {
    localStorage.setItem(progressKey, JSON.stringify(current))
  } catch {
    // 本地存储不可用时只在本次会话记录
  }
  for (const listener of listeners) listener()
}

// useProgress 返回当前引导进度
export function useProgress() {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
    () => current,
  )
}
