import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, Square } from 'lucide-react'

import { runningKey } from '@/app/events'
import { Button } from '@/components/ui/button'
import { Card, CardHeader } from '@/components/ui/card'
import { Table, Td, Th } from '@/components/ui/table'
import { toast } from '@/components/ui/toast'
import { requestMessages } from '@/features/requests/messages'
import type { RequestRecord } from '@/features/requests/types'
import { api } from '@/lib/api'
import { formatDateTime, formatDuration } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// RunningRequests 是事件流推送的进行中请求，可逐个停止
export function RunningRequests() {
  const m = useMessages(requestMessages)
  const { locale } = useI18n()
  const now = useNow(1000)
  const client = useQueryClient()
  const running =
    useQuery({
      queryKey: runningKey,
      queryFn: () => client.getQueryData<RequestRecord[]>(runningKey) ?? [],
      staleTime: Number.POSITIVE_INFINITY,
    }).data ?? []
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
      <Table label={m.runningTitle}>
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
                <div className="mt-1 text-xs text-foreground-muted">{record.protocol}</div>
              </Td>
              <Td className="max-w-field font-mono text-xs wrap-anywhere">{record.model}</Td>
              <Td className="max-w-field text-xs wrap-anywhere text-foreground-muted">
                {record.account || m.none}
              </Td>
              <Td className="text-right whitespace-nowrap">
                {formatDuration(
                  Math.max(0, now.getTime() - new Date(record.time).getTime()),
                  locale,
                )}
              </Td>
              <Td className="text-right">
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
