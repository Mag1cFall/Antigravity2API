import cherryStudioIcon from '@lobehub/icons-static-svg/icons/cherrystudio-color.svg?raw'
import claudeCodeIcon from '@lobehub/icons-static-svg/icons/claudecode-color.svg?raw'
import codexIcon from '@lobehub/icons-static-svg/icons/codex-color.svg?raw'
import deepSeekIcon from '@lobehub/icons-static-svg/icons/deepseek-color.svg?raw'
import magpieIcon from '@lobehub/icons-static-svg/icons/magpie.svg?raw'
import openClawIcon from '@lobehub/icons-static-svg/icons/openclaw-color.svg?raw'
import openCodeIcon from '@lobehub/icons-static-svg/icons/opencode.svg?raw'

import { curlExample } from '@/features/connect/capabilities'
import type { Model } from '@/features/models/types'
import type { Catalog } from '@/locales/zh-CN'

import ompIcon from './icons/omp.svg?raw'
import piIcon from './icons/pi.svg?raw'
import terminalIcon from './icons/terminal.svg?raw'

// ConnectText 是接入页文案
type ConnectText = Catalog['connect']

// ConnectKey 是接入页文案的键
type ConnectKey = keyof ConnectText

// Shell 是写环境变量的终端
export type Shell = 'unix' | 'windows'

// Protocol 是客户端连接本服务使用的接入协议
export type Protocol = 'chat' | 'responses' | 'anthropic' | 'gemini'

// protocolNames 是各接入协议的显示名称
export const protocolNames: Record<Protocol, string> = {
  chat: 'OpenAI Chat Completions',
  responses: 'OpenAI Responses',
  anthropic: 'Anthropic Messages',
  gemini: 'Gemini API',
}

// ClientInput 是生成客户端配置所需的服务地址、API key、所选模型、Claude Code 三档模型、全部对话模型、终端、接入协议与界面文案
export interface ClientInput {
  origin: string
  key: string
  model: Model | undefined
  modelID: string
  tiers: { fable: string; opus: string; sonnet: string; haiku: string }
  models: Model[]
  shell: Shell
  protocol: Protocol
  m: ConnectText
}

// ClientField 是在客户端界面中填写的一个字段：客户端当前语言下的字段名与要填的值，choice 表示从选项中选择
interface ClientField {
  label: string
  value: string
  choice?: boolean
}

// ClientBlock 是一段要写入文件或在终端运行的内容，file 为文件名，没有时按终端显示，wrap 时长行折行
interface ClientBlock {
  file?: string
  code: string
  wrap?: boolean
}

// ClientStep 是客户端配置的一步：code 为要写入文件或在终端运行的内容，fields 为在客户端界面中填写的字段
type ClientStep =
  | { kind: 'code'; text: ConnectKey; blocks: ClientBlock[] }
  | { kind: 'fields'; text: ConnectKey; fields: ClientField[] }

// ClientLink 是一键导入客户端的链接
interface ClientLink {
  label: ConnectKey
  href: string
}

// Client 是接入页的一个客户端：分组、图标、可用协议（第一个为默认）、说明、主页与生成配置的方法，noModel 表示配置不使用所选模型，importsAll 表示一次配置接入全部协议
export interface Client {
  id: string
  name: string
  icon: string
  group: 'agent' | 'chat' | 'test'
  protocols: [Protocol, ...Protocol[]]
  importsAll?: boolean
  summary: ConnectKey
  homepage?: string
  noModel?: boolean
  tiers?: boolean
  shell?: boolean
  steps: (input: ClientInput) => ClientStep[]
  links?: (input: ClientInput) => ClientLink[]
}

// icon 去掉 SVG 中的 title，图标旁已有客户端名称
function icon(svg: string): string {
  return svg.replace(/<title>[^<]*<\/title>/, '')
}

// keyVariable 是客户端从环境变量读取 key 时使用的变量名
const keyVariable = 'ANTIGRAVITY2API_KEY'

// providerID 是写入客户端配置的提供商 ID
const providerID = 'antigravity2api'

// everyProtocol 是四种接入协议，Chat Completions 为默认
const everyProtocol: [Protocol, ...Protocol[]] = ['chat', 'responses', 'anthropic', 'gemini']

// providerApis 是 pi、omp、dsh 与 OpenClaw 配置的 api 字段在各协议下的取值
const providerApis: Record<Protocol, string> = {
  chat: 'openai-completions',
  responses: 'openai-responses',
  anthropic: 'anthropic-messages',
  gemini: 'google-generative-ai',
}

// openCodePackages 是 OpenCode 各协议使用的 AI SDK 包
const openCodePackages: Record<Protocol, string> = {
  chat: '@ai-sdk/openai-compatible',
  responses: '@ai-sdk/openai',
  anthropic: '@ai-sdk/anthropic',
  gemini: '@ai-sdk/google',
}

// openCodeVariants 返回 OpenCode 模型配置中一个思考档位变体的提供商选项，写法同 OpenCode 为各 AI SDK 包内置生成的变体
const openCodeVariants: Record<Protocol, (level: string) => Record<string, unknown>> = {
  chat: (level) => ({ reasoningEffort: level }),
  responses: (level) => ({ reasoningEffort: level, reasoningSummary: 'auto' }),
  anthropic: (level) => ({ thinking: { type: 'adaptive', display: 'summarized' }, effort: level }),
  gemini: (level) => ({ thinkingConfig: { includeThoughts: true, thinkingLevel: level } }),
}

// openCodeLevels 是 OpenCode 为推理模型内置生成的变体可能用到的档位名，配置中停用模型没有的档位
const openCodeLevels = ['minimal', 'low', 'medium', 'high', 'xhigh', 'max']

// piThinkingLevels 是 pi 的全部思考档位，thinkingLevelMap 中模型没有的档位为 null
const piThinkingLevels = ['off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max']

// ccFormats 是 CC Switch 深度链接的 apiFormat 在各协议下的取值
const ccFormats: Record<Protocol, string> = {
  chat: 'openai_chat',
  responses: 'openai_responses',
  anthropic: 'anthropic',
  gemini: 'gemini_native',
}

// cherryTypes 是 Cherry Studio 各协议的提供商类型 ID 与界面名称
const cherryTypes: Record<Protocol, { id: string; label: string }> = {
  chat: { id: 'openai', label: 'OpenAI' },
  responses: { id: 'openai-response', label: 'OpenAI-Response' },
  anthropic: { id: 'anthropic', label: 'Anthropic' },
  gemini: { id: 'gemini', label: 'Gemini' },
}

// protocolURL 是各协议的 API 地址：OpenAI 两种协议带 /v1，Gemini 带 /v1beta，Anthropic 为服务地址
function protocolURL(input: ClientInput): string {
  return {
    chat: `${input.origin}/v1`,
    responses: `${input.origin}/v1`,
    anthropic: input.origin,
    gemini: `${input.origin}/v1beta`,
  }[input.protocol]
}

// openCodeURL 是 OpenCode 提供商的 baseURL，AI SDK 的 Anthropic 提供商在 baseURL 后直接拼 /messages
function openCodeURL(input: ClientInput): string {
  return input.protocol === 'anthropic' ? `${input.origin}/v1` : protocolURL(input)
}

// cherryHost 是 Cherry Studio 各提供商类型的 API 地址：OpenAI 两种类型带 /v1，Gemini 与 Anthropic 为服务地址
function cherryHost(input: ClientInput): string {
  return input.protocol === 'chat' || input.protocol === 'responses'
    ? `${input.origin}/v1`
    : input.origin
}

// envBlock 按终端写出设置环境变量的命令，最后一行是随后运行的命令
function envBlock(shell: Shell, variables: [string, string][], command: string): string {
  const lines = variables.map(([name, value]) =>
    shell === 'windows' ? `$env:${name} = "${value}"` : `export ${name}="${value}"`,
  )
  return [...lines, command].join('\n')
}

// envStep 是在终端设置环境变量后运行命令的一步
function envStep(
  input: ClientInput,
  text: ConnectKey,
  variables: [string, string][],
  command: string,
): ClientStep {
  return { kind: 'code', text, blocks: [{ code: envBlock(input.shell, variables, command) }] }
}

// json 把配置写为两空格缩进的 JSON
function json(value: unknown): string {
  return JSON.stringify(value, null, 2)
}

// limits 返回模型的上下文与输出 token 上限，目录没有时取 Gemini 对话模型的常见值
function limits(model: Model | undefined): { context: number; output: number } {
  return {
    context: model?.input_token_limit ?? 1048576,
    output: model?.output_token_limit ?? 65536,
  }
}

// ccSwitch 返回 CC Switch 导入提供商的深度链接，参数按 CC Switch 深度链接协议 v1 以 URLSearchParams 编码
function ccSwitch(app: string, params: Record<string, string>): ClientLink {
  const query = new URLSearchParams({
    resource: 'provider',
    app,
    name: 'Antigravity2API',
    ...params,
  })
  return { label: 'importCCSwitch', href: `ccswitch://v1/import?${query.toString()}` }
}

// magpieLink 返回 Magpie 的导入链接，模型由 Magpie 从本服务的 /v1/models 读取，各智能体按自己的协议使用对应的 base URL
function magpieLink(input: ClientInput): string {
  const query = new URLSearchParams({
    name: 'Antigravity2API',
    chat: `${input.origin}/v1`,
    responses: `${input.origin}/v1`,
    anthropic: input.origin,
    gemini: `${input.origin}/v1beta`,
    key: input.key,
  })
  return `magpie://import?${query.toString()}`
}

// base64 把 UTF-8 文本编码为标准 Base64
function base64(text: string): string {
  return btoa(String.fromCharCode(...new TextEncoder().encode(text)))
}

// claudeVariables 是 Claude Code 使用的环境变量：Anthropic 地址、key、主模型、fable、opus、sonnet、haiku 四档模型与主模型的上下文窗口
function claudeVariables(input: ClientInput): [string, string][] {
  return [
    ['ANTHROPIC_BASE_URL', input.origin],
    ['ANTHROPIC_AUTH_TOKEN', input.key],
    ['ANTHROPIC_MODEL', input.modelID],
    ['ANTHROPIC_DEFAULT_FABLE_MODEL', input.tiers.fable],
    ['ANTHROPIC_DEFAULT_OPUS_MODEL', input.tiers.opus],
    ['ANTHROPIC_DEFAULT_SONNET_MODEL', input.tiers.sonnet],
    ['ANTHROPIC_DEFAULT_HAIKU_MODEL', input.tiers.haiku],
    ['CLAUDE_CODE_MAX_CONTEXT_TOKENS', String(limits(input.model).context)],
  ]
}

// claudeCode 是默认选中的 Claude Code
export const claudeCode: Client = {
  id: 'claude-code',
  name: 'Claude Code',
  icon: icon(claudeCodeIcon),
  group: 'agent',
  protocols: ['anthropic'],
  summary: 'clientClaude',
  homepage: 'https://github.com/anthropics/claude-code',
  tiers: true,
  shell: true,
  steps: (input) => [
    envStep(input, 'stepEnvRun', claudeVariables(input), 'claude'),
    {
      kind: 'code',
      text: 'stepSettingsFile',
      blocks: [
        {
          file: '~/.claude/settings.json',
          code: json({ env: Object.fromEntries(claudeVariables(input)) }),
        },
      ],
    },
  ],
  links: (input) => [
    ccSwitch('claude', {
      endpoint: input.origin,
      apiKey: input.key,
      apiFormat: 'anthropic',
      model: input.modelID,
      opusModel: input.tiers.opus,
      sonnetModel: input.tiers.sonnet,
      haikuModel: input.tiers.haiku,
    }),
  ],
}

// clients 是接入页列出的客户端
export const clients: Client[] = [
  claudeCode,
  {
    id: 'codex',
    name: 'Codex',
    icon: icon(codexIcon),
    group: 'agent',
    protocols: ['responses'],
    summary: 'clientCodex',
    homepage: 'https://github.com/openai/codex',
    shell: true,
    steps: (input) => [
      {
        kind: 'code',
        text: 'stepConfigFile',
        blocks: [
          {
            file: '~/.codex/config.toml',
            code: [
              `model = "${input.modelID}"`,
              `model_provider = "${providerID}"`,
              `model_context_window = ${String(limits(input.model).context)}`,
              '',
              `[model_providers.${providerID}]`,
              'name = "Antigravity2API"',
              `base_url = "${input.origin}/v1"`,
              `env_key = "${keyVariable}"`,
              'wire_api = "responses"',
            ].join('\n'),
          },
        ],
      },
      envStep(input, 'stepKeyRun', [[keyVariable, input.key]], 'codex'),
    ],
    links: (input) => [
      ccSwitch('codex', {
        endpoint: `${input.origin}/v1`,
        apiKey: input.key,
        apiFormat: 'openai_responses',
        model: input.modelID,
      }),
    ],
  },
  {
    id: 'opencode',
    name: 'OpenCode',
    icon: icon(openCodeIcon),
    group: 'agent',
    protocols: everyProtocol,
    summary: 'clientOpencode',
    homepage: 'https://opencode.ai',
    steps: (input) => [
      {
        kind: 'code',
        text: 'stepConfigFile',
        blocks: [
          {
            file: '~/.config/opencode/opencode.json',
            code: json({
              $schema: 'https://opencode.ai/config.json',
              provider: {
                [providerID]: {
                  npm: openCodePackages[input.protocol],
                  name: 'Antigravity2API',
                  options: { baseURL: openCodeURL(input), apiKey: input.key },
                  models: Object.fromEntries(
                    input.models.map((model) => [
                      model.id,
                      {
                        name: model.name || model.id,
                        limit: { context: limits(model).context, output: limits(model).output },
                        reasoning: Boolean(model.capabilities?.thinking),
                        modalities: {
                          input: model.capabilities?.images ? ['text', 'image', 'pdf'] : ['text'],
                          output: ['text'],
                        },
                        ...(model.thinking_levels?.length
                          ? {
                              variants: Object.fromEntries([
                                ...openCodeLevels
                                  .filter((level) => !model.thinking_levels?.includes(level))
                                  .map((level) => [level, { disabled: true }] as const),
                                ...model.thinking_levels.map(
                                  (level) =>
                                    [level, openCodeVariants[input.protocol](level)] as const,
                                ),
                              ]),
                            }
                          : {}),
                      },
                    ]),
                  ),
                },
              },
              model: `${providerID}/${input.modelID}`,
            }),
          },
        ],
      },
    ],
    links: (input) => [
      ccSwitch('opencode', {
        endpoint: openCodeURL(input),
        apiKey: input.key,
        apiFormat: ccFormats[input.protocol],
        model: input.modelID,
      }),
    ],
  },
  {
    id: 'openclaw',
    name: 'OpenClaw',
    icon: icon(openClawIcon),
    group: 'agent',
    protocols: everyProtocol,
    summary: 'clientOpenclaw',
    homepage: 'https://github.com/openclaw/openclaw',
    steps: (input) => [
      {
        kind: 'code',
        text: 'stepConfigFile',
        blocks: [
          {
            file: '~/.openclaw/openclaw.json',
            code: json({
              agents: { defaults: { model: { primary: `${providerID}/${input.modelID}` } } },
              models: {
                providers: {
                  [providerID]: {
                    baseUrl: protocolURL(input),
                    apiKey: input.key,
                    api: providerApis[input.protocol],
                    models: [
                      {
                        id: input.modelID,
                        name: input.model?.name ?? input.modelID,
                        reasoning: true,
                        contextWindow: limits(input.model).context,
                        maxTokens: limits(input.model).output,
                      },
                    ],
                  },
                },
              },
            }),
          },
        ],
      },
    ],
    links: (input) => [
      ccSwitch('openclaw', {
        endpoint: protocolURL(input),
        apiKey: input.key,
        apiFormat: ccFormats[input.protocol],
        model: input.modelID,
      }),
    ],
  },
  {
    id: 'magpie',
    name: 'Magpie',
    icon: icon(magpieIcon),
    group: 'agent',
    protocols: everyProtocol,
    importsAll: true,
    summary: 'clientMagpie',
    homepage: 'https://usemagpie.ai',
    noModel: true,
    steps: (input) => [
      {
        kind: 'code',
        text: 'stepMagpieImport',
        blocks: [{ code: `magpie import '${magpieLink(input)}'`, wrap: true }],
      },
      {
        kind: 'fields',
        text: 'stepMagpieManual',
        fields: [
          { label: 'Name', value: 'Antigravity2API' },
          { label: 'OpenAI compatible base URL', value: `${input.origin}/v1` },
          { label: 'Anthropic compatible base URL', value: input.origin },
          { label: 'API key', value: input.key },
        ],
      },
    ],
    links: (input) => [{ label: 'importMagpie', href: magpieLink(input) }],
  },
  {
    id: 'pi',
    name: 'pi',
    icon: icon(piIcon),
    group: 'agent',
    protocols: everyProtocol,
    summary: 'clientPi',
    homepage: 'https://github.com/badlogic/pi-mono',
    shell: true,
    steps: (input) => [
      {
        kind: 'code',
        text: 'stepConfigFile',
        blocks: [
          {
            file: '~/.pi/agent/models.json',
            code: json({
              providers: {
                [providerID]: {
                  baseUrl: protocolURL(input),
                  api: providerApis[input.protocol],
                  apiKey: keyVariable,
                  models: [
                    {
                      id: input.modelID,
                      name: input.model?.name ?? input.modelID,
                      reasoning: Boolean(input.model?.capabilities?.thinking),
                      ...(input.model?.thinking_levels?.length
                        ? {
                            thinkingLevelMap: Object.fromEntries(
                              piThinkingLevels.map((level) => [
                                level,
                                input.model?.thinking_levels?.includes(level) ? level : null,
                              ]),
                            ),
                          }
                        : {}),
                      input: input.model?.capabilities?.images ? ['text', 'image'] : ['text'],
                      contextWindow: limits(input.model).context,
                      maxTokens: limits(input.model).output,
                    },
                  ],
                },
              },
            }),
          },
        ],
      },
      envStep(
        input,
        'stepKeyRun',
        [[keyVariable, input.key]],
        `pi --provider ${providerID} --model ${input.modelID}`,
      ),
    ],
    links: (input) => [
      ccSwitch('pi', {
        endpoint: protocolURL(input),
        apiKey: input.key,
        apiFormat: ccFormats[input.protocol],
        model: input.modelID,
      }),
    ],
  },
  {
    id: 'omp',
    name: 'omp',
    icon: icon(ompIcon),
    group: 'agent',
    protocols: everyProtocol,
    summary: 'clientOmp',
    homepage: 'https://github.com/can1357/oh-my-pi',
    shell: true,
    steps: (input) => [
      {
        kind: 'code',
        text: 'stepConfigFile',
        blocks: [
          {
            file: '~/.omp/agent/models.yml',
            code: [
              'providers:',
              `  ${providerID}:`,
              `    baseUrl: ${protocolURL(input)}`,
              `    api: ${providerApis[input.protocol]}`,
              `    apiKey: ${keyVariable}`,
              '    models:',
              `      - id: ${input.modelID}`,
              ...(input.protocol === 'gemini'
                ? []
                : [
                    `  ${providerID}-gemini:`,
                    `    baseUrl: ${input.origin}/v1beta`,
                    '    api: google-generative-ai',
                    `    apiKey: ${keyVariable}`,
                    '    models:',
                    `      - id: ${input.modelID}`,
                  ]),
            ].join('\n'),
          },
        ],
      },
      {
        kind: 'code',
        text: 'stepOmpSearch',
        blocks: [
          {
            file: '~/.omp/agent/config.yml',
            code: [
              'modelRoles:',
              `  web: ${providerID}${input.protocol === 'gemini' ? '' : '-gemini'}/${input.modelID}`,
            ].join('\n'),
          },
        ],
      },
      envStep(
        input,
        'stepKeyRun',
        [[keyVariable, input.key]],
        `omp --provider ${providerID} --model ${input.modelID}`,
      ),
    ],
  },
  {
    id: 'dsh',
    name: 'dsh',
    icon: icon(deepSeekIcon),
    group: 'agent',
    protocols: ['chat', 'responses', 'anthropic'],
    summary: 'clientDsh',
    homepage: 'https://www.npmjs.com/package/@deepseek-ai/dsh',
    shell: true,
    steps: (input) => [
      {
        kind: 'code',
        text: 'stepPatchFile',
        blocks: [
          {
            file: 'antigravity2api.patch.yml',
            code: [
              '- id: llm-pi-ai',
              '  config:',
              '    providers:',
              `      ${providerID}:`,
              `        api: ${providerApis[input.protocol]}`,
              `        baseURL: ${protocolURL(input)}`,
              `        apiKeyEnv: ${keyVariable}`,
              '        models:',
              `          - id: ${input.modelID}`,
              `            contextWindow: ${String(limits(input.model).context)}`,
              '- id: agent-default-model',
              '  config:',
              `    provider: ${providerID}`,
              `    model: ${input.modelID}`,
            ].join('\n'),
          },
        ],
      },
      envStep(
        input,
        'stepKeyRun',
        [[keyVariable, input.key]],
        'dsh --patch antigravity2api.patch.yml',
      ),
    ],
  },
  {
    id: 'cherry-studio',
    name: 'Cherry Studio',
    icon: icon(cherryStudioIcon),
    group: 'chat',
    protocols: ['chat', 'responses', 'gemini', 'anthropic'],
    summary: 'clientCherry',
    homepage: 'https://www.cherry-ai.com',
    noModel: true,
    steps: (input) => [
      {
        kind: 'fields',
        text: 'stepCherry',
        fields: [
          { label: input.m.cherryType, value: cherryTypes[input.protocol].label, choice: true },
          { label: input.m.cherryKey, value: input.key },
          { label: input.m.cherryHost, value: cherryHost(input) },
        ],
      },
    ],
    links: (input) => [
      {
        label: 'importCherry',
        href: `cherrystudio://providers/api-keys?v=1&data=${encodeURIComponent(
          base64(
            JSON.stringify({
              id: providerID,
              baseUrl: cherryHost(input),
              apiKey: input.key,
              name: 'Antigravity2API',
              type: cherryTypes[input.protocol].id,
            }),
          ),
        )}`,
      },
    ],
  },
  {
    id: 'curl',
    name: 'curl',
    icon: icon(terminalIcon),
    group: 'test',
    protocols: ['chat', 'anthropic', 'gemini'],
    importsAll: true,
    summary: 'clientCurl',
    steps: (input) => [
      {
        kind: 'code',
        text: 'curlHint',
        blocks: [
          {
            file: 'OpenAI Chat Completions',
            code: curlExample('openai', input.origin, input.key, input.modelID),
          },
          {
            file: 'Anthropic Messages',
            code: curlExample('anthropic', input.origin, input.key, input.modelID),
          },
          {
            file: 'Gemini streamGenerateContent',
            code: curlExample('gemini', input.origin, input.key, input.modelID),
          },
        ],
      },
    ],
  },
]
