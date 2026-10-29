import { ExternalLink } from 'lucide-react'
import type { ReactNode } from 'react'

import { appMessages } from '@/app/messages'
import logo from '@/assets/logo.png'
import { Button } from '@/components/ui/button'
import { CopyButton } from '@/components/ui/copy-button'
import { ErrorState, Loading } from '@/components/ui/states'
import { useStatus } from '@/features/service/queries'
import { newerVersion } from '@/features/service/version'
import { UpdateItem } from '@/features/settings/UpdateItem'
import { formatFullDateTime, formatRelative } from '@/lib/format'
import { defineMessages, fill, useI18n, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// aboutMessages 是关于页的文案
const aboutMessages = defineMessages(
  {
    title: '关于',
    tagline: 'Google Antigravity 的多账户 API 代理',
    version: '版本',
    update: '在线更新',
    host: '运行方式',
    hostWeb: '浏览器（serve）',
    hostDesktop: '桌面窗口',
    listen: '监听地址',
    started: '启动时间',
    startedAgo: '{time}启动',
    api: '客户端接口地址',
    client: '官方客户端版本',
    clientLatest: '最新 {latest}',
    clientUnknown: '尚未读取最新版本',
    license: '许可证',
    homepage: '项目主页',
    releases: '发布页',
    copy: '复制',
    copied: '已复制',
    copyVersion: '复制版本信息',
    loadFailed: '无法读取服务状态',
  },
  {
    title: 'About',
    tagline: 'Multi-account API proxy for Google Antigravity',
    version: 'Version',
    update: 'Updates',
    host: 'Running as',
    hostWeb: 'Browser (serve)',
    hostDesktop: 'Desktop window',
    listen: 'Listen address',
    started: 'Started',
    startedAgo: 'started {time}',
    api: 'Client base URL',
    client: 'Official client version',
    clientLatest: 'latest {latest}',
    clientUnknown: 'latest version not read yet',
    license: 'License',
    homepage: 'Project page',
    releases: 'Releases',
    copy: 'Copy',
    copied: 'Copied',
    copyVersion: 'Copy version info',
    loadFailed: 'Could not load service status',
  },
  {
    title: '關於',
    tagline: 'Google Antigravity 的多帳號 API 代理',
    version: '版本',
    update: '線上更新',
    host: '執行方式',
    hostWeb: '瀏覽器（serve）',
    hostDesktop: '桌面視窗',
    listen: '監聽位址',
    started: '啟動時間',
    startedAgo: '{time}啟動',
    api: '用戶端 API 網址',
    client: '官方用戶端版本',
    clientLatest: '最新版 {latest}',
    clientUnknown: '尚未取得最新版本',
    license: '授權條款',
    homepage: '專案首頁',
    releases: '發行版本',
    copy: '複製',
    copied: '已複製',
    copyVersion: '複製版本資訊',
    loadFailed: '無法讀取服務狀態',
  },
)

// repository 是项目主页
const repository = 'https://github.com/Mag1cFall/Antigravity2API'

// Item 是关于页的一行键值
function Item({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-h-12 flex-col justify-center gap-1 border-t border-border py-2 first:border-t-0 sm:flex-row sm:items-center sm:justify-start sm:gap-6">
      <dt className="text-sm text-foreground-muted sm:w-cell sm:shrink-0">{label}</dt>
      <dd className="flex min-w-0 items-center gap-1 text-sm wrap-anywhere">{children}</dd>
    </div>
  )
}

// AboutSection 是设置中的关于：版本、运行方式、接口地址与项目链接
export function AboutSection() {
  const m = useMessages(aboutMessages)
  const app = useMessages(appMessages)
  const { locale } = useI18n()
  const now = useNow(60_000)
  const status = useStatus()
  if (status.isPending) return <Loading label={app.loading} />
  if (status.isError) {
    return (
      <ErrorState
        title={m.loadFailed}
        error={status.error}
        retryLabel={app.retry}
        onRetry={() => void status.refetch()}
      />
    )
  }
  const data = status.data
  const base = window.location.origin + '/v1'
  const started = new Date(data.started_at)
  const summary = ['Antigravity2API ' + data.version, data.host, data.listen].join(' · ')
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-4">
        <img src={logo} alt="" width={64} height={64} className="size-16 shrink-0" />
        <div className="min-w-0">
          <p className="text-xl font-semibold">Antigravity2API</p>
          <p className="text-sm text-foreground-muted">{m.tagline}</p>
        </div>
      </div>
      <dl className="w-full">
        <Item label={m.version}>
          <span className="font-mono">{data.version}</span>
          <CopyButton value={summary} label={m.copyVersion} copiedLabel={m.copied} />
        </Item>
        <Item label={m.update}>
          <UpdateItem />
        </Item>
        <Item label={m.host}>{data.host === 'desktop' ? m.hostDesktop : m.hostWeb}</Item>
        <Item label={m.listen}>
          <span className="font-mono">{data.listen}</span>
        </Item>
        <Item label={m.api}>
          <span className="font-mono">{base}</span>
          <CopyButton value={base} label={m.copy} copiedLabel={m.copied} />
        </Item>
        <Item label={m.started}>
          {formatFullDateTime(started, locale)}
          <span className="text-foreground-muted">
            · {fill(m.startedAgo, { time: formatRelative(started, now, locale) })}
          </span>
        </Item>
        <Item label={m.client}>
          <span className="font-mono">{data.client_version}</span>
          <span
            className={
              newerVersion(data.latest_version, data.client_version)
                ? 'text-warning-text'
                : 'text-foreground-muted'
            }
          >
            ·{' '}
            {data.latest_version
              ? fill(m.clientLatest, { latest: data.latest_version })
              : m.clientUnknown}
          </span>
        </Item>
        <Item label={m.license}>MIT</Item>
      </dl>
      <div className="flex flex-wrap gap-3">
        <Button asChild>
          <a href={repository} target="_blank" rel="noopener noreferrer">
            <ExternalLink size={16} aria-hidden />
            {m.homepage}
          </a>
        </Button>
        <Button asChild>
          <a href={repository + '/releases'} target="_blank" rel="noopener noreferrer">
            <ExternalLink size={16} aria-hidden />
            {m.releases}
          </a>
        </Button>
      </div>
    </div>
  )
}
