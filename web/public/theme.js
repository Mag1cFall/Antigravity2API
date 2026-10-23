// theme.js 在首帧绘制前按保存的配色偏好与系统设置写入 html 的 data-theme
document.documentElement.dataset.theme =
  { light: 'light', dark: 'dark' }[localStorage.getItem('antigravity2api.theme')] ??
  (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
