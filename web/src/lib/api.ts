// ApiError 是管理 API 返回的错误，code 与 message 取自响应体
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message)
  }
}

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
    throw new ApiError(0, 'network', error instanceof Error ? error.message : String(error))
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
