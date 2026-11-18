import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { FileText, RefreshCw } from 'lucide-react'
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { CodeBlock } from '@/components/ui/code-block'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import { api } from '@/lib/api'
import { formatFullDateTime, formatRelative } from '@/lib/format'
import { fill, serverText, useI18n, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// RejectedStatus 对应 GET /api/prompt/rejected：列表版本、列表与自定义规则条数、来源、最近一次检查与下载的时间、检查失败的原因与列表原文
interface RejectedStatus {
  version: string
  rules: number
  custom: number
  source: 'builtin' | 'remote'
  checked_at?: string
  updated_at?: string
  error?: string
  list: string
}

// rejectedKey 是上游拒绝句列表状态的查询键，保存设置后失效以更新自定义规则条数
export const rejectedKey = ['prompt', 'rejected'] as const

// RejectedStatusRow 是上游拒绝句列表的状态：版本、条数、来源与上次检查的结果，“立即更新”马上检查一次，“查看列表”打开当前列表原文
export function RejectedStatusRow() {
  const m = useMessages('settings')
  const app = useMessages('app')
  const f = useMessages('files')
  const { locale } = useI18n()
  const now = useNow(30_000)
  const client = useQueryClient()
  const [viewing, setViewing] = useState(false)
  const status = useQuery({
    queryKey: rejectedKey,
    queryFn: () => api<RejectedStatus>('/api/prompt/rejected'),
  })
  const check = useMutation({
    mutationFn: () => api<RejectedStatus>('/api/prompt/rejected/check', { method: 'POST' }),
    onSuccess: (result) => {
      client.setQueryData(rejectedKey, result)
    },
  })
  const data = status.data
  if (status.isError) {
    return (
      <p role="alert" className="text-sm wrap-anywhere text-error-text">
        {fill(m.rejectedFailed, { message: status.error.message })}
      </p>
    )
  }
  if (!data) return <p className="text-sm text-foreground-muted">{app.loading}</p>
  const checked = data.checked_at ? new Date(data.checked_at) : undefined
  return (
    <div className="flex flex-col gap-3">
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-sm">
        <dt className="text-foreground-muted">{m.rejectedVersion}</dt>
        <dd className="font-mono text-xs">{data.version || '—'}</dd>
        <dt className="text-foreground-muted">{m.rejectedRules}</dt>
        <dd>
          {fill(data.custom ? m.rejectedCountCustom : m.rejectedCount, {
            rules: data.rules,
            custom: data.custom,
          })}
        </dd>
        <dt className="text-foreground-muted">{m.rejectedSource}</dt>
        <dd>
          {data.source === 'remote' && data.updated_at
            ? fill(m.rejectedRemote, {
                time: formatFullDateTime(new Date(data.updated_at), locale),
              })
            : m.rejectedBuiltin}
        </dd>
        <dt className="text-foreground-muted">{m.rejectedChecked}</dt>
        <dd className={data.error ? 'wrap-anywhere text-error-text' : undefined}>
          {checked
            ? fill(data.error ? m.rejectedCheckFailed : m.rejectedCheckOk, {
                time: formatRelative(
                  new Date(Math.min(checked.getTime(), now.getTime())),
                  now,
                  locale,
                ),
                message: serverText(data.error ?? ''),
              })
            : m.rejectedNeverChecked}
        </dd>
      </dl>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          loading={check.isPending}
          onClick={() => {
            check.mutate()
          }}
        >
          <RefreshCw size={16} aria-hidden />
          {m.rejectedCheck}
        </Button>
        <Button
          size="sm"
          variant="ghost"
          onClick={() => {
            setViewing(true)
          }}
        >
          <FileText size={16} aria-hidden />
          {m.rejectedView}
        </Button>
        {check.isError && (
          <p role="alert" className="text-sm wrap-anywhere text-error-text">
            {check.error.message}
          </p>
        )}
      </div>
      <Dialog open={viewing} onOpenChange={setViewing}>
        <DialogContent
          width="wide"
          title={m.rejectedListTitle}
          description={fill(m.rejectedListHint, { version: data.version, rules: data.rules })}
          closeLabel={app.closeDialog}
        >
          <CodeBlock
            code={data.list}
            title="rules/rejected.txt"
            copyLabel={f.copy}
            copiedLabel={f.copied}
            wrap
          />
        </DialogContent>
      </Dialog>
    </div>
  )
}
