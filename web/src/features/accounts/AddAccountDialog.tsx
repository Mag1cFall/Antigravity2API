import { type QueryClient, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  CircleAlert,
  CircleCheck,
  Download,
  ExternalLink,
  FileUp,
  Globe,
  KeyRound,
  LoaderCircle,
  type LucideIcon,
  Plus,
  Server,
  X,
} from 'lucide-react'
import { ToggleGroup } from 'radix-ui'
import { type ReactNode, type SyntheticEvent, useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { CopyButton } from '@/components/ui/copy-button'
import { Dialog, DialogContent, DialogTrigger } from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { toast } from '@/components/ui/toast'
import { keepAccounts, useImportAccounts } from '@/features/accounts/actions'
import { accountsKey, loginFailuresKey, loginKey, loginsKey } from '@/features/accounts/queries'
import { ImportAccountFiles } from '@/features/accounts/TransferDialog'
import type { Login, Pairing } from '@/features/accounts/types'
import { validProxy } from '@/features/settings/schema'
import { api, errorMessage } from '@/lib/api'
import { isDesktop } from '@/lib/desktop'
import { fill, serverText, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// countdown 把剩余毫秒向下取整输出为 m:ss
function countdown(ms: number) {
  const seconds = Math.max(0, Math.floor(ms / 1000))
  return String(Math.floor(seconds / 60)) + ':' + String(seconds % 60).padStart(2, '0')
}

// cancelLogin 取消进行中的登录，结果经事件流更新
function cancelLogin(id: string) {
  return api('/api/logins/' + encodeURIComponent(id), { method: 'DELETE' })
}

// dismissLoginFailure 移除一次登录的失败提示
function dismissLoginFailure(client: QueryClient, id: string) {
  client.setQueryData<Login[]>(loginFailuresKey, (failures = []) =>
    failures.filter((item) => item.id !== id),
  )
}

// LoginProgress 显示已生成的登录链接、等待状态、授权码输入与取消登录；关闭对话框后登录在后台继续
function LoginProgress({ login, onRestart }: { login: Login; onRestart: () => void }) {
  const m = useMessages('accounts')
  const client = useQueryClient()
  const now = useNow(1000)
  const [code, setCode] = useState('')
  const [error, setError] = useState<string>()
  const remaining = new Date(login.expires_at).getTime() - now.getTime()
  useEffect(() => {
    if (login.state === 'completed' && login.account) keepAccounts([login.account])
  }, [login.state, login.account])
  const cancel = useMutation({
    mutationFn: () => cancelLogin(login.id),
    onSuccess: onRestart,
    onError: (failure) => {
      toast({ tone: 'error', title: failure.message })
    },
  })
  const submit = useMutation({
    mutationFn: (value: string) =>
      api<Login>('/api/logins/' + encodeURIComponent(login.id) + '/code', {
        method: 'POST',
        json: { code: value },
      }),
    onSuccess: (result) => {
      client.setQueryData(loginKey(login.id), result)
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
          {login.state === 'failed'
            ? fill(m.failed, { message: serverText(login.error ?? '') })
            : m.expired}
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
          <span>· {fill(m.expiresIn, { time: countdown(remaining) })}</span>
        </p>
      )}
      <p className="text-xs text-foreground-muted">{m.loginKeepHint}</p>
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
            <span className="text-sm text-foreground-muted">
              {fill(m.expiresIn, { time: countdown(remaining) })}
            </span>
          ) : (
            <span />
          )}
          <span className="flex gap-2">
            <Button
              variant="ghost"
              loading={cancel.isPending}
              onClick={() => {
                cancel.mutate()
              }}
            >
              {m.loginCancel}
            </Button>
            <Button type="submit" loading={submit.isPending}>
              {login.mode === 'browser' ? m.submitCallback : m.submit}
            </Button>
          </span>
        </div>
      </form>
    </div>
  )
}

// RemotePairing 生成配对令牌，在有浏览器的电脑上运行 login --remote 完成登录后账户写入本服务；有效期内重新打开仍显示同一令牌，可随时换新
function RemotePairing() {
  const m = useMessages('accounts')
  const now = useNow(1000)
  const client = useQueryClient()
  const current = useQuery({
    queryKey: ['pairing'],
    queryFn: async () => (await api<Pairing | undefined>('/api/pairing')) ?? null,
  })
  const pairing = useMutation({
    mutationFn: () => api<Pairing>('/api/pairing', { method: 'POST' }),
    onSuccess: (created) => {
      client.setQueryData(['pairing'], created)
    },
  })
  const token = current.data
  if (!token) {
    return (
      <div className="flex flex-col items-start gap-3">
        <Button
          variant="primary"
          loading={pairing.isPending || current.isPending}
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
  const remaining = new Date(token.expires_at).getTime() - now.getTime()
  const command =
    'antigravity2api login --remote ' + window.location.origin + ' --token ' + token.token
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-start gap-1 rounded-md bg-muted p-3">
        <code className="min-w-0 flex-1 font-mono text-xs wrap-anywhere select-text">
          {command}
        </code>
        <CopyButton value={command} label={m.pairingCopy} copiedLabel={m.copied} />
      </div>
      <div className="flex items-center justify-between gap-3">
        <span className="text-sm text-foreground-muted">
          {remaining > 0 ? fill(m.expiresIn, { time: countdown(remaining) }) : m.pairingExpired}
        </span>
        <Button
          loading={pairing.isPending}
          onClick={() => {
            pairing.mutate()
          }}
        >
          {remaining > 0 ? m.pairingRenew : m.pairingStart}
        </Button>
      </div>
    </div>
  )
}

// Method 是添加账户的方式
type Method = 'browser' | 'code' | 'remote' | 'import' | 'json'

// methodIcons 是各添加方式的图标
export const methodIcons: Record<Method, LucideIcon> = {
  browser: Globe,
  code: KeyRound,
  remote: Server,
  import: Download,
  json: FileUp,
}

// methodGroups 是添加方式的分组与显示顺序：登录新的 Google 账户，导入已在其他地方登录的账户
export const methodGroups: { key: 'login' | 'import'; methods: Method[] }[] = [
  { key: 'login', methods: ['browser', 'code', 'remote'] },
  { key: 'import', methods: ['import', 'json'] },
]

// useMethodText 返回各添加方式与分组的名称与说明
export function useMethodText() {
  const m = useMessages('accounts')
  const methods: Record<Method, [string, string]> = {
    browser: [m.methodBrowser, m.methodBrowserHint],
    code: [m.methodCode, m.methodCodeHint],
    remote: [m.methodRemote, m.methodRemoteHint],
    import: [m.methodImport, m.methodImportHint],
    json: [m.methodJson, m.methodJsonHint],
  }
  return { methods, groups: { login: m.methodGroupLogin, import: m.methodGroupImport } }
}

// preferredMethod 是默认的添加方式：本机访问与桌面窗口用浏览器登录，远程访问用授权码
function preferredMethod(): Method {
  return isDesktop || ['localhost', '127.0.0.1', '[::1]'].includes(window.location.hostname)
    ? 'browser'
    : 'code'
}

// ImportOfficial 导入本机官方客户端已登录的账户
function ImportOfficial() {
  const m = useMessages('accounts')
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

// AddAccountDialog 是添加账户的五种方式：登录 Google（浏览器登录、粘贴授权码、远程配对）与导入已登录的账户（本机官方客户端、账户文件）；trigger 是打开对话框的按钮，method 是打开时选中的方式；给出 resume 时对话框创建即打开并续接这次登录，登录结束后显示结果直到关闭
export function AddAccountDialog({
  trigger,
  method,
  resume,
}: {
  trigger?: ReactNode
  method?: Method
  resume?: Login
}) {
  const m = useMessages('accounts')
  const app = useMessages('app')
  const text = useMethodText()
  const client = useQueryClient()
  const [open, setOpen] = useState(resume !== undefined)
  const [chosen, setChosen] = useState<Method | undefined>(resume?.mode)
  const [loginId, setLoginId] = useState(resume?.id)
  const [proxyValue, setProxyValue] = useState('')
  const proxy = proxyValue.trim()
  const proxyValid = proxy === 'direct' || validProxy(proxy)
  const current = chosen ?? method ?? preferredMethod()
  const create = useMutation({
    mutationFn: (value: 'browser' | 'code') =>
      api<Login>('/api/logins', { method: 'POST', json: { mode: value, proxy } }),
    onSuccess: (login) => {
      client.setQueryData(loginKey(login.id), login)
      setLoginId(login.id)
    },
  })
  const login = useQuery({
    queryKey: loginKey(loginId),
    queryFn: () => client.getQueryData<Login>(loginKey(loginId)) ?? null,
    enabled: false,
    initialData: loginId === resume?.id ? resume : undefined,
  })
  const reset = () => {
    if (loginId) dismissLoginFailure(client, loginId)
    setLoginId(undefined)
    create.reset()
  }
  const closeDialog = () => {
    reset()
    setChosen(undefined)
    setOpen(false)
  }
  const completed = login.data?.state === 'completed'
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (next) setOpen(true)
        else closeDialog()
      }}
    >
      {trigger && <DialogTrigger asChild>{trigger}</DialogTrigger>}
      <DialogContent
        width="wide"
        title={m.dialogTitle}
        closeLabel={app.closeDialog}
        footer={
          <Button variant={completed ? 'primary' : 'secondary'} onClick={closeDialog}>
            {completed ? m.done : m.close}
          </Button>
        }
      >
        <div className="flex flex-col gap-4">
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
              className="flex flex-col gap-3"
            >
              {methodGroups.map((group) => (
                <div key={group.key} className="flex flex-col gap-2">
                  <span className="text-xs font-medium text-foreground-muted">
                    {text.groups[group.key]}
                  </span>
                  <div
                    className={
                      'grid gap-2 ' + (group.methods.length === 3 ? 'grid-cols-3' : 'grid-cols-2')
                    }
                  >
                    {group.methods.map((item) => {
                      const Icon = methodIcons[item]
                      return (
                        <ToggleGroup.Item
                          key={item}
                          value={item}
                          aria-label={text.methods[item][0]}
                          className="flex min-w-0 items-start gap-2 rounded-md border border-border p-3 text-left hover:border-primary-border data-[state=on]:border-primary data-[state=on]:bg-primary-soft max-sm:flex-col"
                        >
                          <Icon size={18} aria-hidden className="shrink-0 text-primary" />
                          <span className="min-w-0">
                            <span className="block text-sm font-semibold">
                              {text.methods[item][0]}
                            </span>
                            <span className="mt-1 block text-xs text-foreground-muted max-sm:hidden">
                              {text.methods[item][1]}
                            </span>
                          </span>
                        </ToggleGroup.Item>
                      )
                    })}
                  </div>
                </div>
              ))}
            </ToggleGroup.Root>
          )}
          {current === 'json' ? (
            <ImportAccountFiles />
          ) : current === 'import' ? (
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
              <p className="text-sm text-foreground-muted sm:hidden">{text.methods[current][1]}</p>
              <div className="w-full">
                <Field
                  id="login-proxy"
                  label={m.loginProxyLabel}
                  description={m.proxyHint}
                  error={proxyValid ? undefined : m.proxyInvalid}
                >
                  <Input
                    id="login-proxy"
                    value={proxyValue}
                    onChange={(event) => {
                      setProxyValue(event.target.value)
                    }}
                    placeholder="socks5://127.0.0.1:1080"
                    spellCheck={false}
                    autoComplete="off"
                    data-1p-ignore
                    data-lpignore="true"
                    aria-invalid={proxyValid ? undefined : true}
                    aria-describedby={proxyValid ? 'login-proxy-description' : 'login-proxy-error'}
                    className="font-mono"
                  />
                </Field>
              </div>
              <Button
                variant="primary"
                loading={create.isPending}
                disabled={!proxyValid}
                onClick={() => {
                  const tab = isDesktop ? null : window.open('', '_blank')
                  create.mutate(current, {
                    onSuccess: (created) => {
                      if (tab) {
                        tab.opener = null
                        tab.location.href = created.url
                      } else {
                        window.open(created.url, '_blank')
                      }
                    },
                    onError: () => {
                      tab?.close()
                    },
                  })
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

// LoginTasks 在账户页列出关闭对话框后仍在进行的登录（方式、剩余时间、继续与取消）与对话框关着时失败或到期的登录（失败原因，可关掉）；“继续”打开的对话框不随任务行消失
export function LoginTasks() {
  const m = useMessages('accounts')
  const app = useMessages('app')
  const text = useMethodText()
  const now = useNow(1000)
  const client = useQueryClient()
  const [resumed, setResumed] = useState<{ login: Login; count: number }>()
  const logins = useQuery({ queryKey: loginsKey, queryFn: () => api<Login[]>('/api/logins') })
  const failures =
    useQuery({
      queryKey: loginFailuresKey,
      queryFn: () => client.getQueryData<Login[]>(loginFailuresKey) ?? [],
      staleTime: Number.POSITIVE_INFINITY,
    }).data ?? []
  const pending = (logins.data ?? []).filter(
    (login) => login.state === 'pending' && new Date(login.expires_at).getTime() > now.getTime(),
  )
  return (
    <>
      {pending.length + failures.length > 0 && (
        <ul
          aria-label={m.loginTasks}
          className="flex flex-col overflow-hidden rounded-md border border-border bg-surface"
        >
          {pending.map((login) => (
            <li
              key={login.id}
              data-login-task={login.id}
              className="flex flex-wrap items-center gap-x-3 gap-y-2 border-t border-border px-4 py-2 first:border-t-0"
            >
              <LoaderCircle size={16} aria-hidden className="animate-spin text-primary" />
              <span className="text-sm font-medium">{text.methods[login.mode][0]}</span>
              <span className="text-sm text-foreground-muted">
                {fill(m.expiresIn, {
                  time: countdown(new Date(login.expires_at).getTime() - now.getTime()),
                })}
              </span>
              <span className="ml-auto flex gap-2">
                <Button
                  size="sm"
                  onClick={() => {
                    setResumed((previous) => ({ login, count: (previous?.count ?? 0) + 1 }))
                  }}
                >
                  {m.loginResume}
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => {
                    void cancelLogin(login.id).catch((error: unknown) => {
                      toast({ tone: 'error', title: errorMessage(error) })
                    })
                  }}
                >
                  {m.loginCancel}
                </Button>
              </span>
            </li>
          ))}
          {failures.map((login) => (
            <li
              key={login.id}
              role="alert"
              data-login-failure={login.id}
              className="flex items-center gap-3 border-t border-border py-2 pr-2 pl-4 first:border-t-0"
            >
              <CircleAlert size={16} aria-hidden className="shrink-0 text-error-text" />
              <span className="min-w-0 flex-1 text-sm break-words">
                <span className="font-medium">{text.methods[login.mode][0]}</span>
                <span className="text-error-text">
                  {' · ' + fill(m.failed, { message: serverText(login.error ?? '') })}
                </span>
              </span>
              <Button
                size="sm"
                variant="ghost"
                aria-label={app.dismiss}
                onClick={() => {
                  dismissLoginFailure(client, login.id)
                }}
              >
                <X size={16} aria-hidden />
              </Button>
            </li>
          ))}
        </ul>
      )}
      {resumed && <AddAccountDialog key={resumed.count} resume={resumed.login} />}
    </>
  )
}

// AddAccountButton 是打开添加账户对话框的主按钮
export function AddAccountButton() {
  const m = useMessages('accounts')
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
