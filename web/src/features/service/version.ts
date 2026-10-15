// newerVersion 判断点分数字版本 latest 是否高于 current，latest 为空时为假
export function newerVersion(latest: string, current: string) {
  const a = latest.split('.').map(Number)
  const b = current.split('.').map(Number)
  for (let index = 0; index < Math.max(a.length, b.length); index++) {
    const diff = (a[index] ?? 0) - (b[index] ?? 0)
    if (diff !== 0) return diff > 0
  }
  return false
}
