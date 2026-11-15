import { ChevronDown, ChevronRight, ExternalLink } from 'lucide-react'
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { CopyButton } from '@/components/ui/copy-button'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import { MethodTag, RouteDetail, useExampleInputs } from '@/features/connect/ApiDetail'
import { address, type Endpoint, endpoints, type Route } from '@/features/connect/endpoints'
import { markProgress } from '@/features/connect/progress'
import { fill, useMessages } from '@/lib/i18n'

// RouteRow 是展开后的一个接口，点开后在右侧面板查看详情
function RouteRow({
  route,
  label,
  onOpen,
}: {
  route: Route
  label: string
  onOpen: (from: HTMLElement) => void
}) {
  return (
    <li>
      <button
        type="button"
        aria-haspopup="dialog"
        data-route={route.method + ' ' + route.path}
        onClick={(event) => {
          onOpen(event.currentTarget)
        }}
        className="grid w-full grid-cols-[3rem_minmax(0,1fr)_auto] items-center gap-x-2 rounded-sm px-2 py-1 text-left hover:bg-hover max-sm:grid-cols-[3rem_minmax(0,1fr)] pointer-coarse:py-2"
      >
        <MethodTag method={route.method} />
        <code className="font-mono text-xs wrap-anywhere">{address(route)}</code>
        <span className="flex items-center gap-1 text-xs text-foreground-muted max-sm:col-start-2 sm:whitespace-nowrap">
          {label}
          <ChevronRight size={14} aria-hidden className="text-foreground-subtle" />
        </span>
      </button>
    </li>
  )
}

// EndpointRoutes 是展开后的一种协议：认证请求头、全部接口与官方文档
function EndpointRoutes({
  item,
  apiKey,
  onOpen,
}: {
  item: Endpoint
  apiKey: string
  onOpen: (route: Route, from: HTMLElement) => void
}) {
  const m = useMessages('connect')
  return (
    <div id={'endpoint-' + item.protocol} className="col-span-full flex flex-col gap-3 pt-2">
      <div className="flex flex-col gap-1">
        <span className="text-xs font-medium text-foreground-muted">{m.auth}</span>
        {item.auth(apiKey).map((header) => (
          <code key={header} className="font-mono text-xs wrap-anywhere select-all">
            {header}
          </code>
        ))}
      </div>
      <div className="flex flex-col gap-1">
        <span className="text-xs font-medium text-foreground-muted">{m.routes}</span>
        <ul className="-mx-2 flex flex-col">
          {item.routes.map((route) => (
            <RouteRow
              key={route.method + route.path}
              route={route}
              label={m[route.label]}
              onOpen={(from) => {
                onOpen(route, from)
              }}
            />
          ))}
        </ul>
      </div>
      <a
        href={item.docs}
        target="_blank"
        rel="noopener noreferrer"
        className="inline-flex items-center gap-1 self-start text-sm text-primary hover:text-primary-hover"
      >
        {m.docs}
        <ExternalLink size={14} aria-hidden />
      </a>
    </div>
  )
}

// EndpointList 列出 OpenAI、Anthropic 与 Gemini 三种协议的 base URL 与主要接口，每种协议可展开认证、全部接口与官方文档，每个接口点开后在右侧面板查看协议、完整地址、认证、主要参数、curl 示例与官方文档；复制任一地址即完成引导的第二步
export function EndpointList() {
  const m = useMessages('connect')
  const app = useMessages('app')
  const hints = { openai: m.openaiHint, anthropic: m.anthropicHint, gemini: m.geminiHint }
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set())
  const [detail, setDetail] = useState<{ item: Endpoint; route: Route; from: HTMLElement }>()
  const [shown, setShown] = useState(false)
  const { origin, apiKey, modelFor } = useExampleInputs()
  return (
    <>
      <ul className="flex flex-col overflow-hidden rounded-md border border-border">
        {endpoints(origin).map((item) => {
          const expanded = open.has(item.protocol)
          return (
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
              <div className="flex items-center gap-1">
                <CopyButton
                  value={item.base}
                  label={fill(m.copyUrl, { name: m[item.protocol] })}
                  copiedLabel={m.copied}
                  onCopy={() => {
                    markProgress({ endpoint: true })
                  }}
                />
                <Button
                  variant="ghost"
                  size="sm"
                  aria-expanded={expanded}
                  aria-controls={'endpoint-' + item.protocol}
                  onClick={() => {
                    setOpen((current) => {
                      const next = new Set(current)
                      if (expanded) next.delete(item.protocol)
                      else next.add(item.protocol)
                      return next
                    })
                  }}
                >
                  {fill(m.routeCount, { count: item.routes.length })}
                  <ChevronDown
                    size={14}
                    aria-hidden
                    className={'transition-transform ' + (expanded ? 'rotate-180' : '')}
                  />
                </Button>
              </div>
              {expanded && (
                <EndpointRoutes
                  item={item}
                  apiKey={apiKey}
                  onOpen={(route, from) => {
                    setDetail({ item, route, from })
                    setShown(true)
                  }}
                />
              )}
            </li>
          )
        })}
      </ul>
      <Dialog open={shown} onOpenChange={setShown}>
        {detail && (
          <DialogContent
            layout="sheet"
            title={m[detail.route.label]}
            description={
              <code className="font-mono text-xs">
                {detail.route.method + ' ' + address(detail.route)}
              </code>
            }
            closeLabel={app.closeDialog}
            returnFocus={detail.from}
          >
            <RouteDetail
              route={detail.route}
              protocol={m[detail.item.protocol]}
              auth={detail.item.auth(apiKey)}
              model={modelFor(detail.route.model)}
              origin={origin}
            />
          </DialogContent>
        )}
      </Dialog>
    </>
  )
}
