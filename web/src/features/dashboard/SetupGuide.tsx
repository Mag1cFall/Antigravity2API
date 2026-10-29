import { Check, X } from 'lucide-react'
import type { ReactNode } from 'react'
import { Link } from 'react-router'

import logo from '@/assets/logo.png'
import { Button } from '@/components/ui/button'
import { Card, CardBody } from '@/components/ui/card'
import {
  AddAccountDialog,
  methodIcons,
  methods,
  useMethodText,
} from '@/features/accounts/AddAccountDialog'
import { ApiKeyList } from '@/features/connect/ApiKeys'
import { ClientConfigs } from '@/features/connect/ClientConfigs'
import { EndpointList } from '@/features/connect/EndpointList'
import { connectMessages } from '@/features/connect/messages'
import { markProgress } from '@/features/connect/progress'
import { fill, useMessages } from '@/lib/i18n'

// SetupSteps 是三步引导各步是否完成
export interface SetupSteps {
  accounts: boolean
  endpoint: boolean
  client: boolean
}

// StepMarker 是步骤序号，完成后显示对勾
function StepMarker({ index, done, label }: { index: number; done: boolean; label: string }) {
  return done ? (
    <span
      role="img"
      aria-label={label}
      className="flex size-8 shrink-0 items-center justify-center rounded-full bg-success text-white"
    >
      <Check size={16} strokeWidth={3} aria-hidden />
    </span>
  ) : (
    <span
      aria-hidden
      className="flex size-8 shrink-0 items-center justify-center rounded-full border-2 border-primary text-sm font-semibold text-primary"
    >
      {index}
    </span>
  )
}

// Step 是引导中的一步：序号、标题、说明与内容
function Step({
  index,
  done,
  title,
  hint,
  children,
}: {
  index: number
  done: boolean
  title: string
  hint: string
  children: ReactNode
}) {
  const m = useMessages(connectMessages)
  return (
    <Card aria-label={title}>
      <div className="flex items-start gap-4 px-6 pt-6 pb-4 max-sm:px-4 max-sm:pt-4">
        <StepMarker index={index} done={done} label={m.stepDone} />
        <div className="min-w-0">
          <h2 className="text-md font-semibold">{title}</h2>
          <p className="mt-1 text-sm text-foreground-muted">{hint}</p>
        </div>
      </div>
      <CardBody className="sm:pl-[72px]">{children}</CardBody>
    </Card>
  )
}

// AccountMethods 是添加账户的四种方式，点击打开选中该方式的添加账户对话框
function AccountMethods() {
  const text = useMethodText()
  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
      {methods.map((method) => {
        const Icon = methodIcons[method]
        return (
          <AddAccountDialog
            key={method}
            method={method}
            trigger={
              <button
                type="button"
                className="flex min-w-0 items-start gap-3 rounded-md border border-border bg-surface p-4 text-left hover:border-primary-border hover:bg-primary-soft"
              >
                <span className="flex size-control-sm shrink-0 items-center justify-center rounded-md bg-info-soft text-primary">
                  <Icon size={16} aria-hidden />
                </span>
                <span className="min-w-0">
                  <span className="block text-sm font-semibold">{text[method][0]}</span>
                  <span className="mt-1 block text-xs text-foreground-muted">
                    {text[method][1]}
                  </span>
                </span>
              </button>
            }
          />
        )
      })}
    </div>
  )
}

// SetupGuide 是首次使用的仪表盘：添加账户、复制接口地址与 API key、选择客户端复制配置三步，每步完成后打勾；添加账户后可进入总览
export function SetupGuide({ steps, onEnter }: { steps: SetupSteps; onEnter: () => void }) {
  const m = useMessages(connectMessages)
  return (
    <>
      <header className="flex flex-col items-center gap-3 pt-6 pb-2 text-center">
        <img src={logo} alt="" width={56} height={56} className="size-[56px]" />
        <h1 className="text-2xl font-semibold">{m.welcome}</h1>
        <p className="max-w-prose text-sm text-foreground-muted">{m.welcomeHint}</p>
        <Button
          variant="primary"
          onClick={onEnter}
          className={steps.accounts ? 'animate-fade-in' : 'invisible'}
        >
          {m.guideEnter}
        </Button>
      </header>
      <Step index={1} done={steps.accounts} title={m.stepAccounts} hint={m.stepAccountsHint}>
        <AccountMethods />
      </Step>
      <Step index={2} done={steps.endpoint} title={m.stepEndpoint} hint={m.stepEndpointHint}>
        <div className="grid grid-cols-1 items-start gap-4 xl:grid-cols-2">
          <EndpointList />
          <ApiKeyList />
        </div>
      </Step>
      <Step index={3} done={steps.client} title={m.stepClient} hint={m.stepClientHint}>
        <ClientConfigs />
      </Step>
    </>
  )
}

// SetupBanner 是有账户但还没完成接入时仪表盘顶部的进度条，可隐藏
export function SetupBanner({ steps }: { steps: SetupSteps }) {
  const m = useMessages(connectMessages)
  const items: [string, boolean][] = [
    [m.stepAccounts, steps.accounts],
    [m.stepEndpoint, steps.endpoint],
    [m.stepClient, steps.client],
  ]
  const left = items.filter(([, done]) => !done).length
  return (
    <Card aria-label={m.guideTitle}>
      <div className="flex flex-wrap items-center gap-x-8 gap-y-4 px-6 py-4 max-sm:px-4">
        <div className="min-w-0">
          <h2 className="text-base font-semibold">{m.guideTitle}</h2>
          <p className="text-xs text-foreground-muted">{fill(m.guideHint, { count: left })}</p>
        </div>
        <ol className="flex flex-1 flex-wrap items-center gap-x-6 gap-y-2">
          {items.map(([label, done], index) => (
            <li key={label} className="flex items-center gap-2 text-sm">
              <StepMarker index={index + 1} done={done} label={m.stepDone} />
              <span className={done ? 'text-foreground-muted line-through' : 'text-foreground'}>
                {label}
              </span>
            </li>
          ))}
        </ol>
        <div className="flex items-center gap-2">
          <Button variant="primary" size="sm" asChild>
            <Link to="/connect">{m.guideContinue}</Link>
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              markProgress({ dismissed: true })
            }}
          >
            <X size={16} aria-hidden />
            {m.guideHide}
          </Button>
        </div>
      </div>
    </Card>
  )
}
