import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import type { Session, Status, UpdateStatus } from '@/features/service/types'
import { api } from '@/lib/api'

// statusKey 是服务状态的查询键，事件流推送时直接写入
const statusKey = ['status'] as const

// sessionKey 是管理会话的查询键
export const sessionKey = ['session'] as const

// useStatus 读取服务状态
export function useStatus() {
  return useQuery({ queryKey: statusKey, queryFn: () => api<Status>('/api/status') })
}

// useSession 读取当前管理会话
export function useSession() {
  return useQuery({ queryKey: sessionKey, queryFn: () => api<Session>('/api/session') })
}

// updateKey 是在线更新状态的查询键
const updateKey = ['update'] as const

// useUpdateStatus 读取当前版本与 GitHub 最新发行版本
export function useUpdateStatus() {
  return useQuery({ queryKey: updateKey, queryFn: () => api<UpdateStatus>('/api/update') })
}

// useCheckUpdate 重新读取 GitHub 最新发行版本
export function useCheckUpdate() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => api<UpdateStatus>('/api/update?refresh=1'),
    onSuccess: (status) => {
      client.setQueryData(updateKey, status)
    },
  })
}

// useApplyUpdate 下载、校验并安装最新发行版本，成功后服务重启
export function useApplyUpdate() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => api<UpdateStatus>('/api/update', { method: 'POST' }),
    onSuccess: (status) => {
      client.setQueryData(updateKey, status)
    },
  })
}
