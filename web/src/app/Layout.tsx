import { Navigate, Outlet, useLocation } from 'react-router'

import { useAdminEvents, useConnection } from '@/app/events'
import { Header } from '@/app/Header'
import { ListenNotice } from '@/app/ListenNotice'
import { appMessages } from '@/app/messages'
import { useRememberLocation } from '@/app/remember'
import { Loading } from '@/components/ui/states'
import { useSession } from '@/features/service/queries'
import { useMessages } from '@/lib/i18n'

// Layout 是登录后的外壳：顶栏与居中的页面内容，内容区单独滚动并至少占满可视高度
export function Layout() {
  const m = useMessages(appMessages)
  const location = useLocation()
  const session = useSession()
  const authenticated = session.data?.authenticated === true
  useAdminEvents(authenticated)
  useRememberLocation()
  const connection = useConnection()
  if (session.isPending) return <Loading label={m.loading} />
  if (session.data && !authenticated) {
    return (
      <Navigate
        to={'/login?next=' + encodeURIComponent(location.pathname + location.search)}
        replace
      />
    )
  }
  return (
    <div className="flex h-dvh flex-col overflow-hidden safe-x">
      <a
        href="#main"
        className="sr-only z-toast rounded-md bg-surface text-sm font-medium shadow-popover focus:not-sr-only focus:fixed focus:top-3 focus:left-3 focus:px-4 focus:py-2"
      >
        {m.skipToContent}
      </a>
      <Header />
      <main
        id="main"
        tabIndex={-1}
        className="relative flex min-h-0 flex-1 [scrollbar-gutter:stable_both-edges] flex-col overflow-y-auto focus:outline-none"
      >
        <ListenNotice />
        {connection === 'closed' && (
          <p
            role="status"
            className="sticky top-0 z-sticky bg-warning-soft px-6 py-2 text-center text-sm text-warning-text"
          >
            {m.eventsDisconnected}
          </p>
        )}
        <div className="mx-auto flex w-full max-w-page flex-1 flex-col gap-4 px-6 pt-2 pb-8 max-sm:px-4">
          <Outlet />
        </div>
      </main>
    </div>
  )
}
