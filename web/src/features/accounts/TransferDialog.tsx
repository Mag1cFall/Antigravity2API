import { useMutation, useQueryClient } from '@tanstack/react-query'
import { FileText, FileUp, FolderOpen, X } from 'lucide-react'
import { useEffect, useEffectEvent, useRef, useState } from 'react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox, SelectAllCheckbox } from '@/components/ui/checkbox'
import { Field } from '@/components/ui/field'
import { Textarea } from '@/components/ui/input'
import { keepAccounts } from '@/features/accounts/actions'
import { accountsKey } from '@/features/accounts/queries'
import { ExportDialog } from '@/features/files/ExportDialog'
import { FileBrowserDialog } from '@/features/files/FileBrowser'
import { useSession } from '@/features/service/queries'
import { api, errorMessage } from '@/lib/api'
import { fill, serverText, useMessages } from '@/lib/i18n'

// TransferResult 是 POST /api/accounts/import/json 的响应
interface TransferResult {
  accounts: string[]
  failed: { account: string; error: string }[]
}

// Discovered 是 POST /api/accounts/import/discover 识别出的一个账户，其余字段原样提交给导入接口
interface Discovered {
  email: string
  refresh_token: string
  source: string
  exists: boolean
}

// Discovery 是 POST /api/accounts/import/discover 的响应
interface Discovery {
  accounts: Discovered[]
  failed: { account: string; error: string }[]
}

// Source 是一份待识别的账户内容：拖入的文件与粘贴的内容带原文，从运行服务的电脑上选择的文件带路径
type Source = { name: string; content: string } | { name: string; path: string }

// sourceKey 标识一份账户内容：运行服务的电脑上的文件为完整路径，拖入的文件与粘贴的内容为名称与原文
function sourceKey(source: Source) {
  return 'path' in source ? source.path : source.name + '\n' + source.content
}

// accountName 是识别出的账户在列表中的名称，没有邮箱时为 refresh token 的开头
function accountName(account: Discovered) {
  return account.email || account.refresh_token.slice(0, 12) + '…'
}

// ImportAccountFiles 是添加账户对话框中的导入账户文件：拖入文件、从运行服务的电脑上选择文件或粘贴内容后读取其中的账户，勾选要导入的账户后导入，结果显示在下方
export function ImportAccountFiles() {
  const m = useMessages('transfer')
  const client = useQueryClient()
  const session = useSession()
  const local = session.data?.local ?? true
  const [sources, setSources] = useState<Source[]>([])
  const [text, setText] = useState('')
  const [dragging, setDragging] = useState(false)
  const [browsing, setBrowsing] = useState(false)
  const [skipped, setSkipped] = useState<ReadonlySet<string>>(new Set())
  const zone = useRef<HTMLDivElement>(null)
  const discover = useMutation({
    mutationFn: () =>
      api<Discovery>('/api/accounts/import/discover', {
        method: 'POST',
        json: {
          sources: [...sources, ...(text.trim() ? [{ name: m.pasted, content: text }] : [])],
        },
      }),
    onSuccess: () => {
      setSkipped(new Set())
      run.reset()
    },
  })
  const found = discover.data?.accounts ?? []
  const chosen = found.filter((account) => !skipped.has(account.refresh_token))
  const run = useMutation({
    mutationFn: () =>
      api<TransferResult>('/api/accounts/import/json', { method: 'POST', json: chosen }),
    onSuccess: (result) => {
      keepAccounts(result.accounts)
      void client.invalidateQueries({ queryKey: accountsKey })
      if (result.failed.length === 0) {
        setSources([])
        setText('')
        discover.reset()
        return
      }
      setSkipped(
        (current) =>
          new Set([
            ...current,
            ...found
              .filter((account) => result.accounts.includes(account.email))
              .map((account) => account.refresh_token),
          ]),
      )
    },
  })
  const add = (items: Source[]) => {
    setSources((current) => {
      const keys = new Set(current.map(sourceKey))
      return [...current, ...items.filter((item) => !keys.has(sourceKey(item)))]
    })
    discover.reset()
  }
  const dropped = useEffectEvent((items: Source[]) => {
    add(items)
  })
  useEffect(() => {
    const element = zone.current
    if (!element) return undefined
    const over = (event: DragEvent) => {
      event.preventDefault()
      if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy'
      setDragging(true)
    }
    const leave = () => {
      setDragging(false)
    }
    const drop = (event: DragEvent) => {
      event.preventDefault()
      setDragging(false)
      const files = [...(event.dataTransfer?.files ?? [])]
      void Promise.all(
        files.map(async (file) => ({ name: file.name, content: await file.text() })),
      ).then(dropped)
    }
    element.addEventListener('dragover', over)
    element.addEventListener('dragleave', leave)
    element.addEventListener('drop', drop)
    return () => {
      element.removeEventListener('dragover', over)
      element.removeEventListener('dragleave', leave)
      element.removeEventListener('drop', drop)
    }
  }, [])
  const result = run.data
  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-foreground-muted">{m.description}</p>
      <Field id="account-files" label={m.files}>
        <div
          ref={zone}
          className={
            'flex flex-col items-center justify-center gap-3 rounded-md border border-dashed px-4 py-6 text-sm text-foreground-muted transition-colors ' +
            (dragging ? 'border-primary bg-primary-soft' : 'border-border')
          }
        >
          <span className="flex items-center gap-2">
            <FileUp size={16} aria-hidden className="shrink-0" />
            {m.drop}
          </span>
          <Button
            size="sm"
            onClick={() => {
              setBrowsing(true)
            }}
          >
            <FolderOpen size={16} aria-hidden />
            {local ? m.choose : m.chooseRemote}
          </Button>
        </div>
        {sources.length > 0 && (
          <ul className="flex flex-wrap gap-2">
            {sources.map((source) => (
              <li
                key={sourceKey(source)}
                className="flex h-control-sm max-w-full min-w-0 items-center gap-1 rounded-sm bg-muted pr-1 pl-3 font-mono text-xs"
              >
                <span className="truncate">{source.name}</span>
                <button
                  type="button"
                  aria-label={fill(m.removeFile, { name: source.name })}
                  className="inline-flex size-6 shrink-0 items-center justify-center rounded-sm text-foreground-muted hover:bg-hover hover:text-foreground"
                  onClick={() => {
                    setSources(sources.filter((item) => item !== source))
                    discover.reset()
                  }}
                >
                  <X size={14} aria-hidden />
                </button>
              </li>
            ))}
          </ul>
        )}
      </Field>
      <Field id="account-paste" label={m.paste}>
        <Textarea
          id="account-paste"
          rows={4}
          value={text}
          spellCheck={false}
          placeholder={m.placeholder}
          onChange={(event) => {
            setText(event.target.value)
            discover.reset()
          }}
          className="font-mono text-xs"
        />
      </Field>
      {!discover.isSuccess && (
        <div className="flex flex-wrap items-center gap-3">
          <Button
            variant="primary"
            loading={discover.isPending}
            disabled={!sources.length && !text.trim()}
            onClick={() => {
              discover.mutate()
            }}
          >
            {m.read}
          </Button>
          {discover.isError && (
            <p role="alert" className="text-sm wrap-anywhere text-error-text">
              {errorMessage(discover.error)}
            </p>
          )}
        </div>
      )}
      {discover.isSuccess && (
        <section aria-label={m.found} className="flex flex-col gap-2">
          {found.length > 0 && (
            <>
              <div className="flex flex-wrap items-center gap-3">
                <label className="flex items-center gap-2 text-sm font-medium">
                  <SelectAllCheckbox
                    selected={chosen.length}
                    total={found.length}
                    onCheckedChange={(checked) => {
                      setSkipped(
                        checked
                          ? new Set()
                          : new Set(found.map((account) => account.refresh_token)),
                      )
                    }}
                  />
                  {fill(m.foundCount, { count: found.length })}
                </label>
              </div>
              <ul className="flex max-h-[240px] flex-col overflow-y-auto rounded-md border border-border">
                {found.map((account) => (
                  <li
                    key={account.refresh_token}
                    className="border-t border-border first:border-t-0"
                  >
                    <label className="flex min-w-0 cursor-pointer items-center gap-3 px-3 py-2 text-sm hover:bg-hover">
                      <Checkbox
                        checked={!skipped.has(account.refresh_token)}
                        onCheckedChange={(checked) => {
                          const next = new Set(skipped)
                          if (checked === true) next.delete(account.refresh_token)
                          else next.add(account.refresh_token)
                          setSkipped(next)
                        }}
                      />
                      <span className="min-w-0 flex-1 truncate font-medium">
                        {accountName(account)}
                      </span>
                      {account.exists && <Badge tone="info">{m.exists}</Badge>}
                      <span className="flex max-w-[40%] min-w-0 items-center gap-1 text-xs text-foreground-muted">
                        <FileText size={12} aria-hidden className="shrink-0" />
                        <span className="truncate">{account.source}</span>
                      </span>
                    </label>
                  </li>
                ))}
              </ul>
              {found.some((account) => account.exists) && (
                <p className="text-xs text-foreground-muted">{m.existsHint}</p>
              )}
            </>
          )}
          {(discover.data.failed.length > 0 || found.length === 0) && (
            <ul className="flex flex-col gap-1 text-xs text-error-text">
              {found.length === 0 && discover.data.failed.length === 0 && <li>{m.noneFound}</li>}
              {discover.data.failed.map((item) => (
                <li key={item.account + item.error} className="break-words">
                  {fill(m.failure, { account: item.account, error: serverText(item.error) })}
                </li>
              ))}
            </ul>
          )}
          {result && result.failed.length > 0 && (
            <div role="status" className="flex flex-col gap-1 text-sm">
              <p>
                {fill(m.partial, { count: result.accounts.length, failed: result.failed.length })}
              </p>
              <ul className="overflow-y-auto text-xs text-error-text">
                {result.failed.map((item) => (
                  <li key={item.account + item.error} className="break-words">
                    {fill(m.failure, { account: item.account, error: serverText(item.error) })}
                  </li>
                ))}
              </ul>
            </div>
          )}
          <div className="flex flex-wrap items-center gap-3">
            <Button
              variant="primary"
              loading={run.isPending}
              disabled={chosen.length === 0}
              onClick={() => {
                run.mutate()
              }}
            >
              {fill(m.submit, { count: chosen.length })}
            </Button>
            <Button
              variant="ghost"
              onClick={() => {
                discover.reset()
                run.reset()
              }}
            >
              {m.back}
            </Button>
            {run.isError && (
              <p role="alert" className="text-sm wrap-anywhere text-error-text">
                {errorMessage(run.error)}
              </p>
            )}
          </div>
        </section>
      )}
      {result?.failed.length === 0 && (
        <p role="status" className="text-sm text-success-text">
          {fill(m.done, { count: result.accounts.length })}
        </p>
      )}
      <FileBrowserDialog
        open={browsing}
        onOpenChange={setBrowsing}
        title={m.chooseTitle}
        description={local ? undefined : m.chooseRemoteHint}
        start=""
        mode="files"
        extensions={['json', 'txt']}
        chooseLabel={m.chooseFiles}
        onChoose={(paths) => {
          add(paths.map((path) => ({ name: path.split(/[\\/]/).pop() ?? path, path })))
        }}
      />
    </div>
  )
}

// AccountExportDialog 是导出账户 JSON 的对话框：ids 为空时导出全部账户，本机访问且多个账户时可选每个账户保存为单独的文件，total 是全部账户数
export function AccountExportDialog({
  ids,
  total,
  onClose,
}: {
  ids: string[] | undefined
  total: number
  onClose: () => void
}) {
  const m = useMessages('transfer')
  const [split, setSplit] = useState(false)
  const count = ids && ids.length > 0 ? ids.length : total
  const query = new URLSearchParams((ids ?? []).map((id) => ['id', id])).toString()
  const content = async () =>
    JSON.stringify(await api<unknown>('/api/accounts/export' + (query ? '?' + query : '')), null, 2)
  return (
    <ExportDialog
      open={ids !== undefined}
      onOpenChange={(open) => {
        if (!open) {
          setSplit(false)
          onClose()
        }
      }}
      title={
        ids?.length === 1 && ids[0]
          ? fill(m.exportOne, { email: ids[0] })
          : fill(m.exportMany, { count })
      }
      description={m.exportDescription}
      path="/api/accounts/export"
      body={{ ids: ids ?? [], split }}
      copy={content}
      download={{
        name:
          ids?.length === 1 && ids[0]
            ? 'antigravity2api-' + ids[0] + '.json'
            : 'antigravity2api-accounts-' +
              new Date().toISOString().slice(0, 10).replaceAll('-', '') +
              '.json',
        file: async () => new Blob([await content()], { type: 'application/json' }),
      }}
      options={
        count > 1 && (
          <label className="flex items-center gap-2 text-sm">
            <Checkbox
              checked={split}
              onCheckedChange={(checked) => {
                setSplit(checked === true)
              }}
            />
            {m.split}
          </label>
        )
      }
    />
  )
}
