import { BarChart3, List, ScrollText } from 'lucide-react'
import { Outlet } from 'react-router'

import { NavTabs } from '@/components/ui/nav-tabs'
import { PageHeader } from '@/components/ui/page'
import { requestMessages } from '@/features/requests/messages'
import { useMessages } from '@/lib/i18n'

// RequestsLayout 是请求页：标题与记录、用量、日志三个子页面的切换
export function RequestsLayout() {
  const m = useMessages(requestMessages)
  return (
    <>
      <PageHeader title={m.pageTitle} description={m.pageDescription} />
      <NavTabs
        label={m.tabs}
        items={[
          { to: '/requests', label: m.tabRecords, icon: List, end: true },
          { to: '/requests/usage', label: m.tabUsage, icon: BarChart3 },
          { to: '/requests/logs', label: m.tabLogs, icon: ScrollText },
        ]}
      />
      <Outlet />
    </>
  )
}
