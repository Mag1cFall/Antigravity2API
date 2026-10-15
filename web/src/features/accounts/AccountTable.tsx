import { Info, MoreHorizontal, Network, ShieldCheck, Trash2 } from 'lucide-react'
import { memo, type ReactNode, useRef } from 'react'

import { Tag } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
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

// RowActions 是账户行可执行的操作
export interface RowActions {
  onDetails: (account: Account) => void
  onToggle: (change: { account: Account; enabled: boolean }) => void
  onVerify: (account: Account) => void
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

// IconAction 是行内图标按钮，带提示
function IconAction({
  label,
  tooltip,
  onClick,
  icon,
}: {
  label: string
  tooltip: string
  onClick: () => void
  icon: ReactNode
}) {
  return (
    <Tooltip label={tooltip}>
      <Button
        variant="ghost"
        size="icon"
        aria-label={label}
        className="size-control-sm"
        onClick={onClick}
      >
        {icon}
      </Button>
    </Tooltip>
  )
}

// MoreActions 是行尾的更多操作菜单：设置代理与删除
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
  verifying,
  actions,
}: {
  account: Account
  lastUsed: string | undefined
  now: Date
  verifying: boolean
  actions: RowActions
}) {
  const m = useMessages(accountMessages)
  const { locale } = useI18n()
  return (
    <tr>
      <Td>
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
        <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-foreground-muted">
          <TierBadge tier={account.tier} />
          <FamilyBadge account={account} />
          {account.priority !== 0 && (
            <Tag>{fill(m.priorityTag, { priority: account.priority })}</Tag>
          )}
          {lastUsed ? (
            <Tooltip label={formatFullDateTime(new Date(lastUsed), locale)}>
              <span className="truncate">
                {fill(m.lastUsedAt, { time: formatRelative(new Date(lastUsed), now, locale) })}
              </span>
            </Tooltip>
          ) : (
            <span className="truncate text-foreground-subtle">{m.neverUsed}</span>
          )}
        </div>
        {account.proxy && (
          <div
            className="mt-1 truncate font-mono text-xs text-foreground-muted"
            title={account.proxy}
          >
            {fill(m.proxyInUse, { proxy: account.proxy })}
          </div>
        )}
      </Td>
      <Td>
        <div className="flex flex-col items-start gap-1">
          <AccountStatusBadge account={account} />
          <AccountReason account={account} now={now} />
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
                <ShieldCheck size={12} aria-hidden />
                {m.verifyShort}
              </Button>
            </Tooltip>
          )}
        </div>
      </Td>
      {groups.map((group) => (
        <Td key={group}>
          <QuotaCell account={account} group={group} now={now} />
        </Td>
      ))}
      <Td>
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
          <SwitchClientDialog email={account.id} variant="icon" />
          <MoreActions account={account} actions={actions} />
        </div>
      </Td>
    </tr>
  )
})

// AccountTable 是账户表：账户、套餐与最近使用，状态与原因，两组额度与操作；列宽固定，窗口变窄时额度列收窄、倒计时换到比例条下方
export function AccountTable({
  accounts,
  lastUsed,
  now,
  verifying,
  actions,
}: {
  accounts: Account[]
  lastUsed: Map<string, string | undefined>
  now: Date
  verifying: string | undefined
  actions: RowActions
}) {
  const m = useMessages(accountMessages)
  const q = useMessages(quotaMessages)
  return (
    <Table label={m.title} fixed minWidth={860}>
      <colgroup>
        <col />
        <col className="w-[156px]" />
        <col className="w-[164px] xl:w-[224px]" />
        <col className="w-[164px] xl:w-[224px]" />
        <col className="w-[184px]" />
      </colgroup>
      <thead>
        <tr>
          <Th>{m.account}</Th>
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
            verifying={verifying === account.id}
            actions={actions}
          />
        ))}
      </tbody>
    </Table>
  )
}
