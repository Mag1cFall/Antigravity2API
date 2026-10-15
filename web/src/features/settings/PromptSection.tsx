import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { ChevronDown, FileText } from 'lucide-react'
import { useState } from 'react'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { CodeBlock } from '@/components/ui/code-block'
import { Dialog, DialogContent, DialogTrigger } from '@/components/ui/dialog'
import { RadioGroup } from '@/components/ui/radio-group'
import { Segmented } from '@/components/ui/segmented'
import { ErrorState, Loading } from '@/components/ui/states'
import { Switch } from '@/components/ui/switch'
import { settingsMessages } from '@/features/settings/messages'
import type { SettingsForm } from '@/features/settings/schema'
import { api } from '@/lib/api'
import { formatNumber } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

// Preview 对应 internal/prompt 的 Preview：以占位数据渲染的系统指令、首轮 user 消息与全部官方段
interface Preview {
  system: string
  user: string
  sections: { name: string; text: string }[]
}

type Tier = SettingsForm['SYSTEM_PROMPT']
type Wrap = SettingsForm['SYSTEM_PROMPT_WRAP']
// usePreview 读取档位的原文，自定义档按传入的段与包装方式渲染
function usePreview(tier: Tier, sections: string[], wrap: Wrap) {
  const params = new URLSearchParams({ tier })
  if (tier === 'custom') {
    params.set('sections', sections.join(','))
    params.set('wrap', wrap)
  }
  return useQuery({
    queryKey: ['prompt-preview', params.toString()],
    queryFn: () => api<Preview>('/api/prompt/preview?' + params.toString()),
    placeholderData: keepPreviousData,
  })
}

// PromptPreviewDialog 是查看各档位原文的对话框，打开时显示 tier，自定义档按表单中尚未保存的选择渲染
export function PromptPreviewDialog({
  tier,
  sections,
  wrap,
}: {
  tier: Tier
  sections: string[]
  wrap: Wrap
}) {
  const m = useMessages(settingsMessages)
  const app = useMessages(appMessages)
  const { locale } = useI18n()
  const [shown, setShown] = useState<Tier>(tier)
  const preview = usePreview(shown, sections, wrap)
  return (
    <Dialog
      onOpenChange={(open) => {
        if (open) setShown(tier)
      }}
    >
      <DialogTrigger asChild>
        <Button size="sm" className="w-fit">
          <FileText size={16} aria-hidden />
          {m.view}
        </Button>
      </DialogTrigger>
      <DialogContent
        wide
        title={m.viewTitle}
        description={m.viewDescription}
        closeLabel={app.closeDialog}
      >
        <div className="flex flex-col gap-4">
          <Segmented
            label={m.tier}
            value={shown}
            onValueChange={setShown}
            options={[
              { value: 'full', label: m.promptFull },
              { value: 'medium', label: m.promptMedium },
              { value: 'aggressive', label: m.promptAggressive },
              { value: 'custom', label: m.promptCustom },
            ]}
          />
          {preview.isPending ? (
            <Loading label={app.loading} />
          ) : preview.isError ? (
            <ErrorState
              title={m.viewFailed}
              error={preview.error}
              retryLabel={app.retry}
              onRetry={() => void preview.refetch()}
            />
          ) : (
            <>
              <CodeBlock
                wrap
                title={fill(m.viewSystem, {
                  count: formatNumber(preview.data.system.length, locale),
                })}
                code={preview.data.system}
                copyLabel={m.copy}
                copiedLabel={m.copied}
              />
              <CodeBlock
                wrap
                title={m.viewUser}
                code={preview.data.user}
                copyLabel={m.copy}
                copiedLabel={m.copied}
              />
            </>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}

// PromptSectionsField 勾选自定义档保留的官方段，每段可展开原文；value 按官方顺序排列
export function PromptSectionsField({
  value,
  onChange,
}: {
  value: string[]
  onChange: (value: string[]) => void
}) {
  const m = useMessages(settingsMessages)
  const app = useMessages(appMessages)
  const { locale } = useI18n()
  const [open, setOpen] = useState<string>()
  const sections = useQuery({
    queryKey: ['prompt-preview', 'tier=full'],
    queryFn: () => api<Preview>('/api/prompt/preview?tier=full'),
    select: (data) => data.sections,
  })
  if (sections.isPending) return <Loading label={app.loading} />
  if (sections.isError) {
    return (
      <ErrorState
        title={m.sectionsFailed}
        error={sections.error}
        retryLabel={app.retry}
        onRetry={() => void sections.refetch()}
      />
    )
  }
  const labels: Record<string, [string, string]> = {
    identity: [m.secIdentity, m.secIdentityHint],
    artifacts: [m.secArtifacts, m.secArtifactsHint],
    conversation_transcript: [m.secTranscript, m.secTranscriptHint],
    guidelines: [m.secGuidelines, m.secGuidelinesHint],
    communication_style: [m.secStyle, m.secStyleHint],
    slash_commands: [m.secSlash, m.secSlashHint],
    messaging: [m.secMessaging, m.secMessagingHint],
    subagents: [m.secSubagents, m.secSubagentsHint],
    skills: [m.secSkills, m.secSkillsHint],
    user_information: [m.secUser, m.secUserHint],
  }
  const chosen = sections.data.filter((section) => value.includes(section.name))
  const chars = chosen.reduce((sum, section) => sum + section.text.length, 0)
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <span className="text-base font-medium">{m.sectionsLabel}</span>
        <p className="text-sm text-foreground-muted">
          {fill(m.sectionsHint, { count: chosen.length, chars: formatNumber(chars, locale) })}
        </p>
      </div>
      <ul className="flex flex-col divide-y divide-border rounded-md border border-border">
        {sections.data.map((section) => {
          const [name, hint] = labels[section.name] ?? [section.name, '']
          const id = 'section-' + section.name
          return (
            <li key={section.name} className="flex flex-col gap-3 px-4 py-3">
              <div className="flex items-start gap-3">
                <span className="flex h-control-sm items-center pointer-coarse:h-touch">
                  <Switch
                    id={id}
                    aria-describedby={id + '-hint'}
                    checked={value.includes(section.name)}
                    onCheckedChange={(checked) => {
                      onChange(
                        sections.data
                          .map((item) => item.name)
                          .filter((item) =>
                            item === section.name ? checked : value.includes(item),
                          ),
                      )
                    }}
                  />
                </span>
                <div className="flex min-w-0 flex-1 flex-col gap-1">
                  <div className="flex flex-wrap items-center justify-between gap-x-3">
                    <label
                      htmlFor={id}
                      className="flex min-w-0 flex-wrap items-baseline gap-x-2 text-sm"
                    >
                      <span className="font-medium">{name}</span>
                      <span className="font-mono text-xs break-all text-foreground-muted">
                        {section.name}
                      </span>
                    </label>
                    <span className="flex shrink-0 items-center gap-1 text-xs text-foreground-muted tabular-nums">
                      {fill(m.sectionChars, { chars: formatNumber(section.text.length, locale) })}
                      <Button
                        size="sm"
                        variant="ghost"
                        aria-expanded={open === section.name}
                        aria-controls={id + '-text'}
                        onClick={() => {
                          setOpen(open === section.name ? undefined : section.name)
                        }}
                      >
                        {m.sectionShow}
                        <ChevronDown
                          size={16}
                          aria-hidden
                          className={open === section.name ? 'rotate-180' : ''}
                        />
                      </Button>
                    </span>
                  </div>
                  <p id={id + '-hint'} className="text-sm text-foreground-muted">
                    {hint}
                  </p>
                </div>
              </div>
              {open === section.name && (
                <pre
                  id={id + '-text'}
                  className="max-h-80 overflow-y-auto rounded-md bg-muted px-4 py-3 font-mono text-xs leading-[20px] break-words whitespace-pre-wrap select-text"
                >
                  {section.text}
                </pre>
              )}
            </li>
          )
        })}
      </ul>
    </div>
  )
}

// PromptWrapField 选择自定义档 user 轮的包装方式
export function PromptWrapField({
  value,
  onChange,
}: {
  value: Wrap
  onChange: (value: Wrap) => void
}) {
  const m = useMessages(settingsMessages)
  return (
    <div className="flex flex-col gap-3">
      <span className="text-base font-medium">{m.wrap}</span>
      <RadioGroup
        label={m.wrap}
        value={value}
        onValueChange={onChange}
        options={[
          { value: 'full', label: m.wrapFull, description: m.wrapFullHint },
          { value: 'time', label: m.wrapTime, description: m.wrapTimeHint },
          { value: 'none', label: m.wrapNone, description: m.wrapNoneHint },
        ]}
      />
    </div>
  )
}
