import { z } from 'zod'

import { splitList } from '@/features/settings/queries'
import type { Settings } from '@/features/settings/types'

// goDuration 匹配 Go time.ParseDuration 接受的正数时长
const goDuration = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/

// validListen 判断 host:port 形式的监听地址，端口为 1 到 65535
function validListen(value: string) {
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

// validBaseURL 判断媒体链接地址为空或为 http、https URL
function validBaseURL(value: string) {
  if (value === '') return true
  try {
    const url = new URL(value)
    return ['http:', 'https:'].includes(url.protocol) && url.hostname !== ''
  } catch {
    return false
  }
}

// proxyLines 返回按行填写的代理，忽略空行
function proxyLines(value: string) {
  return value
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
}

// SchemaMessages 是校验失败时的提示
interface SchemaMessages {
  errListen: string
  errRequired: string
  errProxy: string
  errPositive: string
  errDuration: string
  errLogTotal: string
  errReserve: string
  errAdminUser: string
  errAdminPassword: string
  errMediaBase: string
  errHedge: string
}

// settingsSchema 与 internal/config 的 Validate 规则一致，字段名即 .env 配置项，passwordSet 表示已保存过管理密码；API_KEYS 在接入页、MODEL_MAPPING 在模型页单独保存
export function settingsSchema(m: SchemaMessages, passwordSet: boolean) {
  const positive = z.number({ error: m.errPositive }).int(m.errPositive).min(1, m.errPositive)
  return z
    .object({
      LISTEN: z.string().trim().refine(validListen, m.errListen),
      AUTH_DIR: z.string().trim().min(1, m.errRequired),
      RUNTIME_DIR: z.string().trim().min(1, m.errRequired),
      MEDIA_OUTPUT: z.enum(['inline', 'url']),
      MEDIA_BASE_URL: z.string().trim().refine(validBaseURL, m.errMediaBase),
      PROXY: z.string().refine((value) => proxyLines(value).every(validProxy), m.errProxy),
      SYSTEM_PROMPT: z.enum(['full', 'medium', 'aggressive', 'custom']),
      SYSTEM_PROMPT_TEMPLATE: z.string(),
      SYSTEM_PROMPT_USER_TEMPLATE: z.string(),
      REJECTED_RULES: z.string(),
      ACCOUNT_CONCURRENCY: positive,
      ROUTING_STRATEGY: z.enum(['round-robin', 'fill-first']),
      PREFER_LOWER_TIER_MODELS: z.array(z.string()),
      REQUEST_TIMEOUT: z.string().trim().regex(goDuration, m.errDuration),
      FIRST_EVENT_HEDGE: z.string().regex(/^(off|auto|[1-9]\d*)$/, m.errHedge),
      FIRST_EVENT_HEDGE_MODELS: z.array(z.string()),
      NONSTREAM_KEEPALIVE: z.boolean(),
      FAKE_STREAM: z.boolean(),
      QUOTA_REFRESH_INTERVAL: z
        .string()
        .trim()
        .refine((value) => value === '0' || goDuration.test(value), m.errDuration),
      QUOTA_RESERVE_PERCENT: z
        .number({ error: m.errReserve })
        .int(m.errReserve)
        .min(0, m.errReserve)
        .max(99, m.errReserve),
      WEEKLY_QUOTA_WARMUP: z.boolean(),
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

// toForm 把服务端返回的 .env 文本值换成表单初值
export function toForm(settings: Settings): SettingsForm {
  const v = (key: string) => settings.values[key] ?? ''
  const list = (key: string) => splitList(v(key))
  const bool = (key: string) => v(key) === 'true'
  const num = (key: string) => Number(v(key))
  return {
    LISTEN: v('LISTEN'),
    AUTH_DIR: v('AUTH_DIR'),
    RUNTIME_DIR: v('RUNTIME_DIR'),
    MEDIA_OUTPUT: v('MEDIA_OUTPUT') as SettingsForm['MEDIA_OUTPUT'],
    MEDIA_BASE_URL: v('MEDIA_BASE_URL'),
    PROXY: list('PROXY').join('\n'),
    SYSTEM_PROMPT: v('SYSTEM_PROMPT') as SettingsForm['SYSTEM_PROMPT'],
    SYSTEM_PROMPT_TEMPLATE: v('SYSTEM_PROMPT_TEMPLATE'),
    SYSTEM_PROMPT_USER_TEMPLATE: v('SYSTEM_PROMPT_USER_TEMPLATE'),
    REJECTED_RULES: v('REJECTED_RULES'),
    ACCOUNT_CONCURRENCY: num('ACCOUNT_CONCURRENCY'),
    ROUTING_STRATEGY: v('ROUTING_STRATEGY') as SettingsForm['ROUTING_STRATEGY'],
    PREFER_LOWER_TIER_MODELS: list('PREFER_LOWER_TIER_MODELS'),
    REQUEST_TIMEOUT: v('REQUEST_TIMEOUT'),
    FIRST_EVENT_HEDGE: v('FIRST_EVENT_HEDGE'),
    FIRST_EVENT_HEDGE_MODELS: list('FIRST_EVENT_HEDGE_MODELS'),
    NONSTREAM_KEEPALIVE: bool('NONSTREAM_KEEPALIVE'),
    FAKE_STREAM: bool('FAKE_STREAM'),
    QUOTA_REFRESH_INTERVAL: v('QUOTA_REFRESH_INTERVAL'),
    QUOTA_RESERVE_PERCENT: num('QUOTA_RESERVE_PERCENT'),
    WEEKLY_QUOTA_WARMUP: bool('WEEKLY_QUOTA_WARMUP'),
    LOG_LEVEL: v('LOG_LEVEL') as SettingsForm['LOG_LEVEL'],
    LOG_FORMAT: v('LOG_FORMAT') as SettingsForm['LOG_FORMAT'],
    LOG_FILE: bool('LOG_FILE'),
    LOG_MAX_TOTAL_MB: num('LOG_MAX_TOTAL_MB'),
    REQUEST_LOG_BODIES: bool('REQUEST_LOG_BODIES'),
    ADMIN_AUTH_ENABLED: bool('ADMIN_AUTH_ENABLED'),
    ADMIN_USERNAME: v('ADMIN_USERNAME'),
    ADMIN_PASSWORD: '',
  }
}

// toValues 把表单值换成 PATCH /api/settings 的 values，列表与按行填写的代理以逗号相连，密码留空时不提交
export function toValues(form: SettingsForm): Record<string, string> {
  const { ADMIN_PASSWORD, PROXY, ...rest } = form
  const values: Record<string, string> = { PROXY: proxyLines(PROXY).join(',') }
  for (const [key, value] of Object.entries(rest))
    values[key] = Array.isArray(value) ? value.join(',') : String(value)
  if (ADMIN_PASSWORD) values.ADMIN_PASSWORD = ADMIN_PASSWORD
  return values
}
