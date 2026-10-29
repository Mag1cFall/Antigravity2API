import { TriangleAlert } from 'lucide-react'
import { Link } from 'react-router'

import { Meter } from '@/components/ui/meter'
import { DotList } from '@/components/ui/stats'
import { accountMessages } from '@/features/accounts/messages'
import { type Status, statusOf } from '@/features/accounts/status'
import type { Account } from '@/features/accounts/types'
import { dashboardMessages } from '@/features/dashboard/messages'
import { quotaMessages } from '@/features/quota/messages'
import {
  available,
  groupBuckets,
  groups,
  lowQuota,
  nameOf,
  type QuotaGroup,
  quotaText,
  quotaTone,
} from '@/features/quota/quota'
import type { UsageReport } from '@/features/usage/types'
import { formatCost, formatNumber, formatPercent } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// statusOrder 是账户分布条中各状态的先后，fill 是对应色块
const statusOrder: { status: Status; fill: string }[] = [
  { status: 'ready', fill: 'bg-success' },
  { status: 'cooling', fill: 'bg-warning' },
  { status: 'verify', fill: 'bg-warning' },
  { status: 'error', fill: 'bg-error' },
  { status: 'pending', fill: 'bg-info' },
  { status: 'disabled', fill: 'bg-foreground-subtle' },
]

// average 返回数值的平均值，没有数值时为 null
function average(values: (number | null)[]) {
  const present = values.filter((value): value is number => value !== null)
  return present.length ? present.reduce((sum, value) => sum + value, 0) / present.length : null
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

// AccountsCell 是就绪账户数与按状态着色的账户分布条
function AccountsCell({ accounts }: { accounts: Account[] }) {
  const m = useMessages(dashboardMessages)
  const a = useMessages(accountMessages)
  const { locale } = useI18n()
  const counts = statusOrder
    .map((item) => ({
      ...item,
      count: accounts.filter((account) => statusOf(account) === item.status).length,
    }))
    .filter((item) => item.count > 0)
  const ready = counts.find((item) => item.status === 'ready')?.count ?? 0
  const others = counts.filter((item) => item.status !== 'ready')
  return (
    <>
      <dt className="text-xs text-foreground-muted">{m.accounts}</dt>
      <dd className="flex items-baseline gap-1 tabular-nums">
        <span className="text-2xl font-semibold">{formatNumber(ready, locale)}</span>
        <span className="text-sm text-foreground-muted">
          {'/ ' + formatNumber(accounts.length, locale)}
        </span>
      </dd>
      <dd aria-hidden className="flex h-[6px] gap-px overflow-hidden rounded-full bg-hover">
        {counts.map((item) => (
          <span
            key={item.status}
            className={item.fill}
            style={{ width: `${(item.count / accounts.length) * 100}%` }}
          />
        ))}
      </dd>
      <dd className="text-xs text-foreground-muted">
        {others.length ? (
          <DotList
            items={others.map((item) => (
              <Link
                key={item.status}
                to={'/accounts?state=' + item.status}
                className="inline-flex items-center gap-1 rounded-sm hover:text-foreground"
              >
                <span aria-hidden className={'size-2 rounded-full ' + item.fill} />
                {a[item.status] + ' ' + formatNumber(item.count, locale)}
              </Link>
            ))}
          />
        ) : (
          m.accountsAllReady
        )}
      </dd>
    </>
  )
}

// QuotaRow 是一个额度组的平均剩余比例与 5 小时、每周两条比例条
function QuotaRow({ accounts, group }: { accounts: Account[]; group: QuotaGroup }) {
  const m = useMessages(dashboardMessages)
  const q = useMessages(quotaMessages)
  const { locale } = useI18n()
  const pools = pooled(accounts, group)
  const value = average(pools.map((account) => available(account.quota, group)))
  return (
    <div className="grid max-w-[640px] grid-cols-[minmax(0,7rem)_5rem_minmax(0,1fr)] items-center gap-x-6 max-sm:grid-cols-[minmax(0,1fr)_5rem]">
      <span className="truncate text-sm">{nameOf(q, group)}</span>
      <span
        className={
          'text-right text-lg font-semibold tabular-nums ' +
          (value === null ? 'text-foreground-subtle' : quotaText(value))
        }
      >
        {value === null ? '—' : formatPercent(value, locale)}
      </span>
      <span className="grid grid-cols-[3rem_minmax(2rem,1fr)_3.5rem] items-center gap-x-2 gap-y-1 text-xs max-sm:col-span-2">
        {value === null ? (
          <span className="col-span-3 text-foreground-subtle">{m.quotaNone}</span>
        ) : (
          (['5h', 'weekly'] as const).map((window) => {
            const fraction = windowAverage(pools, group, window)
            if (fraction === null) return null
            return (
              <span key={window} className="contents">
                <span className="truncate text-foreground-muted">{nameOf(q, window)}</span>
                <Meter
                  thin
                  value={fraction}
                  tone={quotaTone(fraction)}
                  label={nameOf(q, group) + ' ' + nameOf(q, window)}
                />
                <span className="text-right tabular-nums">{formatPercent(fraction, locale)}</span>
              </span>
            )
          })
        )}
      </span>
    </div>
  )
}

// QuotaCellGroup 是两组额度的平均剩余比例与低额度账户数
function QuotaCellGroup({ accounts }: { accounts: Account[] }) {
  const m = useMessages(dashboardMessages)
  const { locale } = useI18n()
  const low = accounts.filter((account) =>
    account.quota.some((bucket) => bucket.remaining_fraction < lowQuota),
  ).length
  return (
    <>
      <dt className="text-xs text-foreground-muted">{m.quota}</dt>
      <dd className="flex flex-col gap-2">
        {groups.map((group) => (
          <QuotaRow key={group} accounts={accounts} group={group} />
        ))}
      </dd>
      <dd className="text-xs">
        {low > 0 ? (
          <Link
            to="/accounts?sort=reset"
            className="inline-flex items-center gap-1 rounded-sm text-warning-text hover:underline"
          >
            <TriangleAlert size={12} aria-hidden />
            {fill(m.lowCount, { count: formatNumber(low, locale) })}
          </Link>
        ) : (
          <span className="text-foreground-muted">{m.lowNone}</span>
        )}
      </dd>
    </>
  )
}

// RequestsCell 是 24 小时请求数、失败数与估算费用，下方逐小时请求的柱状图填满剩余高度
function RequestsCell({ usage }: { usage: UsageReport | undefined }) {
  const m = useMessages(dashboardMessages)
  const { locale } = useI18n()
  const totals = usage?.totals
  const buckets = usage?.buckets ?? []
  const peak = Math.max(1, ...buckets.map((bucket) => bucket.requests))
  return (
    <>
      <dt className="text-xs text-foreground-muted">{m.requests}</dt>
      <dd className="text-2xl font-semibold tabular-nums">
        {totals ? formatNumber(totals.requests, locale) : '—'}
      </dd>
      {totals && (
        <dd className="text-xs text-foreground-muted">
          <DotList
            items={[
              <span key="failed" className={totals.failed ? 'text-error-text' : undefined}>
                {fill(m.failedCount, { count: formatNumber(totals.failed, locale) })}
              </span>,
              fill(m.costEstimate, { cost: formatCost(totals.cost_usd, locale) }),
            ]}
          />
        </dd>
      )}
      {buckets.length > 0 && (
        <dd
          role="img"
          aria-label={fill(m.requestsTrend, { peak: formatNumber(peak, locale) })}
          className="flex min-h-8 flex-1 items-end gap-px"
        >
          {buckets.map((bucket) => (
            <span
              key={bucket.at}
              className={
                'min-w-px flex-1 rounded-t-sm ' + (bucket.failed ? 'bg-error' : 'bg-primary')
              }
              style={{
                height: `${String(Math.max(bucket.requests ? 8 : 2, (bucket.requests / peak) * 100))}%`,
                opacity: bucket.requests ? 1 : 0.25,
              }}
            />
          ))}
        </dd>
      )}
    </>
  )
}

// Pulse 是仪表盘顶部的概览：账户分布、两组额度与 24 小时请求，三格形态各不相同，宽度 ≥1024 时单行
export function Pulse({
  accounts,
  usage,
}: {
  accounts: Account[]
  usage: UsageReport | undefined
}) {
  const enabled = accounts.filter((account) => account.state !== 'disabled')
  return (
    <dl className="grid grid-cols-2 overflow-clip rounded-lg border border-card-border bg-surface shadow-card max-sm:grid-cols-1 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.6fr)_minmax(0,1fr)]">
      {[
        <AccountsCell key="accounts" accounts={accounts} />,
        <QuotaCellGroup key="quota" accounts={enabled} />,
        <RequestsCell key="requests" usage={usage} />,
      ].map((cell, index) => (
        <div
          key={index}
          className={
            '-mt-px -ml-px flex min-w-0 flex-col gap-2 border-t border-l border-border px-4 py-3 ' +
            (index === 1 ? 'max-lg:order-last max-lg:col-span-2 max-sm:col-span-1' : '')
          }
        >
          {cell}
        </div>
      ))}
    </dl>
  )
}
