import { useSyncExternalStore } from 'react'

// phoneQuery 是与 Tailwind max-sm 一致的窄屏断点
const phoneQuery = window.matchMedia('(width < 40rem)')

// usePhone 返回窗口宽度是否小于 640px
export function usePhone() {
  return useSyncExternalStore(
    (listener) => {
      phoneQuery.addEventListener('change', listener)
      return () => {
        phoneQuery.removeEventListener('change', listener)
      }
    },
    () => phoneQuery.matches,
  )
}
