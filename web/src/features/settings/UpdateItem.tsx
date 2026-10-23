import { RefreshCw } from 'lucide-react'
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogTrigger } from '@/components/ui/dialog'
import { toast } from '@/components/ui/toast'
import { useApplyUpdate, useCheckUpdate, useUpdateStatus } from '@/features/service/queries'
import { formatFullDateTime } from '@/lib/format'
import { defineMessages, fill, useI18n, useMessages } from '@/lib/i18n'

// updateMessages 是在线更新的文案
const updateMessages = defineMessages(
  {
    check: '检查更新',
    checking: '正在读取最新版本',
    upToDate: '已是最新版本',
    available: '新版本 {version}',
    published: '{time}发布',
    checkedAt: '{time}检查',
    updateTo: '更新到 {version}',
    confirmHint:
      '下载 {version} 的发行包并校验 SHA-256，替换程序后重启，页面在新版本就绪后自动刷新。',
    confirm: '确认更新',
    cancel: '取消',
    restarting: '正在重启到 {version}，页面在新版本就绪后自动刷新',
    development: '源码构建不支持在线更新，新版本从发布页下载',
    container: 'Docker 部署随镜像更新，拉取新镜像后重建容器',
    checkFailed: '无法读取最新版本：{error}',
    statusFailed: '无法读取更新状态：{error}',
  },
  {
    check: 'Check for updates',
    checking: 'Reading the latest version',
    upToDate: 'You are on the latest version',
    available: 'Version {version} is available',
    published: 'published {time}',
    checkedAt: 'checked {time}',
    updateTo: 'Update to {version}',
    confirmHint:
      'Downloads the {version} package, verifies its SHA-256, replaces the program and restarts; the page reloads once the new version is ready.',
    confirm: 'Update',
    cancel: 'Cancel',
    restarting: 'Restarting into {version}; the page reloads once the new version is ready',
    development: 'Builds from source are not updated online; download new versions from Releases',
    container:
      'Docker deployments update with the image; pull the new image and recreate the container',
    checkFailed: 'Could not read the latest version: {error}',
    statusFailed: 'Could not read the update status: {error}',
  },
  {
    check: '檢查更新',
    checking: '正在讀取最新版本',
    upToDate: '已是最新版本',
    available: '有新版本 {version}',
    published: '{time}發布',
    checkedAt: '{time}檢查',
    updateTo: '更新至 {version}',
    confirmHint:
      '下載 {version} 的發行套件並驗證 SHA-256，替換程式後重新啟動，新版本就緒後頁面會自動重新整理。',
    confirm: '確認更新',
    cancel: '取消',
    restarting: '正在重新啟動至 {version}，新版本就緒後頁面會自動重新整理',
    development: '從原始碼建置的版本不支援線上更新，請至發行版本頁面下載新版本',
    container: 'Docker 部署隨映像檔更新，請拉取新映像檔後重建容器',
    checkFailed: '無法讀取最新版本：{error}',
    statusFailed: '無法讀取更新狀態：{error}',
  },
)

// UpdateItem 是关于页的在线更新：读取 GitHub 最新发行版本，确认后下载校验并重启到新版本；读取更新状态失败时显示原因与检查更新
export function UpdateItem() {
  const m = useMessages(updateMessages)
  const { locale } = useI18n()
  const status = useUpdateStatus()
  const check = useCheckUpdate()
  const apply = useApplyUpdate()
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
  if (data.updating) {
    return <span role="status">{fill(m.restarting, { version: latest })}</span>
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
                    apply.mutate(undefined, {
                      onSuccess: () => {
                        setConfirming(false)
                      },
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
