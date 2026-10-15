import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { Navigate, useNavigate, useSearchParams } from 'react-router'
import { z } from 'zod'

import { Header } from '@/app/Header'
import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Loading } from '@/components/ui/states'
import { sessionKey, useSession } from '@/features/service/queries'
import type { Session } from '@/features/service/types'
import { api, ApiError } from '@/lib/api'
import { defineMessages, fill, useMessages } from '@/lib/i18n'

// loginMessages 是管理登录页的文案
const loginMessages = defineMessages(
  {
    title: '登录管理端',
    description: '从其他电脑访问需要管理用户名与密码。',
    username: '用户名',
    password: '密码',
    submit: '登录',
    required: '不能为空。',
    invalid: '用户名或密码错误。',
    limited: '登录失败次数过多，请一分钟后再试。',
    disabled: '远程登录未开启。在服务所在电脑上打开管理端，在“设置 › 远程访问”中开启。',
    failed: '登录失败：{message}',
  },
  {
    title: 'Sign in to the console',
    description:
      'Opening the console from another computer requires the admin username and password.',
    username: 'Username',
    password: 'Password',
    submit: 'Sign in',
    required: 'Required.',
    invalid: 'Wrong username or password.',
    limited: 'Too many failed attempts. Try again in a minute.',
    disabled:
      'Remote sign-in is off. Open the console on the computer running the service and turn it on under Settings › Remote access.',
    failed: 'Sign-in failed: {message}',
  },
  {
    title: '登入管理介面',
    description: '從其他電腦存取時，需要輸入管理員使用者名稱與密碼。',
    username: '使用者名稱',
    password: '密碼',
    submit: '登入',
    required: '此欄位為必填。',
    invalid: '使用者名稱或密碼錯誤。',
    limited: '登入失敗次數過多，請一分鐘後再試。',
    disabled: '遠端登入未啟用。請在執行服務的電腦上開啟管理介面，並在「設定 › 遠端存取」中啟用。',
    failed: '登入失敗：{message}',
  },
)

// LoginPage 是远程访问时的管理登录
export function LoginPage() {
  const m = useMessages(loginMessages)
  const app = useMessages(appMessages)
  const client = useQueryClient()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const session = useSession()
  const next = params.get('next') ?? '/'
  const schema = z.object({
    username: z.string().trim().min(1, m.required),
    password: z.string().min(1, m.required),
  })
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    defaultValues: { username: '', password: '' },
  })
  const login = useMutation({
    mutationFn: (credentials: z.infer<typeof schema>) =>
      api<Session>('/api/session', { method: 'POST', json: credentials }),
    onSuccess: async (result) => {
      client.setQueryData(sessionKey, result)
      await navigate(next.startsWith('/') ? next : '/', { replace: true })
    },
    onError: (error) => {
      const message =
        error instanceof ApiError && error.status === 401
          ? m.invalid
          : error instanceof ApiError && error.status === 429
            ? m.limited
            : fill(m.failed, { message: error.message })
      form.setError('root', { message })
    },
  })
  if (session.isPending) return <Loading label={app.loading} />
  if (session.data?.authenticated) return <Navigate to={next} replace />
  const errors = form.formState.errors
  return (
    <div className="flex min-h-dvh flex-col safe-x">
      <Header minimal />
      <main className="flex flex-1 items-start justify-center px-4 pt-16 pb-16">
        <div className="flex w-full max-w-login flex-col gap-6 rounded-lg bg-surface p-8 shadow-raised max-sm:p-6">
          <div>
            <h1 className="text-2xl font-semibold">{m.title}</h1>
            <p className="mt-1 text-sm text-foreground-muted">{m.description}</p>
          </div>
          {session.data?.enabled === false ? (
            <Callout tone="muted" role="status">
              {m.disabled}
            </Callout>
          ) : (
            <form
              noValidate
              className="flex flex-col gap-4"
              onSubmit={(event) =>
                void form.handleSubmit((credentials) => {
                  login.mutate(credentials)
                })(event)
              }
            >
              <Field id="username" label={m.username} error={errors.username?.message}>
                <Input
                  id="username"
                  autoComplete="username"
                  autoCapitalize="none"
                  spellCheck={false}
                  aria-invalid={errors.username ? true : undefined}
                  aria-describedby={errors.username ? 'username-error' : undefined}
                  {...form.register('username')}
                />
              </Field>
              <Field id="password" label={m.password} error={errors.password?.message}>
                <Input
                  id="password"
                  type="password"
                  autoComplete="current-password"
                  aria-invalid={errors.password ? true : undefined}
                  aria-describedby={errors.password ? 'password-error' : undefined}
                  {...form.register('password')}
                />
              </Field>
              {errors.root && (
                <p role="alert" className="text-sm text-error-text">
                  {errors.root.message}
                </p>
              )}
              <Button type="submit" variant="primary" size="lg" loading={login.isPending}>
                {m.submit}
              </Button>
            </form>
          )}
        </div>
      </main>
    </div>
  )
}
