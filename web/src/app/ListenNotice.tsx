import { useStatus } from '@/features/service/queries'
import { defineMessages, fill, useMessages } from '@/lib/i18n'

// listenMessages 是监听端口改用提示的文案
const listenMessages = defineMessages(
  { reserved: '端口 {reserved} 被 Windows 保留，服务改用 {listen}' },
  { reserved: 'Port {reserved} is reserved by Windows; the service listens on {listen}' },
  { reserved: '連接埠 {reserved} 被 Windows 保留，服務改用 {listen}' },
)

// ListenNotice 在配置的监听端口被 Windows 保留而改用其他端口时，于内容区顶部显示实际监听地址
export function ListenNotice() {
  const m = useMessages(listenMessages)
  const status = useStatus().data
  if (!status?.reserved_listen) return null
  return (
    <p
      role="status"
      className="sticky top-0 z-sticky bg-warning-soft px-6 py-2 text-center text-sm text-warning-text"
    >
      {fill(m.reserved, { reserved: status.reserved_listen, listen: status.listen })}
    </p>
  )
}
