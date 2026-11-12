// Settings 对应 internal/app 的 settings，values 是除 ADMIN_PASSWORD 外各配置项在 .env 中的文本值
export interface Settings {
  values: Record<string, string>
  admin_password_set: boolean
  restart_required: string[]
  invalid: InvalidValue[]
  environment: string[]
}

// FirstEventLatency 对应 internal/service 的 FirstEventLatency，是一个目录模型近 24 小时原请求首字等待的 p95 与样本数
export interface FirstEventLatency {
  model: string
  samples: number
  p95_ms: number
}

// InvalidValue 对应 internal/config 的 InvalidValue，是取值无效、按默认值运行的配置项
interface InvalidValue {
  key: string
  value: string
  reason: string
}
