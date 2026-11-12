import { RefreshCw } from 'lucide-react'
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogTrigger } from '@/components/ui/dialog'
import { Meter } from '@/components/ui/meter'
import { toast } from '@/components/ui/toast'
import { useApplyUpdate, useCheckUpdate, useUpdateStatus } from '@/features/service/queries'
import { formatFullDateTime } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// UpdateItem 是关于页的在线更新：读取 GitHub 最新发行版本，确认后下载校验并重启到新版本；读取更新状态失败时显示原因与检查更新
export function UpdateItem() {
  const m = useMessages('update')
  const { locale } = useI18n()
  const check = useCheckUpdate()
  const apply = useApplyUpdate()
  const status = useUpdateStatus(apply.isPending)
  const [confirming, setConfirming] = useState(false)
  const data = status.data
  const checkButton = (
    <Button
      variant="ghost"
      size="sm"
      loading={check.isPending}
      onClick={() => {
        check.mutate(undefined, {
          onError: (error) => {
            toast({ tone: 'error', title: error.message })
          },
        })
      }}
    >
      <RefreshCw size={16} aria-hidden />
      {m.check}
    </Button>
  )
  if (!data) {
    return status.isError ? (
      <span className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <span role="alert" className="text-error-text">
          {fill(m.statusFailed, { error: status.error.message })}
        </span>
        {checkButton}
      </span>
    ) : (
      <span className="text-foreground-muted">{m.checking}</span>
    )
  }
  if (data.mode !== 'binary') {
    return (
      <span className="text-foreground-muted">
        {data.mode === 'container' ? m.container : m.development}
      </span>
    )
  }
  const latest = data.latest ?? ''
  const progress = data.progress
  if (apply.isPending || data.updating) {
    if (progress?.stage === 'restart') {
      return <span role="status">{fill(m.restarting, { version: latest })}</span>
    }
    const megabytes = new Intl.NumberFormat(locale, {
      style: 'unit',
      unit: 'megabyte',
      maximumFractionDigits: 1,
    })
    const downloaded = progress ? megabytes.format(progress.downloaded / 1e6) : ''
    return (
      <span className="flex w-full max-w-field flex-col gap-2">
        <span role="status" className="flex justify-between gap-3">
          <span>
            {fill(
              progress?.stage === 'verify'
                ? m.verifying
                : progress?.stage === 'install'
                  ? m.installing
                  : m.downloading,
              { version: latest },
            )}
          </span>
          <span className="text-foreground-muted tabular-nums">
            {progress && progress.total > 0
              ? downloaded + ' / ' + megabytes.format(progress.total / 1e6)
              : downloaded}
          </span>
        </span>
        <Meter
          progress
          tone="info"
          label={m.progress}
          value={progress && progress.total > 0 ? progress.downloaded / progress.total : 0}
        />
      </span>
    )
  }
  const when = (template: string, value?: string) =>
    value ? fill(template, { time: formatFullDateTime(new Date(value), locale) }) : ''
  const detail = data.available
    ? when(m.published, data.published_at)
    : when(m.checkedAt, data.checked_at)
  return (
    <span className="flex flex-wrap items-center gap-x-3 gap-y-2">
      {data.error ? (
        <span role="alert" className="text-error-text">
          {fill(m.checkFailed, { error: data.error })}
        </span>
      ) : (
        latest && (
          <span className={data.available ? 'text-warning-text' : undefined}>
            {data.available ? fill(m.available, { version: latest }) : m.upToDate}
            {detail && <span className="text-foreground-muted"> · {detail}</span>}
          </span>
        )
      )}
      {data.available && (
        <Dialog open={confirming} onOpenChange={setConfirming}>
          <DialogTrigger asChild>
            <Button variant="primary" size="sm">
              {fill(m.updateTo, { version: latest })}
            </Button>
          </DialogTrigger>
          <DialogContent
            title={fill(m.updateTo, { version: latest })}
            description={fill(m.confirmHint, { version: latest })}
            closeLabel={m.cancel}
            footer={
              <>
                <Button
                  onClick={() => {
                    setConfirming(false)
                  }}
                >
                  {m.cancel}
                </Button>
                <Button
                  variant="primary"
                  loading={apply.isPending}
                  onClick={() => {
                    setConfirming(false)
                    apply.mutate(undefined, {
                      onError: (error) => {
                        toast({ tone: 'error', title: error.message })
                      },
                    })
                  }}
                >
                  {m.confirm}
                </Button>
              </>
            }
          >
            <span className="sr-only">{fill(m.confirmHint, { version: latest })}</span>
          </DialogContent>
        </Dialog>
      )}
      {checkButton}
    </span>
  )
}
