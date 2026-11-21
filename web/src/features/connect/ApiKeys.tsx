import { Dices, KeyRound, Pencil, Plus, Trash2 } from 'lucide-react'
import { type ReactNode, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { Checkbox } from '@/components/ui/checkbox'
import { CopyButton } from '@/components/ui/copy-button'
import { Dialog, DialogContent, DialogTrigger } from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { SearchInput } from '@/components/ui/search-input'
import { ErrorState, Loading } from '@/components/ui/states'
import { toast } from '@/components/ui/toast'
import { Tooltip } from '@/components/ui/tooltip'
import { useAccounts } from '@/features/accounts/queries'
import { markProgress } from '@/features/connect/progress'
import { splitList, useSaveSettings, useSettings } from '@/features/settings/queries'
import { fill, useMessages } from '@/lib/i18n'

// generateKey 生成 sk- 加 32 个随机字母数字的 API key
function generateKey() {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789'
  const bytes = crypto.getRandomValues(new Uint8Array(32))
  return 'sk-' + Array.from(bytes, (byte) => alphabet[byte % alphabet.length] ?? '').join('')
}

// maskKey 只显示 key 的开头与结尾
export function maskKey(key: string) {
  return key.length > 14 ? key.slice(0, 7) + '…' + key.slice(-4) : key
}

// parseBindings 解析 API_KEY_ACCOUNTS 的 API key:账户|账户，以最后一个冒号分隔
function parseBindings(value: string | undefined) {
  const bindings: Record<string, string[]> = {}
  for (const item of splitList(value)) {
    const index = item.lastIndexOf(':')
    if (index > 0) bindings[item.slice(0, index)] = item.slice(index + 1).split('|')
  }
  return bindings
}

// parseNames 解析 API_KEY_NAMES 的 API key:备注名，以最后一个冒号分隔
function parseNames(value: string | undefined) {
  const names: Record<string, string> = {}
  for (const item of splitList(value)) {
    const index = item.lastIndexOf(':')
    if (index > 0) names[item.slice(0, index)] = item.slice(index + 1)
  }
  return names
}

// recordedKey 返回请求记录与用量中 key 的写法，与 internal/app 的 maskKey 相同
function recordedKey(key: string) {
  const size = Math.floor(key.length / 4)
  return key.slice(0, Math.min(3, size)) + '…' + key.slice(key.length - Math.min(4, size))
}

// useKeyName 返回把请求记录与用量中的 key 换成显示文本的函数：key 有备注名时为“备注名 · 原文”，否则为原文
export function useKeyName() {
  const values = useSettings().data?.values
  const names = parseNames(values?.API_KEY_NAMES)
  const byRecord = new Map(
    splitList(values?.API_KEYS)
      .filter((key) => names[key])
      .map((key) => [recordedKey(key), names[key]]),
  )
  return (recorded: string) => {
    const name = byRecord.get(recorded)
    return name ? name + ' · ' + recorded : recorded
  }
}

// useApiKeys 返回 .env 中保存的 API key、各 key 绑定的账户与备注名、三者是否由进程环境变量决定与保存方法，保存时去掉已不存在的 key 的绑定与备注名
export function useApiKeys() {
  const m = useMessages('connect')
  const settings = useSettings()
  const save = useSaveSettings()
  const keys = splitList(settings.data?.values.API_KEYS)
  const bindings = parseBindings(settings.data?.values.API_KEY_ACCOUNTS)
  const names = parseNames(settings.data?.values.API_KEY_NAMES)
  const keysLocked = settings.data?.environment.includes('API_KEYS') ?? false
  const bindingsLocked = settings.data?.environment.includes('API_KEY_ACCOUNTS') ?? false
  const namesLocked = settings.data?.environment.includes('API_KEY_NAMES') ?? false
  const write = (next: string[], done?: () => void, nextBindings = bindings, nextNames = names) => {
    save.mutate(
      {
        API_KEYS: next.join(','),
        API_KEY_ACCOUNTS: next
          .filter((key) => nextBindings[key]?.length)
          .map((key) => key + ':' + (nextBindings[key] ?? []).join('|'))
          .join(','),
        API_KEY_NAMES: next
          .filter((key) => nextNames[key])
          .map((key) => key + ':' + (nextNames[key] ?? ''))
          .join(','),
      },
      {
        onSuccess: () => done?.(),
        onError: (error) => {
          toast({ tone: 'error', title: fill(m.saveFailed, { message: error.message }) })
        },
      },
    )
  }
  return {
    settings,
    keys,
    bindings,
    names,
    keysLocked,
    bindingsLocked,
    namesLocked,
    write,
    saving: save.isPending,
  }
}

// KeyDialog 是添加或修改一个 API key 的对话框：可填写自定义值，也可随机生成，可填备注名与绑定账户；index 为空时添加，tooltip 是触发按钮的提示
function KeyDialog({
  index,
  trigger,
  tooltip,
}: {
  index?: number
  trigger: ReactNode
  tooltip?: string
}) {
  const m = useMessages('connect')
  const app = useMessages('app')
  const { keys, bindings, names, keysLocked, bindingsLocked, namesLocked, write, saving } =
    useApiKeys()
  const accounts = useAccounts()
  const [open, setOpen] = useState(false)
  const [value, setValue] = useState('')
  const [name, setName] = useState('')
  const [nameError, setNameError] = useState<string>()
  const [bound, setBound] = useState<string[]>([])
  const [query, setQuery] = useState('')
  const needle = query.trim().toLowerCase()
  const shown = (accounts.data?.accounts ?? []).filter((account) =>
    [account.id, account.label ?? ''].some((text) => text.toLowerCase().includes(needle)),
  )
  const [error, setError] = useState<string>()
  const editing = index !== undefined
  const submit = () => {
    const key = value.trim()
    if (!key || /[\s,]/.test(key)) {
      setError(m.errKey)
      return
    }
    if (keys.some((item, position) => item === key && position !== index)) {
      setError(m.errKeyDuplicate)
      return
    }
    if (/[,:]/.test(name)) {
      setNameError(m.errKeyName)
      return
    }
    const next = editing
      ? keys.map((item, position) => (position === index ? key : item))
      : [...keys, key]
    const nextBindings = Object.fromEntries(
      Object.entries(bindings).filter(([item]) => !editing || item !== keys[index]),
    )
    nextBindings[key] = bound
    const nextNames = Object.fromEntries(
      Object.entries(names).filter(([item]) => !editing || item !== keys[index]),
    )
    nextNames[key] = name.trim()
    write(
      next,
      () => {
        setOpen(false)
        toast({ tone: 'success', title: editing ? m.keyUpdated : m.keyAdded })
      },
      nextBindings,
      nextNames,
    )
  }
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (next) {
          setValue(editing ? (keys[index] ?? '') : generateKey())
          setBound(editing ? (bindings[keys[index] ?? ''] ?? []) : [])
          setName(editing ? (names[keys[index] ?? ''] ?? '') : '')
          setQuery('')
          setError(undefined)
          setNameError(undefined)
        }
        setOpen(next)
      }}
    >
      {tooltip ? (
        <Tooltip label={tooltip}>
          <DialogTrigger asChild>{trigger}</DialogTrigger>
        </Tooltip>
      ) : (
        <DialogTrigger asChild>{trigger}</DialogTrigger>
      )}
      <DialogContent
        title={editing ? fill(m.editKeyTitle, { index: index + 1 }) : m.addKey}
        description={editing ? m.editKeyHint : m.addKeyHint}
        closeLabel={app.closeDialog}
        footer={
          <>
            <Button
              onClick={() => {
                setOpen(false)
              }}
            >
              {m.cancel}
            </Button>
            <Button variant="primary" loading={saving} onClick={submit}>
              {m.save}
            </Button>
          </>
        }
      >
        <form
          onSubmit={(event) => {
            event.preventDefault()
            submit()
          }}
        >
          <Field id="api-key-value" label={m.keyValue} error={error}>
            <div className="flex gap-2">
              <Input
                id="api-key-value"
                value={value}
                disabled={keysLocked}
                spellCheck={false}
                autoComplete="off"
                data-1p-ignore
                data-lpignore="true"
                aria-invalid={error ? true : undefined}
                aria-describedby={error ? 'api-key-value-error' : undefined}
                className="font-mono"
                onChange={(event) => {
                  setValue(event.target.value)
                  setError(undefined)
                }}
              />
              <Tooltip label={m.regenerate}>
                <Button
                  size="icon"
                  disabled={keysLocked}
                  aria-label={m.regenerate}
                  onClick={() => {
                    setValue(generateKey())
                    setError(undefined)
                  }}
                >
                  <Dices size={16} aria-hidden />
                </Button>
              </Tooltip>
            </div>
          </Field>
          <div className="mt-4">
            <Field
              id="api-key-name"
              label={m.keyName}
              description={m.keyNameHint}
              error={nameError}
            >
              <Input
                id="api-key-name"
                value={name}
                disabled={namesLocked}
                autoComplete="off"
                data-1p-ignore
                data-lpignore="true"
                aria-invalid={nameError ? true : undefined}
                aria-describedby={nameError ? 'api-key-name-error' : 'api-key-name-description'}
                onChange={(event) => {
                  setName(event.target.value)
                  setNameError(undefined)
                }}
              />
            </Field>
          </div>
          {accounts.data && accounts.data.accounts.length > 0 && (
            <fieldset className="mt-4 flex flex-col gap-2">
              <legend className="text-sm font-medium">{m.keyAccounts}</legend>
              <p className="text-sm text-foreground-muted">{m.keyAccountsHint}</p>
              <SearchInput
                clearLabel={app.clearSearch}
                aria-label={m.keyAccountsSearch}
                placeholder={m.keyAccountsSearch}
                value={query}
                onChange={(event) => {
                  setQuery(event.target.value)
                }}
              />
              <ul className="flex max-h-[264px] flex-col overflow-y-auto rounded-md border border-border py-1">
                {shown.length === 0 ? (
                  <li className="px-3 py-2 text-sm text-foreground-muted">{m.keyAccountsNone}</li>
                ) : (
                  shown.map((account) => (
                    <li key={account.id}>
                      <label className="flex cursor-pointer items-center gap-2 px-3 py-2 text-sm hover:bg-hover">
                        <Checkbox
                          checked={bound.includes(account.id)}
                          disabled={bindingsLocked}
                          onCheckedChange={(checked) => {
                            setBound((current) =>
                              checked === true
                                ? [...current, account.id]
                                : current.filter((id) => id !== account.id),
                            )
                          }}
                        />
                        <span className="truncate">{account.label ?? account.id}</span>
                      </label>
                    </li>
                  ))
                )}
              </ul>
            </fieldset>
          )}
        </form>
      </DialogContent>
    </Dialog>
  )
}

// AddKeyButton 打开添加 API key 的对话框
export function AddKeyButton({ primary = false }: { primary?: boolean }) {
  const m = useMessages('connect')
  const { settings, keysLocked } = useApiKeys()
  return (
    <KeyDialog
      trigger={
        <Button
          variant={primary ? 'primary' : 'secondary'}
          size="sm"
          disabled={!settings.data || keysLocked}
        >
          <Plus size={16} aria-hidden />
          {m.addKey}
        </Button>
      }
    />
  )
}

// ApiKeyList 列出 API key 与备注名，可复制、修改与删除，删除后 5 秒内可撤销；API_KEYS 由进程环境变量决定时只能修改绑定的账户与备注名
export function ApiKeyList() {
  const m = useMessages('connect')
  const app = useMessages('app')
  const settingsText = useMessages('settings')
  const { settings, keys, bindings, names, keysLocked, write } = useApiKeys()
  if (settings.isPending) return <Loading label={app.loading} rows={2} />
  if (settings.isError) {
    return (
      <ErrorState
        title={m.keysLoadFailed}
        error={settings.error}
        retryLabel={app.retry}
        onRetry={() => void settings.refetch()}
      />
    )
  }
  if (keys.length === 0) {
    return (
      <Callout tone="muted">
        <p>{m.keysEmpty}</p>
        <AddKeyButton primary />
      </Callout>
    )
  }
  return (
    <>
      <ul className="flex flex-col overflow-hidden rounded-md border border-border">
        {keys.map((key, index) => (
          <li
            key={key}
            className="flex items-center gap-1 border-t border-border py-2 pr-2 pl-4 first:border-t-0"
          >
            <KeyRound size={16} aria-hidden className="mr-2 shrink-0 text-foreground-muted" />
            {names[key] && (
              <span className="mr-2 max-w-[40%] shrink-0 truncate text-sm font-medium">
                {names[key]}
              </span>
            )}
            <code
              aria-label={fill(m.keyLabel, { index: index + 1 })}
              className="min-w-0 flex-1 truncate font-mono text-sm select-all"
            >
              {key}
            </code>
            {bindings[key]?.length ? (
              <span className="shrink-0 text-sm text-foreground-muted">
                {fill(m.keyBound, { count: bindings[key].length })}
              </span>
            ) : null}
            <CopyButton
              value={key}
              label={fill(m.copyKey, { index: index + 1 })}
              copiedLabel={m.copied}
              onCopy={() => {
                markProgress({ endpoint: true })
              }}
            />
            <KeyDialog
              index={index}
              tooltip={m.editKeyShort}
              trigger={
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={fill(m.editKey, { index: index + 1 })}
                  className="size-control-sm"
                >
                  <Pencil size={16} aria-hidden />
                </Button>
              }
            />
            <Tooltip label={fill(m.deleteKey, { index: index + 1 })}>
              <Button
                variant="ghost"
                size="icon"
                disabled={keysLocked}
                aria-label={fill(m.deleteKey, { index: index + 1 })}
                className="size-control-sm enabled:hover:text-error-text"
                onClick={() => {
                  const before = keys
                  const beforeBindings = bindings
                  const beforeNames = names
                  write(
                    keys.filter((item) => item !== key),
                    () => {
                      toast({
                        tone: 'success',
                        title: fill(m.deletedKey, { key: maskKey(key) }),
                        action: {
                          label: m.undo,
                          onClick: () => {
                            write(before, undefined, beforeBindings, beforeNames)
                          },
                        },
                      })
                    },
                  )
                }}
              >
                <Trash2 size={16} aria-hidden />
              </Button>
            </Tooltip>
          </li>
        ))}
      </ul>
      {keysLocked && (
        <p className="mt-2 text-sm text-foreground-muted">
          {fill(settingsText.environmentHint, { key: 'API_KEYS' })}
        </p>
      )}
    </>
  )
}
