import { Download } from 'lucide-react'
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Tooltip } from '@/components/ui/tooltip'
import { ExportDialog } from '@/features/files/ExportDialog'
import { useMessages } from '@/lib/i18n'

// RecordsExport 是导出请求记录 CSV 的按钮与对话框：query 是请求记录接口的时间范围与筛选参数，disabledReason 非空时按钮停用并在悬停时说明原因
export function RecordsExport({
  label,
  query,
  disabledReason,
}: {
  label: string
  query: URLSearchParams
  disabledReason?: string | false
}) {
  const m = useMessages('requests')
  const [open, setOpen] = useState(false)
  const search = query.toString()
  return (
    <>
      <Tooltip disabledReason={disabledReason}>
        <Button
          size="sm"
          disabled={Boolean(disabledReason)}
          onClick={() => {
            setOpen(true)
          }}
        >
          <Download size={16} aria-hidden />
          {label}
        </Button>
      </Tooltip>
      <ExportDialog
        open={open}
        onOpenChange={setOpen}
        title={m.exportTitle}
        description={m.exportDescription}
        path={'/api/usage/records.csv?' + search}
        copy={async () => {
          const response = await fetch('/api/usage/records.csv?' + search, {
            credentials: 'same-origin',
          })
          if (!response.ok) throw new Error(response.statusText)
          return (await response.text()).replace(/^\uFEFF/, '')
        }}
      />
    </>
  )
}
