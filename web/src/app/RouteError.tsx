import { FileQuestion } from 'lucide-react'
import { Link, useRouteError } from 'react-router'

import { Button } from '@/components/ui/button'
import { EmptyState, ErrorState } from '@/components/ui/states'
import { fill, useMessages } from '@/lib/i18n'

// RouteError 是路由级错误边界，渲染失败时在面板内说明
export function RouteError() {
  const m = useMessages('app')
  const error = useRouteError()
  console.error(error)
  return (
    <ErrorState
      title={m.pageError}
      error={error}
      retryLabel={m.reload}
      onRetry={() => {
        window.location.reload()
      }}
    />
  )
}

// NotFound 是不存在路径的空状态
export function NotFound({ path }: { path: string }) {
  const m = useMessages('app')
  return (
    <EmptyState
      icon={<FileQuestion size={20} aria-hidden />}
      title={m.notFound}
      description={fill(m.notFoundDescription, { path })}
      action={
        <Button asChild>
          <Link to="/">{m.backHome}</Link>
        </Button>
      }
    />
  )
}
