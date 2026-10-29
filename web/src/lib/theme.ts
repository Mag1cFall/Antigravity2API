// Theme 是界面配色
export type Theme = 'light' | 'dark'

// themeKey 是本地存储中保存配色的键，没有保存时跟随系统
const themeKey = 'antigravity2api.theme'

// darkQuery 是系统深色模式的媒体查询
const darkQuery = window.matchMedia('(prefers-color-scheme: dark)')

// savedTheme 读取保存的配色，没有保存或本地存储不可用时为 null
function savedTheme(): Theme | null {
  try {
    const saved = localStorage.getItem(themeKey)
    if (saved === 'light' || saved === 'dark') return saved
  } catch {
    // 本地存储不可用时跟随系统
  }
  return null
}

// currentTheme 返回 html 上正在使用的配色
export function currentTheme(): Theme {
  return document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light'
}

// subscribeTheme 在 html 的 data-theme 变化时通知 listener，返回取消订阅的函数
export function subscribeTheme(listener: () => void) {
  const observer = new MutationObserver(listener)
  observer.observe(document.documentElement, { attributeFilter: ['data-theme'] })
  return () => {
    observer.disconnect()
  }
}

// applyTheme 写入 html 的 data-theme；给出 origin 且浏览器支持视图过渡时，切到浅色的新画面从 origin 圆形展开铺满窗口，切到深色时浅色的旧画面收回 origin
export function applyTheme(next: Theme, origin?: { x: number; y: number }) {
  const root = document.documentElement
  if (root.dataset.theme === next) return
  if (
    !origin ||
    !('startViewTransition' in document) ||
    window.matchMedia('(prefers-reduced-motion: reduce)').matches
  ) {
    root.dataset.theme = next
    return
  }
  root.dataset.themeSwitching = ''
  const radius = Math.hypot(
    Math.max(origin.x, window.innerWidth - origin.x),
    Math.max(origin.y, window.innerHeight - origin.y),
  )
  const circle = [
    `circle(0px at ${origin.x}px ${origin.y}px)`,
    `circle(${radius}px at ${origin.x}px ${origin.y}px)`,
  ]
  const transition = document.startViewTransition(() => {
    root.dataset.theme = next
  })
  void transition.ready.then(() => {
    const reveal = root.animate(
      { clipPath: next === 'light' ? circle : [...circle].reverse() },
      {
        duration: 420,
        easing: next === 'light' ? 'ease-out' : 'ease-in',
        fill: 'forwards',
        pseudoElement: `::view-transition-${next === 'light' ? 'new' : 'old'}(root)`,
      },
    )
    void transition.finished.finally(() => {
      reveal.cancel()
    })
  })
  void transition.finished.finally(() => {
    delete root.dataset.themeSwitching
  })
}

// initialTheme 返回启动时的配色：保存的配色，没有保存时跟随系统
export function initialTheme(): Theme {
  return savedTheme() ?? (darkQuery.matches ? 'dark' : 'light')
}

// toggleTheme 在浅色与深色之间切换并保存，origin 为圆形过渡的中心
export function toggleTheme(origin?: { x: number; y: number }) {
  const next = currentTheme() === 'dark' ? 'light' : 'dark'
  try {
    localStorage.setItem(themeKey, next)
  } catch {
    // 本地存储不可用时只在本次会话生效
  }
  applyTheme(next, origin)
}

// watchSystemTheme 在没有保存配色时响应系统配色变化
export function watchSystemTheme() {
  darkQuery.addEventListener('change', () => {
    if (savedTheme() === null) applyTheme(darkQuery.matches ? 'dark' : 'light')
  })
}
