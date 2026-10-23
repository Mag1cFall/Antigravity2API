import { ExternalLink } from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { accountMessages } from '@/features/accounts/messages'
import { earliestCooldown, statusOf, statusTone } from '@/features/accounts/status'
import type { Account } from '@/features/accounts/types'
import { quotaMessages } from '@/features/quota/messages'
import { nameOf } from '@/features/quota/quota'
import { formatRelative } from '@/lib/format'
import { fill, fillNodes, useI18n, useMessages } from '@/lib/i18n'

// AccountStatusBadge 是账户状态的文字标签
export function AccountStatusBadge({ account }: { account: Account }) {
  const m = useMessages(accountMessages)
  const status = statusOf(account)
  return <Badge tone={statusTone[status]}>{m[status]}</Badge>
}

// AccountReason 说明账户当前状态的原因：冷却到何时、为何异常、验证入口或进行中的请求数；列表中待验证账户只显示验证入口，原因在提示中
export function AccountReason({
  account,
  now,
  full = false,
}: {
  account: Account
  now: Date
  full?: boolean
}) {
  const m = useMessages(accountMessages)
  const q = useMessages(quotaMessages)
  const { locale } = useI18n()
  const status = statusOf(account)
  if (status === 'verify') {
    const link = (
      <a
        href={account.verify_url}
        target="_blank"
        rel="noopener noreferrer"
        title={full ? undefined : m.verifyReason}
        className="rounded-sm text-xs text-primary hover:underline"
      >
        {m.openVerify}
        <span className="whitespace-nowrap">
          {'\u2060'}
          <ExternalLink size={12} aria-hidden className="ml-1 inline align-[-1px]" />
        </span>
      </a>
    )
    if (!full) return link
    return (
      <span className="flex flex-col items-start gap-1 text-xs text-foreground-muted">
        <span>{m.verifyReason}</span>
        {link}
      </span>
    )
  }
  if (status === 'error') {
    return (
      <span
        title={full ? undefined : account.error}
        className="text-xs wrap-anywhere text-error-text"
      >
        {account.error}
      </span>
    )
  }
  if (status === 'cooling') {
    const cooldown = earliestCooldown(account)
    if (!cooldown) return null
    return (
      <span className="text-xs text-warning-text">
        {fillNodes(m.coolingReason, {
          group: nameOf(q, cooldown[0]),
          time: (
            <span className="whitespace-nowrap">
              {formatRelative(new Date(cooldown[1]), now, locale)}
            </span>
          ),
        })}
      </span>
    )
  }
  if (status === 'disabled') {
    return <span className="text-xs text-foreground-muted">{m.disabledReason}</span>
  }
  if (status === 'pending') {
    return <span className="text-xs text-foreground-muted">{m.pendingReason}</span>
  }
  if (account.active > 0) {
    return (
      <span className="text-xs text-primary">
        {fill(m.activeRequests, { count: account.active })}
      </span>
    )
  }
  return full ? <span className="text-xs text-foreground-muted">{m.readyReason}</span> : null
}
