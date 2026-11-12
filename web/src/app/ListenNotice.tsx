import { useStatus } from '@/features/service/queries'
import { fill, useMessages } from '@/lib/i18n'

// ListenNotice 在配置的监听端口被 Windows 保留而改用其他端口时，于内容区顶部显示实际监听地址
export function ListenNotice() {
  const m = useMessages('listen')
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
