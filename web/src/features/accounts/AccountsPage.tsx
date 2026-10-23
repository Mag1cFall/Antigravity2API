import { RefreshCw, Trash2, Users } from 'lucide-react'
import { useCallback, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { Card, CardToolbar } from '@/components/ui/card'
import { PageHeader } from '@/components/ui/page'
import { Pagination } from '@/components/ui/pagination'
import { SearchInput } from '@/components/ui/search-input'
import { Segmented } from '@/components/ui/segmented'
import { Select } from '@/components/ui/select'
import { EmptyState, ErrorState, Loading } from '@/components/ui/states'
import { Tooltip } from '@/components/ui/tooltip'
import { AccountDetailSheet } from '@/features/accounts/AccountDetailSheet'
import { AccountTable, type RowActions } from '@/features/accounts/AccountTable'
import {
  type BatchKind,
  useAccountRemoval,
  useAccountUsage,
  useBatchAccounts,
  useRefreshAccounts,
  useRefreshingAccounts,
  useRefreshQuota,
  useToggleAccount,
  useVerifyAccount,
  useVerifyingAccounts,
} from '@/features/accounts/actions'
import { AddAccountButton } from '@/features/accounts/AddAccountDialog'
import { accountMessages } from '@/features/accounts/messages'
import { ProxyDialog } from '@/features/accounts/ProxyDialog'
import { useAccounts } from '@/features/accounts/queries'
import { type Status, statuses, statusOf } from '@/features/accounts/status'
import type { Account } from '@/features/accounts/types'
import { fill, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// pageSize 是每页账户数
const pageSize = 20

// sorts 是账户列表的排序方式：添加时间（默认）、最早重置时间、最近使用
const sorts = ['added', 'reset', 'used'] as const

// Sort 是账户列表的排序方式
type Sort = (typeof sorts)[number]

// matches 判断账户是否符合状态筛选与搜索词，搜索匹配邮箱、备注名与项目
function matches(account: Account, filter: Status | 'all', query: string) {
  if (filter !== 'all' && statusOf(account) !== filter) return false
  const needle = query.trim().toLowerCase()
  return (
    !needle ||
    account.id.toLowerCase().includes(needle) ||
    (account.label ?? '').toLowerCase().includes(needle) ||
    account.project.toLowerCase().includes(needle)
  )
}

// nextReset 返回账户已开始计时的额度窗口中最早的重置时间，没有时为 Infinity
function nextReset(account: Account) {
  let earliest = Infinity
  for (const bucket of account.quota) {
    if (bucket.remaining_fraction < 1 && bucket.reset_time)
      earliest = Math.min(earliest, Date.parse(bucket.reset_time))
  }
  return earliest
}

// sortAccounts 按排序方式返回账户：reset 时最早重置的在前，used 时最近使用的在前，没有的排在后面并保持添加顺序
function sortAccounts(accounts: Account[], sort: Sort, lastUsed: Map<string, string | undefined>) {
  if (sort === 'reset') return accounts.toSorted((a, b) => nextReset(a) - nextReset(b) || 0)
  if (sort === 'used') {
    const used = (account: Account) => {
      const last = lastUsed.get(account.id)
      return last ? Date.parse(last) : -Infinity
    }
    return accounts.toSorted((a, b) => used(b) - used(a) || 0)
  }
  return accounts
}

// AccountsPage 是账户表：状态筛选、搜索、分页、详情与行内操作，筛选、页码与打开的详情保存在 URL
export function AccountsPage() {
  const m = useMessages(accountMessages)
  const app = useMessages(appMessages)
  const now = useNow(30_000)
  const accounts = useAccounts()
  const usage = useAccountUsage()
  const toggle = useToggleAccount()
  const verify = useVerifyAccount()
  const refreshQuota = useRefreshQuota()
  const verifying = useVerifyingAccounts()
  const refreshing = useRefreshingAccounts()
  const refresh = useRefreshAccounts()
  const batch = useBatchAccounts()
  const { hidden, remove } = useAccountRemoval()
  const [params, setParams] = useSearchParams()
  const filter = statuses.find((value) => value === params.get('state')) ?? 'all'
  const sort = sorts.find((value) => value === params.get('sort')) ?? 'added'
  const query = params.get('q') ?? ''
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set())
  const detailId = params.get('account')
  const [proxyFor, setProxyFor] = useState<{ id: string; trigger: HTMLElement }>()

  const setParam = useCallback(
    (changes: Record<string, string>) => {
      setParams(
        (current) => {
          const next = new URLSearchParams(current)
          for (const [key, value] of Object.entries(changes)) {
            if (value && value !== 'all') next.set(key, value)
            else next.delete(key)
          }
          return next
        },
        { replace: true },
      )
    },
    [setParams],
  )

  const actions = useMemo<RowActions>(
    () => ({
      onSelect: (ids, value) => {
        setSelected((current) => {
          const next = new Set(current)
          for (const id of ids) {
            if (value) next.add(id)
            else next.delete(id)
          }
          return next
        })
      },
      onDetails: (account) => {
        setParam({ account: account.id })
      },
      onToggle: toggle.mutate,
      onVerify: verify.mutate,
      onRefreshQuota: refreshQuota.mutate,
      onProxy: (account, trigger) => {
        setProxyFor({ id: account.id, trigger })
      },
      onRemove: (account) => {
        remove([account])
      },
    }),
    [setParam, setProxyFor, toggle.mutate, verify.mutate, refreshQuota.mutate, remove],
  )

  const list = (accounts.data?.accounts ?? []).filter((account) => !hidden.has(account.id))
  const lastUsed = new Map(list.map((account) => [account.id, usage.get(account.id)?.last_at]))
  const visible = sortAccounts(
    list.filter((account) => matches(account, filter, query)),
    sort,
    lastUsed,
  )
  const picked = visible.filter((account) => selected.has(account.id))
  const failing = picked.filter((account) => account.state === 'error')
  const runBatch = (kind: BatchKind, targets: Account[]) => {
    batch.mutate({ kind, accounts: targets })
  }
  const running = batch.isPending ? batch.variables.kind : undefined
  const pages = Math.max(1, Math.ceil(visible.length / pageSize))
  const page = Math.min(pages, Math.max(1, Number(params.get('page')) || 1))
  const shown = visible.slice((page - 1) * pageSize, page * pageSize)
  const counts = new Map<string, number>()
  for (const account of list)
    counts.set(statusOf(account), (counts.get(statusOf(account)) ?? 0) + 1)
  const ready = counts.get('ready') ?? 0
  const detail = list.find((account) => account.id === detailId)
  const proxyAccount = list.find((account) => account.id === proxyFor?.id)

  return (
    <>
      <PageHeader
        title={m.title}
        description={accounts.data ? fill(m.summary, { total: list.length, ready }) : m.description}
        actions={
          <>
            <Button
              loading={refresh.isPending}
              onClick={() => {
                refresh.mutate()
              }}
            >
              <RefreshCw size={16} aria-hidden />
              {m.refreshAll}
            </Button>
            <AddAccountButton />
          </>
        }
      />
      <Card>
        {accounts.isPending ? (
          <Loading label={app.loading} />
        ) : accounts.isError ? (
          <ErrorState
            title={m.loadFailed}
            error={accounts.error}
            retryLabel={app.retry}
            onRetry={() => void accounts.refetch()}
          />
        ) : list.length === 0 ? (
          <EmptyState
            icon={<Users size={20} aria-hidden />}
            title={m.emptyTitle}
            description={m.emptyDescription}
            action={<AddAccountButton />}
          />
        ) : (
          <>
            <CardToolbar>
              <Segmented
                label={m.filter}
                value={filter}
                onValueChange={(value) => {
                  setParam({ state: value, page: '' })
                }}
                options={(['all', ...statuses] as const).map((value) => ({
                  value,
                  label:
                    (value === 'all' ? m.all : m[value]) +
                    ' ' +
                    String(value === 'all' ? list.length : (counts.get(value) ?? 0)),
                }))}
              />
              <Select
                label={m.sort}
                value={sort}
                onValueChange={(value) => {
                  setParam({ sort: value === 'added' ? '' : value, page: '' })
                }}
                options={[
                  { value: 'added', label: m.sortAdded },
                  { value: 'reset', label: m.sortReset },
                  { value: 'used', label: m.sortUsed },
                ]}
                className="w-auto"
              />
              <SearchInput
                aria-label={m.search}
                placeholder={m.search}
                value={query}
                onChange={(event) => {
                  setParam({ q: event.target.value, page: '' })
                }}
                className="w-full max-w-field sm:ml-auto"
              />
            </CardToolbar>
            {visible.length === 0 ? (
              <EmptyState
                icon={<Users size={20} aria-hidden />}
                title={m.noMatchTitle}
                description={m.noMatchDescription}
                action={
                  <Button
                    onClick={() => {
                      setParams({}, { replace: true })
                    }}
                  >
                    {m.clearFilters}
                  </Button>
                }
              />
            ) : (
              <>
                {picked.length > 0 && (
                  <div
                    role="toolbar"
                    aria-label={fill(m.selected, { count: picked.length })}
                    className="flex flex-wrap items-center gap-2 border-b border-border bg-primary-soft px-6 py-3 max-sm:px-4"
                  >
                    <span className="mr-2 text-sm font-medium">
                      {fill(m.selected, { count: picked.length })}
                    </span>
                    {(
                      [
                        ['enable', m.batchEnable],
                        ['disable', m.batchDisable],
                        ['quota', m.batchRefresh],
                      ] as const
                    ).map(([kind, label]) => (
                      <Button
                        key={kind}
                        size="sm"
                        loading={running === kind}
                        disabled={batch.isPending}
                        onClick={() => {
                          runBatch(kind, picked)
                        }}
                      >
                        {label}
                      </Button>
                    ))}
                    <Tooltip disabledReason={failing.length === 0 && m.batchVerifyNone}>
                      <Button
                        size="sm"
                        loading={running === 'verify'}
                        disabled={batch.isPending || failing.length === 0}
                        onClick={() => {
                          runBatch('verify', failing)
                        }}
                      >
                        {m.batchVerify}
                      </Button>
                    </Tooltip>
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={batch.isPending}
                      className="enabled:hover:text-error-text"
                      onClick={() => {
                        remove(picked)
                        setSelected(new Set())
                      }}
                    >
                      <Trash2 size={16} aria-hidden />
                      {m.batchDelete}
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      className="ml-auto"
                      onClick={() => {
                        setSelected(new Set())
                      }}
                    >
                      {m.batchClear}
                    </Button>
                  </div>
                )}
                <AccountTable
                  accounts={shown}
                  lastUsed={lastUsed}
                  now={now}
                  selected={selected}
                  verifying={verifying}
                  refreshing={refreshing}
                  actions={actions}
                />
                <Pagination
                  page={page}
                  pages={pages}
                  summary={fill(m.pageSummary, {
                    from: (page - 1) * pageSize + 1,
                    to: Math.min(page * pageSize, visible.length),
                    total: visible.length,
                  })}
                  label={m.pagination}
                  previousLabel={m.previous}
                  nextLabel={m.next}
                  pageLabel={(value) => fill(m.page, { page: value })}
                  onPage={(value) => {
                    setParam({ page: value === 1 ? '' : String(value) })
                  }}
                />
              </>
            )}
          </>
        )}
      </Card>
      <AccountDetailSheet
        account={detail}
        usage={detail ? usage.get(detail.id) : undefined}
        now={now}
        verifying={detail !== undefined && verifying.has(detail.id)}
        refreshing={detail !== undefined && refreshing.has(detail.id)}
        actions={actions}
        onClose={() => {
          setParam({ account: '' })
        }}
      />
      {proxyAccount && proxyFor && (
        <ProxyDialog
          key={proxyAccount.id}
          account={proxyAccount}
          trigger={proxyFor.trigger}
          onClose={() => {
            setProxyFor(undefined)
          }}
        />
      )}
    </>
  )
}
