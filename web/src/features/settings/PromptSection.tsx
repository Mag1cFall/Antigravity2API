import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { ChevronDown, FileText } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'

import { appMessages } from '@/app/messages'
import { Button } from '@/components/ui/button'
import { CodeBlock } from '@/components/ui/code-block'
import { Dialog, DialogContent, DialogTrigger } from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Textarea } from '@/components/ui/input'
import { RadioGroup } from '@/components/ui/radio-group'
import { Segmented } from '@/components/ui/segmented'
import { ErrorState, Loading } from '@/components/ui/states'
import { settingsMessages } from '@/features/settings/messages'
import type { SettingsForm } from '@/features/settings/schema'
import { api } from '@/lib/api'
import { formatNumber } from '@/lib/format'
import { fill, useI18n, useMessages } from '@/lib/i18n'

type Tier = SettingsForm['SYSTEM_PROMPT']

// Templates 对应 internal/prompt 的 Custom：一档的 system 模板与 user 模板
interface Templates {
  system: string
  user: string
}

// Preview 对应 internal/prompt 的 Preview：以示例数据渲染的系统指令与首轮 user 消息
type Preview = Templates

// PromptTemplates 对应 GET /api/prompt/templates：内置三档的模板与全部官方段的原文
interface PromptTemplates {
  tiers: Record<Exclude<Tier, 'custom'>, Templates>
  sections: { name: string; source: string }[]
}

// Insert 是插入菜单中的一项：显示名、等宽显示的标识与插入的文字
interface Insert {
  label: string
  code: string
  text: string
}

// usePreview 以示例数据渲染档位的原文，自定义档用传入的两个模板，enabled 为假时不发出请求
function usePreview(tier: Tier, custom: Templates, enabled = true) {
  const body = tier === 'custom' ? { tier, ...custom } : { tier }
  return useQuery({
    queryKey: ['prompt-preview', body],
    queryFn: () => api<Preview>('/api/prompt/preview', { method: 'POST', json: body }),
    placeholderData: keepPreviousData,
    retry: false,
    enabled,
  })
}

// usePromptTemplates 读取内置三档的模板与官方段原文
function usePromptTemplates() {
  return useQuery({
    queryKey: ['prompt-templates'],
    queryFn: () => api<PromptTemplates>('/api/prompt/templates'),
    staleTime: Infinity,
  })
}

// useSettled 返回停止变化 400 ms 后的值
function useSettled<T>(value: T) {
  const [settled, setSettled] = useState(value)
  useEffect(() => {
    const timer = setTimeout(() => {
      setSettled(value)
    }, 400)
    return () => {
      clearTimeout(timer)
    }
  }, [value])
  return settled
}

// PromptTierField 是系统提示档位的单选，内置三档的说明带内置模板的段落数与取整到百位的字符数，模板读到之前说明不带数字
export function PromptTierField({
  value,
  onChange,
}: {
  value: Tier
  onChange: (value: Tier) => void
}) {
  const m = useMessages(settingsMessages)
  const { locale } = useI18n()
  const templates = usePromptTemplates().data
  const chars = (template: string) => formatNumber(Math.round(template.length / 100) * 100, locale)
  const size = templates && {
    full: fill(m.promptFullSize, {
      count: templates.sections.length,
      chars: chars(templates.tiers.full.system),
    }),
    medium: fill(m.promptSize, { chars: chars(templates.tiers.medium.system) }),
    aggressive: fill(m.promptSize, { chars: chars(templates.tiers.aggressive.system) }),
  }
  return (
    <RadioGroup
      label={m.tier}
      value={value}
      onValueChange={onChange}
      options={[
        {
          value: 'full',
          label: m.promptFull,
          description: fill(m.promptFullHint, { size: size?.full ?? '' }),
        },
        {
          value: 'medium',
          label: m.promptMedium,
          description: fill(m.promptMediumHint, { size: size?.medium ?? '' }),
        },
        {
          value: 'aggressive',
          label: m.promptAggressive,
          description: fill(m.promptAggressiveHint, { size: size?.aggressive ?? '' }),
        },
        { value: 'custom', label: m.promptCustom, description: m.promptCustomHint },
      ]}
    />
  )
}

// PromptPreviewDialog 是查看各档位原文的对话框，打开时显示 tier，自定义档按表单中尚未保存的模板渲染
export function PromptPreviewDialog({ tier, custom }: { tier: Tier; custom: Templates }) {
  const m = useMessages(settingsMessages)
  const app = useMessages(appMessages)
  const { locale } = useI18n()
  const [shown, setShown] = useState<Tier>(tier)
  const [open, setOpen] = useState(false)
  const preview = usePreview(shown, custom, open)
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (next) setShown(tier)
        setOpen(next)
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

// TemplateEditor 是一个模板的编辑框：载入预设替换全文，插入菜单把官方段落或变量放到光标处，下方显示 status；locked 时各控件停用
function TemplateEditor({
  id,
  label,
  hint,
  rows,
  value,
  onChange,
  presets,
  groups,
  status,
  error,
  locked,
}: {
  id: string
  label: string
  hint: string
  rows: number
  value: string
  onChange: (value: string) => void
  presets: { label: string; text: string }[]
  groups: { label: string; items: Insert[] }[]
  status: string
  error: string | undefined
  locked: boolean
}) {
  const m = useMessages(settingsMessages)
  const area = useRef<HTMLTextAreaElement>(null)
  const caret = useRef<number>(undefined)
  useEffect(() => {
    if (caret.current === undefined || !area.current) return
    area.current.focus()
    area.current.setSelectionRange(caret.current, caret.current)
    caret.current = undefined
  }, [value])
  const insert = (text: string) => {
    const start = area.current?.selectionStart ?? value.length
    const end = area.current?.selectionEnd ?? value.length
    caret.current = start + text.length
    onChange(value.slice(0, start) + text + value.slice(end))
  }
  return (
    <fieldset disabled={locked} className="flex min-w-0 flex-col gap-2">
      <div className="flex flex-wrap items-end justify-between gap-x-4 gap-y-2">
        <div className="flex min-w-0 flex-col gap-1">
          <label htmlFor={id} className="text-base font-medium">
            {label}
          </label>
          <p id={id + '-hint'} className="text-sm text-foreground-muted">
            {hint}
          </p>
        </div>
        <div className="flex shrink-0 gap-2">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button size="sm">
                {m.templateLoad}
                <ChevronDown size={16} aria-hidden />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent>
              {presets.map((preset) => (
                <DropdownMenuItem
                  key={preset.label}
                  onSelect={() => {
                    caret.current = 0
                    onChange(preset.text)
                  }}
                >
                  {preset.label}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button size="sm">
                {m.templateInsert}
                <ChevronDown size={16} aria-hidden />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent>
              {groups.map((group, index) => (
                <div key={group.label}>
                  {index > 0 && <DropdownMenuSeparator />}
                  <DropdownMenuLabel>{group.label}</DropdownMenuLabel>
                  {group.items.map((item) => (
                    <DropdownMenuItem
                      key={item.code}
                      onSelect={() => {
                        insert(item.text)
                      }}
                    >
                      {item.label}
                      <span className="ml-auto pl-6 font-mono text-xs text-foreground-muted">
                        {item.code}
                      </span>
                    </DropdownMenuItem>
                  ))}
                </div>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>
      <Textarea
        ref={area}
        id={id}
        rows={rows}
        value={value}
        onChange={(event) => {
          onChange(event.target.value)
        }}
        spellCheck={false}
        autoComplete="off"
        aria-invalid={error ? true : undefined}
        aria-describedby={id + '-hint ' + id + '-status'}
        className="resize-y font-mono text-xs leading-[20px] text-clip"
      />
      <p
        id={id + '-status'}
        role={error ? 'alert' : undefined}
        className={'text-sm ' + (error ? 'text-error-text' : 'text-foreground-muted')}
      >
        {error ?? (status || '\u00a0')}
      </p>
    </fieldset>
  )
}

// PromptTemplateFields 编辑自定义档的 system 模板与 user 模板，输入停下后以示例数据渲染并显示字符数或模板错误
export function PromptTemplateFields({
  system,
  user,
  systemLocked,
  userLocked,
  onSystemChange,
  onUserChange,
}: {
  system: string
  user: string
  systemLocked: boolean
  userLocked: boolean
  onSystemChange: (value: string) => void
  onUserChange: (value: string) => void
}) {
  const m = useMessages(settingsMessages)
  const app = useMessages(appMessages)
  const { locale } = useI18n()
  const templates = usePromptTemplates()
  const preview = usePreview('custom', { system: useSettled(system), user: useSettled(user) })
  if (templates.isPending) return <Loading label={app.loading} />
  if (templates.isError) {
    return (
      <ErrorState
        title={m.templatesFailed}
        error={templates.error}
        retryLabel={app.retry}
        onRetry={() => void templates.refetch()}
      />
    )
  }
  const { tiers, sections } = templates.data
  const sectionLabels: Record<string, string> = {
    identity: m.secIdentity,
    artifacts: m.secArtifacts,
    conversation_transcript: m.secTranscript,
    guidelines: m.secGuidelines,
    communication_style: m.secStyle,
    slash_commands: m.secSlash,
    messaging: m.secMessaging,
    subagents: m.secSubagents,
    skills: m.secSkills,
    featured_plugins: m.secFeatured,
    user_information: m.secUser,
  }
  const variable = (label: string, field: string): Insert => ({
    label,
    code: field,
    text: '{{.' + field + '}}',
  })
  // failure 取模板错误中属于 name 模板的说明，Go 的错误以 template: <name>: 标出模板
  const failure = (name: 'system' | 'user') => {
    const message = preview.error?.message
    const at = message?.indexOf('template: ' + name + ':') ?? -1
    return message !== undefined && at >= 0
      ? fill(m.templateError, { message: message.slice(at + 'template: '.length) })
      : undefined
  }
  const chars = (text: string | undefined) =>
    text === undefined ? '' : fill(m.templateChars, { chars: formatNumber(text.length, locale) })
  return (
    <div className="flex flex-col gap-6">
      <TemplateEditor
        id="SYSTEM_PROMPT_TEMPLATE"
        label={m.templateSystem}
        hint={m.templateSystemHint}
        rows={14}
        value={system}
        onChange={onSystemChange}
        presets={[
          { label: m.promptFull, text: tiers.full.system },
          { label: m.promptMedium, text: tiers.medium.system },
          { label: m.promptAggressive, text: tiers.aggressive.system },
        ]}
        groups={[
          {
            label: m.templateVariables,
            items: [
              variable(m.varClient, 'Client'),
              variable(m.varConversation, 'ConversationID'),
              variable(m.varModel, 'ModelDisplayName'),
              variable(m.varOS, 'OS'),
              variable(m.varAppData, 'AppDataDir'),
              variable(m.varSeparator, 'Separator'),
              variable(m.varPlugins, 'FeaturedPlugins'),
            ],
          },
          {
            label: m.templateSections,
            items: sections.map((section) => ({
              label: sectionLabels[section.name] ?? section.name,
              code: section.name,
              text: section.source,
            })),
          },
        ]}
        status={system.trim() === '' ? m.templateSystemEmpty : chars(preview.data?.system)}
        error={failure('system')}
        locked={systemLocked}
      />
      <TemplateEditor
        id="SYSTEM_PROMPT_USER_TEMPLATE"
        label={m.templateUser}
        hint={m.templateUserHint}
        rows={6}
        value={user}
        onChange={onUserChange}
        presets={[
          { label: m.wrapFull, text: tiers.full.user },
          { label: m.wrapTime, text: tiers.medium.user },
          { label: m.wrapNone, text: '' },
        ]}
        groups={[
          {
            label: m.templateVariables,
            items: [
              variable(m.varText, 'Text'),
              variable(m.varTime, 'LocalTime'),
              variable(m.varModel, 'ModelDisplayName'),
              { label: m.varFirst, code: 'First', text: '{{if .First}}{{end}}' },
            ],
          },
        ]}
        status={chars(preview.data?.user)}
        error={failure('user')}
        locked={userLocked}
      />
    </div>
  )
}
