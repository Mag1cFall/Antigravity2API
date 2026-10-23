import {
  Info,
  ListOrdered,
  MoreHorizontal,
  Network,
  RefreshCw,
  RotateCcw,
  Sparkles,
  Trash2,
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
import { accountMessages } from '@/features/accounts/messages'
import { SwitchClientDialog } from '@/features/accounts/SwitchClientDialog'
import { FamilyBadge, TierBadge } from '@/features/accounts/TierBadge'
import type { Account } from '@/features/accounts/types'
import { quotaMessages } from '@/features/quota/messages'
import { groupBuckets, groups, nameOf } from '@/features/quota/quota'
import { QuotaLine } from '@/features/quota/QuotaLine'
import { formatFullDateTime, formatRelative } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

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

// QuotaCell 显示账户一个额度组的 5 小时与每周额度
export function QuotaCell({ account, group, now }: { account: Account; group: string; now: Date }) {
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
    <div className="flex flex-col gap-2">
      {buckets.map((bucket) => (
        <QuotaLine
          key={bucket.bucket_id}
          label={nameOf(q, bucket.window)}
          fraction={bucket.remaining_fraction}
          reset={bucket.reset_time}
          now={now}
        />
      ))}
    </div>
  )
}

// IconTag 是只显示图标与可选数字的标签，全文在提示与读屏文字中
function IconTag({
  label,
  icon,
  children,
}: {
  label: string
  icon: ReactNode
  children?: ReactNode
}) {
  return (
    <Tooltip label={label}>
      <span className="inline-flex">
        <Tag>
          {icon}
          {children}
          <span className="sr-only">{label}</span>
        </Tag>
      </span>
    </Tooltip>
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
  actions,
}: {
  account: Account
  lastUsed: string | undefined
  now: Date
  selected: boolean
  verifying: boolean
  refreshing: boolean
  actions: RowActions
}) {
  const m = useMessages(accountMessages)
  const q = useMessages(quotaMessages)
  const { locale } = useI18n()
  return (
    <tr>
      <Td className="max-sm:basis-full">
        <div className="flex items-start gap-3">
          <Checkbox
            checked={selected}
            aria-label={fill(m.selectOf, { email: account.id })}
            onCheckedChange={(checked) => {
              actions.onSelect([account.id], checked === true)
            }}
            className="mt-0.5"
          />
          <div className="min-w-0 flex-1">
            <Tooltip label={account.id}>
              <button
                type="button"
                className="block max-w-full truncate rounded-sm text-left text-sm font-medium hover:text-primary"
                onClick={() => {
                  actions.onDetails(account)
                }}
              >
                {account.id}
              </button>
            </Tooltip>
            <div className="mt-1 flex h-[52px] min-w-0 flex-col justify-center gap-1 text-xs text-foreground-muted xl:h-6 xl:flex-row xl:items-center xl:justify-start xl:gap-2">
              <div className="flex h-6 min-w-0 items-center gap-2">
                {account.label && (
                  <span
                    className="max-w-40 min-w-0 truncate font-medium text-foreground"
                    title={account.label}
                  >
                    {account.label}
                  </span>
                )}
                <TierBadge tier={account.tier} />
              </div>
              <div className="flex h-6 shrink-0 items-center gap-2 empty:hidden">
                <FamilyBadge account={account} compact />
                {account.priority !== 0 && (
                  <IconTag
                    label={fill(m.priorityTag, { priority: account.priority })}
                    icon={<ListOrdered size={12} aria-hidden className="mr-1" />}
                  >
                    {account.priority}
                  </IconTag>
                )}
                {account.ai_credits && (
                  <IconTag label={m.creditsTag} icon={<Sparkles size={12} aria-hidden />} />
                )}
              </div>
            </div>
            <div className="mt-1 flex h-4 min-w-0 items-center gap-x-2 text-xs text-foreground-muted">
              {lastUsed ? (
                <Tooltip label={formatFullDateTime(new Date(lastUsed), locale)}>
                  <span className="shrink-0">
                    {fill(m.lastUsedAt, { time: formatRelative(new Date(lastUsed), now, locale) })}
                  </span>
                </Tooltip>
              ) : (
                <span className="shrink-0 text-foreground-subtle">{m.neverUsed}</span>
              )}
              {account.proxy && (
                <span className="min-w-0 truncate font-mono" title={account.proxy}>
                  {fill(m.proxyInUse, { proxy: account.proxy })}
                </span>
              )}
            </div>
          </div>
        </div>
      </Td>
      <Td className="max-sm:basis-full">
        <div className="flex h-[80px] flex-col items-start justify-center gap-1 pointer-coarse:h-[92px]">
          <AccountStatusBadge account={account} />
          <div
            className={
              'max-w-full text-xs ' + (account.state === 'error' ? 'line-clamp-1' : 'line-clamp-2')
            }
          >
            <AccountReason account={account} now={now} />
          </div>
          {account.state === 'error' && (
            <Tooltip label={m.verifyAction}>
              <Button
                size="sm"
                variant="outline"
                aria-label={fill(m.verifyActionOf, { email: account.id })}
                loading={verifying}
                className="h-6 px-2 text-xs"
                onClick={() => {
                  actions.onVerify(account)
                }}
              >
                <RotateCcw size={12} aria-hidden />
                {m.verifyShort}
              </Button>
            </Tooltip>
          )}
        </div>
      </Td>
      {groups.map((group) => (
        <Td key={group} label={q[group]} className="max-sm:flex-[1_1_144px]">
          <div className="@container">
            <div className="flex h-[80px] flex-col justify-center @min-[200px]:h-[40px]">
              <QuotaCell account={account} group={group} now={now} />
            </div>
          </div>
        </Td>
      ))}
      <Td className="max-sm:basis-full">
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
          <IconAction
            label={fill(m.detailsOf, { email: account.id })}
            tooltip={m.details}
            icon={<Info size={16} aria-hidden />}
            onClick={() => {
              actions.onDetails(account)
            }}
          />
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
          <SwitchClientDialog email={account.id} variant="icon" />
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
  actions,
}: {
  accounts: Account[]
  lastUsed: Map<string, string | undefined>
  now: Date
  selected: ReadonlySet<string>
  verifying: Set<string>
  refreshing: Set<string>
  actions: RowActions
}) {
  const m = useMessages(accountMessages)
  const q = useMessages(quotaMessages)
  const picked = accounts.filter((account) => selected.has(account.id)).length
  return (
    <Table label={m.title} fixed stack minWidth={880}>
      <colgroup>
        <col />
        <col className="w-[156px]" />
        <col className="w-[164px] xl:w-[224px]" />
        <col className="w-[164px] xl:w-[224px]" />
        <col className="w-[188px] xl:w-[224px]" />
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
            actions={actions}
          />
        ))}
      </tbody>
    </Table>
  )
}
