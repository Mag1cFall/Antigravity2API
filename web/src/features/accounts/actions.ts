import {
  type QueryClient,
  useMutation,
  useMutationState,
  useQueryClient,
} from '@tanstack/react-query'
import { useCallback, useState } from 'react'

import { toast } from '@/components/ui/toast'
import { accountMessages } from '@/features/accounts/messages'
import { accountsKey } from '@/features/accounts/queries'
import type { Account, SwitchResult } from '@/features/accounts/types'
import { useUsage } from '@/features/usage/queries'
import type { UsageStats } from '@/features/usage/types'
import { api } from '@/lib/api'
import { fill, useI18n, useMessages } from '@/lib/i18n'

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

// verifyKey 与 quotaKey 是逐账户重新初始化与读取额度的 mutation 键
const verifyKey = ['account-verify']
const quotaKey = ['account-quota']

// usePendingAccounts 返回 mutation 键下正在执行的账户 ID
function usePendingAccounts(mutationKey: string[]) {
  return new Set(
    useMutationState({
      filters: { mutationKey, status: 'pending' },
      select: (mutation) => (mutation.state.variables as Account).id,
    }),
  )
}

// useVerifyingAccounts 返回正在重新初始化的账户 ID
export function useVerifyingAccounts() {
  return usePendingAccounts(verifyKey)
}

// useRefreshingAccounts 返回正在读取额度的账户 ID
export function useRefreshingAccounts() {
  return usePendingAccounts(quotaKey)
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

// useSetLabel 设置账户的备注名，空串时清除
export function useSetLabel() {
  const m = useMessages(accountMessages)
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ account, label }: { account: Account; label: string }) =>
      api<Account>(accountPath(account.id), { method: 'PATCH', json: { label } }),
    onSuccess: (result) => {
      replaceAccount(client, result)
      toast({ tone: 'success', title: fill(m.labelSaved, { email: result.id }) })
    },
    onError: (error, { account }) => {
      toast({
        tone: 'error',
        title: fill(m.labelFailed, { email: account.id, message: error.message }),
      })
    },
  })
}

// useSetCredits 开启或关闭账户的 AI 积分超额使用
export function useSetCredits() {
  const m = useMessages(accountMessages)
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ account, enabled }: { account: Account; enabled: boolean }) =>
      api<Account>(accountPath(account.id), { method: 'PATCH', json: { ai_credits: enabled } }),
    onSuccess: (result) => {
      replaceAccount(client, result)
      toast({
        tone: 'success',
        title: fill(m.creditsSaved, {
          email: result.id,
          state: result.ai_credits ? m.creditsOn : m.creditsOff,
        }),
      })
    },
    onError: (error, { account }) => {
      toast({
        tone: 'error',
        title: fill(m.creditsFailed, { email: account.id, message: error.message }),
      })
    },
  })
}

// BatchKind 是批量操作：启用、停用、刷新额度与重新初始化
export type BatchKind = 'enable' | 'disable' | 'quota' | 'verify'

// batchRequest 返回一个账户在批量操作中调用的单账户接口
function batchRequest(kind: BatchKind, account: Account) {
  if (kind === 'enable' || kind === 'disable')
    return api<Account>(accountPath(account.id), {
      method: 'PATCH',
      json: { enabled: kind === 'enable' },
    })
  return api<Account>(accountPath(account.id) + (kind === 'quota' ? '/quota' : '/verify'), {
    method: 'POST',
  })
}

// useBatchAccounts 对多个账户逐个调用单账户接口，每个账户完成后更新列表，结束后汇总结果
export function useBatchAccounts() {
  const m = useMessages(accountMessages)
  const client = useQueryClient()
  const actionName: Record<BatchKind, string> = {
    enable: m.batchEnable,
    disable: m.batchDisable,
    quota: m.batchRefresh,
    verify: m.batchVerify,
  }
  return useMutation({
    mutationFn: async ({ kind, accounts }: { kind: BatchKind; accounts: Account[] }) => {
      const failed: string[] = []
      for (const account of accounts) {
        try {
          replaceAccount(client, await batchRequest(kind, account))
        } catch (error) {
          failed.push(account.id + ': ' + (error instanceof Error ? error.message : String(error)))
        }
      }
      return { kind, total: accounts.length, failed }
    },
    onSuccess: ({ kind, total, failed }) => {
      const action = actionName[kind]
      if (failed.length === 0) {
        toast({ tone: 'success', title: fill(m.batchDone, { action, count: total }) })
        return
      }
      toast({
        tone: 'error',
        title: fill(m.batchPartial, { action, done: total - failed.length, failed: failed.length }),
        description: failed.join('; '),
      })
    },
  })
}

// useVerifyAccount 重新初始化需要验证或异常的账户
export function useVerifyAccount() {
  const m = useMessages(accountMessages)
  const client = useQueryClient()
  return useMutation({
    mutationKey: verifyKey,
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

// useRefreshQuota 实时读取一个账户的额度
export function useRefreshQuota() {
  const m = useMessages(accountMessages)
  const client = useQueryClient()
  return useMutation({
    mutationKey: quotaKey,
    mutationFn: (account: Account) =>
      api<Account>(accountPath(account.id) + '/quota', { method: 'POST' }),
    onSuccess: (result) => {
      replaceAccount(client, result)
      toast({ tone: 'success', title: fill(m.quotaRefreshed, { email: result.id }) })
    },
    onError: (error, account) => {
      toast({
        tone: 'error',
        title: fill(m.quotaRefreshFailed, { email: account.id, message: error.message }),
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

// useRefreshAccounts 同步刷新账户池并读取全部账户的额度，failed 为读取额度失败的账户及原因；提示标题列出失败的账户，原因放在说明中
export function useRefreshAccounts() {
  const m = useMessages(accountMessages)
  const { locale } = useI18n()
  const client = useQueryClient()
  return useMutation({
    mutationFn: () =>
      api<{ accounts: Account[]; failed: Record<string, string> }>('/api/accounts/refresh', {
        method: 'POST',
      }),
    onSuccess: ({ accounts, failed }) => {
      client.setQueryData(accountsKey, { accounts })
      void client.invalidateQueries({ queryKey: ['models'] })
      const failures = Object.entries(failed)
      if (failures.length === 0) {
        toast({ tone: 'success', title: m.refreshDone })
        return
      }
      toast({
        tone: 'error',
        title: fill(m.refreshPartial, {
          count: failures.length,
          accounts: new Intl.ListFormat(locale).format(failures.map(([id]) => id)),
        }),
        description: failures.map(([id, message]) => id + ': ' + message).join('; '),
      })
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

// useAccountRemoval 删除账户：立即隐藏并显示 5 秒撤销，期间撤销则不发送删除；多个账户共用一条撤销提示
export function useAccountRemoval() {
  const m = useMessages(accountMessages)
  const client = useQueryClient()
  const [hidden, setHidden] = useState<ReadonlySet<string>>(new Set())
  const remove = useCallback(
    (accounts: Account[]) => {
      const ids = accounts.map((account) => account.id)
      const show = (restored: string[]) => {
        setHidden((current) => {
          const next = new Set(current)
          for (const id of restored) next.delete(id)
          return next
        })
      }
      setHidden((current) => new Set([...current, ...ids]))
      const timer = setTimeout(() => {
        for (const account of accounts) {
          api(accountPath(account.id), { method: 'DELETE' })
            .then(() => client.invalidateQueries({ queryKey: accountsKey }))
            .catch((error: unknown) => {
              show([account.id])
              toast({
                tone: 'error',
                title: fill(m.deleteFailed, {
                  email: account.id,
                  message: error instanceof Error ? error.message : String(error),
                }),
              })
            })
        }
      }, 5000)
      toast({
        tone: 'success',
        title:
          accounts.length === 1
            ? fill(m.deleted, { email: ids[0] ?? '' })
            : fill(m.deletedMany, { count: accounts.length }),
        action: {
          label: m.undo,
          onClick: () => {
            clearTimeout(timer)
            show(ids)
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
