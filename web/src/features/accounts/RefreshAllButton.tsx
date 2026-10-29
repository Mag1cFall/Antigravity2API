import { RefreshCw } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { useQuotaRefresh, useRefreshAll } from '@/features/accounts/actions'
import { accountMessages } from '@/features/accounts/messages'
import type { Account } from '@/features/accounts/types'
import { fill, useMessages } from '@/lib/i18n'

// RefreshAllButton 是刷新全部按钮：逐个读取 accounts 的额度，进行中图标旋转、显示进度并停用
export function RefreshAllButton({ accounts }: { accounts: Account[] }) {
  const m = useMessages(accountMessages)
  const progress = useQuotaRefresh()
  const refreshAll = useRefreshAll()
  const running = progress.total > 0
  return (
    <Button
      disabled={running || accounts.length === 0}
      aria-busy={running || undefined}
      onClick={() => {
        void refreshAll(accounts)
      }}
    >
      <RefreshCw size={16} aria-hidden className={running ? 'animate-spin' : undefined} />
      {running
        ? fill(m.refreshProgress, { done: progress.done, total: progress.total })
        : m.refreshAll}
    </Button>
  )
}
