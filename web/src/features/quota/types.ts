// QuotaBucket 对应 internal/app 的 quotaBucket，bucket_id 为 gemini-5h、gemini-weekly、3p-5h 或 3p-weekly，window 为 5h 或 weekly
export interface QuotaBucket {
  bucket_id: string
  window: string
  remaining_fraction: number
  reset_time?: string
}

// QuotaSnapshot 对应 internal/requestdb 的 QuotaSnapshot，是账户一个额度桶在 time 时刻的剩余比例
export interface QuotaSnapshot {
  time: string
  account: string
  bucket_id: string
  window: string
  remaining_fraction: number
  reset_time?: string
}
