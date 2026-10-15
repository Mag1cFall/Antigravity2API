import { CopyButton } from '@/components/ui/copy-button'
import { endpoints, serviceOrigin } from '@/features/connect/endpoints'
import { connectMessages } from '@/features/connect/messages'
import { markProgress } from '@/features/connect/progress'
import { fill, useMessages } from '@/lib/i18n'

// EndpointList 列出 OpenAI、Anthropic 与 Gemini 三种协议的 base URL 与主要接口，复制任一地址即完成引导的第二步
export function EndpointList() {
  const m = useMessages(connectMessages)
  const hints = { openai: m.openaiHint, anthropic: m.anthropicHint, gemini: m.geminiHint }
  return (
    <ul className="flex flex-col overflow-hidden rounded-md border border-border">
      {endpoints(serviceOrigin()).map((item) => (
        <li
          key={item.protocol}
          className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 border-t border-border px-4 py-3 first:border-t-0 sm:grid-cols-[160px_minmax(0,1fr)_auto]"
        >
          <div className="min-w-0 max-sm:col-span-2">
            <span className="block text-sm font-medium">{m[item.protocol]}</span>
            <span className="block text-xs text-foreground-muted">{hints[item.protocol]}</span>
          </div>
          <div className="min-w-0">
            <code className="block truncate font-mono text-sm select-all">{item.base}</code>
            <span className="block truncate font-mono text-xs text-foreground-subtle">
              {item.paths.join('  ·  ')}
            </span>
          </div>
          <CopyButton
            value={item.base}
            label={fill(m.copyUrl, { name: m[item.protocol] })}
            copiedLabel={m.copied}
            onCopy={() => {
              markProgress({ endpoint: true })
            }}
          />
        </li>
      ))}
    </ul>
  )
}
