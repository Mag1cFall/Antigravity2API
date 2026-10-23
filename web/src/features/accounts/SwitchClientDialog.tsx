import { ArrowRightLeft, CircleCheck, LoaderCircle } from 'lucide-react'
import { useState } from 'react'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { Dialog, DialogContent, DialogTrigger } from '@/components/ui/dialog'
import { Tooltip } from '@/components/ui/tooltip'
import { useSwitchAccount } from '@/features/accounts/actions'
import { accountMessages } from '@/features/accounts/messages'
import { fill, useMessages } from '@/lib/i18n'

// SwitchClientDialog 确认后把本机官方客户端切换到账户 email 并重新打开，进度与结果只在对话框中显示；variant 为 icon 时触发按钮是带提示的图标按钮
export function SwitchClientDialog({
  email,
  variant,
  primary = false,
}: {
  email: string
  variant: 'icon' | 'button'
  primary?: boolean
}) {
  const m = useMessages(accountMessages)
  const app = useMessages(appMessages)
  const [open, setOpen] = useState(false)
  const switching = useSwitchAccount()
  const start = () => {
    switching.mutate(email)
  }
  const close = (next: boolean) => {
    if (switching.isPending) return
    if (!next) switching.reset()
    setOpen(next)
  }
  return (
    <Dialog open={open} onOpenChange={close}>
      {variant === 'icon' ? (
        <Tooltip label={m.switchClient}>
          <DialogTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              aria-label={fill(m.switchClientOf, { email })}
              className="size-control-sm"
            >
              <ArrowRightLeft size={16} aria-hidden />
            </Button>
          </DialogTrigger>
        </Tooltip>
      ) : (
        <DialogTrigger asChild>
          <Button variant={primary ? 'primary' : 'secondary'} size="sm">
            <ArrowRightLeft size={16} aria-hidden />
            {m.switchClient}
          </Button>
        </DialogTrigger>
      )}
      <DialogContent
        title={m.switchTitle}
        closeLabel={app.closeDialog}
        footer={
          switching.isSuccess ? (
            <Button
              variant="primary"
              onClick={() => {
                close(false)
              }}
            >
              {m.done}
            </Button>
          ) : (
            <>
              <Button
                disabled={switching.isPending}
                onClick={() => {
                  close(false)
                }}
              >
                {switching.isError ? m.close : m.cancel}
              </Button>
              <Button variant="primary" loading={switching.isPending} onClick={start}>
                {switching.isError ? m.switchRetry : m.switchConfirm}
              </Button>
            </>
          )
        }
      >
        {switching.isSuccess ? (
          <div role="status" className="flex flex-col items-center gap-3 py-4 text-center">
            <span className="flex size-12 items-center justify-center rounded-lg bg-success-soft text-success-text">
              <CircleCheck size={20} aria-hidden />
            </span>
            <p className="text-base font-medium wrap-anywhere">
              {fill(m.switchDone, { email: switching.data.account })}
            </p>
          </div>
        ) : (
          <div className="flex flex-col gap-4">
            <p className="text-sm wrap-anywhere text-foreground-muted">
              {fill(m.switchDescription, { email })}
            </p>
            {switching.isPending && (
              <p role="status" className="flex items-center gap-2 text-sm">
                <LoaderCircle size={16} aria-hidden className="shrink-0 animate-spin" />
                {m.switchRunning}
              </p>
            )}
            {switching.isError && (
              <Callout tone="error" role="alert">
                <p className="break-words">
                  {fill(m.switchFailed, { message: switching.error.message })}
                </p>
              </Callout>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
