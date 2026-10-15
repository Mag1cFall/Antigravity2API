import type { QuotaBucket, QuotaSnapshot } from '@/features/quota/types'

// groups 是两个额度组，gemini 为 Gemini 模型，3p 为 Claude 与 GPT 模型
export const groups = ['gemini', '3p'] as const

// QuotaGroup 是额度组
export type QuotaGroup = (typeof groups)[number]

// windows 是额度窗口
export const windows = ['5h', 'weekly'] as const

// BucketSummary 是账户池在一个额度桶上的汇总
export interface BucketSummary {
  group: string
  window: string
  accounts: number
  average: number
  exhausted: number
  earliestReset?: string
}

// groupOrder 与 windowOrder 决定额度桶的显示顺序
const groupOrder: string[] = [...groups]
const windowOrder: string[] = [...windows]

// bucketParts 把 gemini-5h 这类 bucket_id 拆成额度组与窗口
export function bucketParts(bucketId: string) {
  const index = bucketId.lastIndexOf('-')
  return index === -1
    ? { group: bucketId, window: '' }
    : { group: bucketId.slice(0, index), window: bucketId.slice(index + 1) }
}

// rank 返回取值在顺序表中的位置，未知取值排在最后
function rank(order: string[], value: string) {
  const index = order.indexOf(value)
  return index === -1 ? order.length : index
}

// compareBuckets 按额度组再按窗口排序
export function compareBuckets(
  a: { group: string; window: string },
  b: { group: string; window: string },
) {
  return (
    rank(groupOrder, a.group) - rank(groupOrder, b.group) ||
    a.group.localeCompare(b.group) ||
    rank(windowOrder, a.window) - rank(windowOrder, b.window) ||
    a.window.localeCompare(b.window)
  )
}

// groupBuckets 返回账户在一个额度组的额度桶，按 5 小时、每周排序
export function groupBuckets(quota: QuotaBucket[], group: string) {
  return quota
    .map((bucket) => ({ ...bucket, ...bucketParts(bucket.bucket_id) }))
    .filter((bucket) => bucket.group === group)
    .sort(compareBuckets)
}

// available 返回账户在一个额度组的可用比例，即 5 小时与每周中较低的一项，没有额度时为 null
export function available(quota: QuotaBucket[], group: string): number | null {
  const buckets = groupBuckets(quota, group)
  if (buckets.length === 0) return null
  return Math.min(...buckets.map((bucket) => bucket.remaining_fraction))
}

// summarizeBuckets 按额度组与窗口汇总各账户的剩余比例、耗尽账户数与最早重置时间
export function summarizeBuckets(buckets: QuotaBucket[]): BucketSummary[] {
  const summaries = new Map<string, BucketSummary & { total: number }>()
  for (const bucket of buckets) {
    const { group, window } = bucketParts(bucket.bucket_id)
    const summary = summaries.get(bucket.bucket_id) ?? {
      group,
      window,
      accounts: 0,
      average: 0,
      exhausted: 0,
      total: 0,
    }
    summary.accounts++
    summary.total += bucket.remaining_fraction
    if (bucket.remaining_fraction <= 0) summary.exhausted++
    if (
      bucket.reset_time &&
      (!summary.earliestReset || Date.parse(bucket.reset_time) < Date.parse(summary.earliestReset))
    ) {
      summary.earliestReset = bucket.reset_time
    }
    summaries.set(bucket.bucket_id, summary)
  }
  return [...summaries.values()]
    .map(({ total, ...summary }) => ({ ...summary, average: total / summary.accounts }))
    .sort(compareBuckets)
}

// nameOf 返回额度组或窗口的显示名，未知取值原样显示
export function nameOf(names: Record<string, string>, key: string) {
  return names[key] ?? key
}

// lowQuota 是低额度的比例阈值
export const lowQuota = 0.2

// quotaTone 按剩余比例给出比例条语气
export function quotaTone(fraction: number): 'success' | 'warning' | 'error' {
  if (fraction < 0.1) return 'error'
  if (fraction < 0.3) return 'warning'
  return 'success'
}

// quotaText 是剩余比例文字的颜色
export function quotaText(fraction: number) {
  if (fraction < 0.1) return 'text-error-text'
  if (fraction < 0.3) return 'text-warning-text'
  return 'text-foreground'
}

// formatCountdown 把剩余毫秒输出为 6d 15h、1h 16m、12m 这类紧凑写法，已过去时为空
export function formatCountdown(ms: number) {
  if (ms <= 0) return ''
  const minutes = Math.ceil(ms / 60_000)
  const days = Math.floor(minutes / 1440)
  const hours = Math.floor((minutes % 1440) / 60)
  const rest = minutes % 60
  if (days > 0) return String(days) + 'd' + (hours ? ' ' + String(hours) + 'h' : '')
  if (hours > 0) return String(hours) + 'h' + (rest ? ' ' + String(rest) + 'm' : '')
  return String(rest) + 'm'
}

// quotaHistory 把升序的额度快照分成 bins 段，每段按额度桶取各账户在段末之前最近一次的剩余比例的平均值，段末之前没有快照的桶为 null
export function quotaHistory(snapshots: QuotaSnapshot[], from: number, to: number, bins: number) {
  const step = (to - from) / bins
  const latest = new Map<string, Map<string, number>>()
  const series = new Map<string, (number | null)[]>()
  let index = 0
  for (let bin = 0; bin < bins; bin++) {
    const end = from + step * (bin + 1)
    for (
      let snapshot = snapshots[index];
      snapshot && Date.parse(snapshot.time) < end;
      snapshot = snapshots[++index]
    ) {
      const accounts = latest.get(snapshot.bucket_id) ?? new Map<string, number>()
      accounts.set(snapshot.account, snapshot.remaining_fraction)
      latest.set(snapshot.bucket_id, accounts)
    }
    for (const [bucket, accounts] of latest) {
      const values = series.get(bucket) ?? Array<number | null>(bins).fill(null)
      values[bin] = [...accounts.values()].reduce((sum, value) => sum + value, 0) / accounts.size
      series.set(bucket, values)
    }
  }
  return [...series.entries()]
    .map(([bucket, values]) => ({ ...bucketParts(bucket), bucket, values }))
    .sort(compareBuckets)
}
