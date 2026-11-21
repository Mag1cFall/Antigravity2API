import { ExternalLink, RefreshCw } from 'lucide-react'
import { useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogTrigger } from '@/components/ui/dialog'
import { Meter } from '@/components/ui/meter'
import { toast } from '@/components/ui/toast'
import { useApplyUpdate, useCheckUpdate, useUpdateStatus } from '@/features/service/queries'
import { formatFullDateTime } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'
import { markdown } from '@/lib/markdown'

// restartTimeout 是在线更新后等新版本接管的毫秒数，超过后提示新版本没有启动
const restartTimeout = 60_000

// UpdateItem 是关于页的在线更新：读取 GitHub 最新发行版本，确认框显示该版本的发行说明与发布页链接，确认后下载校验并重启到新版本，重启后 restartTimeout 内新版本没有接管时提示重新打开并给出日志目录；读取更新状态失败时显示原因与检查更新
export function UpdateItem() {
  const m = useMessages('update')
  const { locale } = useI18n()
  const check = useCheckUpdate()
  const apply = useApplyUpdate()
  const status = useUpdateStatus(apply.isPending)
  const [confirming, setConfirming] = useState(false)
  const [stalled, setStalled] = useState(false)
  const data = status.data
  const restarting =
    !!data && (apply.isPending || data.updating) && data.progress?.stage === 'restart'
  useEffect(() => {
    if (!restarting) return
    const timer = setTimeout(() => {
      setStalled(true)
    }, restartTimeout)
    return () => {
      clearTimeout(timer)
    }
  }, [restarting])
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
      return stalled ? (
        <span role="alert" className="text-error-text">
          {fill(m.restartStalled, { path: data.log_dir })}
        </span>
      ) : (
        <span role="status">{fill(m.restarting, { version: latest })}</span>
      )
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
            width="wide"
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
            {data.notes && (
              <div
                className="[&_ol]:pl-5 [&_ul]:pl-5 [&_code]:rounded max-h-[50vh] overflow-y-auto text-sm leading-6 [overflow-wrap:anywhere] [&_a]:text-primary [&_a]:underline [&_code]:bg-muted [&_code]:px-1 [&_code]:font-mono [&_code]:text-xs [&_h1]:mt-4 [&_h1]:font-semibold [&_h1:first-child]:mt-0 [&_h2]:mt-4 [&_h2]:font-semibold [&_h2:first-child]:mt-0 [&_h3]:mt-3 [&_h3]:font-semibold [&_li]:mt-1 [&_ol]:list-decimal [&_p]:mt-2 [&_table]:mt-2 [&_table]:w-full [&_td]:border [&_td]:border-border [&_td]:px-2 [&_td]:py-1 [&_th]:border [&_th]:border-border [&_th]:px-2 [&_th]:py-1 [&_th]:text-left [&_ul]:mt-2 [&_ul]:list-disc"
                dangerouslySetInnerHTML={{ __html: markdown.render(data.notes) }}
              />
            )}
            {data.url && (
              <Button variant="link" asChild>
                <a href={data.url} target="_blank" rel="noopener noreferrer" className="mt-3">
                  {m.releasePage}
                  <ExternalLink size={14} aria-hidden />
                </a>
              </Button>
            )}
          </DialogContent>
        </Dialog>
      )}
      {checkButton}
    </span>
  )
}
