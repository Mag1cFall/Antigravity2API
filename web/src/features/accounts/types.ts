import type { QuotaBucket } from '@/features/quota/types'

// AccountState 对应 internal/app 的账户状态
export type AccountState = 'ready' | 'cooling' | 'pending' | 'error' | 'disabled'

// Account 对应 internal/app 的 account，id 即账户邮箱，label 是备注名，proxy 中的密码显示为 xxxxx，priority 是调度优先级，verify_url 是上游要求验证时给出的验证链接，quota_at 是额度的读取时间，shared 按额度组列出共用同一额度池的其他就绪账户，ai_credits 为是否开启 AI 积分超额使用，device 是设备档案及由它决定的主机名与 User-Agent
export interface Account {
  id: string
  label?: string
  state: AccountState
  tier: string
  project: string
  proxy?: string
  priority: number
  added_at: string
  token_expiry?: string
  active: number
  error?: string
  verify_url?: string
  cooldown?: Record<string, string>
  quota: QuotaBucket[]
  quota_at?: string
  shared?: Partial<Record<'gemini' | '3p', string[]>>
  ai_credits: boolean
  device: Record<string, string>
}

// SwitchResult 对应 POST /api/accounts/{id}/switch 的返回，account 为本机官方客户端切换到的账户
export interface SwitchResult {
  account: string
}

// LoginMode 对应 internal/app 的登录方式
export type LoginMode = 'browser' | 'code'

// Pairing 对应 internal/app 的 pairingToken，供 login --remote 使用
export interface Pairing {
  token: string
  expires_at: string
}

// Login 对应 internal/app 的 loginState
export interface Login {
  id: string
  mode: LoginMode
  url: string
  state: 'pending' | 'completed' | 'failed' | 'canceled'
  account?: string
  error?: string
  expires_at: string
}
