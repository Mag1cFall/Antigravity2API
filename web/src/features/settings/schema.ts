import { z } from 'zod'

import { splitList } from '@/features/settings/queries'
import type { Settings } from '@/features/settings/types'

// goDuration 匹配 Go time.ParseDuration 接受的正数时长
const goDuration = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/

// validListen 判断 host:port 形式的监听地址，端口为 1 到 65535
export function validListen(value: string) {
  const match = /^(\[[0-9a-fA-F:.]+\]|[^\s:[\]]*):(\d{1,5})$/.exec(value)
  const port = Number(match?.[2])
  return match !== null && port >= 1 && port <= 65535
}

// validProxy 判断代理为空或为不带路径的 http、https、socks5 URL
export function validProxy(value: string) {
  if (value === '') return true
  try {
    const url = new URL(value)
    return (
      ['http:', 'https:', 'socks5:'].includes(url.protocol) &&
      url.hostname !== '' &&
      (url.pathname === '' || url.pathname === '/') &&
      url.search === '' &&
      url.hash === ''
    )
  } catch {
    return false
  }
}

// validAddress 判断 IPv4 或 IPv6 地址
function validAddress(value: string) {
  if (value.includes(':')) {
    try {
      return new URL('http://[' + value + ']').hostname !== ''
    } catch {
      return false
    }
  }
  const parts = value.split('.')
  return parts.length === 4 && parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255)
}

// validPrefixes 判断逗号分隔的 IP 或 CIDR 列表，空值表示不信任任何代理
export function validPrefixes(value: string) {
  return value
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
    .every((item) => {
      const [address = '', bits, extra] = item.split('/')
      if (extra !== undefined || !validAddress(address)) return false
      if (bits === undefined) return true
      const max = address.includes(':') ? 128 : 32
      return /^\d{1,3}$/.test(bits) && Number(bits) <= max
    })
}

// SchemaMessages 是校验失败时的提示
interface SchemaMessages {
  errListen: string
  errRequired: string
  errProxy: string
  errPositive: string
  errDuration: string
  errProxies: string
  errLogTotal: string
  errAdminUser: string
  errAdminPassword: string
}

// settingsSchema 与 internal/config 的 Validate 规则一致，字段名即 .env 配置项，passwordSet 表示已保存过管理密码；API_KEYS 在接入页、MODEL_MAPPING 在模型页单独保存
export function settingsSchema(m: SchemaMessages, passwordSet: boolean) {
  const positive = z.number({ error: m.errPositive }).int(m.errPositive).min(1, m.errPositive)
  return z
    .object({
      LISTEN: z.string().trim().refine(validListen, m.errListen),
      AUTH_DIR: z.string().trim().min(1, m.errRequired),
      RUNTIME_DIR: z.string().trim().min(1, m.errRequired),
      PROXY: z.string().trim().refine(validProxy, m.errProxy),
      SYSTEM_PROMPT: z.enum(['full', 'medium', 'aggressive', 'custom']),
      SYSTEM_PROMPT_SECTIONS: z.array(z.string()),
      SYSTEM_PROMPT_WRAP: z.enum(['full', 'time', 'none']),
      ACCOUNT_CONCURRENCY: positive,
      ROUTING_STRATEGY: z.enum(['round-robin', 'fill-first']),
      PREFER_LOWER_TIER_MODELS: z.array(z.string()),
      REQUEST_TIMEOUT: z.string().trim().regex(goDuration, m.errDuration),
      TRUSTED_PROXIES: z.string().trim().refine(validPrefixes, m.errProxies),
      LOG_LEVEL: z.enum(['debug', 'info', 'warn', 'error']),
      LOG_FORMAT: z.enum(['json', 'text']),
      LOG_FILE: z.boolean(),
      LOG_MAX_TOTAL_MB: z
        .number({ error: m.errLogTotal })
        .int(m.errLogTotal)
        .min(20, m.errLogTotal),
      REQUEST_LOG_BODIES: z.boolean(),
      ADMIN_AUTH_ENABLED: z.boolean(),
      ADMIN_USERNAME: z.string().trim(),
      ADMIN_PASSWORD: z.string(),
    })
    .superRefine((value, context) => {
      if (value.ADMIN_AUTH_ENABLED && !value.ADMIN_USERNAME) {
        context.addIssue({ code: 'custom', path: ['ADMIN_USERNAME'], message: m.errAdminUser })
      }
      if (value.ADMIN_AUTH_ENABLED && !passwordSet && !value.ADMIN_PASSWORD) {
        context.addIssue({ code: 'custom', path: ['ADMIN_PASSWORD'], message: m.errAdminPassword })
      }
    })
}

// SettingsForm 是设置表单的值
export type SettingsForm = z.infer<ReturnType<typeof settingsSchema>>

// parseMapping 把逗号分隔的 别名:模型 ID 拆成表单行，别名以最后一个冒号分隔
export function parseMapping(value: string) {
  return value
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
    .map((item) => {
      const index = item.lastIndexOf(':')
      return { alias: item.slice(0, index).trim(), model: item.slice(index + 1).trim() }
    })
}

// toForm 把 .env 文本值换成表单初值，缺少的项取 internal/config 的默认值
export function toForm(settings: Settings): SettingsForm {
  const v = (key: string, fallback: string) => settings.values[key] ?? fallback
  const list = (key: string, fallback: string) => splitList(v(key, fallback))
  const bool = (key: string, fallback: string) => v(key, fallback) === 'true'
  const num = (key: string, fallback: string) => Number(v(key, fallback))
  return {
    LISTEN: v('LISTEN', '127.0.0.1:8017'),
    AUTH_DIR: v('AUTH_DIR', 'auth'),
    RUNTIME_DIR: v('RUNTIME_DIR', 'runtime'),
    PROXY: v('PROXY', ''),
    SYSTEM_PROMPT: v('SYSTEM_PROMPT', 'full') as SettingsForm['SYSTEM_PROMPT'],
    SYSTEM_PROMPT_SECTIONS: list(
      'SYSTEM_PROMPT_SECTIONS',
      'identity,guidelines,communication_style',
    ),
    SYSTEM_PROMPT_WRAP: v('SYSTEM_PROMPT_WRAP', 'time') as SettingsForm['SYSTEM_PROMPT_WRAP'],
    ACCOUNT_CONCURRENCY: num('ACCOUNT_CONCURRENCY', '2'),
    ROUTING_STRATEGY: v('ROUTING_STRATEGY', 'round-robin') as SettingsForm['ROUTING_STRATEGY'],
    PREFER_LOWER_TIER_MODELS: list('PREFER_LOWER_TIER_MODELS', ''),
    REQUEST_TIMEOUT: v('REQUEST_TIMEOUT', '5m'),
    TRUSTED_PROXIES: v('TRUSTED_PROXIES', ''),
    LOG_LEVEL: v('LOG_LEVEL', 'info') as SettingsForm['LOG_LEVEL'],
    LOG_FORMAT: v('LOG_FORMAT', 'json') as SettingsForm['LOG_FORMAT'],
    LOG_FILE: bool('LOG_FILE', 'true'),
    LOG_MAX_TOTAL_MB: num('LOG_MAX_TOTAL_MB', '500'),
    REQUEST_LOG_BODIES: bool('REQUEST_LOG_BODIES', 'false'),
    ADMIN_AUTH_ENABLED: bool('ADMIN_AUTH_ENABLED', 'false'),
    ADMIN_USERNAME: v('ADMIN_USERNAME', 'admin'),
    ADMIN_PASSWORD: '',
  }
}

// toValues 把表单值换成 PATCH /api/settings 的 values，列表以逗号相连，密码留空时不提交
export function toValues(form: SettingsForm): Record<string, string> {
  const { ADMIN_PASSWORD, ...rest } = form
  const values: Record<string, string> = {}
  for (const [key, value] of Object.entries(rest))
    values[key] = Array.isArray(value) ? value.join(',') : String(value)
  if (ADMIN_PASSWORD) values.ADMIN_PASSWORD = ADMIN_PASSWORD
  return values
}
