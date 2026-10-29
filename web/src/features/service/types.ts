// Status 对应 internal/app getStatus 的响应，listen 是实际监听地址，reserved_listen 是被 Windows 保留而改用 listen 的配置地址（没有改用时为空），client_version 是服务按其发出请求的官方客户端版本，latest_version 是官方更新清单中的最新版本，尚未读到时为空
export interface Status {
  host: 'web' | 'desktop'
  version: string
  listen: string
  reserved_listen: string
  started_at: string
  client_version: string
  latest_version: string
}

// Session 对应 internal/app 的 sessionState
export interface Session {
  enabled: boolean
  authenticated: boolean
}

// UpdateStatus 对应 GET /api/update，mode 为 binary（可在线更新）、container（随镜像更新）或 development（源码构建）
export interface UpdateStatus {
  current: string
  latest?: string
  available: boolean
  mode: 'binary' | 'container' | 'development'
  notes?: string
  url?: string
  published_at?: string
  checked_at?: string
  error?: string
  updating: boolean
}
