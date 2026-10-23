// ThemePreference 是界面配色偏好，system 跟随系统
export type ThemePreference = 'system' | 'light' | 'dark'

// themeKey 是本地存储中保存配色偏好的键
const themeKey = 'antigravity2api.theme'

// darkQuery 是系统深色模式的媒体查询
const darkQuery = window.matchMedia('(prefers-color-scheme: dark)')

// readTheme 读取保存的配色偏好
export function readTheme(): ThemePreference {
  try {
    const saved = localStorage.getItem(themeKey)
    if (saved === 'light' || saved === 'dark') return saved
  } catch {
    // 本地存储不可用时跟随系统
  }
  return 'system'
}

// applyTheme 按偏好与系统设置写入 html 的 data-theme；给出 origin 且浏览器支持视图过渡时，切到浅色的新画面从 origin 圆形展开铺满窗口，切到深色时浅色的旧画面收回 origin
export function applyTheme(preference: ThemePreference, origin?: { x: number; y: number }) {
  const root = document.documentElement
  const next =
    preference === 'dark' || (preference === 'system' && darkQuery.matches) ? 'dark' : 'light'
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

// saveTheme 保存配色偏好并立即应用，origin 为圆形过渡的中心
export function saveTheme(preference: ThemePreference, origin?: { x: number; y: number }) {
  try {
    if (preference === 'system') localStorage.removeItem(themeKey)
    else localStorage.setItem(themeKey, preference)
  } catch {
    // 本地存储不可用时只在本次会话生效
  }
  applyTheme(preference, origin)
}

// watchSystemTheme 在跟随系统时响应系统配色变化
export function watchSystemTheme() {
  darkQuery.addEventListener('change', () => {
    applyTheme(readTheme())
  })
}
