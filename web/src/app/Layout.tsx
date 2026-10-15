import { useQuery } from '@tanstack/react-query'
import { Navigate, Outlet, useLocation } from 'react-router'

import { connectionKey, useAdminEvents } from '@/app/events'
import { Header } from '@/app/Header'
import { appMessages } from '@/app/messages'
import { Loading } from '@/components/ui/states'
import { useSession } from '@/features/service/queries'
import { useMessages } from '@/lib/i18n'

// Layout 是登录后的外壳：顶栏与居中的页面内容，内容区单独滚动
export function Layout() {
  const m = useMessages(appMessages)
  const location = useLocation()
  const session = useSession()
  const authenticated = session.data?.authenticated === true
  useAdminEvents(authenticated)
  const connected = useQuery({
    queryKey: connectionKey,
    queryFn: () => true,
    enabled: false,
    initialData: true,
  }).data
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
        className="sr-only z-toast rounded-md bg-surface px-4 py-2 focus:not-sr-only focus:fixed focus:top-2 focus:left-2"
      >
        {m.skipToContent}
      </a>
      <Header />
      <main
        id="main"
        tabIndex={-1}
        className="relative min-h-0 flex-1 [scrollbar-gutter:stable_both-edges] overflow-y-auto focus:outline-none"
      >
        {!connected && (
          <p
            role="status"
            className="sticky top-0 z-sticky bg-warning-soft px-6 py-2 text-center text-sm text-warning-text"
          >
            {m.eventsDisconnected}
          </p>
        )}
        <div className="mx-auto flex w-full max-w-page flex-col gap-6 px-6 pt-4 pb-12 max-sm:px-4 max-sm:pt-2">
          <Outlet />
        </div>
      </main>
    </div>
  )
}
