import { useQuery } from '@tanstack/react-query'
import { ArrowUp, Folder, FolderOpen } from 'lucide-react'
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogTrigger } from '@/components/ui/dialog'
import { ErrorState, Loading } from '@/components/ui/states'
import { api } from '@/lib/api'
import { fill, useMessages } from '@/lib/i18n'

// PathListing 对应 GET /api/paths：当前目录、上一级与子目录，path 为空表示文件系统根列表
interface PathListing {
  path: string
  parent: string
  entries: { name: string; path: string }[]
}

// DirectoryPicker 是在服务所在电脑上浏览并选择目录的对话框，从 value 或其最近的已存在上级目录开始
export function DirectoryPicker({
  label,
  value,
  onChoose,
}: {
  label: string
  value: string
  onChoose: (path: string) => void
}) {
  const m = useMessages('settings')
  const app = useMessages('app')
  const [open, setOpen] = useState(false)
  const [at, setAt] = useState(value)
  const listing = useQuery({
    queryKey: ['paths', at],
    queryFn: () => api<PathListing>('/api/paths?path=' + encodeURIComponent(at)),
    enabled: open,
    retry: false,
  })
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (next) setAt(value)
        setOpen(next)
      }}
    >
      <DialogTrigger asChild>
        <Button className="shrink-0">
          <FolderOpen size={16} aria-hidden />
          {m.browse}
        </Button>
      </DialogTrigger>
      <DialogContent
        width="wide"
        title={fill(m.browseTitle, { name: label })}
        description={m.browseHint}
        closeLabel={app.closeDialog}
        footer={
          <>
            <Button
              onClick={() => {
                setOpen(false)
              }}
            >
              {m.browseCancel}
            </Button>
            <Button
              variant="primary"
              disabled={!listing.data?.path}
              onClick={() => {
                if (listing.data?.path) onChoose(listing.data.path)
                setOpen(false)
              }}
            >
              {m.browseChoose}
            </Button>
          </>
        }
      >
        {listing.isPending ? (
          <Loading label={app.loading} />
        ) : listing.isError ? (
          <ErrorState
            title={m.browseFailed}
            error={listing.error}
            retryLabel={app.retry}
            onRetry={() => void listing.refetch()}
          />
        ) : (
          <div className="flex flex-col gap-2">
            <div className="flex items-center gap-2">
              <Button
                size="sm"
                disabled={!listing.data.parent}
                onClick={() => {
                  setAt(listing.data.parent)
                }}
              >
                <ArrowUp size={16} aria-hidden />
                {m.browseParent}
              </Button>
              <span className="min-w-0 truncate font-mono text-xs text-foreground-muted">
                {listing.data.path || m.browseRoots}
              </span>
            </div>
            <ul
              aria-label={m.browseFolders}
              className="flex max-h-[320px] flex-col overflow-y-auto rounded-md border border-border"
            >
              {listing.data.entries.length === 0 ? (
                <li className="px-3 py-2 text-sm text-foreground-muted">{m.browseEmpty}</li>
              ) : (
                listing.data.entries.map((entry) => (
                  <li key={entry.path}>
                    <button
                      type="button"
                      className="flex w-full min-w-0 items-center gap-2 px-3 py-2 text-left text-sm hover:bg-hover"
                      onClick={() => {
                        setAt(entry.path)
                      }}
                    >
                      <Folder size={16} aria-hidden className="shrink-0 text-foreground-muted" />
                      <span className="truncate">{entry.name}</span>
                    </button>
                  </li>
                ))
              )}
            </ul>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
