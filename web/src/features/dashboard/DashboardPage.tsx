import { useQuery } from '@tanstack/react-query'
import {
  Activity,
  Bot,
  History,
  RefreshCw,
  Sparkles,
  Star,
  TriangleAlert,
  Users,
} from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { Card, CardBody, CardHeader } from '@/components/ui/card'
import { PageHeader } from '@/components/ui/page'
import { ErrorState, Loading } from '@/components/ui/states'
import { dots, StatCards } from '@/components/ui/stats'
import { useRefreshAccounts } from '@/features/accounts/actions'
import { AddAccountButton } from '@/features/accounts/AddAccountDialog'
import { accountMessages } from '@/features/accounts/messages'
import { useAccounts } from '@/features/accounts/queries'
import { statusOf } from '@/features/accounts/status'
import { SwitchClientDialog } from '@/features/accounts/SwitchClientDialog'
import { TierBadge } from '@/features/accounts/TierBadge'
import type { Account } from '@/features/accounts/types'
import { useProgress } from '@/features/connect/progress'
import { dashboardMessages } from '@/features/dashboard/messages'
import { SetupBanner, SetupGuide, type SetupSteps } from '@/features/dashboard/SetupGuide'
import { quotaMessages } from '@/features/quota/messages'
import {
  available,
  groupBuckets,
  groups,
  lowQuota,
  nameOf,
  type QuotaGroup,
} from '@/features/quota/quota'
import { QuotaHistory } from '@/features/quota/QuotaHistory'
import { QuotaLine } from '@/features/quota/QuotaLine'
import { apiQuery, parseFilter } from '@/features/requests/filters'
import { RequestStateBadge } from '@/features/requests/RequestStateBadge'
import type { RequestPage } from '@/features/requests/types'
import { useUsage } from '@/features/usage/queries'
import { api } from '@/lib/api'
import {
  formatCost,
  formatDateTime,
  formatDuration,
  formatNumber,
  formatPercent,
} from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// average 返回数值的平均值，没有数值时为 null
function average(values: (number | null)[]) {
  const present = values.filter((value): value is number => value !== null)
  return present.length ? present.reduce((sum, value) => sum + value, 0) / present.length : null
}

// windowAverage 返回各账户在一个额度组一个窗口上的平均剩余比例
function windowAverage(accounts: Account[], group: string, window: string) {
  return average(
    accounts.map(
      (account) =>
        groupBuckets(account.quota, group).find((bucket) => bucket.window === window)
          ?.remaining_fraction ?? null,
    ),
  )
}

// pooled 返回一个额度组中每个额度池的一个账户，共用额度池的家庭组成员只计一次
function pooled(accounts: Account[], group: QuotaGroup) {
  const seen = new Set<string>()
  return accounts.filter((account) => {
    if (seen.has(account.id)) return false
    seen.add(account.id)
    for (const member of account.shared?.[group] ?? []) seen.add(member)
    return true
  })
}

// bestAccount 返回一个额度组中可用比例最高的就绪账户
function bestAccount(accounts: Account[], group: QuotaGroup) {
  let best: { account: Account; fraction: number } | undefined
  for (const account of accounts) {
    if (statusOf(account) !== 'ready' && statusOf(account) !== 'cooling') continue
    if (account.cooldown?.[group]) continue
    const fraction = available(account.quota, group)
    if (fraction === null || fraction <= 0) continue
    if (!best || fraction > best.fraction) best = { account, fraction }
  }
  return best
}

// BestAccounts 是两组额度各自的推荐账户与切换官方客户端
function BestAccounts({ accounts, now }: { accounts: Account[]; now: Date }) {
  const m = useMessages(dashboardMessages)
  const q = useMessages(quotaMessages)
  return (
    <Card>
      <CardHeader icon={<Star size={16} aria-hidden />} title={m.best} description={m.bestHint} />
      <CardBody className="flex flex-col gap-3">
        {groups.map((group) => {
          const best = bestAccount(accounts, group)
          return (
            <div
              key={group}
              className="flex flex-wrap items-center gap-x-4 gap-y-3 rounded-md border border-border p-4"
            >
              <div className="min-w-0 flex-[1_1_220px]">
                <span className="text-xs font-medium text-foreground-muted">
                  {fill(m.bestFor, { group: nameOf(q, group) })}
                </span>
                {best ? (
                  <>
                    <div className="mt-1 flex flex-wrap items-center gap-2">
                      <span className="text-sm font-medium wrap-anywhere">{best.account.id}</span>
                      <TierBadge tier={best.account.tier} />
                    </div>
                    <div className="mt-3 flex max-w-[360px] flex-col gap-2">
                      {groupBuckets(best.account.quota, group).map((bucket) => (
                        <QuotaLine
                          key={bucket.bucket_id}
                          label={nameOf(q, bucket.window)}
                          fraction={bucket.remaining_fraction}
                          reset={bucket.reset_time}
                          now={now}
                        />
                      ))}
                    </div>
                  </>
                ) : (
                  <p className="mt-1 text-sm text-foreground-muted">{m.bestNone}</p>
                )}
              </div>
              {best && <SwitchClientDialog email={best.account.id} variant="button" />}
            </div>
          )
        })}
      </CardBody>
    </Card>
  )
}

// RecentRequests 是最近 5 条请求
function RecentRequests({ now }: { now: Date }) {
  const m = useMessages(dashboardMessages)
  const app = useMessages(appMessages)
  const { locale } = useI18n()
  const recent = useQuery({
    queryKey: ['requests', 'recent'],
    queryFn: () =>
      api<RequestPage>(
        '/api/usage/records?' +
          apiQuery(parseFilter(new URLSearchParams()), new Date()).toString() +
          '&limit=5',
      ),
  })
  return (
    <Card>
      <CardHeader
        icon={<History size={16} aria-hidden />}
        title={m.recent}
        description={m.recentHint}
        actions={
          <Button variant="link" asChild>
            <Link to="/requests">{m.allRequests}</Link>
          </Button>
        }
      />
      {recent.isPending ? (
        <Loading label={app.loading} />
      ) : recent.isError ? (
        <ErrorState
          title={m.loadFailed}
          error={recent.error}
          retryLabel={app.retry}
          onRetry={() => void recent.refetch()}
        />
      ) : recent.data.items.length === 0 ? (
        <CardBody>
          <Callout tone="muted">
            <p>{m.noRequests}</p>
            <Button size="sm" asChild>
              <Link to="/connect">{m.goConnect}</Link>
            </Button>
          </Callout>
        </CardBody>
      ) : (
        <ul className="flex flex-col border-t border-border">
          {recent.data.items.map((record) => (
            <li key={record.id} className="border-t border-border first:border-t-0">
              <Link
                to={'/requests?id=' + encodeURIComponent(record.id)}
                className="grid grid-cols-[96px_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 px-6 py-3 hover:bg-muted/60 max-sm:px-4"
              >
                <RequestStateBadge state={record.state} />
                <span className="min-w-0">
                  <span className="block truncate font-mono text-xs">{record.model}</span>
                  <span className="block truncate text-xs text-foreground-muted">
                    {record.account || '—'}
                  </span>
                </span>
                <span className="text-right text-xs text-foreground-muted tabular-nums">
                  <span className="block">
                    {formatDateTime(new Date(record.time), now, locale)}
                  </span>
                  <span className="block">{formatDuration(record.duration_ms, locale)}</span>
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </Card>
  )
}

// Overview 是有账户后的仪表盘：统计卡片、推荐账户、最近请求与额度变化
function Overview({ accounts, now }: { accounts: Account[]; now: Date }) {
  const m = useMessages(dashboardMessages)
  const a = useMessages(accountMessages)
  const { locale } = useI18n()
  const usage = useUsage(86_400_000)
  const totals = usage.data?.totals
  const enabled = accounts.filter((account) => account.state !== 'disabled')
  const count = (status: string) =>
    accounts.filter((account) => statusOf(account) === status).length
  const notReady = (['cooling', 'verify', 'error', 'pending', 'disabled'] as const)
    .filter((status) => count(status) > 0)
    .map((status) => a[status] + ' ' + String(count(status)))
  const low = enabled.filter((account) =>
    account.quota.some((bucket) => bucket.remaining_fraction < lowQuota),
  ).length
  const percent = (value: number | null) => (value === null ? '—' : formatPercent(value, locale))
  const quotaStat = (group: QuotaGroup) => {
    const pools = pooled(enabled, group)
    const value = average(pools.map((account) => available(account.quota, group)))
    return {
      value: percent(value),
      detail:
        value === null
          ? [m.quotaNone]
          : dots(
              fill(m.quotaDetail, {
                short: percent(windowAverage(pools, group, '5h')),
                weekly: percent(windowAverage(pools, group, 'weekly')),
              }),
            ),
    }
  }
  const gemini = quotaStat('gemini')
  const third = quotaStat('3p')
  return (
    <>
      <StatCards
        items={[
          {
            key: 'accounts',
            label: m.accounts,
            icon: <Users size={16} />,
            tone: 'info',
            value:
              formatNumber(count('ready'), locale) + ' / ' + formatNumber(accounts.length, locale),
            detail: notReady.length ? notReady : [m.accountsAllReady],
          },
          {
            key: 'gemini',
            label: m.geminiQuota,
            icon: <Sparkles size={16} />,
            tone: 'success',
            ...gemini,
          },
          { key: '3p', label: m.thirdQuota, icon: <Bot size={16} />, tone: 'violet', ...third },
          {
            key: 'low',
            label: m.lowAccounts,
            icon: <TriangleAlert size={16} />,
            tone: low > 0 ? 'warning' : 'success',
            value: formatNumber(low, locale),
            detail: [m.lowDetail],
          },
          {
            key: 'requests',
            label: m.requests,
            icon: <Activity size={16} />,
            tone: 'info',
            value: totals ? formatNumber(totals.requests, locale) : '—',
            detail: totals
              ? dots(
                  fill(m.requestsDetail, {
                    failed: formatNumber(totals.failed, locale),
                    cost: formatCost(totals.cost_usd, locale),
                  }),
                )
              : [],
          },
        ]}
      />
      {usage.isError && (
        <Callout tone="error" role="alert">
          <p>{fill(m.usageFailed, { message: usage.error.message })}</p>
        </Callout>
      )}
      <div className="grid grid-cols-1 gap-6 xl:grid-cols-2">
        <BestAccounts accounts={accounts} now={now} />
        <RecentRequests now={now} />
      </div>
      <QuotaHistory />
    </>
  )
}

// DashboardPage 是仪表盘：没有账户时为三步引导，引导中添加账户后打勾并可进入总览；总览为账户与额度，接入未完成时顶部显示进度
export function DashboardPage() {
  const m = useMessages(dashboardMessages)
  const app = useMessages(appMessages)
  const now = useNow(30_000)
  const accounts = useAccounts()
  const usage = useUsage(86_400_000)
  const progress = useProgress()
  const refresh = useRefreshAccounts()
  const a = useMessages(accountMessages)
  const [guiding, setGuiding] = useState(false)
  if (accounts.data?.accounts.length === 0 && !guiding) setGuiding(true)
  if (accounts.isPending || accounts.isError) {
    return (
      <>
        <PageHeader title={m.title} />
        <Card>
          {accounts.isPending ? (
            <Loading label={app.loading} />
          ) : (
            <ErrorState
              title={m.accountsFailed}
              error={accounts.error}
              retryLabel={app.retry}
              onRetry={() => void accounts.refetch()}
            />
          )}
        </Card>
      </>
    )
  }
  const list = accounts.data.accounts
  const used = (usage.data?.totals.requests ?? 0) > 0
  const steps: SetupSteps = {
    accounts: list.length > 0,
    endpoint: progress.endpoint || used,
    client: progress.client || used,
  }
  if (guiding) {
    return (
      <SetupGuide
        steps={steps}
        onEnter={() => {
          setGuiding(false)
        }}
      />
    )
  }
  const ready = list.filter((account) => account.state === 'ready').length
  return (
    <>
      <PageHeader
        title={m.title}
        description={fill(m.summary, { total: list.length, ready })}
        actions={
          <>
            <Button
              loading={refresh.isPending}
              onClick={() => {
                refresh.mutate()
              }}
            >
              <RefreshCw size={16} aria-hidden />
              {a.refreshAll}
            </Button>
            <AddAccountButton />
          </>
        }
      />
      {!(steps.endpoint && steps.client) && !progress.dismissed && <SetupBanner steps={steps} />}
      <Overview accounts={list} now={now} />
    </>
  )
}
