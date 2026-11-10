import type { SetURLSearchParams } from 'react-router'

// changeParams 修改 URL 查询参数：changes 中值为空串的键被删除，其余写入；push 为假时替换当前历史记录
export function changeParams(
  setParams: SetURLSearchParams,
  changes: Record<string, string>,
  push = false,
) {
  setParams(
    (current) => {
      const next = new URLSearchParams(current)
      for (const [key, value] of Object.entries(changes)) {
        if (value) next.set(key, value)
        else next.delete(key)
      }
      return next
    },
    { replace: !push },
  )
}
