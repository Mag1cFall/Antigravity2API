import { Gem, UsersRound } from 'lucide-react'

import { Tooltip } from '@/components/ui/tooltip'
import { tierOf } from '@/features/accounts/status'
import type { Account } from '@/features/accounts/types'
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
  const m = useMessages('accounts')
  const kind = tierOf(tier)
  const label = {
    ultra: m.tierUltra,
    ultraLite: m.tierUltraLite,
    pro: m.tierPro,
    free: m.tierFree,
    unknown: m.tierUnknown,
  }[kind]
  return (
    <Tooltip label={tier}>
      <span
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
    </Tooltip>
  )
}

// FamilyBadge 是家庭群组徽标，账户与其他账户在同一 Google One 家庭群组时显示，悬停列出同组的账户；compact 时只显示图标
export function FamilyBadge({ account, compact = false }: { account: Account; compact?: boolean }) {
  const m = useMessages('accounts')
  const { locale } = useI18n()
  if (!account.family?.length) return null
  return (
    <Tooltip
      label={fill(m.familyMembers, {
        members: new Intl.ListFormat(locale, { type: 'conjunction' }).format(account.family),
      })}
    >
      <span className="inline-flex h-icon shrink-0 items-center gap-1 rounded-sm bg-success-soft px-2 text-xs font-semibold whitespace-nowrap text-success-text">
        <UsersRound size={12} aria-hidden />
        <span className={compact ? 'sr-only' : undefined}>{m.family}</span>
      </span>
    </Tooltip>
  )
}
