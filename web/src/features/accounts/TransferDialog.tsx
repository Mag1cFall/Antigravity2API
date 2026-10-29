import { useMutation, useQueryClient } from '@tanstack/react-query'
import { type ReactNode, useState } from 'react'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogTrigger } from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import { Textarea } from '@/components/ui/input'
import { toast } from '@/components/ui/toast'
import { keepAccounts } from '@/features/accounts/actions'
import { accountsKey } from '@/features/accounts/queries'
import { api } from '@/lib/api'
import { defineMessages, fill, useMessages } from '@/lib/i18n'

// transferMessages 是账户 JSON 导入与导出的文案
export const transferMessages = defineMessages(
  {
    import: '导入 JSON',
    export: '导出 JSON',
    exportSelected: '导出所选（{count}）',
    title: '导入账户 JSON',
    description:
      '支持 Antigravity Manager、CLIProxyAPI、sub2api 等项目的账户文件，或粘贴含 refresh token 的文本',
    files: '账户文件',
    paste: '或粘贴内容',
    placeholder: '[{"email": "...", "refresh_token": "1//..."}]',
    submit: '导入',
    cancel: '取消',
    pasted: '粘贴的内容',
    done: '已导入 {count} 个账户',
    partial: '已导入 {count} 个账户，{failed} 项失败',
    failure: '{account}：{error}',
  },
  {
    import: 'Import JSON',
    export: 'Export JSON',
    exportSelected: 'Export selected ({count})',
    title: 'Import account JSON',
    description:
      'Account files from Antigravity Manager, CLIProxyAPI, sub2api and similar tools, or text with refresh tokens',
    files: 'Account files',
    paste: 'Or paste',
    placeholder: '[{"email": "...", "refresh_token": "1//..."}]',
    submit: 'Import',
    cancel: 'Cancel',
    pasted: 'Pasted text',
    done: 'Imported {count} accounts',
    partial: 'Imported {count} accounts, {failed} failed',
    failure: '{account}: {error}',
  },
  {
    import: '匯入 JSON',
    export: '匯出 JSON',
    exportSelected: '匯出所選（{count}）',
    title: '匯入帳號 JSON',
    description:
      '支援 Antigravity Manager、CLIProxyAPI、sub2api 等專案的帳號檔案，或貼上含 refresh token 的文字',
    files: '帳號檔案',
    paste: '或貼上內容',
    placeholder: '[{"email": "...", "refresh_token": "1//..."}]',
    submit: '匯入',
    cancel: '取消',
    pasted: '貼上的內容',
    done: '已匯入 {count} 個帳號',
    partial: '已匯入 {count} 個帳號，{failed} 項失敗',
    failure: '{account}：{error}',
  },
)

// TransferResult 是 POST /api/accounts/import/json 的响应
interface TransferResult {
  accounts: string[]
  failed: { account: string; error: string }[]
}

// exportURL 返回下载账户 JSON 的地址，ids 为空时导出全部账户
export function exportURL(ids: readonly string[]) {
  return (
    '/api/accounts/export' +
    (ids.length ? '?' + new URLSearchParams(ids.map((id) => ['id', id])).toString() : '')
  )
}

// ImportAccountsDialog 从账户文件或粘贴的内容导入账户，每个文件与粘贴内容各发一次请求，结果合并显示；children 为触发按钮
export function ImportAccountsDialog({ children }: { children: ReactNode }) {
  const m = useMessages(transferMessages)
  const app = useMessages(appMessages)
  const client = useQueryClient()
  const [open, setOpen] = useState(false)
  const [files, setFiles] = useState<File[]>([])
  const [text, setText] = useState('')
  const [result, setResult] = useState<TransferResult>()
  const close = (next: boolean) => {
    setOpen(next)
    if (!next) {
      setFiles([])
      setText('')
      setResult(undefined)
    }
  }
  const run = useMutation({
    mutationFn: async () => {
      const sources = [
        ...(await Promise.all(
          files.map(async (file) => ({ name: file.name, body: await file.text() })),
        )),
        ...(text.trim() ? [{ name: m.pasted, body: text }] : []),
      ]
      const merged: TransferResult = { accounts: [], failed: [] }
      for (const source of sources) {
        try {
          const response = await api<TransferResult>('/api/accounts/import/json', {
            method: 'POST',
            body: source.body,
          })
          merged.accounts.push(...response.accounts)
          merged.failed.push(...response.failed)
        } catch (error) {
          merged.failed.push({
            account: source.name,
            error: error instanceof Error ? error.message : String(error),
          })
        }
      }
      return merged
    },
    onSuccess: (merged) => {
      keepAccounts(merged.accounts)
      void client.invalidateQueries({ queryKey: accountsKey })
      if (merged.failed.length) {
        setResult(merged)
        return
      }
      toast({ tone: 'success', title: fill(m.done, { count: merged.accounts.length }) })
      close(false)
    },
  })
  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogTrigger asChild>{children}</DialogTrigger>
      <DialogContent
        title={m.title}
        description={m.description}
        closeLabel={app.closeDialog}
        footer={
          <>
            <Button
              variant="secondary"
              onClick={() => {
                close(false)
              }}
            >
              {m.cancel}
            </Button>
            <Button
              loading={run.isPending}
              disabled={!files.length && !text.trim()}
              onClick={() => {
                run.mutate()
              }}
            >
              {m.submit}
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-4">
          <Field id="account-files" label={m.files}>
            <input
              id="account-files"
              type="file"
              multiple
              accept=".json,.txt,application/json,text/plain"
              onChange={(event) => {
                setFiles([...(event.target.files ?? [])])
              }}
              className="text-sm file:mr-3 file:rounded-md file:border file:border-border file:bg-surface file:px-3 file:py-1 file:text-sm hover:file:border-primary-border"
            />
          </Field>
          <Field id="account-paste" label={m.paste}>
            <Textarea
              id="account-paste"
              rows={5}
              value={text}
              placeholder={m.placeholder}
              onChange={(event) => {
                setText(event.target.value)
              }}
              className="font-mono text-xs"
            />
          </Field>
          {result && (
            <div role="status" className="flex flex-col gap-1 text-sm">
              <p>
                {fill(m.partial, { count: result.accounts.length, failed: result.failed.length })}
              </p>
              <ul className="max-h-32 overflow-y-auto text-xs text-error-text">
                {result.failed.map((item) => (
                  <li key={item.account + item.error} className="break-words">
                    {fill(m.failure, { account: item.account, error: item.error })}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
