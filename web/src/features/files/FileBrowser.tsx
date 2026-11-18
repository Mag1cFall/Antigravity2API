import { useQuery } from '@tanstack/react-query'
import {
  ArrowUp,
  Download,
  FileText,
  Folder,
  FolderOpen,
  HardDrive,
  House,
  Monitor,
} from 'lucide-react'
import { type ReactNode, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import { ErrorState, Loading } from '@/components/ui/states'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// PathEntry 是 GET /api/paths 列出的一个目录或文件
interface PathEntry {
  name: string
  path: string
  file?: boolean
  size?: number
}

// PathListing 对应 GET /api/paths：当前目录、上一级与目录项，path 为空表示文件系统根列表
interface PathListing {
  path: string
  parent: string
  entries: PathEntry[]
}

// Place 对应 GET /api/paths/places 的常用位置
interface Place {
  name: 'downloads' | 'desktop' | 'documents' | 'home'
  path: string
}

// placeIcons 是常用位置的图标
const placeIcons = { downloads: Download, desktop: Monitor, documents: FileText, home: House }

// usePlaces 读取服务所在电脑上的常用位置
export function usePlaces(enabled = true) {
  return useQuery({
    queryKey: ['paths', 'places'],
    queryFn: () => api<{ places: Place[] }>('/api/paths/places'),
    enabled,
    staleTime: Infinity,
  })
}

// FileBrowserDialog 是在服务所在电脑上浏览文件夹的对话框：folder 模式选择当前文件夹，files 模式勾选扩展名在 extensions 中的文件；左侧为下载、桌面、文档与主目录，start 为空时从下载文件夹开始
export function FileBrowserDialog({
  open,
  onOpenChange,
  title,
  description,
  start,
  mode,
  extensions = [],
  chooseLabel,
  onChoose,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: ReactNode
  start: string
  mode: 'folder' | 'files'
  extensions?: string[]
  chooseLabel: string
  onChoose: (paths: string[]) => void
}) {
  const m = useMessages('files')
  const app = useMessages('app')
  const { locale } = useI18n()
  const places = usePlaces(open)
  const [at, setAt] = useState<string>()
  const [picked, setPicked] = useState<string[]>([])
  const location = at ?? (start !== '' ? start : (places.data?.places[0]?.path ?? '.'))
  const listing = useQuery({
    queryKey: ['paths', location, mode === 'files' ? extensions.join(',') : ''],
    queryFn: () =>
      api<PathListing>(
        '/api/paths?' +
          new URLSearchParams({
            path: location,
            ...(mode === 'files' ? { files: extensions.join(',') } : {}),
          }).toString(),
      ),
    enabled: open && (Boolean(start) || at !== undefined || !places.isPending),
    retry: false,
  })
  const go = (path: string) => {
    setAt(path)
  }
  const toggle = (path: string, checked: boolean) => {
    setPicked((current) => (checked ? [...current, path] : current.filter((item) => item !== path)))
  }
  const current = listing.data?.path ?? ''
  const files = listing.data?.entries.filter((entry) => entry.file) ?? []
  const close = () => {
    setAt(undefined)
    setPicked([])
    onOpenChange(false)
  }
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (next) onOpenChange(true)
        else close()
      }}
    >
      <DialogContent
        width="wide"
        title={title}
        description={description}
        closeLabel={app.closeDialog}
        footer={
          <>
            {mode === 'files' && (
              <span role="status" className="mr-auto self-center text-sm text-foreground-muted">
                {picked.length > 0 ? fill(m.picked, { count: picked.length }) : m.pickHint}
              </span>
            )}
            <Button onClick={close}>{m.cancel}</Button>
            <Button
              variant="primary"
              disabled={mode === 'folder' ? !current : picked.length === 0}
              onClick={() => {
                onChoose(mode === 'folder' ? [current] : picked)
                close()
              }}
            >
              {chooseLabel}
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-3 sm:flex-row">
          <nav
            aria-label={m.places}
            className="flex shrink-0 flex-wrap gap-1 sm:w-[144px] sm:flex-col"
          >
            {(places.data?.places ?? []).map((place) => {
              const Icon = placeIcons[place.name]
              return (
                <button
                  key={place.name}
                  type="button"
                  aria-current={current === place.path ? 'true' : undefined}
                  className="flex h-control-sm items-center gap-2 rounded-sm px-2 text-left text-sm hover:bg-hover aria-[current=true]:bg-nav-active aria-[current=true]:text-primary"
                  onClick={() => {
                    go(place.path)
                  }}
                >
                  <Icon size={16} aria-hidden className="shrink-0" />
                  <span className="truncate">{m[place.name]}</span>
                </button>
              )
            })}
            <button
              type="button"
              aria-current={listing.data && !current ? 'true' : undefined}
              className="flex h-control-sm items-center gap-2 rounded-sm px-2 text-left text-sm hover:bg-hover aria-[current=true]:bg-nav-active aria-[current=true]:text-primary"
              onClick={() => {
                go('roots')
              }}
            >
              <HardDrive size={16} aria-hidden className="shrink-0" />
              <span className="truncate">{m.roots}</span>
            </button>
          </nav>
          <div className="flex min-w-0 flex-1 flex-col gap-2">
            <div className="flex items-center gap-2">
              <Button
                size="sm"
                disabled={!listing.data?.parent}
                onClick={() => {
                  if (listing.data?.parent) go(listing.data.parent)
                }}
              >
                <ArrowUp size={16} aria-hidden />
                {m.parent}
              </Button>
              <span className="min-w-0 truncate font-mono text-xs text-foreground-muted">
                {current || m.roots}
              </span>
            </div>
            {listing.isPending ? (
              <Loading label={app.loading} />
            ) : listing.isError ? (
              <ErrorState
                title={m.failed}
                error={listing.error}
                retryLabel={app.retry}
                onRetry={() => void listing.refetch()}
              />
            ) : (
              <ul
                aria-label={mode === 'files' ? m.entries : m.folders}
                className="flex h-[320px] flex-col overflow-y-auto rounded-md border border-border"
              >
                {listing.data.entries.length === 0 ? (
                  <li className="px-3 py-2 text-sm text-foreground-muted">
                    {mode === 'files' ? m.emptyFiles : m.emptyFolders}
                  </li>
                ) : (
                  listing.data.entries.map((entry) =>
                    entry.file ? (
                      <li key={entry.path}>
                        <label className="flex w-full min-w-0 cursor-pointer items-center gap-2 px-3 py-2 text-sm hover:bg-hover">
                          <Checkbox
                            checked={picked.includes(entry.path)}
                            onCheckedChange={(checked) => {
                              toggle(entry.path, checked === true)
                            }}
                          />
                          <FileText
                            size={16}
                            aria-hidden
                            className="shrink-0 text-foreground-muted"
                          />
                          <span className="min-w-0 flex-1 truncate">{entry.name}</span>
                          <span className="shrink-0 text-xs text-foreground-muted">
                            {formatBytes(entry.size ?? 0, locale)}
                          </span>
                        </label>
                      </li>
                    ) : (
                      <li key={entry.path}>
                        <button
                          type="button"
                          className="flex w-full min-w-0 items-center gap-2 px-3 py-2 text-left text-sm hover:bg-hover"
                          onClick={() => {
                            go(entry.path)
                          }}
                        >
                          <Folder
                            size={16}
                            aria-hidden
                            className="shrink-0 text-foreground-muted"
                          />
                          <span className="truncate">{entry.name}</span>
                        </button>
                      </li>
                    ),
                  )
                )}
              </ul>
            )}
            {mode === 'files' && files.length > 1 && (
              <button
                type="button"
                className="self-start text-sm font-medium text-primary hover:text-primary-hover"
                onClick={() => {
                  const all = files.every((entry) => picked.includes(entry.path))
                  setPicked((items) =>
                    all
                      ? items.filter((item) => !files.some((entry) => entry.path === item))
                      : [...new Set([...items, ...files.map((entry) => entry.path)])],
                  )
                }}
              >
                {files.every((entry) => picked.includes(entry.path)) ? m.pickNone : m.pickAll}
              </button>
            )}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}

// DirectoryPicker 是“浏览”按钮与选择文件夹的对话框，从 value 或其最近的已存在上级目录开始
export function DirectoryPicker({
  label,
  value,
  onChoose,
}: {
  label: string
  value: string
  onChoose: (path: string) => void
}) {
  const m = useMessages('files')
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button
        className="shrink-0"
        onClick={() => {
          setOpen(true)
        }}
      >
        <FolderOpen size={16} aria-hidden />
        {m.browse}
      </Button>
      <FileBrowserDialog
        open={open}
        onOpenChange={setOpen}
        title={fill(m.folderTitle, { name: label })}
        description={m.folderHint}
        start={value}
        mode="folder"
        chooseLabel={m.chooseFolder}
        onChoose={([path]) => {
          if (path) onChoose(path)
        }}
      />
    </>
  )
}
