import { ExternalLink } from 'lucide-react'
import type { ReactNode } from 'react'

import { CodeBlock } from '@/components/ui/code-block'
import { CopyButton } from '@/components/ui/copy-button'
import { useApiKeys } from '@/features/connect/ApiKeys'
import { address, curl, type Param, type Route, serviceOrigin } from '@/features/connect/endpoints'
import { useModels } from '@/features/models/queries'
import { kindOf, type ModelKind } from '@/features/models/types'
import { useMessages } from '@/lib/i18n'

// methodTones 是各 HTTP 方法标签的文字颜色
const methodTones: Record<Route['method'], string> = {
  GET: 'text-success-text',
  POST: 'text-primary',
  PATCH: 'text-warning-text',
  DELETE: 'text-error-text',
}

// MethodTag 是固定宽度的 HTTP 方法标签
export function MethodTag({ method }: { method: Route['method'] }) {
  return (
    <span className={'w-12 shrink-0 font-mono text-xs font-semibold ' + methodTones[method]}>
      {method}
    </span>
  )
}

// useExampleInputs 返回示例使用的服务地址、API key 与各用途的模型 ID：key 取第一个 API key，模型取实时目录中该用途的模型，对话模型优先 flash，目录中没有时为占位文字
export function useExampleInputs() {
  const m = useMessages('connect')
  const { keys } = useApiKeys()
  const models = useModels().data?.models ?? []
  const modelFor = (kind: ModelKind = 'chat') => {
    const matched = models.filter((model) => kindOf(model) === kind)
    const preferred =
      kind === 'chat' ? matched.find((model) => model.id.includes('flash')) : undefined
    return (preferred ?? matched[0])?.id ?? m.modelPlaceholder
  }
  return { origin: serviceOrigin(), apiKey: keys[0] ?? 'antigravity2api', modelFor }
}

// Section 是详情中带小标题的一节
function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <h3 className="text-xs font-medium text-foreground-muted">{title}</h3>
      {children}
    </section>
  )
}

// ParamList 列出主要参数：名称、位置、是否必填与说明
function ParamList({ params }: { params: Param[] }) {
  const m = useMessages('connect')
  const places = {
    path: m.inPath,
    query: m.inQuery,
    header: m.inHeader,
    body: m.inBody,
    form: m.inForm,
  }
  if (params.length === 0) return <p className="text-sm text-foreground-muted">{m.noParams}</p>
  return (
    <dl className="flex flex-col rounded-md border border-border">
      {params.map((param) => (
        <div
          key={param.in + param.name}
          className="grid grid-cols-1 gap-1 border-t border-border px-3 py-2 first:border-t-0 sm:grid-cols-[minmax(0,15rem)_minmax(0,1fr)] sm:gap-4"
        >
          <dt className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
            <code className="font-mono text-xs font-semibold wrap-anywhere">{param.name}</code>
            <span className="text-xs text-foreground-subtle">{places[param.in]}</span>
            {param.required && <span className="text-xs text-warning-text">{m.required}</span>}
          </dt>
          <dd className="text-sm text-foreground-muted">{m[param.label]}</dd>
        </div>
      ))}
    </dl>
  )
}

// RouteDetail 是一个接口或能力示例的详情：协议、完整地址、认证请求头、主要参数、curl 示例与官方文档；protocol 是协议名称，auth 是认证请求头，model 是示例中的模型 ID
export function RouteDetail({
  route,
  protocol,
  auth,
  model,
  origin,
}: {
  route: Route
  protocol: string
  auth: string[]
  model: string
  origin: string
}) {
  const m = useMessages('connect')
  const urls = [route.path, ...(route.alias ? [route.alias] : [])].map(
    (path) => origin + address({ ...route, path }),
  )
  return (
    <div className="flex flex-col gap-6">
      <Section title={m.protocol}>
        <p className="text-sm">{protocol}</p>
      </Section>
      <Section title={m.url}>
        <ul className="flex flex-col gap-1">
          {urls.map((url) => (
            <li key={url} className="flex items-center gap-2">
              <MethodTag method={route.method} />
              <code className="min-w-0 flex-1 font-mono text-xs wrap-anywhere select-all">
                {url}
              </code>
              <CopyButton value={url} label={m.copyAddress} copiedLabel={m.copied} />
            </li>
          ))}
        </ul>
      </Section>
      <Section title={m.auth}>
        <ul className="flex flex-col gap-1">
          {auth.map((header) => (
            <li key={header}>
              <code className="font-mono text-xs wrap-anywhere select-all">{header}</code>
            </li>
          ))}
        </ul>
      </Section>
      <Section title={m.params}>
        <ParamList params={route.params} />
      </Section>
      <CodeBlock
        code={curl(route, origin, auth, model)}
        title={m.example}
        copyLabel={m.copy}
        copiedLabel={m.copied}
      />
      <a
        href={route.docs}
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
