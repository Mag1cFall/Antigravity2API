import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { invalidate, settingsDependents } from '@/app/sync'
import type { Settings } from '@/features/settings/types'
import { api } from '@/lib/api'

// settingsKey 是设置的查询键
export const settingsKey = ['settings']

// useSettings 读取 .env 中的设置
export function useSettings() {
  return useQuery({ queryKey: settingsKey, queryFn: () => api<Settings>('/api/settings') })
}

// useSaveSettings 合并保存部分配置项，成功后更新设置缓存
export function useSaveSettings() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (values: Record<string, string>) =>
      api<Settings>('/api/settings', { method: 'PATCH', json: { values } }),
    onSuccess: (result) => {
      client.setQueryData(settingsKey, result)
      invalidate(client, settingsDependents)
    },
  })
}

// splitList 把逗号分隔的配置值拆成去掉空白的非空项
export function splitList(value: string | undefined) {
  return (value ?? '')
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
}
