// Settings 对应 internal/app 的 settings，values 是除 ADMIN_PASSWORD 外各配置项在 .env 中的文本值
export interface Settings {
  values: Record<string, string>
  admin_password_set: boolean
  restart_required: string[]
}
