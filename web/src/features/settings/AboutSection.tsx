import { ExternalLink } from 'lucide-react'
import type { ReactNode } from 'react'

import logo from '@/assets/logo.png'
import { Button } from '@/components/ui/button'
import { CopyButton } from '@/components/ui/copy-button'
import { ErrorState, Loading } from '@/components/ui/states'
import { useStatus } from '@/features/service/queries'
import { newerVersion } from '@/features/service/version'
import { UpdateItem } from '@/features/settings/UpdateItem'
import { formatFullDateTime, formatRelative } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

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
  const m = useMessages('about')
  const app = useMessages('app')
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
