import { useMutation } from '@tanstack/react-query'
import { Check, Copy, Download, FolderOpen, FolderSearch } from 'lucide-react'
import { type ReactNode, useEffect, useRef, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Callout } from '@/components/ui/callout'
import { CopyButton } from '@/components/ui/copy-button'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import { Input, Textarea } from '@/components/ui/input'
import { FileBrowserDialog, usePlaces } from '@/features/files/FileBrowser'
import { useSession } from '@/features/service/queries'
import { api, errorMessage } from '@/lib/api'
import { fill, useMessages } from '@/lib/i18n'

// folderKey 是本地存储中记住的导出文件夹的键
const folderKey = 'antigravity2api.exportFolder'

// rememberedFolder 返回上次导出的文件夹，没有时为空
function rememberedFolder() {
  try {
    return localStorage.getItem(folderKey) ?? ''
  } catch {
    return ''
  }
}

// SavedFiles 是保存文件的接口返回的写入路径
interface SavedFiles {
  files: string[]
}

// revealFile 在服务所在电脑的文件管理器中显示文件
function revealFile(path: string) {
  return api('/api/paths/reveal', { method: 'POST', json: { path } })
}

// ExportDialog 是导出文件的对话框：本机访问时保存到运行服务的电脑上的文件夹（默认上次的文件夹或下载文件夹，可浏览选择），从其他电脑访问时由浏览器下载 download 给出的文件，也可把内容复制到剪贴板；保存后在对话框中列出写入的文件，本机访问时可在文件夹中显示。save 以文件夹调用 path 对应的保存接口，options 是保存前的附加选项，只在本机访问时显示
export function ExportDialog({
  open,
  onOpenChange,
  title,
  description,
  path,
  body = {},
  copy,
  download,
  options,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: ReactNode
  path: string
  body?: Record<string, unknown>
  copy?: () => Promise<string>
  download: { name: string; file: () => Promise<Blob> }
  options?: ReactNode
}) {
  const m = useMessages('files')
  const app = useMessages('app')
  const session = useSession()
  const local = session.data?.local ?? true
  const places = usePlaces(open && local)
  const [folder, setFolder] = useState(rememberedFolder)
  const [browsing, setBrowsing] = useState(false)
  const [copied, setCopied] = useState(false)
  const [manual, setManual] = useState<string>()
  const manualField = useRef<HTMLTextAreaElement>(null)
  useEffect(() => {
    manualField.current?.select()
  }, [manual])
  const target = folder || (places.data?.places[0]?.path ?? '')
  const save = useMutation({
    mutationFn: () =>
      api<SavedFiles>(path, { method: 'POST', json: { ...body, dir: target.trim() } }),
    onSuccess: () => {
      try {
        localStorage.setItem(folderKey, target.trim())
      } catch {
        // 本地存储不可用时下次仍从下载文件夹开始
      }
    },
  })
  const copyContent = useMutation({
    mutationFn: async () => {
      if (!copy) return
      const content = await copy()
      if (window.isSecureContext) {
        await navigator.clipboard.writeText(content)
        setCopied(true)
      } else {
        setManual(content)
      }
    },
  })
  const fetchFile = useMutation({
    mutationFn: async () => {
      const link = document.createElement('a')
      link.href = URL.createObjectURL(await download.file())
      link.download = download.name
      link.click()
      URL.revokeObjectURL(link.href)
    },
  })
  const reveal = useMutation({ mutationFn: revealFile })
  const close = () => {
    onOpenChange(false)
    save.reset()
    copyContent.reset()
    fetchFile.reset()
    reveal.reset()
    setCopied(false)
    setManual(undefined)
  }
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) close()
      }}
    >
      <DialogContent
        title={title}
        description={description}
        closeLabel={app.closeDialog}
        footer={
          save.isSuccess ? (
            <Button variant="primary" onClick={close}>
              {m.done}
            </Button>
          ) : (
            <>
              {copy && (
                <Button
                  className="mr-auto"
                  loading={copyContent.isPending}
                  onClick={() => {
                    copyContent.mutate()
                  }}
                >
                  {copied ? (
                    <Check size={16} aria-hidden className="text-success-text" />
                  ) : (
                    <Copy size={16} aria-hidden />
                  )}
                  {copied ? m.copied : m.copy}
                </Button>
              )}
              <Button onClick={close}>{m.cancel}</Button>
              {local ? (
                <Button
                  variant="primary"
                  disabled={!target.trim()}
                  loading={save.isPending}
                  onClick={() => {
                    save.mutate()
                  }}
                >
                  {m.save}
                </Button>
              ) : (
                <Button
                  variant="primary"
                  loading={fetchFile.isPending}
                  onClick={() => {
                    fetchFile.mutate()
                  }}
                >
                  <Download size={16} aria-hidden />
                  {m.download}
                </Button>
              )}
            </>
          )
        }
      >
        {save.isSuccess ? (
          <div className="flex flex-col gap-3">
            <p role="status" className="text-sm text-success-text">
              {save.data.files.length > 1 ? m.savedMany : m.savedOne}
            </p>
            <ul className="flex flex-col gap-1">
              {save.data.files.map((file) => (
                <li
                  key={file}
                  className="flex min-w-0 items-center gap-1 rounded-sm bg-muted py-1 pr-1 pl-3"
                >
                  <span className="min-w-0 flex-1 font-mono text-xs break-all">{file}</span>
                  <CopyButton value={file} label={m.copyPath} copiedLabel={m.copied} />
                </li>
              ))}
            </ul>
            {local && save.data.files[0] && (
              <Button
                className="self-start"
                loading={reveal.isPending}
                onClick={() => {
                  if (save.data.files[0]) reveal.mutate(save.data.files[0])
                }}
              >
                <FolderSearch size={16} aria-hidden />
                {m.reveal}
              </Button>
            )}
            {reveal.isError && (
              <p role="alert" className="text-sm wrap-anywhere text-error-text">
                {errorMessage(reveal.error)}
              </p>
            )}
          </div>
        ) : (
          <div className="flex flex-col gap-4">
            {local ? (
              <>
                <Field id="export-folder" label={m.folder}>
                  <div className="flex gap-2">
                    <Input
                      id="export-folder"
                      value={target}
                      spellCheck={false}
                      autoComplete="off"
                      onChange={(event) => {
                        setFolder(event.target.value)
                      }}
                      className="font-mono"
                    />
                    <Button
                      className="shrink-0"
                      onClick={() => {
                        setBrowsing(true)
                      }}
                    >
                      <FolderOpen size={16} aria-hidden />
                      {m.browse}
                    </Button>
                  </div>
                </Field>
                {options}
              </>
            ) : (
              <p className="text-sm text-foreground-muted">
                {fill(m.downloadHint, { name: download.name })}
              </p>
            )}
            {fetchFile.isError && (
              <Callout tone="error" role="alert">
                <p className="wrap-anywhere">{errorMessage(fetchFile.error)}</p>
              </Callout>
            )}
            {save.isError && (
              <Callout tone="error" role="alert">
                <p className="wrap-anywhere">{errorMessage(save.error)}</p>
              </Callout>
            )}
            {copyContent.isError && (
              <p role="alert" className="text-sm wrap-anywhere text-error-text">
                {fill(m.copyFailed, { message: errorMessage(copyContent.error) })}
              </p>
            )}
            {manual !== undefined && (
              <Field id="export-content" label={m.copyManual}>
                <Textarea
                  ref={manualField}
                  id="export-content"
                  readOnly
                  rows={8}
                  value={manual}
                  onFocus={(event) => {
                    event.currentTarget.select()
                  }}
                  className="font-mono text-xs"
                />
              </Field>
            )}
            <FileBrowserDialog
              open={browsing}
              onOpenChange={setBrowsing}
              title={m.chooseExportFolder}
              start={target}
              mode="folder"
              chooseLabel={m.chooseFolder}
              onChoose={([chosen]) => {
                if (chosen) setFolder(chosen)
              }}
            />
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
