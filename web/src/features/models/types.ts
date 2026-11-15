// Model 对应 internal/antigravity 的 Model，族模型带可选思考档位 thinking_levels 与默认档位 default_thinking_level，accounts 是目录中含有该模型的就绪账户数
export interface Model {
  id: string
  name: string
  description?: string
  methods: string[]
  input_token_limit?: number
  output_token_limit?: number
  capabilities?: Record<string, boolean>
  provider?: string
  quota_group?: string
  thinking_levels?: string[]
  default_thinking_level?: string
  accounts: number
}

// ModelKind 是模型的用途：对话、生图、语音转写或代码补全
export type ModelKind = 'chat' | 'image' | 'transcription' | 'completion'

// kindOf 按目录能力返回模型用途
export function kindOf(model: Model): ModelKind {
  const capabilities = model.capabilities ?? {}
  if (capabilities.image_generation) return 'image'
  if (capabilities.transcription) return 'transcription'
  if (capabilities.completion) return 'completion'
  return 'chat'
}
