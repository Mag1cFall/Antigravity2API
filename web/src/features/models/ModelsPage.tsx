import { Boxes } from 'lucide-react'
import { Link, useSearchParams } from 'react-router'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { Card, CardToolbar } from '@/components/ui/card'
import { CopyButton } from '@/components/ui/copy-button'
import { PageHeader } from '@/components/ui/page'
import { SearchInput } from '@/components/ui/search-input'
import { Segmented } from '@/components/ui/segmented'
import { EmptyState, ErrorState, Loading } from '@/components/ui/states'
import { Table, Td, Th } from '@/components/ui/table'
import { Tooltip } from '@/components/ui/tooltip'
import { useAccounts } from '@/features/accounts/queries'
import { modelMessages } from '@/features/models/messages'
import { ModelMapping } from '@/features/models/ModelMapping'
import { useModels } from '@/features/models/queries'
import { kindOf, type Model } from '@/features/models/types'
import { quotaMessages } from '@/features/quota/messages'
import { compareBuckets, groups, nameOf } from '@/features/quota/quota'
import { useSettings } from '@/features/settings/queries'
import { parseMapping } from '@/features/settings/schema'
import { formatNumber, formatTokens } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// groupTone 是额度组标签的颜色
const groupTone: Record<string, string> = {
  gemini: 'bg-info-soft text-primary',
  '3p': 'bg-violet-soft text-violet',
}

// Limit 是右对齐的 token 上限，悬停显示精确值
function Limit({ value }: { value: number | undefined }) {
  const m = useMessages(modelMessages)
  const { locale } = useI18n()
  if (!value) return <span className="text-foreground-subtle">—</span>
  return (
    <Tooltip label={fill(m.tokens, { count: formatNumber(value, locale) })}>
      <span>{formatTokens(value)}</span>
    </Tooltip>
  )
}

// ModelRow 是模型表的一行
function ModelRow({
  model,
  alias,
  ready,
}: {
  model: Model
  alias: string | undefined
  ready: number
}) {
  const m = useMessages(modelMessages)
  const q = useMessages(quotaMessages)
  const kind = kindOf(model)
  const kindLabel = {
    chat: '',
    image: m.kindImage,
    transcription: m.kindTranscription,
    completion: m.kindCompletion,
  }[kind]
  const group = model.quota_group ?? ''
  return (
    <tr>
      <Td>
        <div className="flex min-w-0 items-center gap-2">
          <span className="truncate text-sm font-medium">{model.name || model.id}</span>
          {alias && (
            <span className="inline-flex h-icon shrink-0 items-center rounded-sm bg-warning-soft px-2 font-mono text-xs text-warning-text">
              {fill(m.alias, { target: alias })}
            </span>
          )}
          {kindLabel && (
            <span className="inline-flex h-icon shrink-0 items-center rounded-sm bg-muted px-2 text-xs text-foreground-muted">
              {kindLabel}
            </span>
          )}
        </div>
        <div className="flex min-w-0 items-center gap-1">
          <code className="truncate font-mono text-xs text-foreground-muted">{model.id}</code>
          <CopyButton value={model.id} label={m.copyId + ' ' + model.id} copiedLabel={m.copied} />
        </div>
      </Td>
      <Td>
        {group ? (
          <span
            className={
              'inline-flex h-icon items-center rounded-sm px-2 text-xs font-medium ' +
              (groupTone[group] ?? 'bg-muted text-foreground-muted')
            }
          >
            {nameOf(q, group)}
          </span>
        ) : (
          <span className="text-foreground-subtle">—</span>
        )}
      </Td>
      <Td className="text-right">
        <Limit value={model.input_token_limit} />
      </Td>
      <Td className="text-right">
        <Limit value={model.output_token_limit} />
      </Td>
      <Td>
        {model.thinking_levels?.length ? (
          <div className="flex flex-wrap gap-1">
            {model.thinking_levels.map((level) =>
              level === model.default_thinking_level ? (
                <Tooltip key={level} label={fill(m.thinkingDefault, { level })}>
                  <span className="inline-flex h-icon items-center rounded-sm bg-primary-soft px-2 font-mono text-xs font-medium text-primary">
                    {level}
                  </span>
                </Tooltip>
              ) : (
                <span
                  key={level}
                  className="inline-flex h-icon items-center rounded-sm bg-muted px-2 font-mono text-xs text-foreground-muted"
                >
                  {level}
                </span>
              ),
            )}
          </div>
        ) : (
          <span className="text-foreground-subtle">—</span>
        )}
      </Td>
      <Td className="text-right">
        {model.accounts > 0 ? (
          <span>
            {model.accounts}
            <span className="text-foreground-subtle"> / {ready}</span>
          </span>
        ) : (
          <Tooltip label={m.noAccounts}>
            <span className="text-error-text">0 / {ready}</span>
          </Tooltip>
        )}
      </Td>
    </tr>
  )
}

// ModelsPage 是模型目录：按额度组筛选与搜索，列出上下文、输出上限、思考档位与可用账户，下方编辑模型映射
export function ModelsPage() {
  const m = useMessages(modelMessages)
  const q = useMessages(quotaMessages)
  const app = useMessages(appMessages)
  const models = useModels()
  const accounts = useAccounts()
  const settings = useSettings()
  const [params, setParams] = useSearchParams()
  const query = params.get('q') ?? ''
  const group = groups.find((value) => value === params.get('group')) ?? 'all'
  const list = models.data?.models ?? []
  const ready = (accounts.data?.accounts ?? []).filter(
    (account) => account.state === 'ready',
  ).length
  const aliases = new Map(
    parseMapping(settings.data?.values.MODEL_MAPPING ?? '').map((item) => [item.alias, item.model]),
  )
  const needle = query.trim().toLowerCase()
  const visible = list
    .filter((model) => group === 'all' || model.quota_group === group)
    .filter(
      (model) =>
        !needle ||
        model.id.toLowerCase().includes(needle) ||
        model.name.toLowerCase().includes(needle),
    )
    .map((model, index) => ({ model, index }))
    .sort(
      (a, b) =>
        Number(aliases.has(a.model.id)) - Number(aliases.has(b.model.id)) ||
        compareBuckets(
          { group: a.model.quota_group ?? '', window: '' },
          { group: b.model.quota_group ?? '', window: '' },
        ) ||
        a.index - b.index,
    )
    .map(({ model }) => model)
  const targets = list.filter((model) => !aliases.has(model.id)).map((model) => model.id)
  const update = (key: string, value: string) => {
    setParams(
      (current) => {
        const next = new URLSearchParams(current)
        if (value && value !== 'all') next.set(key, value)
        else next.delete(key)
        return next
      },
      { replace: true },
    )
  }

  return (
    <>
      <PageHeader
        title={m.title}
        description={models.data ? fill(m.summary, { count: list.length, ready }) : m.description}
      />
      <Card>
        {models.isPending ? (
          <Loading label={app.loading} />
        ) : models.isError ? (
          <ErrorState
            title={m.loadFailed}
            error={models.error}
            retryLabel={app.retry}
            onRetry={() => void models.refetch()}
          />
        ) : list.length === 0 ? (
          <EmptyState
            icon={<Boxes size={20} aria-hidden />}
            title={m.emptyTitle}
            description={m.emptyDescription}
            action={
              <Button asChild>
                <Link to="/accounts">{m.goAccounts}</Link>
              </Button>
            }
          />
        ) : (
          <>
            <CardToolbar>
              <Segmented
                label={m.group}
                value={group}
                onValueChange={(value) => {
                  update('group', value)
                }}
                options={[
                  { value: 'all', label: m.all },
                  ...groups.map((value) => ({ value, label: q[value] })),
                ]}
              />
              <SearchInput
                aria-label={m.search}
                placeholder={m.search}
                value={query}
                onChange={(event) => {
                  update('q', event.target.value)
                }}
                className="w-full max-w-field sm:ml-auto"
              />
            </CardToolbar>
            {visible.length === 0 ? (
              <EmptyState
                icon={<Boxes size={20} aria-hidden />}
                title={m.noMatchTitle}
                description={m.noMatchDescription}
                action={
                  <Button
                    onClick={() => {
                      setParams({}, { replace: true })
                    }}
                  >
                    {m.clear}
                  </Button>
                }
              />
            ) : (
              <Table label={m.title} fixed minWidth={860}>
                <colgroup>
                  <col />
                  <col className="w-[140px]" />
                  <col className="w-[104px]" />
                  <col className="w-[104px]" />
                  <col className="w-[200px]" />
                  <col className="w-[112px]" />
                </colgroup>
                <thead>
                  <tr>
                    <Th>{m.model}</Th>
                    <Th>{m.group}</Th>
                    <Th className="text-right">{m.context}</Th>
                    <Th className="text-right">{m.output}</Th>
                    <Th>{m.thinking}</Th>
                    <Th className="text-right">
                      <Tooltip label={m.accountsHint}>
                        <span>{m.accounts}</span>
                      </Tooltip>
                    </Th>
                  </tr>
                </thead>
                <tbody>
                  {visible.map((model) => (
                    <ModelRow
                      key={model.id}
                      model={model}
                      alias={aliases.get(model.id)}
                      ready={ready}
                    />
                  ))}
                </tbody>
              </Table>
            )}
          </>
        )}
      </Card>
      <ModelMapping targets={targets} />
    </>
  )
}
