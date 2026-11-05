import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Activity, Square } from 'lucide-react'

import { logsKey, useRunning } from '@/app/events'
import { Button } from '@/components/ui/button'
import { Card, CardHeader } from '@/components/ui/card'
import { Table, Td, Th } from '@/components/ui/table'
import { toast } from '@/components/ui/toast'
import { requestStages } from '@/features/logs/filter'
import type { LogEntry } from '@/features/logs/types'
import { protocolName } from '@/features/requests/filters'
import { requestMessages } from '@/features/requests/messages'
import { api } from '@/lib/api'
import { formatDateTime, formatElapsed } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// RunningRequests 是事件流推送的进行中请求，显示已运行秒数与最近一条进度日志所写的阶段，可逐个停止；宽度小于 640px 时每个请求一张卡片，模型与账户在下方单独成行
export function RunningRequests() {
  const m = useMessages(requestMessages)
  const { locale } = useI18n()
  const now = useNow(1000)
  const running = useRunning()
  const client = useQueryClient()
  const stages = requestStages(client.getQueryData<LogEntry[]>(logsKey) ?? [])
  const stop = useMutation({
    mutationFn: (id: string) =>
      api('/api/requests/' + encodeURIComponent(id) + '/cancel', { method: 'POST' }),
    onError: (error) => {
      toast({ tone: 'error', title: fill(m.stopFailed, { message: error.message }) })
    },
  })
  if (running.length === 0) return null
  return (
    <Card>
      <CardHeader
        icon={<Activity size={16} aria-hidden />}
        title={m.runningTitle}
        description={m.runningDescription}
      />
      <Table label={m.runningTitle} stack>
        <thead>
          <tr>
            <Th>{m.time}</Th>
            <Th>{m.model}</Th>
            <Th>{m.account}</Th>
            <Th className="text-right">{m.elapsed}</Th>
            <Th>
              <span className="sr-only">{m.stop}</span>
            </Th>
          </tr>
        </thead>
        <tbody>
          {running.map((record) => (
            <tr key={record.id}>
              <Td className="whitespace-nowrap">
                {formatDateTime(new Date(record.time), now, locale)}
                <div className="mt-1 text-xs text-foreground-muted">
                  {protocolName(record.protocol)}
                </div>
              </Td>
              <Td className="max-w-field text-xs wrap-anywhere max-sm:order-1 max-sm:basis-full">
                <div className="font-mono">{record.model}</div>
                {stages.get(record.id) && (
                  <div className="mt-1 text-foreground-muted">{stages.get(record.id)}</div>
                )}
              </Td>
              <Td className="max-w-field text-xs wrap-anywhere text-foreground-muted max-sm:order-1 max-sm:basis-full">
                {record.account || m.none}
              </Td>
              <Td label={m.elapsed} className="text-right whitespace-nowrap max-sm:text-left">
                {formatElapsed(now.getTime() - Date.parse(record.time), locale)}
              </Td>
              <Td className="text-right max-sm:ml-auto">
                <Button
                  size="sm"
                  aria-label={fill(m.stopLabel, { id: record.id })}
                  loading={stop.isPending && stop.variables === record.id}
                  onClick={() => {
                    stop.mutate(record.id)
                  }}
                >
                  <Square size={16} aria-hidden />
                  {m.stop}
                </Button>
              </Td>
            </tr>
          ))}
        </tbody>
      </Table>
    </Card>
  )
}
