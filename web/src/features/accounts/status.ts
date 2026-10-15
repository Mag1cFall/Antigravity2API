import type { BadgeTone } from '@/components/ui/badge'
import type { Account } from '@/features/accounts/types'

// Status 是账户在界面上的状态，error 状态带验证链接时为 verify
export type Status = 'ready' | 'cooling' | 'verify' | 'error' | 'disabled' | 'pending'

// statuses 是状态筛选的取值，pending 只在全部中出现
export const statuses = ['ready', 'cooling', 'verify', 'error', 'disabled'] as const

// statusOf 返回账户在界面上的状态
export function statusOf(account: Account): Status {
  if (account.state === 'error') return account.verify_url ? 'verify' : 'error'
  return account.state
}

// statusTone 是状态标签的语气
export const statusTone: Record<Status, BadgeTone> = {
  ready: 'success',
  cooling: 'warning',
  verify: 'warning',
  error: 'error',
  disabled: 'neutral',
  pending: 'info',
}

// Tier 是套餐档位
export type Tier = 'ultra' | 'ultraLite' | 'pro' | 'free' | 'unknown'

// tierOf 按套餐 ID 返回档位，例如 g1-ultra-tier、g1-pro-tier、free-tier
export function tierOf(tier: string): Tier {
  const id = tier.toLowerCase()
  if (id.includes('ultra-lite')) return 'ultraLite'
  if (id.includes('ultra')) return 'ultra'
  if (id.includes('pro')) return 'pro'
  if (id.includes('free') || id.includes('standard')) return 'free'
  return 'unknown'
}

// earliestCooldown 返回最早结束的冷却组与截止时间
export function earliestCooldown(account: Account) {
  const entries = Object.entries(account.cooldown ?? {})
  if (entries.length === 0) return undefined
  return entries.reduce((first, entry) =>
    Date.parse(entry[1]) < Date.parse(first[1]) ? entry : first,
  )
}
