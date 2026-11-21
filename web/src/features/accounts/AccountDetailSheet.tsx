import {
  Download,
  ExternalLink,
  Network,
  RefreshCw,
  RotateCcw,
  Trash2,
  UsersRound,
} from 'lucide-react'
import { type ReactNode, useId, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NumberInput } from '@/components/ui/number-input'
import { Switch } from '@/components/ui/switch'
import { Tooltip } from '@/components/ui/tooltip'
import { AccountReason, AccountStatusBadge } from '@/features/accounts/AccountStatus'
import { QuotaCell, type RowActions } from '@/features/accounts/AccountTable'
import {
  quotaFailure,
  useClearCooldown,
  useQuotaRefresh,
  useSetCredits,
  useSetLabel,
  useSetPriority,
} from '@/features/accounts/actions'
import { useAccountModels } from '@/features/accounts/queries'
import { creditsShort, statusOf } from '@/features/accounts/status'
import { SwitchClientDialog } from '@/features/accounts/SwitchClientDialog'
import { FamilyBadge, TierBadge } from '@/features/accounts/TierBadge'
import type { Account } from '@/features/accounts/types'
import { kindOf } from '@/features/models/types'
import { groups, nameOf, type QuotaGroup } from '@/features/quota/quota'
import type { UsageStats } from '@/features/usage/types'
import {
  formatCompact,
  formatCost,
  formatFullDateTime,
  formatNumber,
  formatRelative,
} from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// Row 是资料中的一行键值
function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-1 gap-1 border-t border-border py-3 first:border-t-0 sm:grid-cols-[160px_minmax(0,1fr)] sm:gap-4">
      <dt className="text-sm text-foreground-muted">{label}</dt>
      <dd className="min-w-0 text-sm break-words">{children}</dd>
    </div>
  )
}

// Block 是详情中的一个分组
function Block({
  title,
  aside,
  children,
}: {
  title: string
  aside?: ReactNode
  children: ReactNode
}) {
  const id = useId()
  return (
    <section aria-labelledby={id} className="flex flex-col gap-3">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h3 id={id} className="text-base font-semibold">
          {title}
        </h3>
        {aside && <span className="text-xs text-foreground-muted">{aside}</span>}
      </div>
      {children}
    </section>
  )
}

// PriorityField 编辑账户的调度优先级，改动后点保存提交
function PriorityField({ account }: { account: Account }) {
  const m = useMessages('accounts')
  const save = useSetPriority()
  const [value, setValue] = useState(account.priority)
  const unchanged = Number.isNaN(value) || value === account.priority
  return (
    <div className="flex flex-wrap items-center gap-3">
      <NumberInput
        aria-label={m.priority}
        value={value}
        onChange={setValue}
        min={0}
        max={99}
        decreaseLabel={m.priorityDecrease}
        increaseLabel={m.priorityIncrease}
        minReason={fill(m.priorityMin, { min: 0 })}
        maxReason={fill(m.priorityMax, { max: 99 })}
      />
      <Tooltip disabledReason={unchanged && m.priorityUnchanged}>
        <Button
          size="sm"
          variant="primary"
          disabled={unchanged}
          loading={save.isPending}
          onClick={() => {
            save.mutate({ account, priority: value })
          }}
        >
          {m.prioritySave}
        </Button>
      </Tooltip>
    </div>
  )
}

// LabelField 编辑账户的备注名，改动后点保存或按 Enter 提交，留空时清除
function LabelField({ account }: { account: Account }) {
  const m = useMessages('accounts')
  const save = useSetLabel()
  const [value, setValue] = useState(account.label ?? '')
  const unchanged = value.trim() === (account.label ?? '')
  return (
    <form
      className="flex flex-wrap items-center gap-3"
      onSubmit={(event) => {
        event.preventDefault()
        if (!unchanged) save.mutate({ account, label: value.trim() })
      }}
    >
      <Input
        aria-label={m.label}
        value={value}
        placeholder={m.labelPlaceholder}
        autoComplete="off"
        onChange={(event) => {
          setValue(event.target.value)
        }}
        className="max-w-field"
      />
      <Tooltip disabledReason={unchanged && m.labelUnchanged}>
        <Button
          type="submit"
          aria-label={m.labelSaveName}
          size="sm"
          variant="primary"
          disabled={unchanged}
          loading={save.isPending}
        >
          {m.labelSave}
        </Button>
      </Tooltip>
    </form>
  )
}

// CreditsField 开启或关闭账户的 AI 积分超额使用
function CreditsField({ account }: { account: Account }) {
  const m = useMessages('accounts')
  const save = useSetCredits()
  return (
    <label className="flex items-center gap-2 text-sm">
      <Switch
        checked={account.ai_credits}
        disabled={save.isPending}
        aria-label={fill(m.creditsOf, { email: account.id })}
        onCheckedChange={(enabled) => {
          save.mutate({ account, enabled })
        }}
      />
      {m.credits}
    </label>
  )
}

// deviceKeys 是设备档案各字段在详情中的顺序
const deviceKeys = [
  'os',
  'arch',
  'user_name',
  'hostname',
  'app_data_dir',
  'time_zone',
  'installation_id',
  'user_agent',
] as const

// familyURL 返回以该账户打开 Google 家庭群组管理页的地址
function familyURL(email: string) {
  return (
    'https://accounts.google.com/AccountChooser?' +
    new URLSearchParams({
      Email: email,
      continue: 'https://myaccount.google.com/family/details',
    }).toString()
  )
}

// FamilyBlock 是账户详情中的家庭群组：共用同一组每周额度的其他账户（点击打开其详情）与在 Google 中管理家庭群组的链接
function FamilyBlock({ account, onOpen }: { account: Account; onOpen: (id: string) => void }) {
  const m = useMessages('accounts')
  const members = account.family ?? []
  return (
    <Block title={m.family}>
      <p className="text-sm text-foreground-muted">
        {members.length > 0 ? m.familyHint : m.familyNone}
      </p>
      {members.length > 0 && (
        <ul className="flex flex-wrap gap-2">
          {members.map((member) => (
            <li key={member}>
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  onOpen(member)
                }}
              >
                <UsersRound size={16} aria-hidden />
                {member}
              </Button>
            </li>
          ))}
        </ul>
      )}
      <a
        href={familyURL(account.id)}
        target="_blank"
        rel="noopener noreferrer"
        className="inline-flex items-center gap-1 self-start text-sm font-medium text-primary hover:text-primary-hover"
      >
        {m.familyManage}
        <ExternalLink size={14} aria-hidden />
      </a>
    </Block>
  )
}

// ModelsBlock 是账户详情中该账户可用的模型：对话模型按额度组分组，生图、语音转写与代码补全各为一组，列出名称、ID 与思考档位
function ModelsBlock({ account }: { account: Account }) {
  const m = useMessages('accounts')
  const q = useMessages('quota')
  const mm = useMessages('models')
  const models = useAccountModels(account.id)
  const list = models.data?.models ?? []
  const chat = list.filter((model) => kindOf(model) === 'chat')
  const chatGroups = [...new Set(chat.map((model) => model.quota_group ?? 'gemini'))].sort(
    (a, b) =>
      (groups.indexOf(a as QuotaGroup) + 1 || groups.length + 1) -
      (groups.indexOf(b as QuotaGroup) + 1 || groups.length + 1),
  )
  const kindTitles = {
    image: mm.kindImage,
    transcription: mm.kindTranscription,
    completion: mm.kindCompletion,
  }
  const sections = [
    ...chatGroups.map((group) => ({
      key: group,
      title: nameOf(q, group),
      items: chat.filter((model) => (model.quota_group ?? 'gemini') === group),
    })),
    ...(['image', 'transcription', 'completion'] as const).map((kind) => ({
      key: kind,
      title: kindTitles[kind],
      items: list.filter((model) => kindOf(model) === kind),
    })),
  ].filter((section) => section.items.length > 0)
  return (
    <Block
      title={m.detailModels}
      aside={list.length > 0 && fill(m.detailModelsCount, { count: list.length })}
    >
      {models.isPending ? (
        <p className="text-sm text-foreground-muted">{m.detailModelsLoading}</p>
      ) : models.isError ? (
        <p role="alert" className="text-sm wrap-anywhere text-error-text">
          {models.error.message}
        </p>
      ) : list.length === 0 ? (
        <p className="text-sm text-foreground-muted">{m.detailModelsNone}</p>
      ) : (
        <div className="flex flex-col gap-4">
          {sections.map((section) => (
            <div key={section.key} className="flex flex-col gap-2">
              <h4 className="text-sm font-medium text-foreground-muted">{section.title}</h4>
              <ul className="flex flex-col overflow-hidden rounded-md border border-border">
                {section.items.map((model) => (
                  <li
                    key={model.id}
                    className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 border-t border-border px-3 py-2 first:border-t-0"
                  >
                    <span className="text-sm font-medium">{model.name || model.id}</span>
                    <span className="min-w-0 font-mono text-xs break-all text-foreground-muted">
                      {model.id}
                    </span>
                    {model.thinking_levels && model.thinking_levels.length > 0 && (
                      <span className="ml-auto text-xs text-foreground-muted">
                        {fill(m.detailModelsLevels, { levels: model.thinking_levels.join(' / ') })}
                      </span>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      )}
    </Block>
  )
}

// AccountDetailSheet 是从右侧滑入的账户详情：状态与原因、额度、可用模型、家庭群组、调度优先级、备注名、AI 积分超额使用、近 30 天用量、资料、设备档案与全部操作
export function AccountDetailSheet({
  account,
  usage,
  now,
  verifying,
  refreshing,
  actions,
  onOpen,
  onClose,
}: {
  account: Account | undefined
  usage: UsageStats | undefined
  now: Date
  verifying: boolean
  refreshing: boolean
  actions: RowActions
  onOpen: (id: string) => void
  onClose: () => void
}) {
  const m = useMessages('accounts')
  const q = useMessages('quota')
  const app = useMessages('app')
  const { locale } = useI18n()
  const status = account ? statusOf(account) : undefined
  const quotaRefresh = useQuotaRefresh()
  const clearCooldown = useClearCooldown()
  const failure = account && quotaFailure(quotaRefresh, account)
  const deviceLabels: Record<(typeof deviceKeys)[number], string> = {
    os: m.deviceOs,
    arch: m.deviceArch,
    user_name: m.deviceUserName,
    hostname: m.deviceHostname,
    app_data_dir: m.deviceAppData,
    time_zone: m.deviceTimeZone,
    installation_id: m.deviceInstallation,
    user_agent: m.deviceUserAgent,
  }
  return (
    <Dialog
      open={account !== undefined}
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      {account && (
        <DialogContent
          layout="sheet"
          title={account.id}
          description={
            <span className="flex flex-wrap items-center gap-2">
              {account.label && (
                <span className="font-medium text-foreground">{account.label}</span>
              )}
              <TierBadge tier={account.tier} />
              <FamilyBadge account={account} />
              <AccountStatusBadge account={account} />
            </span>
          }
          closeLabel={app.closeDialog}
        >
          <div className="flex flex-col gap-6">
            <div className="flex flex-wrap items-center gap-2">
              <SwitchClientDialog email={account.id} variant="button" />
              {status === 'error' && (
                <Button
                  size="sm"
                  loading={verifying}
                  onClick={() => {
                    actions.onVerify(account)
                  }}
                >
                  <RotateCcw size={16} aria-hidden />
                  {m.verifyAction}
                </Button>
              )}
              <Button
                size="sm"
                onClick={(event) => {
                  actions.onProxy(account, event.currentTarget)
                }}
              >
                <Network size={16} aria-hidden />
                {m.proxyMenu}
              </Button>
              <Button
                size="sm"
                onClick={() => {
                  actions.onExport(account)
                }}
              >
                <Download size={16} aria-hidden />
                {m.exportJson}
              </Button>
              <label className="ml-2 flex items-center gap-2 text-sm">
                <Switch
                  checked={account.state !== 'disabled'}
                  aria-label={fill(m.enableOf, { email: account.id })}
                  onCheckedChange={(enabled) => {
                    actions.onToggle({ account, enabled })
                  }}
                />
                {m.enabledLabel}
              </label>
              <Button
                size="sm"
                variant="ghost"
                className="ml-auto enabled:hover:text-error-text"
                onClick={() => {
                  actions.onRemove(account)
                  onClose()
                }}
              >
                <Trash2 size={16} aria-hidden />
                {m.delete}
              </Button>
            </div>
            {status === 'error' && (
              <Callout tone="error" role="status">
                <AccountReason account={account} now={now} full />
              </Callout>
            )}
            {status === 'verify' && (
              <Callout tone="error" role="status">
                <div className="flex flex-col gap-1">
                  <p className="font-medium">{m.verifyReason}</p>
                  <p className="text-xs">{m.verifyHint}</p>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <Button size="sm" variant="primary" asChild>
                    <a href={account.verify_url} target="_blank" rel="noopener noreferrer">
                      <ExternalLink size={16} aria-hidden />
                      {m.openVerify}
                    </a>
                  </Button>
                  <Button
                    size="sm"
                    loading={verifying}
                    onClick={() => {
                      actions.onVerify(account)
                    }}
                  >
                    <RotateCcw size={16} aria-hidden />
                    {m.verifyAction}
                  </Button>
                </div>
              </Callout>
            )}
            {status === 'disabled' && account.verify_url && (
              <Callout tone="muted" role="status">
                <p>{m.disabledLink}</p>
                <Button size="sm" asChild>
                  <a href={account.verify_url} target="_blank" rel="noopener noreferrer">
                    <ExternalLink size={16} aria-hidden />
                    {m.openUpstream}
                  </a>
                </Button>
              </Callout>
            )}
            <Block
              title={m.detailQuota}
              aside={
                <span className="flex items-center gap-2">
                  {account.quota_at &&
                    fill(m.detailQuotaRead, {
                      time: formatRelative(
                        new Date(Math.min(Date.parse(account.quota_at), now.getTime())),
                        now,
                        locale,
                      ),
                    })}
                  <Button
                    size="sm"
                    variant="outline"
                    loading={refreshing}
                    onClick={() => {
                      actions.onRefreshQuota(account)
                    }}
                  >
                    <RefreshCw size={16} aria-hidden />
                    {m.refreshQuota}
                  </Button>
                </span>
              }
            >
              <p className="text-sm text-foreground-muted">{m.detailQuotaHint}</p>
              {failure && (
                <p role="status" className="text-sm wrap-anywhere text-error-text">
                  {fill(m.quotaRefreshFailed, { email: account.id, message: failure })}
                </p>
              )}
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                {groups.map((group) => (
                  <div key={group} className="flex flex-col gap-3 rounded-md bg-muted p-4">
                    <span className="text-sm font-medium">{nameOf(q, group)}</span>
                    <QuotaCell account={account} group={group} now={now} />
                  </div>
                ))}
              </div>
              {account.cooldown && (
                <ul className="flex flex-col gap-1 text-sm text-warning-text">
                  {Object.entries(account.cooldown).map(([group, until]) => (
                    <li key={group} className="flex items-center justify-between gap-3">
                      {fill(m.detailCooldownUntil, {
                        group: nameOf(q, group),
                        time: formatFullDateTime(new Date(until), locale),
                      })}
                      <Button
                        size="sm"
                        loading={clearCooldown.isPending && clearCooldown.variables.scope === group}
                        onClick={() => {
                          clearCooldown.mutate({ account, scope: group })
                        }}
                      >
                        {m.cooldownClear}
                      </Button>
                    </li>
                  ))}
                </ul>
              )}
            </Block>
            <ModelsBlock account={account} />
            <FamilyBlock account={account} onOpen={onOpen} />
            <Block title={m.priority}>
              <p className="text-sm text-foreground-muted">{m.priorityHint}</p>
              <PriorityField key={account.id + ':' + String(account.priority)} account={account} />
            </Block>
            <Block title={m.label}>
              <p className="text-sm text-foreground-muted">{m.labelHint}</p>
              <LabelField key={account.id + ':' + (account.label ?? '')} account={account} />
            </Block>
            <Block title={m.credits}>
              <p className="text-sm text-foreground-muted">{m.creditsHint}</p>
              <CreditsField account={account} />
              {creditsShort(account) && account.credit_minimum !== undefined && (
                <p role="status" className="text-sm text-warning-text">
                  {fill(m.creditsLow, { minimum: formatNumber(account.credit_minimum, locale) })}
                </p>
              )}
            </Block>
            <Block
              title={m.detailUsage}
              aside={
                usage?.last_at
                  ? m.lastUsed + ' ' + formatRelative(new Date(usage.last_at), now, locale)
                  : m.neverUsed
              }
            >
              <dl className="grid grid-cols-3 gap-px overflow-hidden rounded-md border border-border bg-border">
                {[
                  [m.detailRequests, formatNumber(usage?.requests ?? 0, locale)],
                  [m.detailTokens, formatCompact(usage?.total_tokens ?? 0, locale)],
                  [m.detailCost, formatCost(usage?.cost_usd ?? 0, locale)],
                ].map(([label, value]) => (
                  <div key={label} className="flex flex-col gap-1 bg-surface px-4 py-3">
                    <dt className="text-xs text-foreground-muted">{label}</dt>
                    <dd className="text-md font-semibold">{value}</dd>
                  </div>
                ))}
              </dl>
            </Block>
            <Block title={m.detailProfile}>
              <dl>
                <Row label={m.detailTier}>
                  <span className="font-mono text-xs">{account.tier || '—'}</span>
                </Row>
                {account.credit_balance !== undefined && account.credit_minimum !== undefined && (
                  <Row label={m.detailCredits}>
                    <span className={creditsShort(account) ? 'text-warning-text' : undefined}>
                      {formatNumber(account.credit_balance, locale)}
                    </span>
                    <span className="ml-2 text-xs text-foreground-muted">
                      {fill(m.creditsMinimum, {
                        minimum: formatNumber(account.credit_minimum, locale),
                      })}
                    </span>
                  </Row>
                )}
                <Row label={m.detailProject}>
                  <span className="font-mono text-xs">{account.project || '—'}</span>
                </Row>
                <Row label={m.detailProxy}>
                  {account.proxy ? (
                    <span className="font-mono text-xs">{account.proxy}</span>
                  ) : (
                    m.detailProxyGlobal
                  )}
                </Row>
                <Row label={m.detailAdded}>
                  {formatFullDateTime(new Date(account.added_at), locale)}
                </Row>
                {account.token_expiry && (
                  <Row label={m.detailToken}>
                    {formatFullDateTime(new Date(account.token_expiry), locale)}
                  </Row>
                )}
              </dl>
            </Block>
            <Block title={m.device}>
              <p className="text-sm text-foreground-muted">{m.deviceHint}</p>
              <dl>
                {deviceKeys.map((key) => {
                  const value = account.device[key] ?? ''
                  return (
                    <Row key={key} label={deviceLabels[key]}>
                      <span className="font-mono text-xs">{value === '' ? '—' : value}</span>
                    </Row>
                  )
                })}
              </dl>
            </Block>
          </div>
        </DialogContent>
      )}
    </Dialog>
  )
}
