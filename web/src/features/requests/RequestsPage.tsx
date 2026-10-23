import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Download, History } from 'lucide-react'
import { useSearchParams } from 'react-router'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { Card, CardToolbar } from '@/components/ui/card'
import { SearchInput } from '@/components/ui/search-input'
import { Segmented } from '@/components/ui/segmented'
import { Select } from '@/components/ui/select'
import { EmptyState, ErrorState, Loading } from '@/components/ui/states'
import { Table, Td, Th } from '@/components/ui/table'
import { Tooltip } from '@/components/ui/tooltip'
import { useAccounts } from '@/features/accounts/queries'
import { useModels } from '@/features/models/queries'
import {
  apiQuery,
  parseFilter,
  protocols,
  type RangeKey,
  ranges,
  states,
} from '@/features/requests/filters'
import { requestMessages } from '@/features/requests/messages'
import { RequestDetailSheet } from '@/features/requests/RequestDetailSheet'
import { RequestStateBadge } from '@/features/requests/RequestStateBadge'
import { RunningRequests } from '@/features/requests/RunningRequests'
import type { RequestPage } from '@/features/requests/types'
import { api } from '@/lib/api'
import { formatCompact, formatCost, formatDateTime, formatDuration } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'
import { useNow } from '@/lib/time'

// RequestsPage 是请求记录：筛选、分页与详情，全部状态保存在 URL；宽度小于 640px 时每个请求一张卡片
export function RequestsPage() {
  const m = useMessages(requestMessages)
  const app = useMessages(appMessages)
  const { locale } = useI18n()
  const now = useNow(30_000)
  const [params, setParams] = useSearchParams()
  const filter = parseFilter(params)
  const models = useModels()
  const accounts = useAccounts()
  const page = useQuery({
    queryKey: ['requests', filter],
    queryFn: () =>
      api<RequestPage>('/api/usage/records?' + apiQuery(filter, new Date()).toString()),
    placeholderData: keepPreviousData,
  })
  const filtered =
    filter.range !== '24h' ||
    Boolean(filter.state || filter.protocol || filter.model || filter.account || filter.q)

  const update = (changes: Record<string, string>, push = false) => {
    setParams(
      (current) => {
        const next = new URLSearchParams(current)
        for (const [key, value] of Object.entries(changes)) {
          if (value) next.set(key, value)
          else next.delete(key)
        }
        if (!('cursor' in changes)) next.delete('cursor')
        return next
      },
      { replace: !push },
    )
  }
  const rangeLabels: Record<RangeKey, string> = {
    '1h': m.range1h,
    '24h': m.range24h,
    '7d': m.range7d,
    '30d': m.range30d,
  }
  const items = page.data?.items ?? []
  const pageCost = items.reduce((sum, record) => sum + (record.cost_usd ?? 0), 0)
  const pageUnpriced = items.filter((record) => record.cost_usd === null).length
  const csvQuery = apiQuery(filter, now)
  csvQuery.delete('cursor')
  const csv = '/api/usage/records.csv?' + csvQuery.toString()
  const base = window.location.origin + '/v1'

  return (
    <>
      <RunningRequests />
      <Card>
        <CardToolbar className="items-start">
          <div className="flex min-w-0 flex-[1_1_480px] flex-wrap items-center gap-3">
            <Segmented
              label={m.range}
              value={filter.range}
              onValueChange={(value) => {
                update({ range: value === '24h' ? '' : value })
              }}
              options={(Object.keys(ranges) as RangeKey[]).map((value) => ({
                value,
                label: rangeLabels[value],
              }))}
            />
            <Select
              label={m.state}
              size="sm"
              value={filter.state || 'all'}
              onValueChange={(value) => {
                update({ state: value === 'all' ? '' : value })
              }}
              options={[
                { value: 'all', label: m.allStates },
                ...states.map((state) => ({ value: state, label: m[state] })),
              ]}
            />
            <Select
              label={m.protocol}
              size="sm"
              value={filter.protocol || 'all'}
              onValueChange={(value) => {
                update({ protocol: value === 'all' ? '' : value })
              }}
              options={[
                { value: 'all', label: m.allProtocols },
                ...protocols.map((protocol) => ({ value: protocol, label: protocol })),
              ]}
            />
            <Select
              label={m.model}
              size="sm"
              className="max-w-field"
              value={filter.model || 'all'}
              onValueChange={(value) => {
                update({ model: value === 'all' ? '' : value })
              }}
              options={[
                { value: 'all', label: m.allModels },
                ...(models.data?.models ?? []).map((model) => ({
                  value: model.id,
                  label: model.id,
                })),
              ]}
            />
            <Select
              label={m.account}
              size="sm"
              className="max-w-field"
              value={filter.account || 'all'}
              onValueChange={(value) => {
                update({ account: value === 'all' ? '' : value })
              }}
              options={[
                { value: 'all', label: m.allAccounts },
                ...(accounts.data?.accounts ?? []).map((account) => ({
                  value: account.id,
                  label: account.id,
                })),
              ]}
            />
            <SearchInput
              aria-label={m.search}
              placeholder={m.search}
              value={filter.q}
              onChange={(event) => {
                update({ q: event.target.value })
              }}
              className="min-w-[200px] flex-[1_1_240px]"
            />
          </div>
          {items.length > 0 ? (
            <Button size="sm" asChild>
              <a href={csv} download>
                <Download size={16} aria-hidden />
                {m.export}
              </a>
            </Button>
          ) : (
            <Tooltip
              disabledReason={
                page.isPending ? app.loading : page.isError ? m.exportFailed : m.exportEmpty
              }
            >
              <Button size="sm" disabled>
                <Download size={16} aria-hidden />
                {m.export}
              </Button>
            </Tooltip>
          )}
        </CardToolbar>
        {page.isPending ? (
          <Loading label={app.loading} />
        ) : page.isError ? (
          <ErrorState
            title={m.loadFailed}
            error={page.error}
            retryLabel={app.retry}
            onRetry={() => void page.refetch()}
          />
        ) : items.length === 0 ? (
          filtered ? (
            <EmptyState
              icon={<History size={20} aria-hidden />}
              title={m.noMatchTitle}
              description={m.noMatchDescription}
              action={
                <Button
                  onClick={() => {
                    setParams({}, { replace: true })
                  }}
                >
                  {m.clearFilters}
                </Button>
              }
            />
          ) : (
            <EmptyState
              icon={<History size={20} aria-hidden />}
              title={m.emptyTitle}
              description={fill(m.emptyDescription, { base })}
            />
          )
        ) : (
          <>
            <Table label={m.title} fixed stack minWidth={820}>
              <colgroup>
                <col className="w-[128px]" />
                <col className="w-[144px]" />
                <col className="w-[72px]" />
                <col />
                <col className="w-[200px] max-xl:hidden" />
                <col className="w-[88px] max-xl:hidden" />
                <col className="w-[88px]" />
                <col className="w-[168px]" />
                <col className="w-[104px]" />
              </colgroup>
              <thead>
                <tr>
                  <Th>{m.time}</Th>
                  <Th>{m.state}</Th>
                  <Th className="text-right">{m.status}</Th>
                  <Th>{m.model}</Th>
                  <Th className="max-xl:hidden">{m.account}</Th>
                  <Th className="text-right max-xl:hidden">{m.firstEvent}</Th>
                  <Th className="text-right">{m.duration}</Th>
                  <Th className="text-right">{m.tokens}</Th>
                  <Th className="text-right">{m.cost}</Th>
                </tr>
              </thead>
              <tbody>
                {items.map((record) => (
                  <tr
                    key={record.id}
                    className="cursor-pointer hover:bg-muted"
                    onClick={() => {
                      update({ id: record.id, cursor: filter.cursor }, true)
                    }}
                  >
                    <Td className="whitespace-nowrap">
                      <button
                        type="button"
                        aria-label={fill(m.open, { id: record.id })}
                        className="rounded-sm text-left hover:text-primary"
                      >
                        {formatDateTime(new Date(record.time), now, locale)}
                      </button>
                      <div className="mt-1 text-xs text-foreground-muted">{record.protocol}</div>
                    </Td>
                    <Td>
                      <RequestStateBadge state={record.state} />
                    </Td>
                    <Td
                      className={
                        'text-right max-sm:ml-auto ' +
                        (record.status >= 400 ? 'text-error-text' : 'text-foreground')
                      }
                    >
                      {record.status || m.none}
                    </Td>
                    <Td className="max-sm:basis-full">
                      <div className="truncate font-mono text-xs" title={record.model}>
                        {record.model}
                      </div>
                      {record.error && (
                        <div className="mt-1 line-clamp-2 text-xs break-words text-error-text">
                          {record.error}
                        </div>
                      )}
                    </Td>
                    <Td
                      className="truncate text-xs text-foreground-muted max-xl:hidden"
                      title={record.account}
                    >
                      {record.account || m.none}
                    </Td>
                    <Td className="text-right whitespace-nowrap max-xl:hidden">
                      {record.first_event_ms
                        ? formatDuration(record.first_event_ms, locale)
                        : m.none}
                    </Td>
                    <Td
                      label={m.duration}
                      className="text-right whitespace-nowrap max-sm:text-left"
                    >
                      {formatDuration(record.duration_ms, locale)}
                    </Td>
                    <Td label={m.tokens} className="text-right whitespace-nowrap max-sm:text-left">
                      {formatCompact(record.input_tokens, locale)}
                      <span className="text-foreground-subtle"> · </span>
                      <span className="text-success-text">
                        {formatCompact(record.cached_tokens, locale)}
                      </span>
                      <span className="text-foreground-subtle"> · </span>
                      {formatCompact(record.reasoning_tokens + record.reply_tokens, locale)}
                    </Td>
                    <Td label={m.cost} className="text-right whitespace-nowrap max-sm:text-left">
                      {record.cost_usd === null ? (
                        <span title={m.unpriced} className="text-foreground-muted">
                          {m.none}
                        </span>
                      ) : (
                        formatCost(record.cost_usd, locale)
                      )}
                    </Td>
                  </tr>
                ))}
              </tbody>
            </Table>
            <div className="flex flex-wrap items-center justify-end gap-3 border-t border-border px-6 py-3 max-sm:px-4">
              <p className="mr-auto text-sm text-foreground-muted tabular-nums">
                {fill(m.pageCost, { cost: formatCost(pageCost, locale) })}
                {pageUnpriced > 0 && ' · ' + fill(m.pageUnpriced, { count: pageUnpriced })}
              </p>
              {filter.cursor && (
                <Button
                  size="sm"
                  onClick={() => {
                    update({ cursor: '' }, true)
                  }}
                >
                  {m.newest}
                </Button>
              )}
              {page.data.next_cursor && (
                <Button
                  size="sm"
                  loading={page.isFetching && page.isPlaceholderData}
                  onClick={() => {
                    update({ cursor: page.data.next_cursor ?? '' }, true)
                  }}
                >
                  {m.older}
                </Button>
              )}
            </div>
          </>
        )}
      </Card>
      <RequestDetailSheet
        id={params.get('id')}
        onClose={() => {
          update({ id: '', cursor: filter.cursor })
        }}
      />
    </>
  )
}
