import type { ModelKind } from '@/features/models/types'

// serviceOrigin 是客户端访问本服务的地址，取管理端页面自身的协议、主机与端口
export function serviceOrigin() {
  return window.location.origin
}

// Protocol 是公开 API 的协议
export type Protocol = 'openai' | 'anthropic' | 'gemini'

// RouteLabel 是接口用途在 connect 文案中的键
export type RouteLabel =
  | 'routeChat'
  | 'routeCompletions'
  | 'routeResponses'
  | 'routeResponseGet'
  | 'routeResponseDelete'
  | 'routeResponseItems'
  | 'routeResponseCancel'
  | 'routeCompact'
  | 'routeModels'
  | 'routeModel'
  | 'routeImages'
  | 'routeImageEdits'
  | 'routeAudio'
  | 'routeTranslations'
  | 'routeFiles'
  | 'routeFileList'
  | 'routeFileGet'
  | 'routeFileContent'
  | 'routeFileDelete'
  | 'routeMessages'
  | 'routeCountTokens'
  | 'routeGenerate'
  | 'routeStream'
  | 'routeCache'
  | 'routeCacheList'
  | 'routeCacheGet'
  | 'routeCacheUpdate'
  | 'routeCacheDelete'
  | 'routeInteraction'
  | 'routeInteractionGet'
  | 'routeInteractionDelete'
  | 'routeInteractionCancel'

// ParamLabel 是参数说明在 connect 文案中的键
export type ParamLabel =
  | 'paramModel'
  | 'paramMessages'
  | 'paramInput'
  | 'paramContents'
  | 'paramStream'
  | 'paramMaxTokens'
  | 'paramSystem'
  | 'paramTools'
  | 'paramToolChoice'
  | 'paramReasoning'
  | 'paramThinking'
  | 'paramThinkingConfig'
  | 'paramFormat'
  | 'paramSearch'
  | 'paramMedia'
  | 'paramGenerationConfig'
  | 'paramPrevious'
  | 'paramStore'
  | 'paramBackground'
  | 'paramInclude'
  | 'paramResponseId'
  | 'paramFileId'
  | 'paramCacheId'
  | 'paramInteractionId'
  | 'paramLimit'
  | 'paramAfter'
  | 'paramBefore'
  | 'paramOrder'
  | 'paramPageToken'
  | 'paramImagePrompt'
  | 'paramEditImage'
  | 'paramMask'
  | 'paramSize'
  | 'paramQuality'
  | 'paramAudio'
  | 'paramAudioPrompt'
  | 'paramAudioFormat'
  | 'paramFile'
  | 'paramPurpose'
  | 'paramExpires'
  | 'paramMetadata'
  | 'paramUploadProtocol'
  | 'paramPrompt'
  | 'paramSuffix'
  | 'paramTtl'
  | 'paramUpdateMask'
  | 'paramCachedContent'
  | 'paramCountRequest'
  | 'paramModalities'
  | 'paramImageConfig'
  | 'paramAlt'
  | 'paramBeta'

// Param 是接口的一个主要参数：名称、所在位置、是否必填与说明的文案键
export interface Param {
  name: string
  in: 'path' | 'query' | 'header' | 'body' | 'form'
  required?: boolean
  label: ParamLabel
}

// Route 是一个公开接口与它的 curl 示例：path 中的 {model} 换成 model 类模型的 ID，其余路径参数换成 sample；alias 是行为相同的另一路径，query 是示例的查询串，body 是 JSON 请求体（其中 "{model}" 换成模型 ID），form 是 multipart 字段，headers 是认证之外的请求头
export interface Route {
  method: 'GET' | 'POST' | 'PATCH' | 'DELETE'
  path: string
  alias?: string
  label: RouteLabel
  docs: string
  params: Param[]
  model?: ModelKind
  sample?: string
  query?: string
  body?: unknown
  form?: string[]
  headers?: string[]
}

// Endpoint 是一种协议的 base URL、列表行显示的主要接口、认证请求头、官方文档与全部接口
export interface Endpoint {
  protocol: Protocol
  base: string
  paths: string[]
  auth: (key: string) => string[]
  docs: string
  routes: Route[]
}

// openaiDocs、anthropicDocs 与 geminiDocs 是三家官方 API 参考的地址前缀
export const openaiDocs = 'https://developers.openai.com/api/reference/resources/'
const anthropicDocs = 'https://platform.claude.com/docs/en/api/'
const geminiDocs = 'https://ai.google.dev/api/'

// authHeaders 是各协议携带 API key 的请求头
export const authHeaders: Record<Protocol, (key: string) => string[]> = {
  openai: (key) => ['Authorization: Bearer ' + key],
  anthropic: (key) => ['x-api-key: ' + key, 'anthropic-version: 2023-06-01'],
  gemini: (key) => ['x-goog-api-key: ' + key],
}

// hello 是只有一条用户消息的对话
export const hello = [{ role: 'user', content: 'Hello' }]

// geminiHello 是只有一轮用户文字的 Gemini contents
export const geminiHello = [{ role: 'user', parts: [{ text: 'Hello' }] }]

// filesBeta 是 Anthropic Files API 的 beta 请求头
export const filesBeta = 'anthropic-beta: files-api-2025-04-14'

// id 是路径中的资源 ID 参数
function id(label: ParamLabel, name = 'id'): Param {
  return { name, in: 'path', required: true, label }
}

// modelParam 是路径或请求体中的模型 ID 参数
export function modelParam(where: 'path' | 'body' = 'body', required = true): Param {
  return { name: 'model', in: where, required, label: 'paramModel' }
}

// fileRoutes 是 OpenAI 与 Anthropic 同路径的文件接口：上传、列出、读取信息、下载与删除；docs 是各接口的官方文档，upload 与 form 是上传的参数与示例字段，list 是列出的查询参数，beta 为 true 时各接口带 Anthropic Files API 的 beta 请求头
function fileRoutes(
  docs: Record<'upload' | 'list' | 'get' | 'content' | 'delete', string>,
  upload: Param[],
  form: string[],
  list: Param[],
  beta: boolean,
): Route[] {
  const header: Param[] = beta
    ? [{ name: 'anthropic-beta', in: 'header', required: true, label: 'paramBeta' }]
    : []
  const headers = beta ? [filesBeta] : []
  const file = [...header, id('paramFileId')]
  const sample = 'file-abc123'
  return [
    {
      method: 'POST',
      path: '/v1/files',
      label: 'routeFiles',
      docs: docs.upload,
      params: [...header, ...upload],
      form,
      headers,
    },
    {
      method: 'GET',
      path: '/v1/files',
      label: 'routeFileList',
      docs: docs.list,
      params: [...header, ...list],
      headers,
    },
    {
      method: 'GET',
      path: '/v1/files/{id}',
      label: 'routeFileGet',
      docs: docs.get,
      params: file,
      sample,
      headers,
    },
    {
      method: 'GET',
      path: '/v1/files/{id}/content',
      label: 'routeFileContent',
      docs: docs.content,
      params: file,
      sample,
      headers,
    },
    {
      method: 'DELETE',
      path: '/v1/files/{id}',
      label: 'routeFileDelete',
      docs: docs.delete,
      params: file,
      sample,
      headers,
    },
  ]
}

// endpoints 返回各协议 SDK 填写的 base URL 与全部公开接口，与 docs/protocol.md 和 README 的端点一致
export function endpoints(origin: string): Endpoint[] {
  return [
    {
      protocol: 'openai',
      base: origin + '/v1',
      paths: ['/v1/chat/completions', '/v1/responses'],
      auth: authHeaders.openai,
      docs: 'https://developers.openai.com/api/reference/overview',
      routes: [
        {
          method: 'POST',
          path: '/v1/chat/completions',
          label: 'routeChat',
          docs: openaiDocs + 'chat/subresources/completions/methods/create',
          params: [
            modelParam(),
            { name: 'messages', in: 'body', required: true, label: 'paramMessages' },
            { name: 'stream', in: 'body', label: 'paramStream' },
            { name: 'tools', in: 'body', label: 'paramTools' },
            { name: 'reasoning_effort', in: 'body', label: 'paramReasoning' },
            { name: 'max_completion_tokens', in: 'body', label: 'paramMaxTokens' },
          ],
          body: { model: '{model}', stream: true, messages: hello },
        },
        {
          method: 'POST',
          path: '/v1/completions',
          label: 'routeCompletions',
          docs: openaiDocs + 'completions/methods/create',
          params: [
            modelParam(),
            { name: 'prompt', in: 'body', required: true, label: 'paramPrompt' },
            { name: 'suffix', in: 'body', label: 'paramSuffix' },
            { name: 'max_tokens', in: 'body', label: 'paramMaxTokens' },
            { name: 'stream', in: 'body', label: 'paramStream' },
          ],
          body: { model: '{model}', prompt: 'Write a haiku about the sea' },
        },
        {
          method: 'POST',
          path: '/v1/responses',
          label: 'routeResponses',
          docs: openaiDocs + 'responses/methods/create',
          params: [
            modelParam(),
            { name: 'input', in: 'body', required: true, label: 'paramInput' },
            { name: 'instructions', in: 'body', label: 'paramSystem' },
            { name: 'stream', in: 'body', label: 'paramStream' },
            { name: 'tools', in: 'body', label: 'paramTools' },
            { name: 'previous_response_id', in: 'body', label: 'paramPrevious' },
            { name: 'store', in: 'body', label: 'paramStore' },
            { name: 'background', in: 'body', label: 'paramBackground' },
          ],
          body: { model: '{model}', input: 'Hello' },
        },
        {
          method: 'GET',
          path: '/v1/responses/{id}',
          label: 'routeResponseGet',
          docs: openaiDocs + 'responses/methods/retrieve',
          params: [
            id('paramResponseId'),
            { name: 'include', in: 'query', label: 'paramInclude' },
            { name: 'stream', in: 'query', label: 'paramStream' },
          ],
          sample: 'resp_abc123',
        },
        {
          method: 'DELETE',
          path: '/v1/responses/{id}',
          label: 'routeResponseDelete',
          docs: openaiDocs + 'responses/methods/delete',
          params: [id('paramResponseId')],
          sample: 'resp_abc123',
        },
        {
          method: 'GET',
          path: '/v1/responses/{id}/input_items',
          label: 'routeResponseItems',
          docs: openaiDocs + 'responses/subresources/input_items/methods/list',
          params: [
            id('paramResponseId'),
            { name: 'limit', in: 'query', label: 'paramLimit' },
            { name: 'after', in: 'query', label: 'paramAfter' },
            { name: 'order', in: 'query', label: 'paramOrder' },
          ],
          sample: 'resp_abc123',
        },
        {
          method: 'POST',
          path: '/v1/responses/{id}/cancel',
          label: 'routeResponseCancel',
          docs: openaiDocs + 'responses/methods/cancel',
          params: [id('paramResponseId')],
          sample: 'resp_abc123',
        },
        {
          method: 'POST',
          path: '/v1/responses/input_tokens',
          label: 'routeCountTokens',
          docs: openaiDocs + 'responses/subresources/input_tokens/methods/count',
          params: [
            modelParam('body', false),
            { name: 'input', in: 'body', label: 'paramInput' },
            { name: 'previous_response_id', in: 'body', label: 'paramPrevious' },
          ],
          body: { model: '{model}', input: 'Hello' },
        },
        {
          method: 'POST',
          path: '/v1/responses/compact',
          label: 'routeCompact',
          docs: openaiDocs + 'responses/methods/compact',
          params: [
            modelParam(),
            { name: 'input', in: 'body', required: true, label: 'paramInput' },
          ],
          body: { model: '{model}', input: hello },
        },
        {
          method: 'GET',
          path: '/v1/models',
          label: 'routeModels',
          docs: openaiDocs + 'models/methods/list',
          params: [],
        },
        {
          method: 'GET',
          path: '/v1/models/{model}',
          label: 'routeModel',
          docs: openaiDocs + 'models/methods/retrieve',
          params: [modelParam('path')],
        },
        {
          method: 'POST',
          path: '/v1/images/generations',
          label: 'routeImages',
          docs: openaiDocs + 'images/methods/generate',
          params: [
            { name: 'prompt', in: 'body', required: true, label: 'paramImagePrompt' },
            modelParam('body', false),
            { name: 'size', in: 'body', label: 'paramSize' },
            { name: 'quality', in: 'body', label: 'paramQuality' },
          ],
          body: { prompt: 'A watercolor fox in the snow', size: '1024x1024' },
        },
        {
          method: 'POST',
          path: '/v1/images/edits',
          label: 'routeImageEdits',
          docs: openaiDocs + 'images/methods/edit',
          params: [
            { name: 'image', in: 'form', required: true, label: 'paramEditImage' },
            { name: 'prompt', in: 'form', required: true, label: 'paramImagePrompt' },
            { name: 'mask', in: 'form', label: 'paramMask' },
            { name: 'model', in: 'form', label: 'paramModel' },
            { name: 'size', in: 'form', label: 'paramSize' },
          ],
          form: ['"image=@photo.png"', '"prompt=Add a red scarf"'],
        },
        {
          method: 'POST',
          path: '/v1/audio/transcriptions',
          label: 'routeAudio',
          docs: openaiDocs + 'audio/subresources/transcriptions/methods/create',
          params: [
            { name: 'file', in: 'form', required: true, label: 'paramAudio' },
            { name: 'model', in: 'form', label: 'paramModel' },
            { name: 'prompt', in: 'form', label: 'paramAudioPrompt' },
            { name: 'response_format', in: 'form', label: 'paramAudioFormat' },
          ],
          form: ['"file=@speech.mp3"'],
        },
        {
          method: 'POST',
          path: '/v1/audio/translations',
          label: 'routeTranslations',
          docs: openaiDocs + 'audio/subresources/translations/methods/create',
          params: [
            { name: 'file', in: 'form', required: true, label: 'paramAudio' },
            { name: 'model', in: 'form', label: 'paramModel' },
            { name: 'prompt', in: 'form', label: 'paramAudioPrompt' },
            { name: 'response_format', in: 'form', label: 'paramAudioFormat' },
          ],
          form: ['"file=@speech.mp3"'],
        },
        ...fileRoutes(
          {
            upload: openaiDocs + 'files/methods/create',
            list: openaiDocs + 'files/methods/list',
            get: openaiDocs + 'files/methods/retrieve',
            content: openaiDocs + 'files/methods/content',
            delete: openaiDocs + 'files/methods/delete',
          },
          [
            { name: 'file', in: 'form', required: true, label: 'paramFile' },
            { name: 'purpose', in: 'form', label: 'paramPurpose' },
            { name: 'expires_after', in: 'form', label: 'paramExpires' },
          ],
          ['"file=@document.pdf"', '"purpose=user_data"'],
          [
            { name: 'purpose', in: 'query', label: 'paramPurpose' },
            { name: 'limit', in: 'query', label: 'paramLimit' },
            { name: 'after', in: 'query', label: 'paramAfter' },
            { name: 'order', in: 'query', label: 'paramOrder' },
          ],
          false,
        ),
      ],
    },
    {
      protocol: 'anthropic',
      base: origin,
      paths: ['/v1/messages', '/v1/messages/count_tokens'],
      auth: authHeaders.anthropic,
      docs: anthropicDocs + 'overview',
      routes: [
        {
          method: 'POST',
          path: '/v1/messages',
          label: 'routeMessages',
          docs: anthropicDocs + 'messages/create',
          params: [
            modelParam(),
            { name: 'max_tokens', in: 'body', required: true, label: 'paramMaxTokens' },
            { name: 'messages', in: 'body', required: true, label: 'paramMessages' },
            { name: 'system', in: 'body', label: 'paramSystem' },
            { name: 'stream', in: 'body', label: 'paramStream' },
            { name: 'tools', in: 'body', label: 'paramTools' },
            { name: 'thinking', in: 'body', label: 'paramThinking' },
          ],
          body: { model: '{model}', max_tokens: 1024, messages: hello },
        },
        {
          method: 'POST',
          path: '/v1/messages/count_tokens',
          label: 'routeCountTokens',
          docs: anthropicDocs + 'messages/count_tokens',
          params: [
            modelParam(),
            { name: 'messages', in: 'body', required: true, label: 'paramMessages' },
            { name: 'system', in: 'body', label: 'paramSystem' },
            { name: 'tools', in: 'body', label: 'paramTools' },
          ],
          body: { model: '{model}', messages: hello },
        },
        {
          method: 'GET',
          path: '/v1/models',
          label: 'routeModels',
          docs: anthropicDocs + 'models/list',
          params: [
            { name: 'limit', in: 'query', label: 'paramLimit' },
            { name: 'after_id', in: 'query', label: 'paramAfter' },
            { name: 'before_id', in: 'query', label: 'paramBefore' },
          ],
        },
        {
          method: 'GET',
          path: '/v1/models/{model}',
          label: 'routeModel',
          docs: anthropicDocs + 'models/retrieve',
          params: [modelParam('path')],
        },
        ...fileRoutes(
          {
            upload: anthropicDocs + 'beta/files/upload',
            list: anthropicDocs + 'beta/files/list',
            get: anthropicDocs + 'beta/files/retrieve_metadata',
            content: anthropicDocs + 'beta/files/download',
            delete: anthropicDocs + 'beta/files/delete',
          },
          [{ name: 'file', in: 'form', required: true, label: 'paramFile' }],
          ['"file=@document.pdf"'],
          [
            { name: 'limit', in: 'query', label: 'paramLimit' },
            { name: 'after_id', in: 'query', label: 'paramAfter' },
            { name: 'before_id', in: 'query', label: 'paramBefore' },
          ],
          true,
        ),
      ],
    },
    {
      protocol: 'gemini',
      base: origin,
      paths: ['/v1beta/models/{model}:generateContent'],
      auth: authHeaders.gemini,
      docs: geminiDocs,
      routes: [
        {
          method: 'POST',
          path: '/v1beta/models/{model}:generateContent',
          label: 'routeGenerate',
          docs: geminiDocs + 'generate-content',
          params: [
            modelParam('path'),
            { name: 'contents', in: 'body', required: true, label: 'paramContents' },
            { name: 'systemInstruction', in: 'body', label: 'paramSystem' },
            { name: 'generationConfig', in: 'body', label: 'paramGenerationConfig' },
            { name: 'tools', in: 'body', label: 'paramTools' },
            { name: 'cachedContent', in: 'body', label: 'paramCachedContent' },
          ],
          body: { contents: geminiHello },
        },
        {
          method: 'POST',
          path: '/v1beta/models/{model}:streamGenerateContent',
          label: 'routeStream',
          docs: geminiDocs + 'generate-content',
          params: [
            modelParam('path'),
            { name: 'alt', in: 'query', label: 'paramAlt' },
            { name: 'contents', in: 'body', required: true, label: 'paramContents' },
            { name: 'generationConfig', in: 'body', label: 'paramGenerationConfig' },
          ],
          query: '?alt=sse',
          body: { contents: geminiHello },
        },
        {
          method: 'POST',
          path: '/v1beta/models/{model}:countTokens',
          label: 'routeCountTokens',
          docs: geminiDocs + 'tokens',
          params: [
            modelParam('path'),
            { name: 'contents', in: 'body', label: 'paramContents' },
            { name: 'generateContentRequest', in: 'body', label: 'paramCountRequest' },
          ],
          body: { contents: geminiHello },
        },
        {
          method: 'GET',
          path: '/v1beta/models',
          label: 'routeModels',
          docs: geminiDocs + 'models',
          params: [],
        },
        {
          method: 'GET',
          path: '/v1beta/models/{model}',
          label: 'routeModel',
          docs: geminiDocs + 'models',
          params: [modelParam('path')],
        },
        {
          method: 'POST',
          path: '/upload/v1beta/files',
          label: 'routeFiles',
          docs: geminiDocs + 'files',
          params: [
            {
              name: 'X-Goog-Upload-Protocol',
              in: 'header',
              required: true,
              label: 'paramUploadProtocol',
            },
            { name: 'metadata', in: 'form', label: 'paramMetadata' },
            { name: 'file', in: 'form', required: true, label: 'paramFile' },
          ],
          headers: ['X-Goog-Upload-Protocol: multipart'],
          form: [
            `'metadata={"file": {"display_name": "document.pdf"}};type=application/json'`,
            '"file=@document.pdf"',
          ],
        },
        {
          method: 'GET',
          path: '/v1beta/files',
          label: 'routeFileList',
          docs: geminiDocs + 'files',
          params: [
            { name: 'pageSize', in: 'query', label: 'paramLimit' },
            { name: 'pageToken', in: 'query', label: 'paramPageToken' },
          ],
        },
        {
          method: 'GET',
          path: '/v1beta/files/{name}',
          label: 'routeFileGet',
          docs: geminiDocs + 'files',
          params: [id('paramFileId', 'name')],
          sample: 'file-abc123',
        },
        {
          method: 'DELETE',
          path: '/v1beta/files/{name}',
          label: 'routeFileDelete',
          docs: geminiDocs + 'files',
          params: [id('paramFileId', 'name')],
          sample: 'file-abc123',
        },
        {
          method: 'POST',
          path: '/v1beta/cachedContents',
          label: 'routeCache',
          docs: geminiDocs + 'caching',
          params: [
            modelParam(),
            { name: 'contents', in: 'body', label: 'paramContents' },
            { name: 'systemInstruction', in: 'body', label: 'paramSystem' },
            { name: 'tools', in: 'body', label: 'paramTools' },
            { name: 'ttl', in: 'body', label: 'paramTtl' },
          ],
          body: {
            model: 'models/{model}',
            systemInstruction: { parts: [{ text: 'You are a contract reviewer.' }] },
            contents: [{ role: 'user', parts: [{ text: 'Contract text...' }] }],
            ttl: '3600s',
          },
        },
        {
          method: 'GET',
          path: '/v1beta/cachedContents',
          label: 'routeCacheList',
          docs: geminiDocs + 'caching',
          params: [
            { name: 'pageSize', in: 'query', label: 'paramLimit' },
            { name: 'pageToken', in: 'query', label: 'paramPageToken' },
          ],
        },
        {
          method: 'GET',
          path: '/v1beta/cachedContents/{id}',
          label: 'routeCacheGet',
          docs: geminiDocs + 'caching',
          params: [id('paramCacheId')],
          sample: 'abc123',
        },
        {
          method: 'PATCH',
          path: '/v1beta/cachedContents/{id}',
          label: 'routeCacheUpdate',
          docs: geminiDocs + 'caching',
          params: [
            id('paramCacheId'),
            { name: 'updateMask', in: 'query', required: true, label: 'paramUpdateMask' },
            { name: 'ttl', in: 'body', label: 'paramTtl' },
          ],
          sample: 'abc123',
          query: '?updateMask=ttl',
          body: { ttl: '7200s' },
        },
        {
          method: 'DELETE',
          path: '/v1beta/cachedContents/{id}',
          label: 'routeCacheDelete',
          docs: geminiDocs + 'caching',
          params: [id('paramCacheId')],
          sample: 'abc123',
        },
        {
          method: 'POST',
          path: '/v1beta/interactions',
          alias: '/v1/interactions',
          label: 'routeInteraction',
          docs: geminiDocs + 'interactions-api',
          params: [
            modelParam(),
            { name: 'input', in: 'body', required: true, label: 'paramInput' },
            { name: 'system_instruction', in: 'body', label: 'paramSystem' },
            { name: 'stream', in: 'body', label: 'paramStream' },
            { name: 'previous_interaction_id', in: 'body', label: 'paramPrevious' },
            { name: 'background', in: 'body', label: 'paramBackground' },
          ],
          body: { model: '{model}', input: 'Hello' },
        },
        {
          method: 'GET',
          path: '/v1beta/interactions/{id}',
          alias: '/v1/interactions/{id}',
          label: 'routeInteractionGet',
          docs: geminiDocs + 'interactions-api',
          params: [id('paramInteractionId'), { name: 'stream', in: 'query', label: 'paramStream' }],
          sample: 'abc123',
        },
        {
          method: 'DELETE',
          path: '/v1beta/interactions/{id}',
          alias: '/v1/interactions/{id}',
          label: 'routeInteractionDelete',
          docs: geminiDocs + 'interactions-api',
          params: [id('paramInteractionId')],
          sample: 'abc123',
        },
        {
          method: 'POST',
          path: '/v1beta/interactions/{id}/cancel',
          alias: '/v1/interactions/{id}/cancel',
          label: 'routeInteractionCancel',
          docs: geminiDocs + 'interactions-api',
          params: [id('paramInteractionId')],
          sample: 'abc123',
        },
      ],
    },
  ]
}

// address 返回接口的显示路径：路径加示例查询串中的 alt=sse
export function address(route: Route) {
  return route.path + (route.query === '?alt=sse' ? route.query : '')
}

// curl 返回一条可直接运行的 curl 请求：路径与请求体中的 {model} 换成 model，其余路径参数换成 sample，auth 为认证请求头
export function curl(route: Route, origin: string, auth: string[], model: string) {
  const path = route.path.replace('{model}', model).replace(/\{\w+\}/, route.sample ?? '')
  const method =
    route.method === 'GET' || (route.method === 'POST' && (route.body ?? route.form))
      ? ''
      : ' -X ' + route.method
  const lines = [`curl${method} "${origin}${path}${route.query ?? ''}"`]
  for (const header of [...auth, ...(route.headers ?? [])]) lines.push(`  -H "${header}"`)
  if (route.body !== undefined) {
    const json = JSON.stringify(route.body, null, 2).replaceAll('{model}', model)
    lines.push('  -H "Content-Type: application/json"', `  -d '${json.replaceAll('\n', '\n  ')}'`)
  }
  for (const field of route.form ?? []) lines.push('  -F ' + field)
  return lines.join(' \\\n')
}
