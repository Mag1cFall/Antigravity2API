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

// applyTheme 按偏好与系统设置写入 html 的 data-theme
export function applyTheme(preference: ThemePreference) {
  const dark = preference === 'dark' || (preference === 'system' && darkQuery.matches)
  document.documentElement.dataset.theme = dark ? 'dark' : 'light'
}

// saveTheme 保存配色偏好并立即应用
export function saveTheme(preference: ThemePreference) {
  try {
    if (preference === 'system') localStorage.removeItem(themeKey)
    else localStorage.setItem(themeKey, preference)
  } catch {
    // 本地存储不可用时只在本次会话生效
  }
  applyTheme(preference)
}

// watchSystemTheme 在跟随系统时响应系统配色变化
export function watchSystemTheme() {
  darkQuery.addEventListener('change', () => {
    applyTheme(readTheme())
  })
}
