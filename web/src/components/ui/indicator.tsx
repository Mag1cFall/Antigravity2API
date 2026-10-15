import { useLayoutEffect, useRef } from 'react'
import { twMerge } from 'tailwind-merge'

// Indicator 是分段控件、标签页与导航中滑到选中项下方的背景块，放在定位的列表内，active 是选中项的选择器
export function Indicator({ active, className }: { active: string; className?: string }) {
  const ref = useRef<HTMLSpanElement>(null)
  useLayoutEffect(() => {
    const indicator = ref.current
    const list = indicator?.parentElement
    if (!indicator || !list) return undefined
    let placed = false
    const place = () => {
      const item = list.querySelector<HTMLElement>(active)
      if (!item) {
        indicator.style.opacity = '0'
        placed = false
        return
      }
      indicator.style.transition = placed ? '' : 'none'
      indicator.style.opacity = '1'
      indicator.style.width = String(item.offsetWidth) + 'px'
      indicator.style.height = String(item.offsetHeight) + 'px'
      indicator.style.transform = `translate(${String(item.offsetLeft)}px, ${String(item.offsetTop)}px)`
      placed = true
    }
    place()
    const resize = new ResizeObserver(place)
    resize.observe(list)
    for (const child of list.children) if (child !== indicator) resize.observe(child)
    const mutation = new MutationObserver(place)
    mutation.observe(list, {
      subtree: true,
      attributes: true,
      attributeFilter: ['data-state', 'aria-current'],
    })
    return () => {
      resize.disconnect()
      mutation.disconnect()
    }
  }, [active])
  return (
    <span
      ref={ref}
      aria-hidden
      className={twMerge(
        'pointer-events-none absolute top-0 left-0 opacity-0 transition-[transform,width,opacity] duration-200 ease-out',
        className,
      )}
    />
  )
}
