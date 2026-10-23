import { Dices, KeyRound, Pencil, Plus, Trash2 } from 'lucide-react'
import { type ReactNode, useState } from 'react'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { CopyButton } from '@/components/ui/copy-button'
import { Dialog, DialogContent, DialogTrigger } from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { ErrorState, Loading } from '@/components/ui/states'
import { toast } from '@/components/ui/toast'
import { Tooltip } from '@/components/ui/tooltip'
import { connectMessages } from '@/features/connect/messages'
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

// useApiKeys 返回 .env 中保存的 API key 与保存方法
export function useApiKeys() {
  const m = useMessages(connectMessages)
  const settings = useSettings()
  const save = useSaveSettings()
  const keys = splitList(settings.data?.values.API_KEYS)
  const write = (next: string[], done?: () => void) => {
    save.mutate(
      { API_KEYS: next.join(',') },
      {
        onSuccess: () => done?.(),
        onError: (error) => {
          toast({ tone: 'error', title: fill(m.saveFailed, { message: error.message }) })
        },
      },
    )
  }
  return { settings, keys, write, saving: save.isPending }
}

// KeyDialog 是添加或修改一个 API key 的对话框：可填写自定义值，也可随机生成；index 为空时添加，tooltip 是触发按钮的提示
function KeyDialog({
  index,
  trigger,
  tooltip,
}: {
  index?: number
  trigger: ReactNode
  tooltip?: string
}) {
  const m = useMessages(connectMessages)
  const app = useMessages(appMessages)
  const { keys, write, saving } = useApiKeys()
  const [open, setOpen] = useState(false)
  const [value, setValue] = useState('')
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
    const next = editing
      ? keys.map((item, position) => (position === index ? key : item))
      : [...keys, key]
    write(next, () => {
      setOpen(false)
      toast({ tone: 'success', title: editing ? m.keyUpdated : m.keyAdded })
    })
  }
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (next) {
          setValue(editing ? (keys[index] ?? '') : generateKey())
          setError(undefined)
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
                spellCheck={false}
                autoComplete="off"
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
        </form>
      </DialogContent>
    </Dialog>
  )
}

// AddKeyButton 打开添加 API key 的对话框
export function AddKeyButton({ primary = false }: { primary?: boolean }) {
  const m = useMessages(connectMessages)
  const { settings } = useApiKeys()
  return (
    <KeyDialog
      trigger={
        <Button variant={primary ? 'primary' : 'secondary'} size="sm" disabled={!settings.data}>
          <Plus size={16} aria-hidden />
          {m.addKey}
        </Button>
      }
    />
  )
}

// ApiKeyList 列出 API key，可复制、修改与删除，删除后 5 秒内可撤销
export function ApiKeyList() {
  const m = useMessages(connectMessages)
  const app = useMessages(appMessages)
  const { settings, keys, write } = useApiKeys()
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
    <ul className="flex flex-col overflow-hidden rounded-md border border-border">
      {keys.map((key, index) => (
        <li
          key={key}
          className="flex items-center gap-1 border-t border-border py-2 pr-2 pl-4 first:border-t-0"
        >
          <KeyRound size={16} aria-hidden className="mr-2 shrink-0 text-foreground-muted" />
          <code
            aria-label={fill(m.keyLabel, { index: index + 1 })}
            className="min-w-0 flex-1 truncate font-mono text-sm select-all"
          >
            {key}
          </code>
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
              aria-label={fill(m.deleteKey, { index: index + 1 })}
              className="size-control-sm enabled:hover:text-error-text"
              onClick={() => {
                const before = keys
                write(
                  keys.filter((item) => item !== key),
                  () => {
                    toast({
                      tone: 'success',
                      title: fill(m.deletedKey, { key: maskKey(key) }),
                      action: {
                        label: m.undo,
                        onClick: () => {
                          write(before)
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
  )
}
