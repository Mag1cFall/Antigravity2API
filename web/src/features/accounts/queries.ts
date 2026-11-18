import { useQuery } from '@tanstack/react-query'

import type { Account } from '@/features/accounts/types'
import type { Model } from '@/features/models/types'
import { api } from '@/lib/api'

// accountsKey 是账户列表的查询键，事件流推送时直接写入
export const accountsKey = ['accounts'] as const

// loginsKey 是进行中登录列表的查询键
export const loginsKey = ['logins'] as const

// loginKey 是一次登录最新状态的查询键，由创建、提交与事件流写入
export function loginKey(id: string | undefined) {
  return ['login', id] as const
}

// loginFailuresKey 保存本页面看到的进行中登录在后台失败或到期的结果，直到用户关掉提示或在对话框中看过
export const loginFailuresKey = ['login-failures'] as const

// useAccounts 读取全部账户
export function useAccounts() {
  return useQuery({
    queryKey: accountsKey,
    queryFn: () => api<{ accounts: Account[] }>('/api/accounts'),
  })
}

// useAccountModels 读取账户最近一次读到的目录中的公开模型，id 为空时不读取
export function useAccountModels(id: string | undefined) {
  return useQuery({
    queryKey: ['accounts', id, 'models'],
    queryFn: () =>
      api<{ models: Omit<Model, 'accounts'>[] }>(
        '/api/accounts/' + encodeURIComponent(id ?? '') + '/models',
      ),
    enabled: id !== undefined,
  })
}
