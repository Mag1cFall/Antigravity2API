import { type QueryClient, useMutation, useQueryClient } from '@tanstack/react-query'
import { useCallback, useRef, useState } from 'react'

import { toast } from '@/components/ui/toast'
import { accountMessages } from '@/features/accounts/messages'
import { accountsKey } from '@/features/accounts/queries'
import type { Account, SwitchResult } from '@/features/accounts/types'
import { useUsage } from '@/features/usage/queries'
import type { UsageStats } from '@/features/usage/types'
import { api } from '@/lib/api'
import { fill, useMessages } from '@/lib/i18n'

// accountPath 返回账户的管理 API 路径
export function accountPath(id: string) {
  return '/api/accounts/' + encodeURIComponent(id)
}

// replaceAccount 用接口返回的账户替换缓存中的同一账户
export function replaceAccount(client: QueryClient, account: Account) {
  client.setQueryData<{ accounts: Account[] }>(
    accountsKey,
    (data) =>
      data && { accounts: data.accounts.map((item) => (item.id === account.id ? account : item)) },
  )
}

// useToggleAccount 启用或停用账户，先更新列表，失败时回滚
export function useToggleAccount() {
  const m = useMessages(accountMessages)
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ account, enabled }: { account: Account; enabled: boolean }) =>
      api<Account>(accountPath(account.id), { method: 'PATCH', json: { enabled } }),
    onMutate: async ({ account, enabled }) => {
      await client.cancelQueries({ queryKey: accountsKey })
      const previous = client.getQueryData<{ accounts: Account[] }>(accountsKey)
      replaceAccount(client, { ...account, state: enabled ? 'pending' : 'disabled' })
      return { previous }
    },
    onError: (error, { account }, context) => {
      client.setQueryData(accountsKey, context?.previous)
      toast({
        tone: 'error',
        title: fill(m.updateFailed, { email: account.id, message: error.message }),
      })
    },
  })
}

// useSetPriority 设置账户的调度优先级
export function useSetPriority() {
  const m = useMessages(accountMessages)
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ account, priority }: { account: Account; priority: number }) =>
      api<Account>(accountPath(account.id), { method: 'PATCH', json: { priority } }),
    onSuccess: (result) => {
      replaceAccount(client, result)
      toast({
        tone: 'success',
        title: fill(m.prioritySaved, { email: result.id, priority: result.priority }),
      })
    },
    onError: (error, { account }) => {
      toast({
        tone: 'error',
        title: fill(m.priorityFailed, { email: account.id, message: error.message }),
      })
    },
  })
}

// useVerifyAccount 重新初始化需要验证或异常的账户
export function useVerifyAccount() {
  const m = useMessages(accountMessages)
  const client = useQueryClient()
  return useMutation({
    mutationFn: (account: Account) =>
      api<Account>(accountPath(account.id) + '/verify', { method: 'POST' }),
    onSuccess: (result) => {
      replaceAccount(client, result)
      toast({ tone: 'success', title: fill(m.verified, { email: result.id }) })
    },
    onError: (error, account) => {
      toast({
        tone: 'error',
        title: fill(m.verifyFailed, { email: account.id, message: error.message }),
      })
    },
  })
}

// useImportAccounts 导入本机官方客户端的账户
export function useImportAccounts() {
  const m = useMessages(accountMessages)
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => api<{ accounts: string[] }>('/api/accounts/import', { method: 'POST' }),
    onSuccess: (result) => {
      void client.invalidateQueries({ queryKey: accountsKey })
      toast({
        tone: 'success',
        title: result.accounts.length
          ? fill(m.imported, { count: result.accounts.length })
          : m.importedNone,
      })
    },
    onError: (error) => {
      toast({ tone: 'error', title: fill(m.importFailed, { message: error.message }) })
    },
  })
}

// useRefreshAccounts 同步刷新账户池与额度
export function useRefreshAccounts() {
  const m = useMessages(accountMessages)
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => api<{ accounts: Account[] }>('/api/accounts/refresh', { method: 'POST' }),
    onSuccess: (result) => {
      client.setQueryData(accountsKey, result)
      void client.invalidateQueries({ queryKey: ['models'] })
      toast({ tone: 'success', title: m.refreshDone })
    },
    onError: (error) => {
      toast({ tone: 'error', title: fill(m.refreshFailed, { message: error.message }) })
    },
  })
}

// useSwitchAccount 把账户写入本机官方客户端并重新启动它
export function useSwitchAccount() {
  return useMutation({
    mutationFn: (id: string) => api<SwitchResult>(accountPath(id) + '/switch', { method: 'POST' }),
  })
}

// useAccountRemoval 删除账户：立即隐藏并显示 5 秒撤销，期间撤销则不发送删除
export function useAccountRemoval() {
  const m = useMessages(accountMessages)
  const client = useQueryClient()
  const [hidden, setHidden] = useState<ReadonlySet<string>>(new Set())
  const timers = useRef(new Map<string, ReturnType<typeof setTimeout>>())
  const remove = useCallback(
    (account: Account) => {
      setHidden((current) => new Set(current).add(account.id))
      const restore = () => {
        setHidden((current) => {
          const next = new Set(current)
          next.delete(account.id)
          return next
        })
      }
      timers.current.set(
        account.id,
        setTimeout(() => {
          timers.current.delete(account.id)
          api(accountPath(account.id), { method: 'DELETE' })
            .then(() => client.invalidateQueries({ queryKey: accountsKey }))
            .catch((error: unknown) => {
              restore()
              toast({
                tone: 'error',
                title: fill(m.deleteFailed, {
                  email: account.id,
                  message: error instanceof Error ? error.message : String(error),
                }),
              })
            })
        }, 5000),
      )
      toast({
        tone: 'success',
        title: fill(m.deleted, { email: account.id }),
        action: {
          label: m.undo,
          onClick: () => {
            clearTimeout(timers.current.get(account.id))
            timers.current.delete(account.id)
            restore()
          },
        },
      })
    },
    [client, m],
  )
  return { hidden, remove }
}

// usageWindow 是账户用量与最近使用时间的统计范围
const usageWindow = 30 * 86_400_000

// useAccountUsage 返回各账户近 30 天的请求、token、费用与最近使用时间
export function useAccountUsage() {
  const usage = useUsage(usageWindow)
  const byAccount = new Map<string, UsageStats>()
  for (const group of usage.data?.groups.account ?? []) byAccount.set(group.key, group)
  return byAccount
}
