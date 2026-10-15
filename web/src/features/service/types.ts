// Status 对应 internal/app getStatus 的响应，client_version 是服务按其发出请求的官方客户端版本，latest_version 是官方更新清单中的最新版本，尚未读到时为空
export interface Status {
  host: 'web' | 'desktop'
  version: string
  listen: string
  started_at: string
  client_version: string
  latest_version: string
}

// Session 对应 internal/app 的 sessionState
export interface Session {
  enabled: boolean
  authenticated: boolean
}
