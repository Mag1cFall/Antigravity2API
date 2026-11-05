// Settings 对应 internal/app 的 settings，values 是除 ADMIN_PASSWORD 外各配置项在 .env 中的文本值
export interface Settings {
  values: Record<string, string>
  admin_password_set: boolean
  restart_required: string[]
  invalid: InvalidValue[]
  environment: string[]
}

// InvalidValue 对应 internal/config 的 InvalidValue，是取值无效、按默认值运行的配置项
export interface InvalidValue {
  key: string
  value: string
  reason: string
}
