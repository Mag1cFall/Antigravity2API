import { useQuery } from '@tanstack/react-query'
import { LoaderCircle } from 'lucide-react'
import { Fragment, type ReactNode } from 'react'
import { Link } from 'react-router'

import { useRunning } from '@/app/events'
import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { CopyButton } from '@/components/ui/copy-button'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import { EmptyState, ErrorState, Loading } from '@/components/ui/states'
import { DotList } from '@/components/ui/stats'
import { Table, Td, Th } from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { quotaMessages } from '@/features/quota/messages'
import { nameOf } from '@/features/quota/quota'
import { requestMessages } from '@/features/requests/messages'
import { RequestStateBadge } from '@/features/requests/RequestStateBadge'
import type { Body, RequestDetail } from '@/features/requests/types'
import { settingsMessages } from '@/features/settings/messages'
import { api } from '@/lib/api'
import {
  formatBytes,
  formatCost,
  formatDuration,
  formatFullDateTime,
  formatNumber,
} from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// pretty 把 JSON 正文缩进显示，SSE 等其他文本原样返回
function pretty(text: string) {
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return text
  }
}

// BodyView 显示一段保存的正文、原始大小与截断说明
function BodyView({ text, size }: { text: string; size: number }) {
  const m = useMessages(requestMessages)
  const { locale } = useI18n()
  const truncated = size > new TextEncoder().encode(text).length
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-3 text-xs text-foreground-muted">
        <span>
          {truncated
            ? fill(m.truncated, { size: formatBytes(size, locale) })
            : formatBytes(size, locale)}
        </span>
        <CopyButton value={text} label={m.copy} copiedLabel={m.copied} />
      </div>
      <pre className="max-h-dialog overflow-auto rounded-md bg-muted p-4 font-mono text-xs break-all whitespace-pre-wrap select-text">
        {pretty(text)}
      </pre>
    </div>
  )
}

// breakableURL 在 URL 的 /、.、?、&、= 与 : 之后放换行机会，长地址只在这些位置折行
function breakableURL(url: string) {
  return url.split(/(?<=[/.?&=:])/).map((part, index) => (
    <Fragment key={index}>
      {index > 0 && <wbr />}
      {part}
    </Fragment>
  ))
}

// Row 是概览中的一行键值
function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-1 gap-1 border-t border-border py-3 sm:grid-cols-3 sm:gap-4">
      <dt className="text-sm text-foreground-muted">{label}</dt>
      <dd className="min-w-0 text-sm break-words sm:col-span-2">{children}</dd>
    </div>
  )
}

// DetailBody 是请求详情的全部内容
function DetailBody({ detail }: { detail: RequestDetail }) {
  const m = useMessages(requestMessages)
  const q = useMessages(quotaMessages)
  const s = useMessages(settingsMessages)
  const { locale } = useI18n()
  const record = detail.record
  const n = (value: number) => formatNumber(value, locale)
  const typeNames: Record<string, string> = {
    agent: m.typeAgent,
    image_gen: m.typeImage,
    tab: m.typeTab,
    checkpoint: m.typeCheckpoint,
  }
  const tierNames: Record<string, string> = {
    full: s.promptFull,
    medium: s.promptMedium,
    aggressive: s.promptAggressive,
    custom: s.promptCustom,
  }
  const attempts = detail.upstream ?? []
  const bodies: { key: string; label: string; body: Body | null; side: 'request' | 'response' }[] =
    [
      { key: 'request', label: m.requestBody, body: detail.body, side: 'request' },
      { key: 'response', label: m.responseBody, body: detail.body, side: 'response' },
      ...attempts.flatMap((body, index) => {
        const suffix = attempts.length > 1 ? ' #' + String(index + 1) : ''
        return [
          {
            key: 'upstream-request-' + String(index),
            label: m.upstreamRequest + suffix,
            body,
            side: 'request' as const,
          },
          {
            key: 'upstream-response-' + String(index),
            label: m.upstreamResponse + suffix,
            body,
            side: 'response' as const,
          },
        ]
      }),
    ]
  const available = bodies.filter((item) => item.body)
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <RequestStateBadge state={record.state} />
        <span className={'text-sm ' + (record.status >= 400 ? 'text-error-text' : '')}>
          HTTP {record.status || m.none}
        </span>
        <span className="text-sm text-foreground-muted">
          {formatFullDateTime(new Date(record.time), locale)}
        </span>
        <Button variant="link" asChild>
          <Link
            to={'/requests/logs?view=saved&range=30d&request_id=' + encodeURIComponent(record.id)}
          >
            {m.viewLogs}
          </Link>
        </Button>
      </div>
      {record.error && (
        <Callout tone="error">
          <p className="font-mono text-xs break-words whitespace-pre-wrap select-text">
            {record.error}
          </p>
        </Callout>
      )}
      <dl className="border-b border-border">
        {record.error_phase && (
          <Row label={m.errorPhase}>
            {record.error_phase}
            {record.upstream_status > 0 && (
              <span className="text-foreground-muted">
                {' '}
                · {m.upstreamStatus} {record.upstream_status}
              </span>
            )}
            {record.error_code && (
              <span className="font-mono text-foreground-muted"> · {record.error_code}</span>
            )}
          </Row>
        )}
        <Row label={m.path}>
          {record.protocol} · <span className="font-mono">{record.path}</span>
        </Row>
        <Row label={m.publicModel}>
          <span className="font-mono">{record.model}</span>
        </Row>
        <Row label={m.upstreamModel}>
          <DotList
            items={[
              <span className="font-mono">{record.upstream_model || m.none}</span>,
              record.model_version && (
                <span className="text-foreground-muted">{record.model_version}</span>
              ),
            ].filter(Boolean)}
          />
        </Row>
        <Row label={m.account}>
          <DotList
            items={[
              record.account || m.none,
              record.quota_group && (
                <span className="text-foreground-muted">{nameOf(q, record.quota_group)}</span>
              ),
            ].filter(Boolean)}
          />
        </Row>
        <Row label={m.timing}>
          {[record.queue_ms, record.first_event_ms, record.duration_ms]
            .map((value) => formatDuration(value, locale))
            .join(' · ')}
        </Row>
        <Row label={m.tokenUsage}>
          <DotList
            items={[
              m.input + ' ' + n(record.input_tokens),
              m.cached + ' ' + n(record.cached_tokens),
              m.reasoning + ' ' + n(record.reasoning_tokens),
              m.reply + ' ' + n(record.reply_tokens),
              m.total +
                ' ' +
                n(record.input_tokens + record.reasoning_tokens + record.reply_tokens),
            ]}
          />
        </Row>
        <Row label={m.cost}>
          {record.cost_usd === null ? m.unpriced : formatCost(record.cost_usd, locale)}
        </Row>
        {record.tool_calls > 0 && <Row label={m.toolCalls}>{n(record.tool_calls)}</Row>}
        <Row label={m.stream}>{record.stream ? m.yes : m.no}</Row>
        {(record.request_type || record.prompt_tier) && (
          <Row label={m.requestType}>
            {[
              typeNames[record.request_type] ?? record.request_type,
              tierNames[record.prompt_tier] ?? record.prompt_tier,
            ]
              .filter(Boolean)
              .join(' · ')}
          </Row>
        )}
        {record.cascade_id && (
          <Row label={m.cascade}>
            <span className="font-mono">{record.cascade_id}</span>
            <span className="text-foreground-muted"> · {record.step_index}</span>
          </Row>
        )}
        <Row label={m.apiKey}>
          <span className="font-mono">{record.api_key || m.none}</span>
        </Row>
        <Row label={m.client}>
          <span className="font-mono">{record.client_ip}</span>
          {record.user_agent && (
            <span className="block text-xs text-foreground-muted">{record.user_agent}</span>
          )}
        </Row>
        <Row label={m.responseId}>
          <span className="font-mono">{record.response_id || m.none}</span>
        </Row>
        {record.client_request_id && (
          <Row label={m.clientRequestId}>
            <span className="font-mono">{record.client_request_id}</span>
          </Row>
        )}
      </dl>
      {record.attempts && record.attempts.length > 0 && (
        <section className="flex flex-col gap-3">
          <h3 className="text-md font-semibold">{m.attempts}</h3>
          <Table label={m.attempts} framed stack>
            <thead>
              <tr>
                <Th>{m.account}</Th>
                <Th className="text-right">{m.status}</Th>
                <Th className="text-right">{m.duration}</Th>
                <Th>{m.error}</Th>
              </tr>
            </thead>
            <tbody>
              {record.attempts.map((attempt, index) => (
                <tr key={index}>
                  <Td className="text-xs break-words max-sm:basis-full">
                    {attempt.account}
                    <span className="mt-1 block font-mono text-foreground-muted">
                      {breakableURL(attempt.url)}
                    </span>
                  </Td>
                  <Td
                    label={m.status}
                    className={
                      'text-right whitespace-nowrap max-sm:text-left ' +
                      (attempt.status === 200 ? '' : 'text-error-text')
                    }
                  >
                    {attempt.status || m.none}
                    {attempt.code && (
                      <span className="block font-mono text-xs">{attempt.code}</span>
                    )}
                  </Td>
                  <Td label={m.duration} className="text-right whitespace-nowrap max-sm:text-left">
                    {formatDuration(attempt.duration_ms, locale)}
                    {attempt.retry_delay && (
                      <span className="block text-xs text-foreground-muted">
                        {m.retryDelay} {attempt.retry_delay}
                      </span>
                    )}
                  </Td>
                  <Td label={m.error} className="text-xs break-words max-sm:basis-full">
                    {attempt.error ?? m.none}
                    {attempt.trace_id && (
                      <span className="mt-1 block font-mono text-foreground-muted">
                        {m.traceId} {attempt.trace_id}
                      </span>
                    )}
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        </section>
      )}
      <section className="flex flex-col gap-3">
        <h3 className="text-md font-semibold">{m.bodies}</h3>
        {available.length === 0 ? (
          <Callout tone="muted">
            <p>{m.noBody}</p>
            <Button size="sm" asChild>
              <Link to="/settings/logging">{m.openSettings}</Link>
            </Button>
          </Callout>
        ) : (
          <Tabs defaultValue={available[0]?.key ?? 'request'}>
            <TabsList aria-label={m.bodies}>
              {available.map((item) => (
                <TabsTrigger key={item.key} value={item.key}>
                  {item.label}
                </TabsTrigger>
              ))}
            </TabsList>
            {available.map((item) => (
              <TabsContent key={item.key} value={item.key} className="mt-3">
                {item.body && (
                  <BodyView
                    text={item.side === 'request' ? item.body.request : item.body.response}
                    size={
                      item.side === 'request' ? item.body.request_size : item.body.response_size
                    }
                  />
                )}
              </TabsContent>
            ))}
          </Tabs>
        )}
      </section>
    </div>
  )
}

// RequestDetailSheet 是从右侧滑入的请求详情，id 来自 URL；请求进行中时显示等待，结束后读取记录
export function RequestDetailSheet({ id, onClose }: { id: string | null; onClose: () => void }) {
  const m = useMessages(requestMessages)
  const app = useMessages(appMessages)
  const running = useRunning().some((record) => record.id === id)
  const detail = useQuery({
    queryKey: ['request', id],
    queryFn: () => api<RequestDetail>('/api/usage/records/' + encodeURIComponent(id ?? '')),
    enabled: id !== null && !running,
  })
  return (
    <Dialog
      open={id !== null}
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      {id !== null && (
        <DialogContent
          layout="sheet"
          title={detail.data?.record.model ?? m.detail}
          description={
            <span className="flex items-center gap-1">
              <span className="font-mono text-xs wrap-anywhere">{id}</span>
              <CopyButton value={id} label={m.copyId} copiedLabel={m.copied} />
            </span>
          }
          closeLabel={app.closeDialog}
        >
          {running ? (
            <EmptyState
              icon={<LoaderCircle size={20} aria-hidden className="animate-spin" />}
              title={m.detailRunning}
            />
          ) : detail.isPending ? (
            <Loading label={app.loading} />
          ) : detail.isError ? (
            <ErrorState
              title={m.detailFailed}
              error={detail.error}
              retryLabel={app.retry}
              onRetry={() => void detail.refetch()}
            />
          ) : (
            <DetailBody detail={detail.data} />
          )}
        </DialogContent>
      )}
    </Dialog>
  )
}
