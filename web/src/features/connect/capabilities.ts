import {
  authHeaders,
  curl,
  filesBeta,
  geminiHello,
  hello,
  modelParam,
  openaiDocs,
  type Protocol,
  type Route,
} from '@/features/connect/endpoints'

// CapabilityId 是接入页能力的标识，能力名称与说明的文案键为 cap 加首字母大写的标识与其后加 Hint
export type CapabilityId =
  | 'chat'
  | 'thinking'
  | 'tools'
  | 'structured'
  | 'search'
  | 'vision'
  | 'image'
  | 'transcription'
  | 'files'
  | 'tokens'
  | 'completion'
  | 'cache'

// Example 是一项能力在一种协议下的用法：name 是协议的官方名称，docs 是该协议这项能力的官方指南
export interface Example extends Route {
  protocol: Protocol
  name: string
}

// Capability 是接入页的一项能力与各协议的用法
export interface Capability {
  id: CapabilityId
  examples: Example[]
}

// chatRoutes 是四种对话接口的路径、协议与示例请求的公共部分
const chatRoutes = {
  chat: {
    method: 'POST',
    path: '/v1/chat/completions',
    protocol: 'openai',
    name: 'OpenAI Chat Completions',
    label: 'routeChat',
  },
  responses: {
    method: 'POST',
    path: '/v1/responses',
    protocol: 'openai',
    name: 'OpenAI Responses',
    label: 'routeResponses',
  },
  messages: {
    method: 'POST',
    path: '/v1/messages',
    protocol: 'anthropic',
    name: 'Anthropic Messages',
    label: 'routeMessages',
  },
  gemini: {
    method: 'POST',
    path: '/v1beta/models/{model}:generateContent',
    protocol: 'gemini',
    name: 'Gemini API',
    label: 'routeGenerate',
  },
} as const

// weatherSchema 是函数参数与结构化输出示例共用的 JSON Schema
const weatherSchema = {
  type: 'object',
  properties: { city: { type: 'string' } },
  required: ['city'],
}

// pixel 是图片输入示例中的 1×1 PNG 的 Base64
const pixel =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=='

// chatExamples 是四种对话接口的流式对话示例
const chatExamples = {
  chat: {
    ...chatRoutes.chat,
    docs: 'https://developers.openai.com/api/docs/guides/streaming-responses',
    params: [
      modelParam(),
      { name: 'messages', in: 'body', required: true, label: 'paramMessages' },
      { name: 'stream', in: 'body', label: 'paramStream' },
    ],
    body: { model: '{model}', stream: true, messages: hello },
  },
  responses: {
    ...chatRoutes.responses,
    docs: 'https://developers.openai.com/api/docs/guides/streaming-responses',
    params: [
      modelParam(),
      { name: 'input', in: 'body', required: true, label: 'paramInput' },
      { name: 'stream', in: 'body', label: 'paramStream' },
    ],
    body: { model: '{model}', stream: true, input: 'Hello' },
  },
  messages: {
    ...chatRoutes.messages,
    docs: 'https://platform.claude.com/docs/en/build-with-claude/streaming',
    params: [
      modelParam(),
      { name: 'max_tokens', in: 'body', required: true, label: 'paramMaxTokens' },
      { name: 'messages', in: 'body', required: true, label: 'paramMessages' },
      { name: 'stream', in: 'body', label: 'paramStream' },
    ],
    body: { model: '{model}', max_tokens: 1024, stream: true, messages: hello },
  },
  gemini: {
    ...chatRoutes.gemini,
    path: '/v1beta/models/{model}:streamGenerateContent',
    label: 'routeStream',
    docs: 'https://ai.google.dev/gemini-api/docs/text-generation',
    params: [
      modelParam('path'),
      { name: 'alt', in: 'query', label: 'paramAlt' },
      { name: 'contents', in: 'body', required: true, label: 'paramContents' },
    ],
    query: '?alt=sse',
    body: { contents: geminiHello },
  },
} satisfies Record<string, Example>

// capabilities 是接入页按用途列出的能力，每项给出各协议的接口、主要参数、示例请求与官方指南
export const capabilities: Capability[] = [
  { id: 'chat', examples: Object.values(chatExamples) },
  {
    id: 'thinking',
    examples: [
      {
        ...chatRoutes.chat,
        docs: 'https://developers.openai.com/api/docs/guides/reasoning',
        params: [
          modelParam(),
          { name: 'reasoning_effort', in: 'body', label: 'paramReasoning' },
          { name: 'messages', in: 'body', required: true, label: 'paramMessages' },
        ],
        body: { model: '{model}', reasoning_effort: 'high', messages: hello },
      },
      {
        ...chatRoutes.responses,
        docs: 'https://developers.openai.com/api/docs/guides/reasoning',
        params: [
          modelParam(),
          { name: 'reasoning', in: 'body', label: 'paramReasoning' },
          { name: 'input', in: 'body', required: true, label: 'paramInput' },
        ],
        body: { model: '{model}', reasoning: { effort: 'high', summary: 'auto' }, input: 'Hello' },
      },
      {
        ...chatRoutes.messages,
        docs: 'https://platform.claude.com/docs/en/build-with-claude/extended-thinking',
        params: [
          modelParam(),
          { name: 'thinking', in: 'body', label: 'paramThinking' },
          { name: 'max_tokens', in: 'body', required: true, label: 'paramMaxTokens' },
          { name: 'messages', in: 'body', required: true, label: 'paramMessages' },
        ],
        body: {
          model: '{model}',
          max_tokens: 16000,
          thinking: { type: 'enabled', budget_tokens: 10000 },
          messages: hello,
        },
      },
      {
        ...chatRoutes.gemini,
        docs: 'https://ai.google.dev/gemini-api/docs/thinking',
        params: [
          modelParam('path'),
          { name: 'generationConfig.thinkingConfig', in: 'body', label: 'paramThinkingConfig' },
          { name: 'contents', in: 'body', required: true, label: 'paramContents' },
        ],
        body: {
          contents: geminiHello,
          generationConfig: { thinkingConfig: { thinkingBudget: 8192, includeThoughts: true } },
        },
      },
    ],
  },
  {
    id: 'tools',
    examples: [
      {
        ...chatRoutes.chat,
        docs: 'https://developers.openai.com/api/docs/guides/function-calling',
        params: [
          modelParam(),
          { name: 'tools', in: 'body', label: 'paramTools' },
          { name: 'tool_choice', in: 'body', label: 'paramToolChoice' },
          { name: 'messages', in: 'body', required: true, label: 'paramMessages' },
        ],
        body: {
          model: '{model}',
          messages: [{ role: 'user', content: 'What is the weather in Paris?' }],
          tools: [
            {
              type: 'function',
              function: {
                name: 'get_weather',
                description: 'Get the current weather',
                parameters: weatherSchema,
              },
            },
          ],
        },
      },
      {
        ...chatRoutes.responses,
        docs: 'https://developers.openai.com/api/docs/guides/function-calling',
        params: [
          modelParam(),
          { name: 'tools', in: 'body', label: 'paramTools' },
          { name: 'tool_choice', in: 'body', label: 'paramToolChoice' },
          { name: 'input', in: 'body', required: true, label: 'paramInput' },
        ],
        body: {
          model: '{model}',
          input: 'What is the weather in Paris?',
          tools: [
            {
              type: 'function',
              name: 'get_weather',
              description: 'Get the current weather',
              parameters: weatherSchema,
            },
          ],
        },
      },
      {
        ...chatRoutes.messages,
        docs: 'https://platform.claude.com/docs/en/agents-and-tools/tool-use/overview',
        params: [
          modelParam(),
          { name: 'tools', in: 'body', label: 'paramTools' },
          { name: 'tool_choice', in: 'body', label: 'paramToolChoice' },
          { name: 'messages', in: 'body', required: true, label: 'paramMessages' },
        ],
        body: {
          model: '{model}',
          max_tokens: 1024,
          messages: [{ role: 'user', content: 'What is the weather in Paris?' }],
          tools: [
            {
              name: 'get_weather',
              description: 'Get the current weather',
              input_schema: weatherSchema,
            },
          ],
        },
      },
      {
        ...chatRoutes.gemini,
        docs: 'https://ai.google.dev/gemini-api/docs/function-calling',
        params: [
          modelParam('path'),
          { name: 'tools', in: 'body', label: 'paramTools' },
          { name: 'toolConfig', in: 'body', label: 'paramToolChoice' },
          { name: 'contents', in: 'body', required: true, label: 'paramContents' },
        ],
        body: {
          contents: [{ role: 'user', parts: [{ text: 'What is the weather in Paris?' }] }],
          tools: [
            {
              functionDeclarations: [
                {
                  name: 'get_weather',
                  description: 'Get the current weather',
                  parameters: weatherSchema,
                },
              ],
            },
          ],
        },
      },
    ],
  },
  {
    id: 'structured',
    examples: [
      {
        ...chatRoutes.chat,
        docs: 'https://developers.openai.com/api/docs/guides/structured-outputs',
        params: [
          modelParam(),
          { name: 'response_format', in: 'body', label: 'paramFormat' },
          { name: 'messages', in: 'body', required: true, label: 'paramMessages' },
        ],
        body: {
          model: '{model}',
          messages: [{ role: 'user', content: 'Which city hosts the Louvre?' }],
          response_format: {
            type: 'json_schema',
            json_schema: { name: 'city', schema: weatherSchema },
          },
        },
      },
      {
        ...chatRoutes.responses,
        docs: 'https://developers.openai.com/api/docs/guides/structured-outputs',
        params: [
          modelParam(),
          { name: 'text.format', in: 'body', label: 'paramFormat' },
          { name: 'input', in: 'body', required: true, label: 'paramInput' },
        ],
        body: {
          model: '{model}',
          input: 'Which city hosts the Louvre?',
          text: { format: { type: 'json_schema', name: 'city', schema: weatherSchema } },
        },
      },
      {
        ...chatRoutes.messages,
        docs: 'https://platform.claude.com/docs/en/build-with-claude/structured-outputs',
        params: [
          modelParam(),
          { name: 'output_config.format', in: 'body', label: 'paramFormat' },
          { name: 'messages', in: 'body', required: true, label: 'paramMessages' },
        ],
        body: {
          model: '{model}',
          max_tokens: 1024,
          messages: [{ role: 'user', content: 'Which city hosts the Louvre?' }],
          output_config: { format: { type: 'json_schema', schema: weatherSchema } },
        },
      },
      {
        ...chatRoutes.gemini,
        docs: 'https://ai.google.dev/gemini-api/docs/structured-output',
        params: [
          modelParam('path'),
          { name: 'generationConfig.responseJsonSchema', in: 'body', label: 'paramFormat' },
          { name: 'contents', in: 'body', required: true, label: 'paramContents' },
        ],
        body: {
          contents: [{ role: 'user', parts: [{ text: 'Which city hosts the Louvre?' }] }],
          generationConfig: {
            responseMimeType: 'application/json',
            responseJsonSchema: weatherSchema,
          },
        },
      },
    ],
  },
  {
    id: 'search',
    examples: [
      {
        ...chatRoutes.chat,
        docs: 'https://developers.openai.com/api/docs/guides/tools-web-search',
        params: [
          modelParam(),
          { name: 'web_search_options', in: 'body', label: 'paramSearch' },
          { name: 'messages', in: 'body', required: true, label: 'paramMessages' },
        ],
        body: {
          model: '{model}',
          web_search_options: {},
          messages: [{ role: 'user', content: 'What happened in tech news today?' }],
        },
      },
      {
        ...chatRoutes.responses,
        docs: 'https://developers.openai.com/api/docs/guides/tools-web-search',
        params: [
          modelParam(),
          { name: 'tools', in: 'body', label: 'paramSearch' },
          { name: 'input', in: 'body', required: true, label: 'paramInput' },
        ],
        body: {
          model: '{model}',
          tools: [{ type: 'web_search' }],
          input: 'What happened in tech news today?',
        },
      },
      {
        ...chatRoutes.messages,
        docs: 'https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-search-tool',
        params: [
          modelParam(),
          { name: 'tools', in: 'body', label: 'paramSearch' },
          { name: 'messages', in: 'body', required: true, label: 'paramMessages' },
        ],
        body: {
          model: '{model}',
          max_tokens: 1024,
          tools: [{ type: 'web_search_20250305', name: 'web_search' }],
          messages: [{ role: 'user', content: 'What happened in tech news today?' }],
        },
      },
      {
        ...chatRoutes.gemini,
        docs: 'https://ai.google.dev/gemini-api/docs/google-search',
        params: [
          modelParam('path'),
          { name: 'tools', in: 'body', label: 'paramSearch' },
          { name: 'contents', in: 'body', required: true, label: 'paramContents' },
        ],
        body: {
          contents: [{ role: 'user', parts: [{ text: 'What happened in tech news today?' }] }],
          tools: [{ googleSearch: {} }],
        },
      },
    ],
  },
  {
    id: 'vision',
    examples: [
      {
        ...chatRoutes.chat,
        docs: 'https://developers.openai.com/api/docs/guides/images-vision',
        params: [
          modelParam(),
          { name: 'messages[].content[].image_url', in: 'body', label: 'paramMedia' },
        ],
        body: {
          model: '{model}',
          messages: [
            {
              role: 'user',
              content: [
                { type: 'text', text: 'Describe this image' },
                { type: 'image_url', image_url: { url: 'data:image/png;base64,' + pixel } },
              ],
            },
          ],
        },
      },
      {
        ...chatRoutes.responses,
        docs: 'https://developers.openai.com/api/docs/guides/images-vision',
        params: [modelParam(), { name: 'input[].content[]', in: 'body', label: 'paramMedia' }],
        body: {
          model: '{model}',
          input: [
            {
              role: 'user',
              content: [
                { type: 'input_text', text: 'Describe this image' },
                { type: 'input_image', image_url: 'data:image/png;base64,' + pixel },
              ],
            },
          ],
        },
      },
      {
        ...chatRoutes.messages,
        docs: 'https://platform.claude.com/docs/en/build-with-claude/vision',
        params: [modelParam(), { name: 'messages[].content[]', in: 'body', label: 'paramMedia' }],
        body: {
          model: '{model}',
          max_tokens: 1024,
          messages: [
            {
              role: 'user',
              content: [
                {
                  type: 'image',
                  source: { type: 'base64', media_type: 'image/png', data: pixel },
                },
                { type: 'text', text: 'Describe this image' },
              ],
            },
          ],
        },
      },
      {
        ...chatRoutes.gemini,
        docs: 'https://ai.google.dev/gemini-api/docs/image-understanding',
        params: [
          modelParam('path'),
          { name: 'contents[].parts[]', in: 'body', label: 'paramMedia' },
        ],
        body: {
          contents: [
            {
              role: 'user',
              parts: [
                { inlineData: { mimeType: 'image/png', data: pixel } },
                { text: 'Describe this image' },
              ],
            },
          ],
        },
      },
    ],
  },
  {
    id: 'image',
    examples: [
      {
        method: 'POST',
        path: '/v1/images/generations',
        protocol: 'openai',
        name: 'OpenAI Images',
        label: 'routeImages',
        docs: 'https://developers.openai.com/api/docs/guides/image-generation',
        params: [
          { name: 'prompt', in: 'body', required: true, label: 'paramImagePrompt' },
          modelParam('body', false),
          { name: 'size', in: 'body', label: 'paramSize' },
          { name: 'quality', in: 'body', label: 'paramQuality' },
        ],
        body: { prompt: 'A watercolor fox in the snow', size: '1536x1024', quality: 'high' },
      },
      {
        ...chatRoutes.gemini,
        model: 'image',
        docs: 'https://ai.google.dev/gemini-api/docs/image-generation',
        params: [
          modelParam('path'),
          { name: 'generationConfig.responseModalities', in: 'body', label: 'paramModalities' },
          { name: 'generationConfig.imageConfig', in: 'body', label: 'paramImageConfig' },
          { name: 'contents', in: 'body', required: true, label: 'paramContents' },
        ],
        body: {
          contents: [{ role: 'user', parts: [{ text: 'A watercolor fox in the snow' }] }],
          generationConfig: {
            responseModalities: ['IMAGE', 'TEXT'],
            imageConfig: { aspectRatio: '16:9' },
          },
        },
      },
    ],
  },
  {
    id: 'transcription',
    examples: [
      {
        method: 'POST',
        path: '/v1/audio/transcriptions',
        protocol: 'openai',
        name: 'OpenAI Transcriptions',
        label: 'routeAudio',
        docs: 'https://developers.openai.com/api/docs/guides/speech-to-text',
        params: [
          { name: 'file', in: 'form', required: true, label: 'paramAudio' },
          { name: 'model', in: 'form', label: 'paramModel' },
          { name: 'response_format', in: 'form', label: 'paramAudioFormat' },
        ],
        form: ['"file=@speech.mp3"', '"response_format=text"'],
      },
      {
        method: 'POST',
        path: '/v1/audio/translations',
        protocol: 'openai',
        name: 'OpenAI Translations',
        label: 'routeTranslations',
        docs: 'https://developers.openai.com/api/docs/guides/speech-to-text',
        params: [
          { name: 'file', in: 'form', required: true, label: 'paramAudio' },
          { name: 'model', in: 'form', label: 'paramModel' },
          { name: 'prompt', in: 'form', label: 'paramAudioPrompt' },
        ],
        form: ['"file=@speech.mp3"'],
      },
    ],
  },
  {
    id: 'files',
    examples: [
      {
        method: 'POST',
        path: '/v1/files',
        protocol: 'openai',
        name: 'OpenAI Files',
        label: 'routeFiles',
        docs: 'https://developers.openai.com/api/docs/guides/file-inputs',
        params: [
          { name: 'file', in: 'form', required: true, label: 'paramFile' },
          { name: 'purpose', in: 'form', label: 'paramPurpose' },
        ],
        form: ['"file=@document.pdf"', '"purpose=user_data"'],
      },
      {
        method: 'POST',
        path: '/v1/files',
        protocol: 'anthropic',
        name: 'Anthropic Files',
        label: 'routeFiles',
        docs: 'https://platform.claude.com/docs/en/build-with-claude/files',
        params: [
          { name: 'anthropic-beta', in: 'header', required: true, label: 'paramBeta' },
          { name: 'file', in: 'form', required: true, label: 'paramFile' },
        ],
        headers: [filesBeta],
        form: ['"file=@document.pdf"'],
      },
      {
        method: 'POST',
        path: '/upload/v1beta/files',
        protocol: 'gemini',
        name: 'Gemini Files',
        label: 'routeFiles',
        docs: 'https://ai.google.dev/gemini-api/docs/files',
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
    ],
  },
  {
    id: 'tokens',
    examples: [
      {
        method: 'POST',
        path: '/v1/responses/input_tokens',
        protocol: 'openai',
        name: 'OpenAI Responses',
        label: 'routeCountTokens',
        docs: openaiDocs + 'responses/subresources/input_tokens/methods/count',
        params: [modelParam(), { name: 'input', in: 'body', label: 'paramInput' }],
        body: { model: '{model}', input: 'Hello' },
      },
      {
        method: 'POST',
        path: '/v1/messages/count_tokens',
        protocol: 'anthropic',
        name: 'Anthropic Messages',
        label: 'routeCountTokens',
        docs: 'https://platform.claude.com/docs/en/build-with-claude/token-counting',
        params: [
          modelParam(),
          { name: 'messages', in: 'body', required: true, label: 'paramMessages' },
        ],
        body: { model: '{model}', messages: hello },
      },
      {
        method: 'POST',
        path: '/v1beta/models/{model}:countTokens',
        protocol: 'gemini',
        name: 'Gemini API',
        label: 'routeCountTokens',
        docs: 'https://ai.google.dev/gemini-api/docs/tokens',
        params: [modelParam('path'), { name: 'contents', in: 'body', label: 'paramContents' }],
        body: { contents: geminiHello },
      },
    ],
  },
  {
    id: 'completion',
    examples: [
      {
        method: 'POST',
        path: '/v1/completions',
        protocol: 'openai',
        name: 'OpenAI Completions',
        label: 'routeCompletions',
        model: 'completion',
        docs: openaiDocs + 'completions/methods/create',
        params: [
          modelParam(),
          { name: 'prompt', in: 'body', required: true, label: 'paramPrompt' },
          { name: 'suffix', in: 'body', label: 'paramSuffix' },
          { name: 'max_tokens', in: 'body', label: 'paramMaxTokens' },
        ],
        body: {
          model: '{model}',
          prompt: 'def fibonacci(n):\n    ',
          suffix: '\n\nprint(fibonacci(10))\n',
          max_tokens: 64,
        },
      },
    ],
  },
  {
    id: 'cache',
    examples: [
      {
        method: 'POST',
        path: '/v1beta/cachedContents',
        protocol: 'gemini',
        name: 'Gemini cachedContents',
        label: 'routeCache',
        docs: 'https://ai.google.dev/gemini-api/docs/caching',
        params: [
          modelParam(),
          { name: 'contents', in: 'body', label: 'paramContents' },
          { name: 'systemInstruction', in: 'body', label: 'paramSystem' },
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
        ...chatRoutes.gemini,
        name: 'Gemini generateContent',
        docs: 'https://ai.google.dev/gemini-api/docs/caching',
        params: [
          modelParam('path'),
          { name: 'cachedContent', in: 'body', label: 'paramCachedContent' },
          { name: 'contents', in: 'body', required: true, label: 'paramContents' },
        ],
        body: {
          cachedContent: 'cachedContents/abc123',
          contents: [{ role: 'user', parts: [{ text: 'List the termination clauses.' }] }],
        },
      },
    ],
  },
]

// curlExample 返回一种协议的流式对话请求：OpenAI 为 Chat Completions，Anthropic 为 Messages，Gemini 为 streamGenerateContent
export function curlExample(protocol: Protocol, origin: string, key: string, model: string) {
  const example = {
    openai: chatExamples.chat,
    anthropic: chatExamples.messages,
    gemini: chatExamples.gemini,
  }[protocol]
  return curl(example, origin, authHeaders[protocol](key), model)
}
