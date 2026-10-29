import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { Link } from 'react-router'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { Card, CardBody, CardHeader } from '@/components/ui/card'
import { PageHeader } from '@/components/ui/page'
import { ErrorState, Loading } from '@/components/ui/states'
import { AddAccountButton } from '@/features/accounts/AddAccountDialog'
import { useAccounts } from '@/features/accounts/queries'
import { RefreshAllButton } from '@/features/accounts/RefreshAllButton'
import { statusOf } from '@/features/accounts/status'
import { SwitchClientDialog } from '@/features/accounts/SwitchClientDialog'
import { TierBadge } from '@/features/accounts/TierBadge'
import type { Account } from '@/features/accounts/types'
import { useProgress } from '@/features/connect/progress'
import { dashboardMessages } from '@/features/dashboard/messages'
import { Pulse } from '@/features/dashboard/Pulse'
import { SetupBanner, SetupGuide, type SetupSteps } from '@/features/dashboard/SetupGuide'
import { quotaMessages } from '@/features/quota/messages'
import { available, groupBuckets, groups, nameOf, type QuotaGroup } from '@/features/quota/quota'
import { QuotaHistory } from '@/features/quota/QuotaHistory'
import { QuotaLine } from '@/features/quota/QuotaLine'
import { apiQuery, parseFilter } from '@/features/requests/filters'
import { RequestStateBadge } from '@/features/requests/RequestStateBadge'
import type { RequestPage } from '@/features/requests/types'
import { useUsage } from '@/features/usage/queries'
import { api } from '@/lib/api'
import { formatDateTime, formatDuration } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

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
      <CardHeader title={m.best} />
      <CardBody className="flex flex-col gap-2">
        {groups.map((group) => {
          const best = bestAccount(accounts, group)
          return (
            <div
              key={group}
              className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-md border border-border px-3 py-2"
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
                    <div className="mt-2 flex max-w-[360px] flex-col gap-1">
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
        title={m.recent}
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
                className="grid grid-cols-[96px_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 px-4 py-2 hover:bg-muted/60"
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

// Overview 是有账户后的仪表盘：概览条、推荐账户、最近请求与额度变化
function Overview({ accounts, now }: { accounts: Account[]; now: Date }) {
  const m = useMessages(dashboardMessages)
  const usage = useUsage(86_400_000)
  return (
    <>
      <Pulse accounts={accounts} usage={usage.data} />
      {usage.isError && (
        <Callout tone="error" role="alert">
          <p>{fill(m.usageFailed, { message: usage.error.message })}</p>
        </Callout>
      )}
      <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
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
            <RefreshAllButton accounts={list} />
            <AddAccountButton />
          </>
        }
      />
      {!(steps.endpoint && steps.client) && !progress.dismissed && <SetupBanner steps={steps} />}
      <Overview accounts={list} now={now} />
    </>
  )
}
