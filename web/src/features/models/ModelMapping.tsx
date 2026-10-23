import { ArrowRight, Plus, Shuffle, Trash2 } from 'lucide-react'
import { useState } from 'react'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { Card, CardBody, CardHeader } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { ErrorState, Loading } from '@/components/ui/states'
import { toast } from '@/components/ui/toast'
import { Tooltip } from '@/components/ui/tooltip'
import { modelMessages } from '@/features/models/messages'
import { useSaveSettings, useSettings } from '@/features/settings/queries'
import { parseMapping } from '@/features/settings/schema'
import { fill, useMessages } from '@/lib/i18n'

// Mapping 是一条模型映射
interface Mapping {
  alias: string
  model: string
}

// MappingEditor 编辑已读取的模型映射，保存时只提交 MODEL_MAPPING
function MappingEditor({ saved, targets }: { saved: Mapping[]; targets: string[] }) {
  const m = useMessages(modelMessages)
  const [rows, setRows] = useState(saved)
  const [checked, setChecked] = useState(false)
  const save = useSaveSettings()
  const aliases = rows.map((row) => row.alias.trim())
  const errors = rows.map((row, index) => {
    const alias = aliases[index] ?? ''
    if (!alias || alias.includes(',')) return m.errAlias
    if (aliases.indexOf(alias) !== index) return m.errDuplicate
    if (!row.model) return m.errModel
    return undefined
  })
  const dirty = JSON.stringify(rows) !== JSON.stringify(saved)
  const update = (index: number, change: Partial<Mapping>) => {
    setRows((current) => current.map((row, at) => (at === index ? { ...row, ...change } : row)))
  }
  const submit = () => {
    setChecked(true)
    if (errors.some(Boolean)) return
    save.mutate(
      { MODEL_MAPPING: rows.map((row) => row.alias.trim() + ':' + row.model).join(',') },
      {
        onSuccess: () => {
          setChecked(false)
          toast({ tone: 'success', title: m.saved })
        },
        onError: (error) => {
          toast({ tone: 'error', title: fill(m.saveFailed, { message: error.message }) })
        },
      },
    )
  }
  return (
    <div className="flex flex-col gap-4">
      {rows.length === 0 ? (
        <p className="text-sm text-foreground-muted">{m.mappingEmpty}</p>
      ) : (
        <ul className="flex flex-col gap-3">
          {rows.map((row, index) => {
            const id = 'mapping-' + String(index)
            const error = checked ? errors[index] : undefined
            const options =
              row.model && !targets.includes(row.model) ? [row.model, ...targets] : targets
            return (
              <li key={index} className="flex flex-col gap-1">
                <div className="grid grid-cols-[16px_minmax(0,1fr)_auto] items-center gap-2 sm:grid-cols-[minmax(0,1fr)_16px_minmax(0,1fr)_auto]">
                  <Input
                    id={id}
                    aria-label={fill(m.aliasLabel, { index: index + 1 })}
                    aria-invalid={error ? true : undefined}
                    aria-describedby={error ? id + '-error' : undefined}
                    placeholder={m.aliasPlaceholder}
                    spellCheck={false}
                    autoComplete="off"
                    value={row.alias}
                    onChange={(event) => {
                      update(index, { alias: event.target.value })
                    }}
                    className="font-mono max-sm:col-span-3"
                  />
                  <ArrowRight
                    size={16}
                    aria-hidden
                    className="text-foreground-muted max-sm:rotate-90"
                  />
                  <Select
                    label={fill(m.targetLabel, { index: index + 1 })}
                    placeholder={m.targetPlaceholder}
                    value={row.model}
                    onValueChange={(value) => {
                      update(index, { model: value })
                    }}
                    options={options.map((value) => ({ value, label: value }))}
                    className="w-full font-mono"
                  />
                  <Tooltip label={fill(m.removeMapping, { index: index + 1 })}>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={fill(m.removeMapping, { index: index + 1 })}
                      className="enabled:hover:text-error-text"
                      onClick={() => {
                        setRows((current) => current.filter((_, at) => at !== index))
                      }}
                    >
                      <Trash2 size={16} aria-hidden />
                    </Button>
                  </Tooltip>
                </div>
                {error && (
                  <p id={id + '-error'} role="alert" className="text-sm text-error-text">
                    {error}
                  </p>
                )}
              </li>
            )
          })}
        </ul>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          onClick={() => {
            setRows((current) => [...current, { alias: '', model: '' }])
          }}
        >
          <Plus size={16} aria-hidden />
          {m.addMapping}
        </Button>
        {dirty && (
          <>
            <Button
              size="sm"
              variant="ghost"
              className="ml-auto"
              onClick={() => {
                setRows(saved)
                setChecked(false)
              }}
            >
              {m.discard}
            </Button>
            <Button size="sm" variant="primary" loading={save.isPending} onClick={submit}>
              {m.save}
            </Button>
          </>
        )}
      </div>
    </div>
  )
}

// ModelMapping 是模型映射卡片，targets 是目录中可作为目标的模型 ID
export function ModelMapping({ targets }: { targets: string[] }) {
  const m = useMessages(modelMessages)
  const app = useMessages(appMessages)
  const settings = useSettings()
  const value = settings.data?.values.MODEL_MAPPING ?? ''
  return (
    <Card>
      <CardHeader
        icon={<Shuffle size={16} aria-hidden />}
        title={m.mapping}
        description={m.mappingHint}
      />
      <CardBody>
        {settings.isPending ? (
          <Loading label={app.loading} />
        ) : settings.isError ? (
          <ErrorState
            title={m.mappingLoadFailed}
            error={settings.error}
            retryLabel={app.retry}
            onRetry={() => void settings.refetch()}
          />
        ) : (
          <MappingEditor key={value} saved={parseMapping(value)} targets={targets} />
        )}
      </CardBody>
    </Card>
  )
}
