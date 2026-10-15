import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  ArrowRight,
  ChevronDown,
  FileText,
  Gauge,
  Globe,
  Info,
  type LucideIcon,
  MessageSquareText,
  Server,
  ShieldCheck,
} from 'lucide-react'
import { type ReactNode, useEffect, useState } from 'react'
import { Controller, type FieldErrors, useForm, useWatch } from 'react-hook-form'
import { Link, NavLink, useNavigate, useParams } from 'react-router'

import { appMessages } from '@/app/messages'
import { Tag } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { Card } from '@/components/ui/card'
import { FormRow } from '@/components/ui/form-row'
import { Indicator } from '@/components/ui/indicator'
import { Input } from '@/components/ui/input'
import { NumberInput } from '@/components/ui/number-input'
import { PageHeader } from '@/components/ui/page'
import { RadioGroup } from '@/components/ui/radio-group'
import { Select } from '@/components/ui/select'
import { ErrorState, Loading } from '@/components/ui/states'
import { Switch } from '@/components/ui/switch'
import { toast } from '@/components/ui/toast'
import { Tooltip } from '@/components/ui/tooltip'
import { useModels } from '@/features/models/queries'
import { kindOf } from '@/features/models/types'
import { AboutSection } from '@/features/settings/AboutSection'
import { settingsMessages } from '@/features/settings/messages'
import {
  PromptPreviewDialog,
  PromptSectionsField,
  PromptWrapField,
} from '@/features/settings/PromptSection'
import { settingsKey, splitList, useSettings } from '@/features/settings/queries'
import { type SettingsForm, settingsSchema, toForm, toValues } from '@/features/settings/schema'
import type { Settings } from '@/features/settings/types'
import { api } from '@/lib/api'
import { isDesktop } from '@/lib/desktop'
import { fill, useMessages } from '@/lib/i18n'

// sections 是设置分区，顺序即左侧目录顺序
const sections = [
  'service',
  'network',
  'prompt',
  'scheduling',
  'logging',
  'remote',
  'about',
] as const
type Section = (typeof sections)[number]

// sectionIcons 是各分区的图标
const sectionIcons: Record<Section, LucideIcon> = {
  service: Server,
  network: Globe,
  prompt: MessageSquareText,
  scheduling: Gauge,
  logging: FileText,
  remote: ShieldCheck,
  about: Info,
}

// fieldSection 是每个配置项所在的分区，校验失败时跳到第一个出错的分区
const fieldSection: Record<keyof SettingsForm, Section> = {
  LISTEN: 'service',
  AUTH_DIR: 'service',
  RUNTIME_DIR: 'service',
  PROXY: 'network',
  TRUSTED_PROXIES: 'network',
  SYSTEM_PROMPT: 'prompt',
  SYSTEM_PROMPT_SECTIONS: 'prompt',
  SYSTEM_PROMPT_WRAP: 'prompt',
  ACCOUNT_CONCURRENCY: 'scheduling',
  ROUTING_STRATEGY: 'scheduling',
  PREFER_LOWER_TIER_MODELS: 'scheduling',
  REQUEST_TIMEOUT: 'scheduling',
  QUOTA_REFRESH_INTERVAL: 'scheduling',
  LOG_LEVEL: 'logging',
  LOG_FORMAT: 'logging',
  LOG_FILE: 'logging',
  LOG_MAX_TOTAL_MB: 'logging',
  REQUEST_LOG_BODIES: 'logging',
  ADMIN_AUTH_ENABLED: 'remote',
  ADMIN_USERNAME: 'remote',
  ADMIN_PASSWORD: 'remote',
}

// restartKeys 是 internal/config 的 RestartKeys，重启服务后才生效
const restartKeys = new Set(['LISTEN', 'AUTH_DIR', 'RUNTIME_DIR'])

// LinkRow 是在其他页面管理的配置项：当前状态与前往的链接
function LinkRow({
  label,
  hint,
  status,
  to,
  action,
}: {
  label: string
  hint: string
  status: string
  to: string
  action: string
}) {
  return (
    <div className="flex flex-col gap-3 border-t border-border py-4 first:border-t-0 md:flex-row md:items-start md:justify-between md:gap-8">
      <div className="flex min-w-0 flex-col gap-1 md:max-w-prose">
        <span className="text-base font-medium">{label}</span>
        <p className="text-sm text-foreground-muted">{hint}</p>
      </div>
      <div className="flex w-full shrink-0 items-center justify-between gap-3 md:w-field">
        <span className="text-sm text-foreground-muted">{status}</span>
        <Button size="sm" asChild>
          <Link to={to}>
            {action}
            <ArrowRight size={16} aria-hidden />
          </Link>
        </Button>
      </div>
    </div>
  )
}

// LowerTierField 勾选优先用低套餐账户的模型，列出就绪账户目录中的对话模型与已选但当前不在目录中的模型
function LowerTierField({
  value,
  onChange,
}: {
  value: string[]
  onChange: (value: string[]) => void
}) {
  const m = useMessages(settingsMessages)
  const app = useMessages(appMessages)
  const models = useModels()
  if (models.isPending) return <Loading label={app.loading} />
  if (models.isError) {
    return (
      <ErrorState
        title={m.loadFailed}
        error={models.error}
        retryLabel={app.retry}
        onRetry={() => void models.refetch()}
      />
    )
  }
  const chat = models.data.models.filter((model) => kindOf(model) === 'chat')
  const ids = [
    ...chat.map((model) => model.id),
    ...value.filter((id) => !chat.some((model) => model.id === id)),
  ]
  if (ids.length === 0) return <p className="text-sm text-foreground-muted">{m.lowerTierEmpty}</p>
  return (
    <ul
      id="PREFER_LOWER_TIER_MODELS"
      aria-describedby="PREFER_LOWER_TIER_MODELS-hint"
      className="max-h-80 flex flex-col divide-y divide-border overflow-y-auto rounded-md border border-border"
    >
      {ids.map((id) => (
        <li key={id}>
          <label
            htmlFor={'lower-tier-' + id}
            className="flex cursor-pointer items-center gap-3 px-3 py-2"
          >
            <Switch
              id={'lower-tier-' + id}
              checked={value.includes(id)}
              onCheckedChange={(checked) => {
                onChange(ids.filter((item) => (item === id ? checked : value.includes(item))))
              }}
            />
            <span className="flex min-w-0 flex-col">
              <span className="truncate text-sm">
                {chat.find((model) => model.id === id)?.name ?? id}
              </span>
              <span className="truncate font-mono text-xs text-foreground-muted">{id}</span>
            </span>
          </label>
        </li>
      ))}
    </ul>
  )
}

// SectionNav 是设置卡片左侧的分区目录，含错误的分区带红点
function SectionNav({ section, invalid }: { section: Section; invalid: Set<Section> }) {
  const m = useMessages(settingsMessages)
  return (
    <nav
      aria-label={m.sections}
      className="shrink-0 border-border p-3 max-md:border-b md:w-[200px] md:border-r"
    >
      <div className="relative overflow-x-auto">
        <Indicator active="[aria-current=page]" className="rounded-md bg-muted" />
        <ul className="flex gap-1 md:flex-col">
          {sections.map((item) => {
            const Icon = sectionIcons[item]
            return (
              <li key={item}>
                <NavLink
                  to={'/settings/' + item}
                  replace
                  aria-current={item === section ? 'page' : undefined}
                  className="relative flex h-control items-center gap-2 rounded-md px-3 text-sm whitespace-nowrap text-foreground-muted transition-colors duration-200 ease-out hover:text-foreground not-aria-[current=page]:hover:bg-muted aria-[current=page]:text-primary pointer-coarse:h-touch"
                >
                  <Icon size={16} aria-hidden className="shrink-0" />
                  <span className="flex-1">{m[item]}</span>
                  <span
                    aria-hidden
                    className={
                      'size-2 rounded-full bg-error ' + (invalid.has(item) ? '' : 'invisible')
                    }
                  />
                </NavLink>
              </li>
            )
          })}
        </ul>
      </div>
    </nav>
  )
}

// SectionTitle 是右侧当前分区的标题与说明
function SectionTitle({ section }: { section: Section }) {
  const m = useMessages(settingsMessages)
  const descriptions: Record<Section, string> = {
    service: m.serviceDescription,
    network: m.networkDescription,
    prompt: m.promptDescription,
    scheduling: m.schedulingDescription,
    logging: m.loggingDescription,
    remote: m.remoteDescription,
    about: m.aboutDescription,
  }
  return (
    <div className="mb-4 border-b border-border pb-4">
      <h2 id="settings-section" className="text-xl font-semibold">
        {m[section]}
      </h2>
      <p className="mt-1 text-sm text-foreground-muted">{descriptions[section]}</p>
    </div>
  )
}

// SettingsEditor 是设置表单：右侧当前分区的设置行，有修改时底部浮出保存栏
function SettingsEditor({
  settings,
  section,
  onInvalid,
}: {
  settings: Settings
  section: Section
  onInvalid: (sections: Set<Section>) => void
}) {
  const m = useMessages(settingsMessages)
  const client = useQueryClient()
  const navigate = useNavigate()
  const form = useForm<SettingsForm>({
    resolver: zodResolver(settingsSchema(m, settings.admin_password_set)),
    defaultValues: toForm(settings),
    mode: 'onTouched',
  })
  const errors = form.formState.errors
  const dirty = Object.keys(form.formState.dirtyFields).length
  const values = useWatch({ control: form.control })
  const [tier, promptSections, wrap] = useWatch({
    control: form.control,
    name: ['SYSTEM_PROMPT', 'SYSTEM_PROMPT_SECTIONS', 'SYSTEM_PROMPT_WRAP'],
  })
  const [remoteShown, setRemoteShown] = useState(false)
  const save = useMutation({
    mutationFn: (next: SettingsForm) =>
      api<Settings>('/api/settings', { method: 'PATCH', json: { values: toValues(next) } }),
    onSuccess: (result) => {
      client.setQueryData(settingsKey, result)
      form.reset(toForm(result))
      toast({ tone: 'success', title: m.saved })
    },
    onError: (error) => {
      form.setError('root', { message: fill(m.saveFailed, { message: error.message }) })
    },
  })
  const invalidSections = new Set(
    Object.keys(errors)
      .filter((key) => key in fieldSection)
      .map((key) => fieldSection[key as keyof SettingsForm]),
  )
  const invalidKey = [...invalidSections].sort().join(',')
  useEffect(() => {
    onInvalid(new Set(invalidKey ? (invalidKey.split(',') as Section[]) : []))
  }, [invalidKey, onInvalid])
  const handleInvalid = (invalid: FieldErrors<SettingsForm>) => {
    const first = sections.find((item) =>
      Object.keys(invalid).some((key) => fieldSection[key as keyof SettingsForm] === item),
    )
    if (first && first !== section) void navigate('/settings/' + first)
  }
  const submit = form.handleSubmit((next) => {
    save.mutate(next)
  }, handleInvalid)

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') {
        event.preventDefault()
        void submit()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('keydown', onKey)
    }
  }, [submit])

  const restartTag = (key: string) => (restartKeys.has(key) ? <Tag>{m.restart}</Tag> : undefined)
  const describe = (key: keyof SettingsForm) => ({
    id: key,
    'aria-invalid': errors[key] ? true : undefined,
    'aria-describedby': errors[key] ? key + '-error' : key + '-hint',
  })
  const text = (
    key:
      | 'LISTEN'
      | 'AUTH_DIR'
      | 'RUNTIME_DIR'
      | 'PROXY'
      | 'ADMIN_USERNAME'
      | 'TRUSTED_PROXIES'
      | 'REQUEST_TIMEOUT'
      | 'QUOTA_REFRESH_INTERVAL',
    label: string,
    hint: string,
    extra?: { placeholder?: string; disabledReason?: string | false; autoComplete?: string },
  ) => (
    <FormRow id={key} label={label} hint={hint} tag={restartTag(key)} error={errors[key]?.message}>
      <Tooltip disabledReason={extra?.disabledReason}>
        <Input
          {...describe(key)}
          {...form.register(key)}
          spellCheck={false}
          autoComplete={extra?.autoComplete ?? 'off'}
          placeholder={extra?.placeholder}
          disabled={Boolean(extra?.disabledReason)}
          className="font-mono"
        />
      </Tooltip>
    </FormRow>
  )
  const toggle = (
    key: 'LOG_FILE' | 'REQUEST_LOG_BODIES' | 'ADMIN_AUTH_ENABLED',
    label: string,
    hint: string,
  ) => (
    <FormRow id={key} label={label} hint={hint}>
      <Controller
        control={form.control}
        name={key}
        render={({ field }) => (
          <Switch
            id={key}
            aria-describedby={key + '-hint'}
            checked={field.value}
            onCheckedChange={field.onChange}
          />
        )}
      />
    </FormRow>
  )
  const number = (
    key: 'ACCOUNT_CONCURRENCY' | 'LOG_MAX_TOTAL_MB',
    label: string,
    hint: string,
    min: number,
    max: number,
    disabledReason?: string | false,
  ) => (
    <FormRow id={key} label={label} hint={hint} error={errors[key]?.message}>
      <Controller
        control={form.control}
        name={key}
        render={({ field }) => (
          <Tooltip disabledReason={disabledReason}>
            <NumberInput
              {...describe(key)}
              value={field.value}
              onChange={field.onChange}
              onBlur={field.onBlur}
              min={min}
              max={max}
              disabled={Boolean(disabledReason)}
              decreaseLabel={m.decrease}
              increaseLabel={m.increase}
              minReason={fill(m.minReason, { min })}
              maxReason={fill(m.maxReason, { max })}
            />
          </Tooltip>
        )}
      />
    </FormRow>
  )
  const keyCount = splitList(settings.values.API_KEYS).length
  const mappingCount = splitList(settings.values.MODEL_MAPPING).length

  const body: Record<Section, ReactNode> = {
    about: null,
    service: (
      <>
        {text('LISTEN', m.listen, m.listenHint)}
        {text('AUTH_DIR', m.authDir, m.authDirHint)}
        {text('RUNTIME_DIR', m.runtimeDir, m.runtimeDirHint)}
        <LinkRow
          label={m.apiKeys}
          hint={m.apiKeysHint}
          status={keyCount ? fill(m.apiKeysCount, { count: keyCount }) : m.apiKeysNone}
          to="/connect"
          action={m.apiKeysManage}
        />
      </>
    ),
    network: (
      <>
        {text('PROXY', m.proxy, m.proxyHint, { placeholder: 'socks5://127.0.0.1:1080' })}
        {text('TRUSTED_PROXIES', m.trustedProxies, m.trustedProxiesHint, {
          placeholder: '10.0.0.0/8,192.168.1.10',
        })}
      </>
    ),
    prompt: (
      <>
        <div className="flex flex-col gap-3 py-4">
          <span id="SYSTEM_PROMPT-label" className="text-base font-medium">
            {m.tier}
          </span>
          <Controller
            control={form.control}
            name="SYSTEM_PROMPT"
            render={({ field }) => (
              <RadioGroup
                label={m.tier}
                value={field.value}
                onValueChange={field.onChange}
                options={[
                  { value: 'full', label: m.promptFull, description: m.promptFullHint },
                  { value: 'medium', label: m.promptMedium, description: m.promptMediumHint },
                  {
                    value: 'aggressive',
                    label: m.promptAggressive,
                    description: m.promptAggressiveHint,
                  },
                  { value: 'custom', label: m.promptCustom, description: m.promptCustomHint },
                ]}
              />
            )}
          />
          <PromptPreviewDialog tier={tier} sections={promptSections} wrap={wrap} />
        </div>
        {tier === 'custom' && (
          <>
            <div className="border-t border-border py-4">
              <Controller
                control={form.control}
                name="SYSTEM_PROMPT_SECTIONS"
                render={({ field }) => (
                  <PromptSectionsField value={field.value} onChange={field.onChange} />
                )}
              />
            </div>
            <div className="border-t border-border py-4">
              <Controller
                control={form.control}
                name="SYSTEM_PROMPT_WRAP"
                render={({ field }) => (
                  <PromptWrapField value={field.value} onChange={field.onChange} />
                )}
              />
            </div>
          </>
        )}
      </>
    ),
    scheduling: (
      <>
        <FormRow id="ROUTING_STRATEGY" label={m.routing} hint={m.routingHint}>
          <Controller
            control={form.control}
            name="ROUTING_STRATEGY"
            render={({ field }) => (
              <RadioGroup
                label={m.routing}
                value={field.value}
                onValueChange={field.onChange}
                options={[
                  { value: 'round-robin', label: m.roundRobin, description: m.roundRobinHint },
                  { value: 'fill-first', label: m.fillFirst, description: m.fillFirstHint },
                ]}
              />
            )}
          />
        </FormRow>
        <FormRow id="PREFER_LOWER_TIER_MODELS" label={m.lowerTier} hint={m.lowerTierHint}>
          <Controller
            control={form.control}
            name="PREFER_LOWER_TIER_MODELS"
            render={({ field }) => <LowerTierField value={field.value} onChange={field.onChange} />}
          />
        </FormRow>
        {number('ACCOUNT_CONCURRENCY', m.concurrency, m.concurrencyHint, 1, 64)}
        {text('REQUEST_TIMEOUT', m.timeout, m.timeoutHint, { placeholder: '5m' })}
        {text('QUOTA_REFRESH_INTERVAL', m.quotaRefresh, m.quotaRefreshHint, { placeholder: '15m' })}
        <LinkRow
          label={m.mapping}
          hint={m.mappingHint}
          status={mappingCount ? fill(m.mappingCount, { count: mappingCount }) : m.mappingNone}
          to="/models"
          action={m.mappingManage}
        />
      </>
    ),
    logging: (
      <>
        <FormRow id="LOG_LEVEL" label={m.logLevel} hint={m.logLevelHint}>
          <Controller
            control={form.control}
            name="LOG_LEVEL"
            render={({ field }) => (
              <Select
                id="LOG_LEVEL"
                label={m.logLevel}
                value={field.value}
                onValueChange={field.onChange}
                options={(['debug', 'info', 'warn', 'error'] as const).map((value) => ({
                  value,
                  label: value.toUpperCase(),
                }))}
              />
            )}
          />
        </FormRow>
        <FormRow id="LOG_FORMAT" label={m.logFormat} hint={m.logFormatHint}>
          <Controller
            control={form.control}
            name="LOG_FORMAT"
            render={({ field }) => (
              <Select
                id="LOG_FORMAT"
                label={m.logFormat}
                value={field.value}
                onValueChange={field.onChange}
                options={[
                  { value: 'json', label: 'JSON' },
                  { value: 'text', label: m.logFormatText },
                ]}
              />
            )}
          />
        </FormRow>
        {toggle('LOG_FILE', m.logFile, m.logFileHint)}
        {number(
          'LOG_MAX_TOTAL_MB',
          m.logTotal,
          m.logTotalHint,
          20,
          100_000,
          !values.LOG_FILE && m.logTotalDisabled,
        )}
        {toggle('REQUEST_LOG_BODIES', m.bodies, m.bodiesHint)}
      </>
    ),
    remote:
      isDesktop && !remoteShown && !errors.ADMIN_USERNAME && !errors.ADMIN_PASSWORD ? (
        <div className="py-4">
          <Button
            size="sm"
            aria-expanded={false}
            onClick={() => {
              setRemoteShown(true)
            }}
          >
            <ChevronDown size={16} aria-hidden />
            {m.remoteShow}
          </Button>
        </div>
      ) : (
        <>
          {toggle('ADMIN_AUTH_ENABLED', m.adminAuth, m.adminAuthHint)}
          {text('ADMIN_USERNAME', m.adminUser, m.adminUserHint, {
            autoComplete: 'username',
            disabledReason: !values.ADMIN_AUTH_ENABLED && m.adminDisabled,
          })}
          <FormRow
            id="ADMIN_PASSWORD"
            label={m.adminPassword}
            hint={m.adminPasswordHint}
            error={errors.ADMIN_PASSWORD?.message}
          >
            <Tooltip disabledReason={!values.ADMIN_AUTH_ENABLED && m.adminDisabled}>
              <Input
                {...describe('ADMIN_PASSWORD')}
                {...form.register('ADMIN_PASSWORD')}
                type="password"
                autoComplete="new-password"
                disabled={!values.ADMIN_AUTH_ENABLED}
                placeholder={settings.admin_password_set ? m.adminPasswordSet : ''}
              />
            </Tooltip>
          </FormRow>
        </>
      ),
  }

  return (
    <form noValidate onSubmit={(event) => void submit(event)} aria-labelledby="settings-section">
      {body[section]}
      {(dirty > 0 || errors.root) && (
        <div className="fixed inset-x-4 bottom-6 z-sticky mx-auto flex w-fit max-w-dialog-wide flex-wrap items-center justify-end gap-3 rounded-lg border border-border bg-surface py-3 pr-3 pl-4 shadow-popover">
          {errors.root ? (
            <p role="alert" className="mr-auto text-sm break-words text-error-text">
              {errors.root.message}
            </p>
          ) : (
            <p role="status" className="mr-auto text-sm text-foreground-muted">
              {fill(m.unsaved, { count: dirty })}
            </p>
          )}
          <Button
            size="sm"
            onClick={() => {
              form.reset()
            }}
          >
            {m.discard}
          </Button>
          <Tooltip label={m.save} shortcut="Ctrl S">
            <Button size="sm" type="submit" variant="primary" loading={save.isPending}>
              {m.save}
            </Button>
          </Tooltip>
        </div>
      )}
    </form>
  )
}

// SettingsPage 是设置页：左侧分区目录，右侧当前分区，分区来自 URL
export function SettingsPage() {
  const m = useMessages(settingsMessages)
  const app = useMessages(appMessages)
  const params = useParams()
  const section = sections.find((item) => item === params.section) ?? 'service'
  const settings = useSettings()
  const [invalid, setInvalid] = useState<Set<Section>>(new Set())
  return (
    <>
      <PageHeader title={m.title} description={m.description} />
      {settings.data && settings.data.restart_required.length > 0 && (
        <Callout tone="warning" role="status">
          {fill(m.restartNotice, { keys: settings.data.restart_required.join('、') })}
        </Callout>
      )}
      <Card className="md:flex-row">
        <SectionNav section={section} invalid={invalid} />
        <div className="min-w-0 flex-1 px-8 pt-6 pb-8 max-sm:px-4 max-sm:pt-4">
          <SectionTitle section={section} />
          {section === 'about' && <AboutSection />}
          {settings.isPending ? (
            section !== 'about' && <Loading label={app.loading} />
          ) : settings.isError ? (
            section !== 'about' && (
              <ErrorState
                title={m.loadFailed}
                error={settings.error}
                retryLabel={app.retry}
                onRetry={() => void settings.refetch()}
              />
            )
          ) : (
            <SettingsEditor settings={settings.data} section={section} onInvalid={setInvalid} />
          )}
        </div>
      </Card>
    </>
  )
}
