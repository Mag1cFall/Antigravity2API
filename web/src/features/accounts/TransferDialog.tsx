import { useMutation, useQueryClient } from '@tanstack/react-query'
import { FileUp, X } from 'lucide-react'
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Field } from '@/components/ui/field'
import { Textarea } from '@/components/ui/input'
import { keepAccounts } from '@/features/accounts/actions'
import { accountsKey } from '@/features/accounts/queries'
import { api } from '@/lib/api'
import { defineMessages, fill, useMessages } from '@/lib/i18n'

// transferMessages 是账户文件导入与导出的文案
export const transferMessages = defineMessages(
  {
    export: '导出 JSON',
    exportSelected: '导出所选（{count}）',
    description: '支持 Antigravity Manager、CLIProxyAPI、sub2api 等项目导出的账户文件',
    files: '账户文件',
    drop: '把文件拖到这里，或',
    choose: '选择文件',
    removeFile: '移除 {name}',
    paste: '或粘贴内容',
    placeholder: '[{"email": "...", "refresh_token": "1//..."}]',
    submit: '导入',
    pasted: '粘贴的内容',
    done: '已导入 {count} 个账户',
    partial: '已导入 {count} 个账户，{failed} 项失败',
    failure: '{account}：{error}',
  },
  {
    export: 'Export JSON',
    exportSelected: 'Export selected ({count})',
    description:
      'Account files exported by Antigravity Manager, CLIProxyAPI, sub2api and similar tools',
    files: 'Account files',
    drop: 'Drop files here, or',
    choose: 'choose files',
    removeFile: 'Remove {name}',
    paste: 'Or paste',
    placeholder: '[{"email": "...", "refresh_token": "1//..."}]',
    submit: 'Import',
    pasted: 'Pasted text',
    done: 'Imported {count} accounts',
    partial: 'Imported {count} accounts, {failed} failed',
    failure: '{account}: {error}',
  },
  {
    export: '匯出 JSON',
    exportSelected: '匯出所選（{count}）',
    description: '支援 Antigravity Manager、CLIProxyAPI、sub2api 等專案匯出的帳號檔案',
    files: '帳號檔案',
    drop: '把檔案拖曳到這裡，或',
    choose: '選擇檔案',
    removeFile: '移除 {name}',
    paste: '或貼上內容',
    placeholder: '[{"email": "...", "refresh_token": "1//..."}]',
    submit: '匯入',
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

// ImportAccountFiles 是添加账户对话框中的导入账户文件：拖入或选择文件（透明的文件输入框盖住整块区域）、粘贴内容，每个文件与粘贴内容各发一次请求，结果合并显示在下方
export function ImportAccountFiles() {
  const m = useMessages(transferMessages)
  const client = useQueryClient()
  const [files, setFiles] = useState<File[]>([])
  const [text, setText] = useState('')
  const [dragging, setDragging] = useState(false)
  const add = (list: FileList | null) => {
    const names = new Set(files.map((file) => file.name))
    setFiles([...files, ...[...(list ?? [])].filter((file) => !names.has(file.name))])
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
      if (merged.failed.length === 0) {
        setFiles([])
        setText('')
      }
    },
  })
  const result = run.data
  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-foreground-muted">{m.description}</p>
      <Field id="account-files" label={m.files}>
        <label
          className={
            'relative flex cursor-pointer items-center justify-center gap-2 rounded-md border border-dashed px-4 py-6 text-sm text-foreground-muted transition-colors hover:border-primary-border has-focus-visible:border-primary ' +
            (dragging ? 'border-primary bg-primary-soft' : 'border-border')
          }
        >
          <FileUp size={16} aria-hidden className="shrink-0" />
          <span>
            {m.drop} <span className="font-medium text-primary">{m.choose}</span>
          </span>
          <input
            id="account-files"
            type="file"
            multiple
            accept=".json,.txt,application/json,text/plain"
            title={m.choose}
            onDragEnter={() => {
              setDragging(true)
            }}
            onDragLeave={() => {
              setDragging(false)
            }}
            onDrop={() => {
              setDragging(false)
            }}
            onChange={(event) => {
              add(event.target.files)
              event.target.value = ''
            }}
            className="absolute inset-0 cursor-pointer opacity-0"
          />
        </label>
        {files.length > 0 && (
          <ul className="flex flex-wrap gap-2">
            {files.map((file) => (
              <li
                key={file.name}
                className="flex h-control-sm max-w-full min-w-0 items-center gap-1 rounded-sm bg-muted pr-1 pl-3 font-mono text-xs"
              >
                <span className="truncate">{file.name}</span>
                <button
                  type="button"
                  aria-label={fill(m.removeFile, { name: file.name })}
                  className="inline-flex size-6 shrink-0 items-center justify-center rounded-sm text-foreground-muted hover:bg-hover hover:text-foreground"
                  onClick={() => {
                    setFiles(files.filter((item) => item !== file))
                  }}
                >
                  <X size={14} aria-hidden />
                </button>
              </li>
            ))}
          </ul>
        )}
      </Field>
      <Field id="account-paste" label={m.paste}>
        <Textarea
          id="account-paste"
          rows={4}
          value={text}
          placeholder={m.placeholder}
          onChange={(event) => {
            setText(event.target.value)
          }}
          className="font-mono text-xs"
        />
      </Field>
      <div className="flex flex-wrap items-center gap-3">
        <Button
          variant="primary"
          loading={run.isPending}
          disabled={!files.length && !text.trim()}
          onClick={() => {
            run.mutate()
          }}
        >
          {m.submit}
        </Button>
        {result?.failed.length === 0 && (
          <p role="status" className="text-sm text-success-text">
            {fill(m.done, { count: result.accounts.length })}
          </p>
        )}
      </div>
      {result && result.failed.length > 0 ? (
        <div role="status" className="flex flex-col gap-1 text-sm">
          <p>{fill(m.partial, { count: result.accounts.length, failed: result.failed.length })}</p>
          <ul className="max-h-32 overflow-y-auto text-xs text-error-text">
            {result.failed.map((item) => (
              <li key={item.account + item.error} className="break-words">
                {fill(m.failure, { account: item.account, error: item.error })}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  )
}
