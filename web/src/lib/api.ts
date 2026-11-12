import { serverText } from '@/lib/i18n'

// ApiError 是管理 API 返回的错误，code 与 detail 取自响应体，message 是按当前界面语言翻译的 detail
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    readonly detail: string,
  ) {
    super()
  }
}
Object.defineProperty(ApiError.prototype, 'message', {
  get(this: ApiError) {
    return serverText(this.detail)
  },
})

// errorMessage 返回错误的说明文字
export function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : String(error)
}

// ErrorBody 是管理 API 错误响应的正文
interface ErrorBody {
  error?: { code?: string; message?: string }
}

// api 以 JSON 调用同源管理 API，非 2xx 响应抛出 ApiError
export async function api<T>(
  path: string,
  init: RequestInit & { json?: unknown } = {},
): Promise<T> {
  const { json, ...rest } = init
  const headers = new Headers(rest.headers)
  headers.set('Accept', 'application/json')
  if (json !== undefined) headers.set('Content-Type', 'application/json')
  let response: Response
  try {
    response = await fetch(path, {
      ...rest,
      headers,
      body: json === undefined ? (rest.body ?? null) : JSON.stringify(json),
      credentials: 'same-origin',
    })
  } catch (error) {
    throw new ApiError(0, 'network', errorMessage(error))
  }
  if (response.status === 204) return undefined as T
  const text = await response.text()
  const body: unknown = text ? JSON.parse(text) : undefined
  if (!response.ok) {
    const detail = (body as ErrorBody | undefined)?.error
    throw new ApiError(
      response.status,
      detail?.code ?? 'http_' + String(response.status),
      detail?.message ?? response.statusText,
    )
  }
  return body as T
}
