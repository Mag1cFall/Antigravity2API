import { useState } from 'react'
import { useSearchParams } from 'react-router'

import { appMessages } from '@/app/messages'
import { Callout } from '@/components/ui/callout'
import { CodeBlock } from '@/components/ui/code-block'
import { CopyButton } from '@/components/ui/copy-button'
import { Segmented } from '@/components/ui/segmented'
import { Select } from '@/components/ui/select'
import { ErrorState, Loading } from '@/components/ui/states'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { maskKey, useApiKeys } from '@/features/connect/ApiKeys'
import { serviceOrigin } from '@/features/connect/endpoints'
import { connectMessages } from '@/features/connect/messages'
import { markProgress } from '@/features/connect/progress'
import {
  claudeEnv,
  claudeSettings,
  codexConfig,
  codexEnv,
  curlAnthropic,
  curlGemini,
  curlOpenai,
  opencodeConfig,
  type Shell,
  type SnippetInput,
} from '@/features/connect/snippets'
import { useModels } from '@/features/models/queries'
import { kindOf, type Model } from '@/features/models/types'
import { fill, useMessages } from '@/lib/i18n'

// clients 是提供配置的客户端
const clients = ['claude-code', 'codex', 'opencode', 'cherry-studio', 'curl'] as const
type Client = (typeof clients)[number]

// clientNames 是客户端的显示名
const clientNames: Record<Client, string> = {
  'claude-code': 'Claude Code',
  codex: 'Codex',
  opencode: 'OpenCode',
  'cherry-studio': 'Cherry Studio',
  curl: 'curl',
}

// pick 返回满足条件的第一个模型 ID，没有时取第一个模型
function pick(models: Model[], test: (model: Model) => boolean) {
  return (models.find(test) ?? models[0])?.id ?? ''
}

// ModelSelect 是选择模型的下拉框
function ModelSelect({
  id,
  label,
  value,
  models,
  onChange,
}: {
  id: string
  label: string
  value: string
  models: Model[]
  onChange: (value: string) => void
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <label htmlFor={id} className="text-xs text-foreground-muted">
        {label}
      </label>
      <Select
        id={id}
        label={label}
        value={value}
        onValueChange={onChange}
        options={models.map((model) => ({ value: model.id, label: model.id }))}
        className="w-full font-mono sm:w-[256px]"
      />
    </div>
  )
}

// ClientConfigs 按客户端给出可直接复制的配置，模型取自实时目录，key 取自已保存的 API key；读到 API key 与模型目录之前不生成配置，复制任一配置即完成引导的第三步
export function ClientConfigs() {
  const m = useMessages(connectMessages)
  const app = useMessages(appMessages)
  const [params, setParams] = useSearchParams()
  const client = clients.find((item) => item === params.get('client')) ?? 'claude-code'
  const catalog = useModels()
  const { settings, keys } = useApiKeys()
  const models = (catalog.data?.models ?? []).filter((model) => kindOf(model) === 'chat')
  const [choice, setChoice] = useState<{ model?: string; small?: string; key?: string }>({})
  const [shell, setShell] = useState<Shell>(
    navigator.userAgent.includes('Windows') ? 'windows' : 'unix',
  )
  const claudeDefault = pick(models, (model) => /anthropic/i.test(model.provider ?? ''))
  const flashDefault = pick(models, (model) => model.id.includes('flash'))
  const fallback = m.modelPlaceholder
  const key = choice.key ?? keys[0] ?? 'antigravity2api'
  const input = (model: string): SnippetInput => ({
    origin: serviceOrigin(),
    key,
    model: model || fallback,
    smallModel: (choice.small ?? flashDefault) || fallback,
    models: models.map((item) => ({ id: item.id, name: item.name || item.id })),
  })
  const claudeModel = choice.model ?? claudeDefault
  const generalModel = choice.model ?? flashDefault
  const done = () => {
    markProgress({ client: true })
  }
  const block = (title: string, code: string) => (
    <CodeBlock
      title={title}
      code={code}
      copyLabel={m.copy + ' ' + title}
      copiedLabel={m.copied}
      onCopy={done}
    />
  )
  const step = (text: string) => <p className="text-sm text-foreground-muted">{text}</p>
  const modelSelect = (value: string) =>
    models.length > 0 && (
      <ModelSelect
        id={'client-model-' + client}
        label={m.model}
        value={value}
        models={models}
        onChange={(next) => {
          setChoice((current) => ({ ...current, model: next }))
        }}
      />
    )
  const shellSelect = (
    <div className="flex min-w-0 flex-col gap-1">
      <span className="text-xs text-foreground-muted">{m.shell}</span>
      <Segmented
        label={m.shell}
        value={shell}
        onValueChange={setShell}
        options={[
          { value: 'unix', label: m.shellUnix },
          { value: 'windows', label: m.shellWindows },
        ]}
      />
    </div>
  )
  const keySelect = keys.length > 1 && (
    <div className="flex min-w-0 flex-col gap-1">
      <label htmlFor="client-key" className="text-xs text-foreground-muted">
        {m.key}
      </label>
      <Select
        id="client-key"
        label={m.key}
        value={key}
        onValueChange={(next) => {
          setChoice((current) => ({ ...current, key: next }))
        }}
        options={keys.map((item) => ({ value: item, label: maskKey(item) }))}
        className="w-full font-mono sm:w-[192px]"
      />
    </div>
  )
  const notes = (
    <>
      {catalog.isError ? (
        <Callout tone="error" role="alert">
          <p>{fill(m.modelsFailed, { message: catalog.error.message })}</p>
        </Callout>
      ) : (
        models.length === 0 && (
          <Callout tone="muted">
            <p>{m.noModels}</p>
          </Callout>
        )
      )}
      {keys.length === 0 && <p className="text-xs text-foreground-muted">{m.keyNone}</p>}
    </>
  )
  if (settings.isPending || catalog.isPending) return <Loading label={app.loading} rows={3} />
  if (settings.isError) {
    return (
      <ErrorState
        title={m.configsFailed}
        error={settings.error}
        retryLabel={app.retry}
        onRetry={() => void settings.refetch()}
      />
    )
  }
  return (
    <Tabs
      value={client}
      onValueChange={(value) => {
        setParams(
          (current) => {
            const next = new URLSearchParams(current)
            next.set('client', value)
            return next
          },
          { replace: true },
        )
      }}
    >
      <TabsList aria-label={m.clientLabel}>
        {clients.map((item) => (
          <TabsTrigger key={item} value={item}>
            {clientNames[item]}
          </TabsTrigger>
        ))}
      </TabsList>
      <TabsContent value="claude-code" className="mt-4 flex flex-col gap-4">
        <div className="flex flex-wrap items-start gap-4">
          {modelSelect(claudeModel)}
          {models.length > 0 && (
            <ModelSelect
              id="client-small-model"
              label={m.smallModel}
              value={choice.small ?? flashDefault}
              models={models}
              onChange={(next) => {
                setChoice((current) => ({ ...current, small: next }))
              }}
            />
          )}
          {keySelect}
          {shellSelect}
        </div>
        {notes}
        {step(m.claudeEnv)}
        {block(
          shell === 'windows' ? 'PowerShell' : 'bash / zsh',
          claudeEnv(input(claudeModel), shell),
        )}
        {step(m.claudeSettings)}
        {block('~/.claude/settings.json', claudeSettings(input(claudeModel)))}
      </TabsContent>
      <TabsContent value="codex" className="mt-4 flex flex-col gap-4">
        <div className="flex flex-wrap items-start gap-4">
          {modelSelect(generalModel)}
          {keySelect}
          {shellSelect}
        </div>
        {notes}
        {step(m.codexConfig)}
        {block('~/.codex/config.toml', codexConfig(input(generalModel)))}
        {step(m.codexEnv)}
        {block(
          shell === 'windows' ? 'PowerShell' : 'bash / zsh',
          codexEnv(input(generalModel), shell),
        )}
      </TabsContent>
      <TabsContent value="opencode" className="mt-4 flex flex-col gap-4">
        <div className="flex flex-wrap items-start gap-4">
          {modelSelect(generalModel)}
          {keySelect}
        </div>
        {notes}
        {step(m.opencodeConfig)}
        {block('~/.config/opencode/opencode.json', opencodeConfig(input(generalModel)))}
      </TabsContent>
      <TabsContent value="cherry-studio" className="mt-4 flex flex-col gap-4">
        {keySelect && <div className="flex flex-wrap items-start gap-4">{keySelect}</div>}
        {notes}
        {step(m.cherrySteps)}
        <dl className="overflow-hidden rounded-md border border-border">
          {(
            [
              [m.cherryType, m.cherryTypeValue],
              [m.cherryHost, serviceOrigin()],
              [m.cherryKey, key],
              [m.cherryModels, models.map((model) => model.id).join(', ') || fallback],
            ] as const
          ).map(([label, value]) => (
            <div
              key={label}
              className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 border-t border-border py-2 pr-2 pl-4 first:border-t-0 sm:grid-cols-[180px_minmax(0,1fr)_auto]"
            >
              <dt className="text-sm text-foreground-muted max-sm:col-span-2">{label}</dt>
              <dd className="min-w-0 font-mono text-sm break-all">{value}</dd>
              <CopyButton
                value={value}
                label={m.copy + ' ' + label}
                copiedLabel={m.copied}
                onCopy={done}
              />
            </div>
          ))}
        </dl>
      </TabsContent>
      <TabsContent value="curl" className="mt-4 flex flex-col gap-4">
        <div className="flex flex-wrap items-start gap-4">
          {modelSelect(generalModel)}
          {keySelect}
        </div>
        {notes}
        {step(m.curlHint)}
        {block(m.curlOpenai, curlOpenai(input(generalModel)))}
        {block(m.curlAnthropic, curlAnthropic(input(generalModel)))}
        {block(m.curlGemini, curlGemini(input(generalModel)))}
      </TabsContent>
    </Tabs>
  )
}
