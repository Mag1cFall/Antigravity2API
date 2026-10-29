import {
  type QueryClient,
  useMutation,
  useMutationState,
  useQueryClient,
} from '@tanstack/react-query'
import { useCallback, useState, useSyncExternalStore } from 'react'

import { toast } from '@/components/ui/toast'
import { accountMessages } from '@/features/accounts/messages'
import { accountsKey } from '@/features/accounts/queries'
import { errorReason } from '@/features/accounts/status'
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
        title: fill(m.verifyFailed, { email: account.id }),
        description: errorReason(error.message).message,
      })
    },
  })
}

// refreshWorkers 是刷新全部时同时读取额度的账户数
const refreshWorkers = 4

// QuotaRefresh 是逐账户读取额度的进度：刷新全部的完成数与总数、排队与正在读取的账户，以及读取失败的账户的原因与失败时的额度读取时间
export interface QuotaRefresh {
  done: number
  total: number
  queued: ReadonlySet<string>
  running: ReadonlySet<string>
  failed: ReadonlyMap<string, { message: string; quotaAt: string | undefined }>
}

// quotaRefresh 是当前进度，离开账户页后保留
let quotaRefresh: QuotaRefresh = {
  done: 0,
  total: 0,
  queued: new Set(),
  running: new Set(),
  failed: new Map(),
}

// refreshListeners 是订阅进度的组件
const refreshListeners = new Set<() => void>()

// updateRefresh 修改进度并通知订阅的组件
function updateRefresh(change: (current: QuotaRefresh) => Partial<QuotaRefresh>) {
  quotaRefresh = { ...quotaRefresh, ...change(quotaRefresh) }
  for (const listener of refreshListeners) listener()
}

// without 返回去掉 id 的集合
function without(items: ReadonlySet<string>, id: string): Set<string>
function without<T>(items: ReadonlyMap<string, T>, id: string): Map<string, T>
function without<T>(items: ReadonlySet<string> | ReadonlyMap<string, T>, id: string) {
  const next = items instanceof Map ? new Map(items) : new Set(items as ReadonlySet<string>)
  next.delete(id)
  return next
}

// recordQuotaResult 记下一个账户读取额度的结果：成功时更新该行并清除失败原因，失败时记下原因
function recordQuotaResult(
  client: QueryClient,
  account: Account,
  result: { account: Account } | { error: string },
) {
  if ('account' in result) {
    replaceAccount(client, result.account)
    updateRefresh((current) => ({ failed: without(current.failed, account.id) }))
    return
  }
  updateRefresh((current) => ({
    failed: new Map(current.failed).set(account.id, {
      message: result.error,
      quotaAt: account.quota_at,
    }),
  }))
}

// useQuotaRefresh 返回逐账户读取额度的进度
export function useQuotaRefresh() {
  return useSyncExternalStore(
    (listener) => {
      refreshListeners.add(listener)
      return () => {
        refreshListeners.delete(listener)
      }
    },
    () => quotaRefresh,
  )
}

// quotaFailure 返回账户读取额度失败的原因，之后读到了额度时为 undefined
export function quotaFailure(refresh: QuotaRefresh, account: Account) {
  const failure = refresh.failed.get(account.id)
  return failure && failure.quotaAt === account.quota_at ? failure.message : undefined
}

// useRefreshQuota 实时读取一个账户的额度
export function useRefreshQuota() {
  const m = useMessages(accountMessages)
  const client = useQueryClient()
  return useMutation({
    mutationKey: quotaKey,
    mutationFn: (account: Account) =>
      api<Account>(accountPath(account.id) + '/quota', { method: 'POST' }),
    onSuccess: (result, account) => {
      recordQuotaResult(client, account, { account: result })
      toast({ tone: 'success', title: fill(m.quotaRefreshed, { email: result.id }) })
    },
    onError: (error, account) => {
      recordQuotaResult(client, account, { error: error.message })
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
      keepAccounts(result.accounts)
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

// useRefreshAll 逐个刷新账户，同时最多 refreshWorkers 个：异常账户先重新初始化，再读取额度；每个账户完成后立即更新该行，结束后提示失败的账户，提示标题列出失败的账户，原因放在说明中
export function useRefreshAll() {
  const m = useMessages(accountMessages)
  const { locale } = useI18n()
  const client = useQueryClient()
  return useCallback(
    async (accounts: Account[]) => {
      const queue = [...accounts]
      const failures: [string, string][] = []
      updateRefresh(() => ({
        done: 0,
        total: accounts.length,
        queued: new Set(accounts.map((account) => account.id)),
      }))
      const work = async () => {
        for (let account = queue.shift(); account; account = queue.shift()) {
          const id = account.id
          updateRefresh((current) => ({
            queued: without(current.queued, id),
            running: new Set(current.running).add(id),
          }))
          try {
            if (account.state === 'error')
              await api(accountPath(id) + '/verify', { method: 'POST' })
            const result = await api<Account>(accountPath(id) + '/quota', { method: 'POST' })
            recordQuotaResult(client, account, { account: result })
          } catch (error) {
            const message = error instanceof Error ? error.message : String(error)
            failures.push([id, message])
            recordQuotaResult(client, account, { error: message })
          }
          updateRefresh((current) => ({
            done: current.done + 1,
            running: without(current.running, id),
          }))
        }
      }
      await Promise.all(Array.from({ length: refreshWorkers }, work))
      updateRefresh(() => ({ done: 0, total: 0 }))
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
    [client, locale, m],
  )
}

// useSwitchAccount 把账户写入本机官方客户端并重新启动它
export function useSwitchAccount() {
  return useMutation({
    mutationFn: (id: string) => api<SwitchResult>(accountPath(id) + '/switch', { method: 'POST' }),
  })
}

// pendingRemovals 是撤销期内尚未发出删除的账户及取消该删除的函数
const pendingRemovals = new Map<string, () => void>()

// keepAccounts 取消这些账户在撤销期内待发出的删除并恢复显示，账户被重新导入或登录时调用
export function keepAccounts(ids: readonly string[]) {
  for (const id of ids) pendingRemovals.get(id)?.()
}

// useAccountRemoval 删除账户：立即隐藏并显示 5 秒撤销，期间撤销、重新导入或登录同一账户则不发送删除，删除完成且列表刷新后不再隐藏；多个账户共用一条撤销提示
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
      const pending = new Set(ids)
      const keep = (id: string) => {
        pending.delete(id)
        pendingRemovals.delete(id)
        show([id])
      }
      for (const id of ids) {
        pendingRemovals.set(id, () => {
          keep(id)
        })
      }
      setHidden((current) => new Set([...current, ...ids]))
      const timer = setTimeout(() => {
        for (const account of accounts) {
          if (!pending.has(account.id)) continue
          pendingRemovals.delete(account.id)
          api(accountPath(account.id), { method: 'DELETE' })
            .then(() => client.invalidateQueries({ queryKey: accountsKey }))
            .then(() => {
              show([account.id])
            })
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
            for (const id of pending) keep(id)
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
