import { Boxes, Shuffle } from 'lucide-react'
import { Outlet } from 'react-router'

import { NavTabs } from '@/components/ui/nav-tabs'
import { PageHeader } from '@/components/ui/page'
import { useAccounts } from '@/features/accounts/queries'
import { modelMessages } from '@/features/models/messages'
import { useModels } from '@/features/models/queries'
import { fill, useMessages } from '@/lib/i18n'

// ModelsLayout 是模型页：标题、模型与就绪账户数，以及目录、映射两个子页面的切换
export function ModelsLayout() {
  const m = useMessages(modelMessages)
  const models = useModels()
  const accounts = useAccounts()
  const ready = (accounts.data?.accounts ?? []).filter(
    (account) => account.state === 'ready',
  ).length
  return (
    <>
      <PageHeader
        title={m.title}
        description={
          models.data ? fill(m.summary, { count: models.data.models.length, ready }) : m.description
        }
      />
      <NavTabs
        label={m.tabs}
        items={[
          { to: '/models', label: m.tabCatalog, icon: Boxes, end: true },
          { to: '/models/mapping', label: m.mapping, icon: Shuffle },
        ]}
      />
      <Outlet />
    </>
  )
}
