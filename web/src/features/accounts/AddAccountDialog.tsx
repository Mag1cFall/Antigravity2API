import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  CircleCheck,
  Download,
  ExternalLink,
  Globe,
  KeyRound,
  LoaderCircle,
  type LucideIcon,
  Plus,
  Server,
} from 'lucide-react'
import { ToggleGroup } from 'radix-ui'
import { type ReactNode, type SyntheticEvent, useState } from 'react'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { CopyButton } from '@/components/ui/copy-button'
import { Dialog, DialogContent, DialogTrigger } from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { useImportAccounts } from '@/features/accounts/actions'
import { accountMessages } from '@/features/accounts/messages'
import { accountsKey } from '@/features/accounts/queries'
import type { Login, Pairing } from '@/features/accounts/types'
import { api } from '@/lib/api'
import { isDesktop } from '@/lib/desktop'
import { fill, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// countdown 把剩余毫秒输出为 m:ss
function countdown(ms: number) {
  const seconds = Math.max(0, Math.ceil(ms / 1000))
  return String(Math.floor(seconds / 60)) + ':' + String(seconds % 60).padStart(2, '0')
}

// LoginProgress 显示已生成的登录链接、等待状态与授权码输入
function LoginProgress({ login, onRestart }: { login: Login; onRestart: () => void }) {
  const m = useMessages(accountMessages)
  const client = useQueryClient()
  const now = useNow(1000)
  const [code, setCode] = useState('')
  const [error, setError] = useState<string>()
  const remaining = new Date(login.expires_at).getTime() - now.getTime()
  const submit = useMutation({
    mutationFn: (value: string) =>
      api<Login>('/api/logins/' + encodeURIComponent(login.id) + '/code', {
        method: 'POST',
        json: { code: value },
      }),
    onSuccess: (result) => {
      client.setQueryData(['login', login.id], result)
      void client.invalidateQueries({ queryKey: accountsKey })
    },
    onError: (failure) => {
      setError(failure.message)
    },
  })
  const onSubmit = (event: SyntheticEvent) => {
    event.preventDefault()
    if (!code.trim()) {
      setError(m.codeRequired)
      return
    }
    setError(undefined)
    submit.mutate(code.trim())
  }

  if (login.state === 'completed') {
    return (
      <div className="flex flex-col items-center gap-3 py-6 text-center">
        <span className="flex size-12 items-center justify-center rounded-lg bg-success-soft text-success-text">
          <CircleCheck size={20} aria-hidden />
        </span>
        <p className="text-md font-semibold wrap-anywhere">
          {fill(m.completed, { account: login.account ?? '' })}
        </p>
        <p className="text-sm text-foreground-muted">{m.completedHint}</p>
      </div>
    )
  }
  if (login.state === 'failed' || login.state === 'canceled' || remaining <= 0) {
    return (
      <div role="alert" className="flex flex-col items-start gap-3">
        <p className="text-sm break-words text-error-text">
          {login.state === 'failed' ? fill(m.failed, { message: login.error ?? '' }) : m.expired}
        </p>
        <Button onClick={onRestart}>{m.restart}</Button>
      </div>
    )
  }
  const codeId = 'login-code'
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="primary" asChild>
          <a href={login.url} target="_blank" rel="noopener noreferrer">
            <ExternalLink size={16} aria-hidden />
            {m.openGoogle}
          </a>
        </Button>
        <CopyButton value={login.url} label={m.copyLink} copiedLabel={m.copied} />
      </div>
      {login.mode === 'browser' && (
        <p role="status" className="flex items-center gap-2 text-sm text-foreground-muted">
          <LoaderCircle size={16} aria-hidden className="animate-spin" />
          {m.waiting}
          <span className="tabular-nums">
            · {fill(m.expiresIn, { time: countdown(remaining) })}
          </span>
        </p>
      )}
      <form noValidate onSubmit={onSubmit} className="flex flex-col gap-3">
        <Field
          id={codeId}
          label={login.mode === 'browser' ? m.callbackLabel : m.codeLabel}
          error={error}
        >
          <Input
            id={codeId}
            value={code}
            autoComplete="off"
            spellCheck={false}
            placeholder={login.mode === 'browser' ? m.callbackPlaceholder : m.codePlaceholder}
            aria-invalid={error ? true : undefined}
            aria-describedby={error ? codeId + '-error' : undefined}
            onChange={(event) => {
              setCode(event.target.value)
            }}
            className="font-mono"
          />
        </Field>
        <div className="flex items-center justify-between gap-3">
          {login.mode === 'code' ? (
            <span className="text-sm text-foreground-muted tabular-nums">
              {fill(m.expiresIn, { time: countdown(remaining) })}
            </span>
          ) : (
            <span />
          )}
          <Button type="submit" loading={submit.isPending}>
            {login.mode === 'browser' ? m.submitCallback : m.submit}
          </Button>
        </div>
      </form>
    </div>
  )
}

// RemotePairing 生成配对令牌，在有浏览器的电脑上运行 login --remote 完成登录后账户写入本服务
function RemotePairing() {
  const m = useMessages(accountMessages)
  const now = useNow(1000)
  const pairing = useMutation({
    mutationFn: () => api<Pairing>('/api/pairing', { method: 'POST' }),
  })
  if (!pairing.data) {
    return (
      <div className="flex flex-col items-start gap-3">
        <Button
          variant="primary"
          loading={pairing.isPending}
          onClick={() => {
            pairing.mutate()
          }}
        >
          {m.pairingStart}
        </Button>
        {pairing.isError && (
          <p role="alert" className="text-sm wrap-anywhere text-error-text">
            {fill(m.failed, { message: pairing.error.message })}
          </p>
        )}
      </div>
    )
  }
  const remaining = new Date(pairing.data.expires_at).getTime() - now.getTime()
  const command =
    'antigravity2api login --remote ' + window.location.origin + ' --token ' + pairing.data.token
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-start gap-1 rounded-md bg-muted p-3">
        <code className="min-w-0 flex-1 font-mono text-xs wrap-anywhere select-text">
          {command}
        </code>
        <CopyButton value={command} label={m.pairingCopy} copiedLabel={m.copied} />
      </div>
      <div className="flex items-center justify-between gap-3">
        <span className="text-sm text-foreground-muted tabular-nums">
          {remaining > 0 ? fill(m.expiresIn, { time: countdown(remaining) }) : m.pairingExpired}
        </span>
        {remaining <= 0 && (
          <Button
            onClick={() => {
              pairing.mutate()
            }}
          >
            {m.pairingStart}
          </Button>
        )}
      </div>
    </div>
  )
}

// Method 是添加账户的方式
export type Method = 'browser' | 'code' | 'import' | 'remote'

// methodIcons 是各添加方式的图标
export const methodIcons: Record<Method, LucideIcon> = {
  browser: Globe,
  code: KeyRound,
  import: Download,
  remote: Server,
}

// methods 是添加方式的显示顺序
export const methods: Method[] = ['browser', 'code', 'import', 'remote']

// useMethodText 返回各添加方式的名称与说明
export function useMethodText(): Record<Method, [string, string]> {
  const m = useMessages(accountMessages)
  return {
    browser: [m.methodBrowser, m.methodBrowserHint],
    code: [m.methodCode, m.methodCodeHint],
    import: [m.methodImport, m.methodImportHint],
    remote: [m.methodRemote, m.methodRemoteHint],
  }
}

// preferredMethod 是默认的添加方式：本机访问与桌面窗口用浏览器登录，远程访问用授权码
function preferredMethod(): Method {
  return isDesktop || ['localhost', '127.0.0.1', '[::1]'].includes(window.location.hostname)
    ? 'browser'
    : 'code'
}

// ImportOfficial 导入本机官方客户端已登录的账户
function ImportOfficial() {
  const m = useMessages(accountMessages)
  const importing = useImportAccounts()
  return (
    <div className="flex flex-col items-start gap-3">
      <Button
        variant="primary"
        loading={importing.isPending}
        onClick={() => {
          importing.mutate()
        }}
      >
        <Download size={16} aria-hidden />
        {m.importStart}
      </Button>
      {importing.isSuccess && (
        <p role="status" className="text-sm text-success-text">
          {importing.data.accounts.length
            ? fill(m.imported, { count: importing.data.accounts.length })
            : m.importedNone}
        </p>
      )}
      {importing.isError && (
        <p role="alert" className="text-sm break-words text-error-text">
          {fill(m.importFailed, { message: importing.error.message })}
        </p>
      )}
    </div>
  )
}

// AddAccountDialog 是添加账户的四种方式：浏览器登录、粘贴授权码、导入本机官方客户端与远程配对；trigger 是打开对话框的按钮，method 是打开时选中的方式
export function AddAccountDialog({ trigger, method }: { trigger: ReactNode; method?: Method }) {
  const m = useMessages(accountMessages)
  const app = useMessages(appMessages)
  const text = useMethodText()
  const client = useQueryClient()
  const [open, setOpen] = useState(false)
  const [chosen, setChosen] = useState<Method>()
  const [loginId, setLoginId] = useState<string>()
  const current = chosen ?? method ?? preferredMethod()
  const create = useMutation({
    mutationFn: (value: 'browser' | 'code') =>
      api<Login>('/api/logins', { method: 'POST', json: { mode: value } }),
    onSuccess: (login) => {
      client.setQueryData(['login', login.id], login)
      setLoginId(login.id)
    },
  })
  const login = useQuery({
    queryKey: ['login', loginId],
    queryFn: () => client.getQueryData<Login>(['login', loginId]) ?? null,
    enabled: false,
  })
  const reset = () => {
    setLoginId(undefined)
    create.reset()
  }
  const close = (next: boolean) => {
    if (!next && loginId && login.data?.state === 'pending') {
      void api('/api/logins/' + encodeURIComponent(loginId), { method: 'DELETE' }).catch(
        () => undefined,
      )
    }
    if (!next) {
      reset()
      setChosen(undefined)
    }
    setOpen(next)
  }
  const completed = login.data?.state === 'completed'
  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent
        wide
        title={m.dialogTitle}
        description={m.dialogDescription}
        closeLabel={app.closeDialog}
        footer={
          <Button
            variant={completed ? 'primary' : 'secondary'}
            onClick={() => {
              close(false)
            }}
          >
            {completed ? m.done : m.close}
          </Button>
        }
      >
        <div className="flex flex-col gap-6">
          {!completed && (
            <ToggleGroup.Root
              type="single"
              value={current}
              onValueChange={(value) => {
                if (!value) return
                setChosen(value as Method)
                reset()
              }}
              aria-label={m.methodLabel}
              className="grid grid-cols-2 gap-2"
            >
              {methods.map((item) => {
                const Icon = methodIcons[item]
                return (
                  <ToggleGroup.Item
                    key={item}
                    value={item}
                    aria-label={text[item][0]}
                    className="flex min-w-0 items-start gap-3 rounded-md border border-border p-3 text-left hover:border-primary-border data-[state=on]:border-primary data-[state=on]:bg-primary-soft"
                  >
                    <Icon size={20} aria-hidden className="shrink-0 text-primary" />
                    <span className="min-w-0">
                      <span className="block text-sm font-semibold">{text[item][0]}</span>
                      <span className="mt-1 block text-xs text-foreground-muted max-sm:hidden">
                        {text[item][1]}
                      </span>
                    </span>
                  </ToggleGroup.Item>
                )
              })}
            </ToggleGroup.Root>
          )}
          {current === 'import' ? (
            <ImportOfficial />
          ) : current === 'remote' ? (
            <div className="flex flex-col gap-3">
              <p className="text-sm text-foreground-muted">{m.remoteHint}</p>
              <RemotePairing />
            </div>
          ) : login.data ? (
            <LoginProgress login={login.data} onRestart={reset} />
          ) : (
            <div className="flex flex-col items-start gap-3">
              <p className="text-sm text-foreground-muted">{text[current][1]}</p>
              <Button
                variant="primary"
                loading={create.isPending}
                onClick={() => {
                  create.mutate(current)
                }}
              >
                {m.start}
              </Button>
              {create.isError && (
                <p role="alert" className="text-sm break-words text-error-text">
                  {fill(m.failed, { message: create.error.message })}
                </p>
              )}
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}

// AddAccountButton 是打开添加账户对话框的主按钮
export function AddAccountButton() {
  const m = useMessages(accountMessages)
  return (
    <AddAccountDialog
      trigger={
        <Button variant="primary">
          <Plus size={16} aria-hidden />
          {m.add}
        </Button>
      }
    />
  )
}
