import { Gem, UsersRound } from 'lucide-react'

import { Tooltip } from '@/components/ui/tooltip'
import { accountMessages } from '@/features/accounts/messages'
import { tierOf } from '@/features/accounts/status'
import type { Account } from '@/features/accounts/types'
import { quotaMessages } from '@/features/quota/messages'
import { nameOf } from '@/features/quota/quota'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// tierClass 是各套餐徽标的颜色
const tierClass = {
  ultra: 'bg-violet-soft text-violet',
  ultraLite: 'bg-violet-soft text-violet',
  pro: 'bg-info-soft text-primary',
  free: 'bg-muted text-foreground-muted',
  unknown: 'bg-muted text-foreground-subtle',
}

// TierBadge 是套餐徽标，悬停显示套餐 ID
export function TierBadge({ tier }: { tier: string }) {
  const m = useMessages(accountMessages)
  const kind = tierOf(tier)
  const label = {
    ultra: m.tierUltra,
    ultraLite: m.tierUltraLite,
    pro: m.tierPro,
    free: m.tierFree,
    unknown: m.tierUnknown,
  }[kind]
  return (
    <span
      title={tier || undefined}
      className={
        'inline-flex h-icon shrink-0 items-center gap-1 rounded-sm px-2 text-xs font-semibold whitespace-nowrap ' +
        tierClass[kind]
      }
    >
      {(kind === 'ultra' || kind === 'ultraLite' || kind === 'pro') && (
        <Gem size={12} aria-hidden />
      )}
      {label}
    </span>
  )
}

// FamilyBadge 是家庭组徽标，账户与其他就绪账户共用额度池时显示，悬停列出各额度组的共用账户；compact 时只显示图标
export function FamilyBadge({ account, compact = false }: { account: Account; compact?: boolean }) {
  const m = useMessages(accountMessages)
  const q = useMessages(quotaMessages)
  const { locale } = useI18n()
  const list = new Intl.ListFormat(locale, { type: 'conjunction' })
  const pools = Object.entries(account.shared ?? {}).filter(([, members]) => members.length > 0)
  if (pools.length === 0) return null
  return (
    <Tooltip
      label={
        <span className="flex flex-col gap-1">
          {pools.map(([group, members]) => (
            <span key={group}>
              {fill(m.familyShared, { group: nameOf(q, group), members: list.format(members) })}
            </span>
          ))}
        </span>
      }
    >
      <span className="inline-flex h-icon shrink-0 items-center gap-1 rounded-sm bg-success-soft px-2 text-xs font-semibold whitespace-nowrap text-success-text">
        <UsersRound size={12} aria-hidden />
        <span className={compact ? 'sr-only' : undefined}>{m.family}</span>
      </span>
    </Tooltip>
  )
}
