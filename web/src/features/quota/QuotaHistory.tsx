import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { LineChart } from 'lucide-react'
import { useState } from 'react'

import { appMessages } from '@/app/messages'
import { Callout } from '@/components/ui/callout'
import { Card, CardBody, CardHeader } from '@/components/ui/card'
import { Segmented } from '@/components/ui/segmented'
import { Select } from '@/components/ui/select'
import { ErrorState, Loading } from '@/components/ui/states'
import { useAccounts } from '@/features/accounts/queries'
import { quotaMessages } from '@/features/quota/messages'
import { nameOf, quotaHistory } from '@/features/quota/quota'
import type { QuotaSnapshot } from '@/features/quota/types'
import { api } from '@/lib/api'
import { formatPercent } from '@/lib/format'
import { defineMessages, useI18n, useMessages } from '@/lib/i18n'

// historyMessages 是额度历史的文案
const historyMessages = defineMessages(
  {
    title: '额度变化',
    range: '时间范围',
    range24h: '24 小时',
    range7d: '7 天',
    account: '账户',
    allAccounts: '全部账户',
    chart: '额度剩余比例折线图',
    empty: '这段时间没有额度快照。账户初始化与首次成功生成后会读取额度。',
    loadFailed: '无法读取额度历史',
    start24h: '24 小时前',
    start7d: '7 天前',
    now: '现在',
  },
  {
    title: 'Quota over time',
    range: 'Time range',
    range24h: '24 hours',
    range7d: '7 days',
    account: 'Account',
    allAccounts: 'All accounts',
    chart: 'Line chart of remaining quota',
    empty:
      'No quota snapshots in this period. Quota is read when an account is initialized and after its first successful generation.',
    loadFailed: 'Could not load quota history',
    start24h: '24 hours ago',
    start7d: '7 days ago',
    now: 'Now',
  },
  {
    title: '額度變化',
    range: '時間範圍',
    range24h: '24 小時',
    range7d: '7 天',
    account: '帳號',
    allAccounts: '所有帳號',
    chart: '額度剩餘比例折線圖',
    empty: '這段期間沒有額度快照。帳號初始化完成與首次成功產生內容後，會讀取額度。',
    loadFailed: '無法讀取額度歷史記錄',
    start24h: '24 小時前',
    start7d: '7 天前',
    now: '現在',
  },
)

// ranges 是额度历史的时间范围与分段数
const ranges = { '24h': [86_400_000, 96], '7d': [604_800_000, 168] } as const
type RangeKey = keyof typeof ranges

// strokes 是各额度组折线的颜色，未知额度组用中性色
const strokes: Record<string, string> = { gemini: 'stroke-primary', '3p': 'stroke-violet' }

// segments 把含 null 的序列拆成连续的折线点串，x 取段中点，y 为 0 到 100 的倒置比例
function segments(values: (number | null)[]) {
  const lines: string[] = []
  let points: string[] = []
  values.forEach((value, index) => {
    if (value === null) {
      if (points.length) lines.push(points.join(' '))
      points = []
      return
    }
    points.push(String(index + 0.5) + ',' + String(100 - value * 100))
  })
  if (points.length) lines.push(points.join(' '))
  return lines
}

// QuotaHistory 是按额度桶绘制剩余比例随时间变化的折线图，可按账户筛选
export function QuotaHistory() {
  const m = useMessages(historyMessages)
  const q = useMessages(quotaMessages)
  const app = useMessages(appMessages)
  const { locale } = useI18n()
  const [range, setRange] = useState<RangeKey>('24h')
  const [account, setAccount] = useState('all')
  const accounts = useAccounts()
  const [span, bins] = ranges[range]
  const history = useQuery({
    queryKey: ['quota-snapshots', range, account],
    queryFn: async () => {
      const to = Date.now()
      const from = to - span
      const query = new URLSearchParams({
        from: new Date(from).toISOString(),
        to: new Date(to + 1000).toISOString(),
      })
      if (account !== 'all') query.set('account', account)
      const data = await api<{ snapshots: QuotaSnapshot[] }>(
        '/api/quota/snapshots?' + query.toString(),
      )
      return quotaHistory(data.snapshots, from, to, bins)
    },
    placeholderData: keepPreviousData,
  })
  return (
    <Card>
      <CardHeader
        icon={<LineChart size={16} aria-hidden />}
        title={m.title}
        actions={
          <div className="flex flex-wrap items-center gap-3">
            <Select
              label={m.account}
              size="sm"
              className="max-w-field"
              value={account}
              onValueChange={setAccount}
              options={[
                { value: 'all', label: m.allAccounts },
                ...(accounts.data?.accounts ?? []).map((item) => ({
                  value: item.id,
                  label: item.id,
                })),
              ]}
            />
            <Segmented
              label={m.range}
              value={range}
              onValueChange={setRange}
              options={[
                { value: '24h', label: m.range24h },
                { value: '7d', label: m.range7d },
              ]}
            />
          </div>
        }
      />
      <CardBody>
        {history.isPending ? (
          <Loading label={app.loading} />
        ) : history.isError ? (
          <ErrorState
            title={m.loadFailed}
            error={history.error}
            retryLabel={app.retry}
            onRetry={() => void history.refetch()}
          />
        ) : history.data.length === 0 ? (
          <Callout tone="muted">
            <p>{m.empty}</p>
          </Callout>
        ) : (
          <div className="flex flex-col gap-2">
            <div className="flex gap-2">
              <div className="flex h-chart w-8 shrink-0 flex-col justify-between text-right text-xs text-foreground-muted tabular-nums">
                <span>100%</span>
                <span>50%</span>
                <span>0%</span>
              </div>
              <svg
                role="img"
                aria-label={m.chart}
                viewBox={'0 0 ' + String(bins) + ' 100'}
                preserveAspectRatio="none"
                className="h-chart min-w-0 flex-1 overflow-visible"
              >
                {[0, 50, 100].map((y) => (
                  <line
                    key={y}
                    x1={0}
                    x2={bins}
                    y1={y}
                    y2={y}
                    className="stroke-border"
                    vectorEffect="non-scaling-stroke"
                  />
                ))}
                {history.data.map((series) =>
                  segments(series.values).map((points, index) => (
                    <polyline
                      key={series.bucket + String(index)}
                      points={points}
                      fill="none"
                      strokeWidth={2}
                      strokeLinejoin="round"
                      strokeDasharray={series.window === 'weekly' ? '5 4' : undefined}
                      vectorEffect="non-scaling-stroke"
                      className={strokes[series.group] ?? 'stroke-foreground-muted'}
                    />
                  )),
                )}
              </svg>
            </div>
            <div className="flex justify-between pl-[40px] text-xs text-foreground-muted">
              <span>{range === '24h' ? m.start24h : m.start7d}</span>
              <span>{m.now}</span>
            </div>
            <ul className="flex flex-wrap gap-x-6 gap-y-2 text-sm">
              {history.data.map((series) => {
                const last = series.values.findLast((value) => value !== null) ?? null
                return (
                  <li key={series.bucket} className="flex items-center gap-2">
                    <svg aria-hidden viewBox="0 0 24 4" className="h-1 w-6">
                      <line
                        x1={0}
                        x2={24}
                        y1={2}
                        y2={2}
                        strokeWidth={3}
                        strokeDasharray={series.window === 'weekly' ? '5 4' : undefined}
                        className={strokes[series.group] ?? 'stroke-foreground-muted'}
                      />
                    </svg>
                    <span>{nameOf(q, series.group) + ' · ' + nameOf(q, series.window)}</span>
                    {last !== null && (
                      <span className="text-foreground-muted tabular-nums">
                        {formatPercent(last, locale)}
                      </span>
                    )}
                  </li>
                )
              })}
            </ul>
          </div>
        )}
      </CardBody>
    </Card>
  )
}
