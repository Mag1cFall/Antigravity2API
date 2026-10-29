import {
  Clock,
  Hourglass,
  Info,
  ListOrdered,
  LoaderCircle,
  MoreHorizontal,
  Network,
  RefreshCw,
  RotateCcw,
  Sparkles,
  Trash2,
  TriangleAlert,
} from 'lucide-react'
import { memo, type ReactNode, useRef } from 'react'

import { Tag } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Switch } from '@/components/ui/switch'
import { Table, Td, Th } from '@/components/ui/table'
import { Tooltip } from '@/components/ui/tooltip'
import { AccountReason, AccountStatusBadge } from '@/features/accounts/AccountStatus'
import { quotaFailure, type QuotaRefresh } from '@/features/accounts/actions'
import { accountMessages } from '@/features/accounts/messages'
import { creditsShort, statusOf } from '@/features/accounts/status'
import { SwitchClientDialog } from '@/features/accounts/SwitchClientDialog'
import { FamilyBadge, TierBadge } from '@/features/accounts/TierBadge'
import type { Account } from '@/features/accounts/types'
import { quotaMessages } from '@/features/quota/messages'
import { groupBuckets, groups, nameOf } from '@/features/quota/quota'
import { QuotaLine } from '@/features/quota/QuotaLine'
import { formatFullDateTime, formatNumber, formatRelative, type Locale } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'
import { usePhone } from '@/lib/media'

// RowActions 是账户行可执行的操作，onSelect 勾选或取消勾选账户
export interface RowActions {
  onSelect: (ids: string[], selected: boolean) => void
  onDetails: (account: Account) => void
  onToggle: (change: { account: Account; enabled: boolean }) => void
  onVerify: (account: Account) => void
  onRefreshQuota: (account: Account) => void
  onProxy: (account: Account, trigger: HTMLElement) => void
  onRemove: (account: Account) => void
}

// QuotaCell 显示账户一个额度组的 5 小时与每周额度，compact 时窄屏两个窗口排成一行、只显示百分比
export function QuotaCell({
  account,
  group,
  now,
  compact = false,
}: {
  account: Account
  group: string
  now: Date
  compact?: boolean
}) {
  const m = useMessages(accountMessages)
  const q = useMessages(quotaMessages)
  const buckets = groupBuckets(account.quota, group)
  if (buckets.length === 0) {
    return (
      <span className="text-xs text-foreground-subtle">
        {account.quota.length === 0 ? m.noQuota : m.noGroupQuota}
      </span>
    )
  }
  return (
    <div
      className={
        'flex flex-col gap-2 ' + (compact ? 'max-sm:flex-1 max-sm:flex-row max-sm:gap-4' : '')
      }
    >
      {buckets.map((bucket) => (
        <QuotaLine
          key={bucket.bucket_id}
          label={nameOf(q, bucket.window)}
          fraction={bucket.remaining_fraction}
          reset={bucket.reset_time}
          now={now}
          compact={compact}
        />
      ))}
    </div>
  )
}

// creditsTip 是 AI 积分标签的提示：开启超额使用、积分余额与使用积分所需的最低余额
function creditsTip(m: (typeof accountMessages)['zh'], account: Account, locale: Locale) {
  if (account.credit_balance === undefined || account.credit_minimum === undefined) {
    return m.creditsTag
  }
  return [
    m.creditsTag,
    fill(m.creditsBalance, { balance: formatNumber(account.credit_balance, locale) }),
    fill(creditsShort(account) ? m.creditsLow : m.creditsMinimum, {
      minimum: formatNumber(account.credit_minimum, locale),
    }),
  ].join(' · ')
}

// IconTag 是只显示图标与可选数字的标签，label 是读屏文字，tip 是提示中的全文，缺省时与 label 相同
function IconTag({
  label,
  tip = label,
  icon,
  warning = false,
  children,
}: {
  label: string
  tip?: string
  icon: ReactNode
  warning?: boolean
  children?: ReactNode
}) {
  return (
    <Tooltip label={tip}>
      <span className="inline-flex">
        <Tag warning={warning}>
          {icon}
          {children}
          <span className="sr-only">{label}</span>
        </Tag>
      </span>
    </Tooltip>
  )
}

// CreditsTag 是开启 AI 积分超额使用的账户的积分标签，显示余额，余额低于最低余额时为警示色
function CreditsTag({ account }: { account: Account }) {
  const m = useMessages(accountMessages)
  const { locale } = useI18n()
  return (
    <IconTag
      label={m.creditsTag}
      tip={creditsTip(m, account, locale)}
      warning={creditsShort(account)}
      icon={<Sparkles size={12} aria-hidden />}
    >
      {account.credit_balance !== undefined && formatNumber(account.credit_balance, locale)}
    </IconTag>
  )
}

// IconAction 是行内图标按钮，带提示
function IconAction({
  label,
  tooltip,
  onClick,
  icon,
  loading,
}: {
  label: string
  tooltip: string
  onClick: () => void
  icon: ReactNode
  loading?: boolean | undefined
}) {
  return (
    <Tooltip label={tooltip}>
      <Button
        variant="ghost"
        size="icon"
        aria-label={label}
        className="size-control-sm"
        loading={loading ?? false}
        onClick={onClick}
      >
        {icon}
      </Button>
    </Tooltip>
  )
}

// wideQuery 是行内显示刷新额度按钮的窗口宽度，与 xl 断点一致
const wideQuery = '(min-width: 80rem)'

// RefreshQuotaItem 是行内没有刷新额度按钮时菜单中的刷新额度，菜单打开时按窗口宽度决定是否显示
function RefreshQuotaItem({ onSelect }: { onSelect: () => void }) {
  const m = useMessages(accountMessages)
  if (window.matchMedia(wideQuery).matches) return null
  return (
    <DropdownMenuItem onSelect={onSelect}>
      <RefreshCw size={16} aria-hidden className="text-foreground-muted" />
      {m.refreshQuota}
    </DropdownMenuItem>
  )
}

// MoreActions 是行尾的更多操作菜单：设置代理与删除，窗口宽度不足 1280px 时另含刷新额度
function MoreActions({ account, actions }: { account: Account; actions: RowActions }) {
  const m = useMessages(accountMessages)
  const trigger = useRef<HTMLButtonElement>(null)
  return (
    <DropdownMenu>
      <Tooltip label={m.more}>
        <DropdownMenuTrigger asChild>
          <Button
            ref={trigger}
            variant="ghost"
            size="icon"
            aria-label={fill(m.moreOf, { email: account.id })}
            className="size-control-sm"
          >
            <MoreHorizontal size={16} aria-hidden />
          </Button>
        </DropdownMenuTrigger>
      </Tooltip>
      <DropdownMenuContent>
        <DropdownMenuItem
          onSelect={() => {
            actions.onDetails(account)
          }}
        >
          <Info size={16} aria-hidden className="text-foreground-muted" />
          {m.details}
        </DropdownMenuItem>
        <RefreshQuotaItem
          onSelect={() => {
            actions.onRefreshQuota(account)
          }}
        />
        <DropdownMenuItem
          onSelect={() => {
            actions.onProxy(account, trigger.current ?? document.body)
          }}
        >
          <Network size={16} aria-hidden className="text-foreground-muted" />
          {m.proxyMenu}
        </DropdownMenuItem>
        <DropdownMenuItem
          danger
          onSelect={() => {
            actions.onRemove(account)
          }}
        >
          <Trash2 size={16} aria-hidden />
          {m.delete}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

// AccountRow 是账户表的一行，账户对象不变时跳过重渲染
const AccountRow = memo(function AccountRow({
  account,
  lastUsed,
  now,
  selected,
  verifying,
  refreshing,
  queued,
  failure,
  phone,
  actions,
}: {
  account: Account
  lastUsed: string | undefined
  now: Date
  selected: boolean
  verifying: boolean
  refreshing: boolean
  queued: boolean
  failure: string | undefined
  phone: boolean
  actions: RowActions
}) {
  const m = useMessages(accountMessages)
  const q = useMessages(quotaMessages)
  const { locale } = useI18n()
  return (
    <tr className="max-sm:grid max-sm:grid-cols-[minmax(0,1fr)_auto] max-sm:gap-x-3 max-sm:gap-y-1 max-sm:py-2">
      <Td className="max-sm:col-start-1">
        <div className="flex items-start gap-3 max-sm:items-center">
          <Checkbox
            checked={selected}
            aria-label={fill(m.selectOf, { email: account.id })}
            onCheckedChange={(checked) => {
              actions.onSelect([account.id], checked === true)
            }}
            className="mt-[2px] max-sm:mt-0"
          />
          <div className="min-w-0 flex-1 max-sm:flex max-sm:items-center max-sm:gap-2">
            <Tooltip label={account.id}>
              <button
                type="button"
                className="block max-w-full min-w-0 truncate rounded-sm text-left text-sm font-medium hover:text-primary"
                onClick={() => {
                  actions.onDetails(account)
                }}
              >
                {account.id}
              </button>
            </Tooltip>
            <div className="relative mt-1 flex h-[72px] min-w-0 flex-col justify-center gap-1 overflow-hidden text-xs text-foreground-muted max-sm:hidden xl:h-6 xl:flex-row xl:items-center xl:justify-start xl:gap-2">
              <div className="flex h-6 min-w-0 shrink-0 items-center gap-2 *:shrink-0">
                {account.label && (
                  <span
                    className="max-w-[120px] min-w-0 truncate font-medium text-foreground"
                    title={account.label}
                  >
                    {account.label}
                  </span>
                )}
                {!phone && <TierBadge tier={account.tier} />}
              </div>
              <div className="flex h-6 shrink-0 items-center gap-2 empty:hidden">
                <FamilyBadge account={account} compact />
                {account.priority !== 0 && (
                  <IconTag
                    label={fill(m.priorityTag, { priority: account.priority })}
                    icon={<ListOrdered size={12} aria-hidden />}
                  >
                    {account.priority}
                  </IconTag>
                )}
                {!phone && account.ai_credits && <CreditsTag account={account} />}
                {account.proxy && (
                  <IconTag
                    label={fill(m.proxyInUse, { proxy: account.proxy })}
                    icon={<Network size={12} aria-hidden />}
                  />
                )}
              </div>
              <div className="flex h-4 min-w-0 items-center">
                {lastUsed ? (
                  <Tooltip
                    label={
                      fill(m.lastUsedAt, {
                        time: formatRelative(new Date(lastUsed), now, locale),
                      }) +
                      ' · ' +
                      formatFullDateTime(new Date(lastUsed), locale)
                    }
                  >
                    <span className="flex min-w-0 items-center gap-1">
                      <Clock size={12} aria-hidden className="shrink-0" />
                      <span className="truncate">
                        {formatRelative(new Date(lastUsed), now, locale)}
                      </span>
                    </span>
                  </Tooltip>
                ) : (
                  <span className="shrink-0 text-foreground-subtle">{m.neverUsed}</span>
                )}
              </div>
            </div>
          </div>
        </div>
      </Td>
      <Td className="max-sm:col-span-2">
        <div className="flex h-[72px] flex-col items-start justify-center gap-1 max-sm:h-6 max-sm:flex-row max-sm:items-center max-sm:justify-start max-sm:gap-2 max-sm:overflow-hidden xl:h-[60px]">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1 max-sm:shrink-0 max-sm:flex-nowrap">
            <AccountStatusBadge account={account} />
            {account.state === 'error' && (
              <Tooltip label={m.verifyAction}>
                <Button
                  size="sm"
                  variant="outline"
                  aria-label={fill(m.verifyActionOf, { email: account.id })}
                  loading={verifying}
                  className="size-6 px-0 pointer-coarse:size-6"
                  onClick={() => {
                    actions.onVerify(account)
                  }}
                >
                  <RotateCcw size={12} aria-hidden />
                </Button>
              </Tooltip>
            )}
            {refreshing && (
              <span role="status" className="inline-flex text-foreground-muted xl:hidden">
                <LoaderCircle size={14} aria-hidden className="animate-spin" />
                <span className="sr-only">{fill(m.quotaRefreshingOf, { email: account.id })}</span>
              </span>
            )}
            {queued && (
              <Tooltip label={m.quotaQueued}>
                <span className="inline-flex text-foreground-subtle">
                  <Hourglass size={14} aria-hidden />
                  <span className="sr-only">{fill(m.quotaQueuedOf, { email: account.id })}</span>
                </span>
              </Tooltip>
            )}
            {failure && !refreshing && !queued && (
              <Tooltip label={failure}>
                <button
                  type="button"
                  aria-label={fill(m.quotaFailedOf, { email: account.id })}
                  className="inline-flex rounded-sm text-error-text"
                  onClick={() => {
                    actions.onDetails(account)
                  }}
                >
                  <TriangleAlert size={14} aria-hidden />
                </button>
              </Tooltip>
            )}
          </div>
          {phone && (
            <span className="flex shrink-0 items-center gap-x-2">
              <TierBadge tier={account.tier} />
              {account.ai_credits && <CreditsTag account={account} />}
            </span>
          )}
          {statusOf(account) === 'error' ? (
            <Tooltip label={account.error}>
              <button
                type="button"
                aria-label={fill(m.errorOf, { email: account.id })}
                className="line-clamp-2 max-w-full min-w-0 rounded-sm text-left text-xs max-sm:line-clamp-1 max-sm:flex-1"
                onClick={() => {
                  actions.onDetails(account)
                }}
              >
                <AccountReason account={account} now={now} />
              </button>
            </Tooltip>
          ) : (
            <div className="line-clamp-2 max-w-full min-w-0 text-xs max-sm:line-clamp-1 max-sm:flex-1">
              <AccountReason account={account} now={now} />
            </div>
          )}
        </div>
      </Td>
      {groups.map((group) => (
        <Td key={group} className="max-sm:col-span-2">
          <div className="@container">
            <div className="flex h-[80px] flex-col justify-center max-sm:h-4! max-sm:flex-row max-sm:items-center max-sm:justify-start max-sm:gap-3 @min-[200px]:h-[40px]">
              <span className="w-[6rem] shrink-0 truncate text-xs text-foreground-muted sm:hidden">
                {q[group]}
              </span>
              <QuotaCell account={account} group={group} now={now} compact />
            </div>
          </div>
        </Td>
      ))}
      <Td className="max-sm:col-start-2 max-sm:row-start-1 max-sm:-my-2 max-sm:self-center">
        <div className="flex items-center justify-end gap-1">
          <Tooltip label={m.enabledLabel}>
            <span className="mr-2 inline-flex">
              <Switch
                checked={account.state !== 'disabled'}
                aria-label={fill(m.enableOf, { email: account.id })}
                onCheckedChange={(enabled) => {
                  actions.onToggle({ account, enabled })
                }}
              />
            </span>
          </Tooltip>
          <span className="inline-flex max-xl:hidden">
            <IconAction
              label={fill(m.refreshQuotaOf, { email: account.id })}
              tooltip={m.refreshQuota}
              icon={<RefreshCw size={16} aria-hidden />}
              loading={refreshing}
              onClick={() => {
                actions.onRefreshQuota(account)
              }}
            />
          </span>
          <span className="contents max-sm:hidden">
            <SwitchClientDialog email={account.id} variant="icon" />
          </span>
          <MoreActions account={account} actions={actions} />
        </div>
      </Td>
    </tr>
  )
})

// AccountTable 是账户表：勾选框、账户、套餐与最近使用，状态与原因，两组额度与操作；列宽固定，窗口变窄时额度列与操作列收窄、倒计时换到比例条下方，宽度小于 640px 时每个账户一张卡片；表头勾选框选择本页全部账户
export function AccountTable({
  accounts,
  lastUsed,
  now,
  selected,
  verifying,
  refreshing,
  quotaRefresh,
  actions,
}: {
  accounts: Account[]
  lastUsed: Map<string, string | undefined>
  now: Date
  selected: ReadonlySet<string>
  verifying: Set<string>
  refreshing: Set<string>
  quotaRefresh: QuotaRefresh
  actions: RowActions
}) {
  const m = useMessages(accountMessages)
  const q = useMessages(quotaMessages)
  const phone = usePhone()
  const picked = accounts.filter((account) => selected.has(account.id)).length
  return (
    <Table label={m.title} fixed stack minWidth={880}>
      <colgroup>
        <col />
        <col className="w-[156px] xl:w-[192px]" />
        <col className="w-[164px] xl:w-[224px]" />
        <col className="w-[164px] xl:w-[224px]" />
        <col className="w-[188px] xl:w-[192px]" />
      </colgroup>
      <thead>
        <tr>
          <Th data-stack>
            <span className="flex items-center gap-3">
              <Checkbox
                checked={picked === 0 ? false : picked === accounts.length ? true : 'indeterminate'}
                aria-label={m.selectPage}
                onCheckedChange={(checked) => {
                  actions.onSelect(
                    accounts.map((account) => account.id),
                    checked === true,
                  )
                }}
              />
              {m.account}
            </span>
          </Th>
          <Th>{m.state}</Th>
          <Th>{q.gemini}</Th>
          <Th>{q['3p']}</Th>
          <Th className="text-right">{m.actions}</Th>
        </tr>
      </thead>
      <tbody>
        {accounts.map((account) => (
          <AccountRow
            key={account.id}
            account={account}
            lastUsed={lastUsed.get(account.id)}
            now={now}
            selected={selected.has(account.id)}
            verifying={verifying.has(account.id)}
            refreshing={refreshing.has(account.id)}
            queued={quotaRefresh.queued.has(account.id)}
            failure={quotaFailure(quotaRefresh, account)}
            phone={phone}
            actions={actions}
          />
        ))}
      </tbody>
    </Table>
  )
}
