import { Badge, type BadgeTone } from '@/components/ui/badge'
import { requestMessages } from '@/features/requests/messages'
import type { RequestState } from '@/features/requests/types'
import { useMessages } from '@/lib/i18n'

// requestTone 是请求结果对应的标签语气
const requestTone: Record<RequestState, BadgeTone> = {
  running: 'info',
  completed: 'success',
  tool_calls: 'success',
  limited: 'warning',
  blocked: 'error',
  failed: 'error',
  cancelled: 'neutral',
}

// RequestStateBadge 显示请求结果
export function RequestStateBadge({ state }: { state: RequestState }) {
  const m = useMessages(requestMessages)
  return <Badge tone={requestTone[state]}>{m[state]}</Badge>
}
