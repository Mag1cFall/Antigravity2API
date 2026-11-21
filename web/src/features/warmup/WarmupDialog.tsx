import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Flame, LoaderCircle } from 'lucide-react'
import { type ReactNode, useState } from 'react'
import { Link } from 'react-router'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox, SelectAllCheckbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogTrigger } from '@/components/ui/dialog'
import { Table, Td, Th } from '@/components/ui/table'
import { AccountStatusBadge } from '@/features/accounts/AccountStatus'
import { accountsKey } from '@/features/accounts/queries'
import type { Account } from '@/features/accounts/types'
import type { Model } from '@/features/models/types'
import { bucketParts, compareBuckets, groups, nameOf } from '@/features/quota/quota'
import type { QuotaBucket } from '@/features/quota/types'
import { api, errorMessage } from '@/lib/api'
import { formatDuration, formatPercent } from '@/lib/format'
import { fill, serverText, useI18n, useMessages } from '@/lib/i18n'

// WarmupResult 对应 POST /api/warmup 的返回，request 是请求记录 ID，before 与 after 是预热前后该模型额度组的额度桶
interface WarmupResult {
  account: string
  model: string
  quota_group?: string
  ok: boolean
  duration_ms: number
  error?: string
  request: string
  before: QuotaBucket[]
  after: QuotaBucket[]
}

// Run 是一个账户与模型组合的预热进度，result 在完成后出现
interface Run {
  account: string
  model: string
  result?: WarmupResult
  running: boolean
}

// WarmupModel 对应 GET /api/warmup 的模型条目：全部账户（含停用账户）上一次目录中的对话模型，warmup 表示它是所属额度组默认选中的模型
interface WarmupModel extends Omit<Model, 'accounts'> {
  warmup?: boolean
}

// warmupKey 是预热对话框模型列表的查询键
const warmupKey = ['warmup'] as const

// toggle 返回加入或移出 values 后的集合
function toggle(set: ReadonlySet<string>, values: string[], on: boolean) {
  const next = new Set(set)
  for (const value of values) {
    if (on) next.add(value)
    else next.delete(value)
  }
  return next
}

// QuotaChange 按窗口列出额度组各额度桶预热前后的剩余比例
function QuotaChange({ before, after }: { before: QuotaBucket[]; after: QuotaBucket[] }) {
  const q = useMessages('quota')
  const w = useMessages('warmup')
  const { locale } = useI18n()
  const buckets = after
    .map((bucket) => ({ ...bucket, ...bucketParts(bucket.bucket_id) }))
    .sort(compareBuckets)
  if (buckets.length === 0) return <span className="text-foreground-muted">{w.noQuota}</span>
  return (
    <ul className="flex flex-col gap-1 text-xs whitespace-nowrap">
      {buckets.map((bucket) => {
        const previous = before.find((item) => item.bucket_id === bucket.bucket_id)
        return (
          <li key={bucket.bucket_id}>
            <span className="text-foreground-muted">{nameOf(q, bucket.window)}</span>{' '}
            {previous ? formatPercent(previous.remaining_fraction, locale) + ' → ' : ''}
            {formatPercent(bucket.remaining_fraction, locale)}
          </li>
        )
      })}
    </ul>
  )
}

// WarmupDialog 选定账户与对话模型后对每个组合发一次最小生成请求，各账户并行、同一账户内依次进行，逐行显示结果、耗时、错误原因与额度变化；selected 非空时默认选中这些账户，否则默认选中就绪账户；模型取全部账户（含停用账户）上一次的目录，打开时重新读取，按额度组分组，用户改动前默认选中各额度组费用最低的模型
export function WarmupDialog({ accounts, selected }: { accounts: Account[]; selected: string[] }) {
  const w = useMessages('warmup')
  const q = useMessages('quota')
  const app = useMessages('app')
  const { locale } = useI18n()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const models = useQuery({
    queryKey: warmupKey,
    queryFn: () => api<{ models: WarmupModel[] }>('/api/warmup'),
    enabled: open,
  })
  const chat = models.data?.models ?? []
  const [pickedAccounts, setPickedAccounts] = useState<ReadonlySet<string>>(new Set())
  const [changedModels, setChangedModels] = useState<ReadonlySet<string>>()
  const pickedModels =
    changedModels ?? new Set(chat.filter((model) => model.warmup).map((model) => model.id))
  const [runs, setRuns] = useState<Run[]>()
  const running = runs?.some((run) => !run.result) ?? false

  const onOpenChange = (next: boolean) => {
    if (running) return
    if (next) {
      setPickedAccounts(
        new Set(
          selected.length > 0
            ? selected
            : accounts.filter((account) => account.state === 'ready').map((account) => account.id),
        ),
      )
      setChangedModels(undefined)
      setRuns(undefined)
    }
    setOpen(next)
  }

  const update = (account: string, model: string, change: Partial<Run>) => {
    setRuns((current) =>
      current?.map((run) =>
        run.account === account && run.model === model ? { ...run, ...change } : run,
      ),
    )
  }

  const start = async () => {
    const targets = accounts.filter((account) => pickedAccounts.has(account.id))
    const ids = chat.filter((model) => pickedModels.has(model.id)).map((model) => model.id)
    setRuns(
      targets.flatMap((account) =>
        ids.map((model) => ({ account: account.id, model, running: false })),
      ),
    )
    await Promise.all(
      targets.map(async (account) => {
        for (const model of ids) {
          update(account.id, model, { running: true })
          const result = await api<WarmupResult>('/api/warmup', {
            method: 'POST',
            json: { account: account.id, model },
          }).catch((error: unknown): WarmupResult => ({
            account: account.id,
            model,
            ok: false,
            duration_ms: 0,
            error: errorMessage(error),
            request: '',
            before: [],
            after: [],
          }))
          update(account.id, model, { running: false, result })
        }
      }),
    )
    void queryClient.invalidateQueries({ queryKey: accountsKey })
  }

  const total =
    accounts.filter((account) => pickedAccounts.has(account.id)).length *
    chat.filter((model) => pickedModels.has(model.id)).length
  const done = runs?.filter((run) => run.result).length ?? 0
  const succeeded = runs?.filter((run) => run.result?.ok).length ?? 0

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button disabled={accounts.length === 0}>
          <Flame size={16} aria-hidden />
          {w.open}
        </Button>
      </DialogTrigger>
      <DialogContent
        title={w.title}
        description={w.description}
        width="wide"
        closeLabel={app.closeDialog}
        footer={
          runs ? (
            <>
              <Button
                disabled={running}
                onClick={() => {
                  setRuns(undefined)
                }}
              >
                {w.back}
              </Button>
              <Button
                variant="primary"
                loading={running}
                onClick={() => {
                  void start()
                }}
              >
                {w.rerun}
              </Button>
            </>
          ) : (
            <>
              <Button
                onClick={() => {
                  onOpenChange(false)
                }}
              >
                {w.cancel}
              </Button>
              <Button
                variant="primary"
                disabled={total === 0}
                onClick={() => {
                  void start()
                }}
              >
                {fill(w.start, { count: total })}
              </Button>
            </>
          )
        }
      >
        {runs ? (
          <div className="flex flex-col gap-3">
            <p role="status" className="text-sm text-foreground-muted">
              {fill(w.progress, { done, total: runs.length, ok: succeeded })}
            </p>
            <Table label={w.results} framed stack>
              <thead>
                <tr>
                  <Th>{w.target}</Th>
                  <Th>{w.result}</Th>
                  <Th>{w.quota}</Th>
                </tr>
              </thead>
              <tbody>
                {runs.map((run) => (
                  <tr key={run.account + '\n' + run.model} data-warmup-row>
                    <Td className="max-sm:w-full">
                      <div className="wrap-anywhere">
                        <Email address={run.account} />
                      </div>
                      <div className="mt-1 font-mono text-xs text-foreground-muted">
                        {run.model}
                      </div>
                    </Td>
                    <Td label={w.result} className="max-sm:w-full">
                      {run.result ? (
                        <div className="flex flex-col items-start gap-1">
                          <span className="flex items-center gap-2 whitespace-nowrap">
                            <Badge tone={run.result.ok ? 'success' : 'error'}>
                              {run.result.ok ? w.succeeded : w.failed}
                            </Badge>
                            <span className="text-xs text-foreground-muted">
                              {formatDuration(run.result.duration_ms, locale)}
                            </span>
                          </span>
                          {run.result.error && (
                            <p className="text-xs break-words text-error-text">
                              {serverText(run.result.error)}
                            </p>
                          )}
                          {run.result.request && (
                            <Link
                              to={'/requests?id=' + encodeURIComponent(run.result.request)}
                              className="text-xs text-primary hover:underline"
                            >
                              {w.record}
                            </Link>
                          )}
                        </div>
                      ) : (
                        <span className="flex items-center gap-2 text-foreground-muted">
                          {run.running && (
                            <LoaderCircle size={14} aria-hidden className="animate-spin" />
                          )}
                          {run.running ? w.running : w.waiting}
                        </span>
                      )}
                    </Td>
                    <Td label={w.quota}>
                      {run.result && (
                        <QuotaChange before={run.result.before} after={run.result.after} />
                      )}
                    </Td>
                  </tr>
                ))}
              </tbody>
            </Table>
          </div>
        ) : (
          <div className="grid gap-6 sm:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
            <Picker
              legend={w.models}
              hint={w.modelsHint}
              selectAll={w.selectAll}
              empty={
                models.isPending
                  ? app.loading
                  : models.isError
                    ? errorMessage(models.error)
                    : w.noModels
              }
              sections={[...new Set([...groups, ...chat.map((model) => model.quota_group ?? '')])]
                .map((group) => ({
                  label: nameOf(q, group),
                  options: chat
                    .filter((model) => (model.quota_group ?? '') === group)
                    .map((model) => ({
                      value: model.id,
                      label: (
                        <span className="font-mono text-xs whitespace-nowrap">{model.id}</span>
                      ),
                      badge: model.warmup && <Badge tone="neutral">{w.cheapest}</Badge>,
                    })),
                }))
                .filter((section) => section.options.length > 0)}
              picked={pickedModels}
              onChange={setChangedModels}
            />
            <Picker
              legend={w.accounts}
              hint={w.accountsHint}
              selectAll={w.selectAll}
              sections={[
                {
                  options: accounts.map((account) => ({
                    value: account.id,
                    label: (
                      <>
                        {account.label && account.label + ' · '}
                        <Email address={account.id} />
                      </>
                    ),
                    badge: <AccountStatusBadge account={account} />,
                  })),
                },
              ]}
              picked={pickedAccounts}
              onChange={setPickedAccounts}
            />
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

// Email 显示邮箱地址，换行时优先在 @ 之前断开
function Email({ address }: { address: string }) {
  const at = address.indexOf('@')
  if (at <= 0) return address
  return (
    <>
      {address.slice(0, at)}
      <wbr />
      {address.slice(at)}
    </>
  )
}

// PickerOption 是复选框列表的一项，badge 显示在右侧，放不下时换到下一行右侧
interface PickerOption {
  value: string
  label: ReactNode
  badge?: ReactNode
}

// Picker 是带全选的复选框列表，选项分成若干节，带 label 的节有自己的整节勾选；没有选项时显示 empty，hint 是标题下的说明
function Picker({
  legend,
  hint,
  selectAll,
  empty,
  sections,
  picked,
  onChange,
}: {
  legend: string
  hint?: string
  selectAll: string
  empty?: string
  sections: { label?: string; options: PickerOption[] }[]
  picked: ReadonlySet<string>
  onChange: (next: ReadonlySet<string>) => void
}) {
  const options = sections.flatMap((section) => section.options)
  return (
    <fieldset className="flex min-w-0 flex-col gap-2" data-warmup-picker={legend}>
      <legend className="text-sm font-medium">{legend}</legend>
      {hint && <p className="text-xs text-foreground-muted">{hint}</p>}
      {options.length === 0 ? (
        <p className="text-sm text-foreground-muted">{empty}</p>
      ) : (
        <div className="flex flex-col overflow-y-auto rounded-md border border-border">
          <Toggle label={selectAll} options={options} picked={picked} onChange={onChange} />
          {sections.map((section, index) => (
            <div key={section.label ?? index} className="flex flex-col">
              {section.label && (
                <Toggle
                  label={section.label}
                  options={section.options}
                  picked={picked}
                  onChange={onChange}
                  heading
                />
              )}
              {section.options.map((option) => (
                <label
                  key={option.value}
                  className="flex flex-wrap items-center gap-x-2 gap-y-1 px-3 py-2 text-sm hover:bg-muted/60"
                >
                  <Checkbox
                    checked={picked.has(option.value)}
                    onCheckedChange={(checked) => {
                      onChange(toggle(picked, [option.value], checked === true))
                    }}
                  />
                  <span className="flex-1 wrap-anywhere">{option.label}</span>
                  {option.badge && <span className="ml-auto">{option.badge}</span>}
                </label>
              ))}
            </div>
          ))}
        </div>
      )}
    </fieldset>
  )
}

// Toggle 是一组选项的整组勾选行，heading 为节标题样式
function Toggle({
  label,
  options,
  picked,
  onChange,
  heading = false,
}: {
  label: string
  options: PickerOption[]
  picked: ReadonlySet<string>
  onChange: (next: ReadonlySet<string>) => void
  heading?: boolean
}) {
  const chosen = options.filter((option) => picked.has(option.value)).length
  return (
    <label
      className={
        heading
          ? 'flex items-center gap-2 border-y border-border px-3 pt-3 text-xs font-medium text-foreground-muted'
          : 'flex items-center gap-2 border-b border-border bg-muted px-3 py-2 text-sm'
      }
    >
      <SelectAllCheckbox
        selected={chosen}
        total={options.length}
        onCheckedChange={(checked) => {
          onChange(
            toggle(
              picked,
              options.map((option) => option.value),
              checked,
            ),
          )
        }}
      />
      {label}
    </label>
  )
}
