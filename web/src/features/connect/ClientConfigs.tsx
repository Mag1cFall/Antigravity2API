import { ExternalLink, Import } from 'lucide-react'
import { Tabs as Primitive } from 'radix-ui'
import { type ReactNode, useState } from 'react'
import { useSearchParams } from 'react-router'

import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { CodeBlock } from '@/components/ui/code-block'
import { CopyButton } from '@/components/ui/copy-button'
import { Indicator } from '@/components/ui/indicator'
import { Segmented } from '@/components/ui/segmented'
import { Select } from '@/components/ui/select'
import { ErrorState, Loading } from '@/components/ui/states'
import { maskKey, useApiKeys } from '@/features/connect/ApiKeys'
import {
  claudeCode,
  type Client,
  type ClientInput,
  clients,
  type Protocol,
  protocolNames,
  type Shell,
} from '@/features/connect/clients'
import { serviceOrigin } from '@/features/connect/endpoints'
import { markProgress } from '@/features/connect/progress'
import { useModels } from '@/features/models/queries'
import { kindOf, type Model } from '@/features/models/types'
import { fill, useMessages } from '@/lib/i18n'
import { changeParams } from '@/lib/params'

// groups 是客户端分组与组名的文案键
const groups = [
  { id: 'agent', label: 'groupAgent' },
  { id: 'chat', label: 'groupChat' },
  { id: 'test', label: 'groupTest' },
] as const

// ClientIcon 是客户端图标，单色图标跟随文字颜色
function ClientIcon({ client, className }: { client: Client; className: string }) {
  return (
    <span
      aria-hidden
      className={'flex shrink-0 items-center justify-center [&>svg]:size-full ' + className}
      dangerouslySetInnerHTML={{ __html: client.icon }}
    />
  )
}

// pick 返回满足条件的第一个模型 ID，没有时取第一个模型
function pick(models: Model[], test: (model: Model) => boolean) {
  return (models.find(test) ?? models[0])?.id ?? ''
}

// Labeled 是控件上方带说明文字的一列
function Labeled({ id, label, children }: { id?: string; label: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1 max-sm:w-full">
      {id ? (
        <label htmlFor={id} className="text-xs text-foreground-muted">
          {label}
        </label>
      ) : (
        <span className="text-xs text-foreground-muted">{label}</span>
      )}
      {children}
    </div>
  )
}

// ClientConfigs 按客户端给出可直接复制的配置与导入链接，模型取自实时目录，key 取自已保存的 API key；读到 API key 与模型目录之前不生成配置，复制任一配置即完成引导的第三步
export function ClientConfigs() {
  const m = useMessages('connect')
  const app = useMessages('app')
  const [params, setParams] = useSearchParams()
  const client = clients.find((item) => item.id === params.get('client')) ?? claudeCode
  const catalog = useModels()
  const { settings, keys } = useApiKeys()
  const models = (catalog.data?.models ?? []).filter((model) => kindOf(model) === 'chat')
  const [choice, setChoice] = useState<{
    model?: string
    opus?: string
    sonnet?: string
    haiku?: string
    key?: string
  }>({})
  const [protocols, setProtocols] = useState<Record<string, Protocol>>({})
  const [shell, setShell] = useState<Shell>(
    navigator.userAgent.includes('Windows') ? 'windows' : 'unix',
  )
  const flashDefault = pick(models, (model) => model.id.includes('flash'))
  const modelID =
    choice.model ??
    (client.tiers ? pick(models, (model) => /anthropic/i.test(model.provider ?? '')) : flashDefault)
  const key = choice.key ?? keys[0] ?? 'antigravity2api'
  const protocol = protocols[client.id] ?? client.protocols[0]
  const input: ClientInput = {
    origin: serviceOrigin(),
    key,
    model: models.find((model) => model.id === modelID),
    modelID: modelID || m.modelPlaceholder,
    tiers: {
      opus: (choice.opus ?? modelID) || m.modelPlaceholder,
      sonnet: (choice.sonnet ?? modelID) || m.modelPlaceholder,
      haiku: (choice.haiku ?? flashDefault) || m.modelPlaceholder,
    },
    models,
    shell,
    protocol,
    m,
  }
  const done = () => {
    markProgress({ client: true })
  }
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
  const modelSelect = (id: string, label: string, value: string, field: keyof typeof choice) => (
    <Labeled key={id} id={id} label={label}>
      <Select
        id={id}
        label={label}
        value={value}
        onValueChange={(next) => {
          setChoice((current) => ({ ...current, [field]: next }))
        }}
        options={models.map((model) => ({ value: model.id, label: model.id }))}
        className="w-full font-mono sm:w-[256px]"
      />
    </Labeled>
  )
  const links = client.links?.(input) ?? []
  const steps = client.steps(input)
  return (
    <Primitive.Root
      value={client.id}
      onValueChange={(value) => {
        changeParams(setParams, { client: value })
      }}
    >
      <Primitive.List aria-label={m.clientLabel} className="relative flex flex-col gap-1">
        <Indicator active="[data-state=active]" className="rounded-md bg-muted" />
        {groups.map((group) => (
          <div key={group.id} className="grid gap-x-3 gap-y-1 sm:grid-cols-[96px_minmax(0,1fr)]">
            <span className="text-xs leading-[32px] text-foreground-muted max-sm:leading-normal">
              {m[group.label]}
            </span>
            <div className="flex flex-wrap gap-1">
              {clients
                .filter((item) => item.group === group.id)
                .map((item) => (
                  <Primitive.Trigger
                    key={item.id}
                    value={item.id}
                    className="relative inline-flex h-control-sm shrink-0 items-center gap-2 rounded-md px-3 text-sm whitespace-nowrap text-foreground-muted transition-colors duration-200 ease-out hover:text-foreground data-[state=active]:text-foreground data-[state=inactive]:hover:bg-hover pointer-coarse:h-touch"
                  >
                    <ClientIcon client={item} className="size-4" />
                    {item.name}
                  </Primitive.Trigger>
                ))}
            </div>
          </div>
        ))}
      </Primitive.List>
      <Primitive.Content
        value={client.id}
        className="mt-4 flex flex-col border-t border-border data-[state=active]:animate-fade-in"
      >
        <div className="flex flex-wrap items-start gap-x-4 gap-y-2 border-b border-border py-4">
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <ClientIcon client={client} className="size-icon" />
              <h3 className="text-base font-semibold">{client.name}</h3>
              <span className="text-xs text-foreground-muted">
                {client.protocols.map((item) => protocolNames[item]).join(' · ')}
              </span>
            </div>
            <p className="mt-1 text-sm text-foreground-muted">{m[client.summary]}</p>
          </div>
          {client.homepage && (
            <Button variant="link" asChild>
              <a href={client.homepage} target="_blank" rel="noopener noreferrer">
                {m.homepage}
                <ExternalLink size={14} aria-hidden />
              </a>
            </Button>
          )}
        </div>
        <div className="flex flex-wrap items-end gap-4 border-b border-border py-4 empty:hidden">
          {!client.noModel &&
            models.length > 0 &&
            modelSelect('client-model-' + client.id, m.model, modelID, 'model')}
          {client.tiers &&
            models.length > 0 && [
              modelSelect('client-tier-opus', m.tierOpus, input.tiers.opus, 'opus'),
              modelSelect('client-tier-sonnet', m.tierSonnet, input.tiers.sonnet, 'sonnet'),
              modelSelect('client-tier-haiku', m.tierHaiku, input.tiers.haiku, 'haiku'),
            ]}
          {client.protocols.length > 1 && !client.importsAll && (
            <Labeled id="client-protocol" label={m.protocol}>
              <Select
                id="client-protocol"
                label={m.protocol}
                value={protocol}
                onValueChange={(next) => {
                  setProtocols((current) => ({ ...current, [client.id]: next }))
                }}
                options={client.protocols.map((item) => ({
                  value: item,
                  label: protocolNames[item],
                }))}
                className="w-full sm:w-[256px]"
              />
            </Labeled>
          )}
          {keys.length > 1 && (
            <Labeled id="client-key" label={m.key}>
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
            </Labeled>
          )}
          {client.shell && (
            <Labeled label={m.shell}>
              <Segmented
                label={m.shell}
                value={shell}
                onValueChange={setShell}
                options={[
                  { value: 'unix', label: m.shellUnix },
                  { value: 'windows', label: m.shellWindows },
                ]}
              />
            </Labeled>
          )}
        </div>
        <div className="flex flex-col gap-4 pt-4">
          {catalog.isError ? (
            <Callout tone="error" role="alert">
              <p>{fill(m.modelsFailed, { message: catalog.error.message })}</p>
            </Callout>
          ) : (
            models.length === 0 &&
            !client.noModel && (
              <Callout tone="muted">
                <p>{m.noModels}</p>
              </Callout>
            )
          )}
          {keys.length === 0 && <p className="text-xs text-foreground-muted">{m.keyNone}</p>}
          {links.length > 0 && (
            <div className="flex flex-wrap items-center gap-3">
              {links.map((link) => (
                <Button key={link.href} variant="primary" asChild>
                  <a href={link.href} onClick={done}>
                    <Import size={16} aria-hidden />
                    {m[link.label]}
                  </a>
                </Button>
              ))}
              <span className="text-xs text-foreground-muted">{m.importHelp}</span>
            </div>
          )}
          <ol className="flex flex-col gap-6">
            {steps.map((step, index) => (
              <li key={step.text + String(index)} className="flex gap-3">
                <span className="w-4 shrink-0 text-right text-sm text-foreground-muted tabular-nums">
                  {index + 1}
                </span>
                <div className="flex min-w-0 flex-1 flex-col gap-2">
                  <p className="text-sm">{fill(m[step.text], { name: client.name })}</p>
                  {step.kind === 'code' ? (
                    step.blocks.map((block) => {
                      const title =
                        block.file ??
                        (client.shell
                          ? shell === 'windows'
                            ? 'PowerShell'
                            : 'bash / zsh'
                          : m.shell)
                      return (
                        <CodeBlock
                          key={title}
                          title={title}
                          code={block.code}
                          wrap={block.wrap ?? false}
                          copyLabel={m.copy + ' ' + title}
                          copiedLabel={m.copied}
                          onCopy={done}
                        />
                      )
                    })
                  ) : (
                    <dl className="overflow-hidden rounded-md border border-border">
                      {step.fields.map((field) => (
                        <div
                          key={field.label}
                          className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 border-t border-border py-2 pr-2 pl-4 first:border-t-0 sm:grid-cols-[220px_minmax(0,1fr)_auto]"
                        >
                          <dt className="text-sm text-foreground-muted max-sm:col-span-2">
                            {field.label}
                          </dt>
                          <dd
                            className={
                              field.choice
                                ? 'min-h-control-sm py-1 text-sm max-sm:col-span-2 sm:col-span-2'
                                : 'min-w-0 font-mono text-sm break-all'
                            }
                          >
                            {field.value}
                          </dd>
                          {!field.choice && (
                            <CopyButton
                              value={field.value}
                              label={m.copy + ' ' + field.label}
                              copiedLabel={m.copied}
                              onCopy={done}
                            />
                          )}
                        </div>
                      ))}
                    </dl>
                  )}
                </div>
              </li>
            ))}
          </ol>
        </div>
      </Primitive.Content>
    </Primitive.Root>
  )
}
