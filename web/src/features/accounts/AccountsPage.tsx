import { RefreshCw, Users } from 'lucide-react'
import { useCallback, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { Card, CardToolbar } from '@/components/ui/card'
import { PageHeader } from '@/components/ui/page'
import { Pagination } from '@/components/ui/pagination'
import { SearchInput } from '@/components/ui/search-input'
import { Segmented } from '@/components/ui/segmented'
import { EmptyState, ErrorState, Loading } from '@/components/ui/states'
import { AccountDetailSheet } from '@/features/accounts/AccountDetailSheet'
import { AccountTable, type RowActions } from '@/features/accounts/AccountTable'
import {
  useAccountRemoval,
  useAccountUsage,
  useRefreshAccounts,
  useToggleAccount,
  useVerifyAccount,
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

// matches 判断账户是否符合状态筛选与搜索词
function matches(account: Account, filter: Status | 'all', query: string) {
  if (filter !== 'all' && statusOf(account) !== filter) return false
  const needle = query.trim().toLowerCase()
  return (
    !needle ||
    account.id.toLowerCase().includes(needle) ||
    account.project.toLowerCase().includes(needle)
  )
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
  const refresh = useRefreshAccounts()
  const { hidden, remove } = useAccountRemoval()
  const [params, setParams] = useSearchParams()
  const filter = statuses.find((value) => value === params.get('state')) ?? 'all'
  const query = params.get('q') ?? ''
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
      onDetails: (account) => {
        setParam({ account: account.id })
      },
      onToggle: toggle.mutate,
      onVerify: verify.mutate,
      onProxy: (account, trigger) => {
        setProxyFor({ id: account.id, trigger })
      },
      onRemove: remove,
    }),
    [setParam, setProxyFor, toggle.mutate, verify.mutate, remove],
  )

  const list = (accounts.data?.accounts ?? []).filter((account) => !hidden.has(account.id))
  const visible = list.filter((account) => matches(account, filter, query))
  const pages = Math.max(1, Math.ceil(visible.length / pageSize))
  const page = Math.min(pages, Math.max(1, Number(params.get('page')) || 1))
  const shown = visible.slice((page - 1) * pageSize, page * pageSize)
  const counts = new Map<string, number>()
  for (const account of list)
    counts.set(statusOf(account), (counts.get(statusOf(account)) ?? 0) + 1)
  const lastUsed = new Map(list.map((account) => [account.id, usage.get(account.id)?.last_at]))
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
                <AccountTable
                  accounts={shown}
                  lastUsed={lastUsed}
                  now={now}
                  verifying={verify.isPending ? verify.variables.id : undefined}
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
        verifying={verify.isPending && verify.variables.id === detail?.id}
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
