import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { ChevronDown, FileText, RotateCcw } from 'lucide-react'
import { type ReactNode, useEffect, useRef, useState } from 'react'

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
import { Switch } from '@/components/ui/switch'
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
  const m = useMessages('settings')
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
  const m = useMessages('settings')
  const app = useMessages('app')
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
        width="wide"
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

// Section 对应 internal/prompt 的 Section：官方段的段名与放进 system 模板的原文
type Section = PromptTemplates['sections'][number]

// Block 是系统指令模板中首尾相接的一段文字，name 为段名，段与段之间的其余文字没有段名
interface Block {
  name?: string
  text: string
}

// Entry 是列表中固定的一行：官方段或客户端系统提示，official 为它放进模板的原文
interface Entry {
  key: string
  name?: string
  label: string
  hint: string
  official: string
}

// sectionStart 匹配一个段的开头：推荐插件段带条件头，之后是 <段名>
const sectionStart = /(\{\{if \.FeaturedPlugins\}\}\n?)?<([a-z_]+)>/g

// splitBlocks 把模板切成首尾相接的块：以 <段名> 开头、</段名> 结尾的段各为一块，推荐插件段连同条件头尾，其余文字各为一块
function splitBlocks(template: string): Block[] {
  const blocks: Block[] = []
  let at = 0
  for (const match of template.matchAll(sectionStart)) {
    const name = match[2] ?? ''
    const close = template.indexOf('</' + name + '>', match.index)
    if (match.index < at || close < 0) continue
    let end = close + name.length + 3
    if (match[1] && template.startsWith('{{end}}', end)) end += '{{end}}'.length
    if (match.index > at) blocks.push({ text: template.slice(at, match.index) })
    blocks.push({ name, text: template.slice(match.index, end) })
    at = end
  }
  if (at < template.length) blocks.push({ text: template.slice(at) })
  return blocks
}

// sectionParts 把段的原文拆成开头、正文与结尾：开头到 <段名> 后的换行为止，结尾从 </段名> 前的换行开始
function sectionParts(text: string, name: string): [string, string, string] {
  const open = text.indexOf('<' + name + '>') + name.length + 2
  const close = text.lastIndexOf('</' + name + '>')
  const head = open + (text[open] === '\n' ? 1 : 0)
  const tail = close > head && text[close - 1] === '\n' ? close - 1 : close
  return [text.slice(0, head), text.slice(head, tail), text.slice(tail)]
}

// officialText 返回官方段放进模板时的原文，推荐插件段的条件头后换行，与内置档的拼法相同
function officialText(section: Section) {
  return section.name === 'featured_plugins' ? section.source.replace('}}', '}}\n') : section.source
}

// blank 判断块是否为段之间只有空白的连接文字
function blank(block: Block | undefined) {
  return block !== undefined && block.name === undefined && block.text.trim() === ''
}

// conditional 判断块是否以条件开头，这样的块自带与前文之间的换行
function conditional(text: string) {
  return text.startsWith('{{if ')
}

// useInsert 返回编辑框的引用与在光标处插入文字的函数，插入后光标停在插入文字之后
function useInsert(value: string, onChange: (value: string) => void) {
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
  return { area, caret, insert }
}

// InsertMenu 是把变量放到光标处的下拉菜单
function InsertMenu({
  groups,
  onInsert,
}: {
  groups: { label: string; items: Insert[] }[]
  onInsert: (text: string) => void
}) {
  const m = useMessages('settings')
  return (
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
                  onInsert(item.text)
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
  )
}

// BlockEditor 是系统指令中一块的编辑框，official 不为空时可恢复官方原文
function BlockEditor({
  id,
  label,
  value,
  official,
  groups,
  onChange,
}: {
  id: string
  label: string
  value: string
  official: string | undefined
  groups: { label: string; items: Insert[] }[]
  onChange: (value: string) => void
}) {
  const m = useMessages('settings')
  const { area, insert } = useInsert(value, onChange)
  return (
    <div id={id} className="flex animate-fade-in flex-col gap-2">
      <div className="flex flex-wrap justify-end gap-2">
        {official !== undefined && (
          <Button
            size="sm"
            disabled={value === official}
            onClick={() => {
              onChange(official)
            }}
          >
            <RotateCcw size={16} aria-hidden />
            {m.blockRestore}
          </Button>
        )}
        <InsertMenu groups={groups} onInsert={insert} />
      </div>
      <Textarea
        ref={area}
        aria-label={label}
        rows={Math.min(16, Math.max(3, value.split('\n').length))}
        value={value}
        onChange={(event) => {
          onChange(event.target.value)
        }}
        spellCheck={false}
        autoComplete="off"
        className="resize-y font-mono text-xs leading-[20px] text-clip"
      />
    </div>
  )
}

// SystemBlocks 按段编辑 system 模板：官方段按官方顺序逐行列出，客户端系统提示排在最后，开启的行与其余文字依次拼成模板；段的编辑框只编辑 <段名> 之间的正文，关闭的行展开时显示官方原文，开启时插回官方顺序的位置
function SystemBlocks({
  id,
  value,
  onChange,
  entries,
  groups,
}: {
  id: string
  value: string
  onChange: (value: string) => void
  entries: Entry[]
  groups: { label: string; items: Insert[] }[]
}) {
  const m = useMessages('settings')
  const { locale } = useI18n()
  const [open, setOpen] = useState<string>()
  useEffect(() => {
    if (open === undefined) return
    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    document
      .getElementById(id + '-' + open + '-text')
      ?.scrollIntoView({ block: 'nearest', behavior: reduced ? 'auto' : 'smooth' })
  }, [id, open])
  const blocks = splitBlocks(value)
  const client = blocks.findIndex(
    (block) => block.name === undefined && block.text.includes('{{.Client}}'),
  )
  const clientRank = entries.findIndex((entry) => entry.name === undefined)
  const order = new Map(entries.map((entry, index) => [entry.name, index]))
  const rankOf = (index: number) => {
    const name = blocks[index]?.name
    if (name !== undefined) return order.get(name)
    return index === client && clientRank >= 0 ? clientRank : undefined
  }
  const present = new Set(blocks.map((_, index) => rankOf(index)))
  const emit = (next: Block[]) => {
    onChange(next.map((block) => block.text).join(''))
  }
  const remove = (index: number) => {
    const next = [...blocks]
    if (conditional(next[index]?.text ?? '')) next.splice(index, 1)
    else if (blank(next[index - 1])) next.splice(index - 1, 2)
    else next.splice(index, blank(next[index + 1]) ? 2 : 1)
    emit(next)
  }
  const add = (rank: number) => {
    const entry = entries[rank]
    if (!entry) return
    let after = -1
    blocks.forEach((_, index) => {
      if ((rankOf(index) ?? Infinity) < rank) after = index
    })
    const block = { name: entry.name, text: entry.official }
    const following = blocks[after + 1]
    const inserted = conditional(entry.official)
      ? [block]
      : after >= 0
        ? [{ text: '\n' }, block]
        : following && !conditional(following.text)
          ? [block, { text: '\n' }]
          : [block]
    emit([...blocks.slice(0, after + 1), ...inserted, ...blocks.slice(after + 1)])
  }
  const rows: { rank: number | undefined; index: number | undefined }[] = []
  let pending = 0
  const flush = (until: number) => {
    for (; pending < Math.min(until, entries.length); pending++) {
      if (!present.has(pending)) rows.push({ rank: pending, index: undefined })
    }
  }
  blocks.forEach((block, index) => {
    const rank = rankOf(index)
    if (rank !== undefined) {
      flush(rank)
      pending = Math.max(pending, rank + 1)
      rows.push({ rank, index })
    } else if (!blank(block)) {
      let next = index + 1
      while (next < blocks.length && rankOf(next) === undefined) next++
      flush(rankOf(next) ?? entries.length)
      rows.push({ rank: undefined, index })
    }
  })
  flush(entries.length)
  return (
    <ul
      aria-labelledby={id + '-label'}
      className="flex flex-col divide-y divide-border rounded-md border border-border"
    >
      {rows.map(({ rank, index }) => {
        const entry = rank === undefined ? undefined : entries[rank]
        const block = index === undefined ? undefined : blocks[index]
        const key = entry?.key ?? 'block-' + String(index)
        const label = entry?.label ?? m.blockOther
        const hint = entry?.hint ?? m.blockOtherHint
        const rowId = id + '-' + key
        const parts = block?.name === undefined ? undefined : sectionParts(block.text, block.name)
        const text = parts ? parts[1] : block?.text
        const official =
          entry === undefined
            ? undefined
            : entry.name === undefined
              ? entry.official
              : sectionParts(entry.official, entry.name)[1]
        const expanded = open === key
        return (
          <li key={key} className="flex flex-col gap-3 px-4 py-3">
            <div className="flex items-start gap-3">
              <span className="flex h-control-sm items-center pointer-coarse:h-touch">
                <Switch
                  id={rowId}
                  aria-describedby={rowId + '-hint'}
                  checked={block !== undefined}
                  onCheckedChange={(checked) => {
                    if (checked && rank !== undefined) add(rank)
                    if (!checked && index !== undefined) remove(index)
                    if (!checked && entry === undefined) setOpen(undefined)
                  }}
                />
              </span>
              <div className="flex min-w-0 flex-1 flex-col gap-1">
                <div className="flex flex-wrap items-center justify-between gap-x-3">
                  <label
                    htmlFor={rowId}
                    className="flex min-w-0 flex-wrap items-baseline gap-x-2 text-sm"
                  >
                    <span className="font-medium">{label}</span>
                    {entry?.name && (
                      <span className="font-mono text-xs break-all text-foreground-muted">
                        {entry.name}
                      </span>
                    )}
                    {text !== undefined && official !== undefined && text !== official && (
                      <span className="text-xs text-foreground-muted">{m.blockEdited}</span>
                    )}
                  </label>
                  <span className="flex shrink-0 items-center gap-1 text-xs text-foreground-muted tabular-nums">
                    {fill(m.blockChars, {
                      chars: formatNumber((block?.text ?? entry?.official ?? '').length, locale),
                    })}
                    <Button
                      size="sm"
                      variant="ghost"
                      aria-label={fill(block ? m.blockEdit : m.blockView, { name: label })}
                      aria-expanded={expanded}
                      aria-controls={rowId + '-text'}
                      onClick={() => {
                        setOpen(expanded ? undefined : key)
                      }}
                    >
                      {block ? m.blockOpen : m.blockText}
                      <ChevronDown size={16} aria-hidden className={expanded ? 'rotate-180' : ''} />
                    </Button>
                  </span>
                </div>
                <p id={rowId + '-hint'} className="text-sm text-foreground-muted">
                  {hint}
                </p>
              </div>
            </div>
            {expanded &&
              (block !== undefined && text !== undefined ? (
                <BlockEditor
                  id={rowId + '-text'}
                  label={label}
                  value={text}
                  official={official}
                  groups={groups}
                  onChange={(edited) => {
                    const replaced = parts ? parts[0] + edited + parts[2] : edited
                    emit(
                      blocks.map((item, at) => (at === index ? { ...item, text: replaced } : item)),
                    )
                  }}
                />
              ) : (
                <pre
                  id={rowId + '-text'}
                  className="max-h-[320px] animate-fade-in overflow-y-auto rounded-md bg-muted px-4 py-3 font-mono text-xs leading-[20px] break-words whitespace-pre-wrap select-text"
                >
                  {official}
                </pre>
              ))}
          </li>
        )
      })}
    </ul>
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
  children,
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
  children?: ReactNode
}) {
  const m = useMessages('settings')
  const { area, caret, insert } = useInsert(value, onChange)
  return (
    <fieldset disabled={locked} className="flex min-w-0 flex-col gap-2">
      <div className="flex flex-wrap items-end justify-between gap-x-4 gap-y-2">
        <div className="flex min-w-0 flex-col gap-1">
          {children === undefined ? (
            <label htmlFor={id} className="text-base font-medium">
              {label}
            </label>
          ) : (
            <span id={id + '-label'} className="text-base font-medium">
              {label}
            </span>
          )}
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
          {children === undefined && <InsertMenu groups={groups} onInsert={insert} />}
        </div>
      </div>
      {children ?? (
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
      )}
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
  const m = useMessages('settings')
  const app = useMessages('app')
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
    featured_plugins: [m.secFeatured, m.secFeaturedHint],
    user_information: [m.secUser, m.secUserHint],
  }
  const client = splitBlocks(tiers.full.system).find(
    (block) => block.name === undefined && block.text.includes('{{.Client}}'),
  )
  const entries: Entry[] = sections.map((section) => {
    const [label, hint] = labels[section.name] ?? [section.name, '']
    return { key: section.name, name: section.name, label, hint, official: officialText(section) }
  })
  if (client) {
    entries.push({
      key: 'client',
      label: m.varClient,
      hint: m.blockClientHint,
      official: client.text,
    })
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
        groups={[]}
        status={system.trim() === '' ? m.templateSystemEmpty : chars(preview.data?.system)}
        error={failure('system')}
        locked={systemLocked}
      >
        <SystemBlocks
          id="SYSTEM_PROMPT_TEMPLATE"
          value={system}
          onChange={onSystemChange}
          entries={entries}
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
          ]}
        />
      </TemplateEditor>
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
