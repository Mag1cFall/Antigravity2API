import {
  AudioLines,
  Braces,
  Brain,
  Calculator,
  ChevronRight,
  DatabaseZap,
  FileUp,
  Globe,
  ImagePlus,
  type LucideIcon,
  MessagesSquare,
  ScanEye,
  TextCursorInput,
  Wrench,
} from 'lucide-react'
import { useState } from 'react'

import { Dialog, DialogContent } from '@/components/ui/dialog'
import { Segmented } from '@/components/ui/segmented'
import { RouteDetail, useExampleInputs } from '@/features/connect/ApiDetail'
import { capabilities, type Capability, type CapabilityId } from '@/features/connect/capabilities'
import { authHeaders } from '@/features/connect/endpoints'
import { useMessages } from '@/lib/i18n'

// capabilityIcons 是各能力的图标
const capabilityIcons: Record<CapabilityId, LucideIcon> = {
  chat: MessagesSquare,
  thinking: Brain,
  tools: Wrench,
  structured: Braces,
  search: Globe,
  vision: ScanEye,
  image: ImagePlus,
  transcription: AudioLines,
  files: FileUp,
  tokens: Calculator,
  completion: TextCursorInput,
  cache: DatabaseZap,
}

// useCapabilityText 返回各能力的名称与一句话说明
function useCapabilityText() {
  const m = useMessages('connect')
  const text: Record<CapabilityId, [string, string]> = {
    chat: [m.capChat, m.capChatHint],
    thinking: [m.capThinking, m.capThinkingHint],
    tools: [m.capTools, m.capToolsHint],
    structured: [m.capStructured, m.capStructuredHint],
    search: [m.capSearch, m.capSearchHint],
    vision: [m.capVision, m.capVisionHint],
    image: [m.capImage, m.capImageHint],
    transcription: [m.capTranscription, m.capTranscriptionHint],
    files: [m.capFiles, m.capFilesHint],
    tokens: [m.capTokens, m.capTokensHint],
    completion: [m.capCompletion, m.capCompletionHint],
    cache: [m.capCache, m.capCacheHint],
  }
  return text
}

// CapabilityList 按用途列出服务支持的能力，每项点开后在右侧面板按协议查看完整地址、认证、主要参数、curl 示例与官方指南
export function CapabilityList() {
  const m = useMessages('connect')
  const app = useMessages('app')
  const text = useCapabilityText()
  const { origin, apiKey, modelFor } = useExampleInputs()
  const [detail, setDetail] = useState<{ capability: Capability; from: HTMLElement }>()
  const [shown, setShown] = useState(false)
  const [chosen, setChosen] = useState(0)
  const example = detail?.capability.examples[chosen]
  return (
    <>
      <ul className="grid grid-cols-1 overflow-hidden rounded-md border border-border md:grid-cols-2 xl:grid-cols-3">
        {capabilities.map((capability) => {
          const Icon = capabilityIcons[capability.id]
          return (
            <li key={capability.id} className="-mt-px -ml-px border-t border-l border-border">
              <button
                type="button"
                aria-haspopup="dialog"
                data-capability={capability.id}
                onClick={(event) => {
                  setDetail({ capability, from: event.currentTarget })
                  setChosen(0)
                  setShown(true)
                }}
                className="flex h-full w-full items-start gap-3 px-4 py-3 text-left hover:bg-hover"
              >
                <Icon size={16} aria-hidden className="mt-[2px] shrink-0 text-primary" />
                <span className="min-w-0 flex-1">
                  <span className="block text-sm font-medium">{text[capability.id][0]}</span>
                  <span className="mt-1 block text-xs text-foreground-muted">
                    {text[capability.id][1]}
                  </span>
                </span>
                <ChevronRight
                  size={14}
                  aria-hidden
                  className="mt-[2px] shrink-0 text-foreground-subtle"
                />
              </button>
            </li>
          )
        })}
      </ul>
      <Dialog open={shown} onOpenChange={setShown}>
        {detail && example && (
          <DialogContent
            layout="sheet"
            title={text[detail.capability.id][0]}
            description={text[detail.capability.id][1]}
            closeLabel={app.closeDialog}
            returnFocus={detail.from}
          >
            <div className="flex flex-col gap-6">
              {detail.capability.examples.length > 1 && (
                <Segmented
                  label={m.protocol}
                  value={String(chosen)}
                  onValueChange={(value) => {
                    setChosen(Number(value))
                  }}
                  options={detail.capability.examples.map((item, index) => ({
                    value: String(index),
                    label: item.name,
                  }))}
                />
              )}
              <RouteDetail
                route={example}
                protocol={example.name}
                auth={authHeaders[example.protocol](apiKey)}
                model={modelFor(example.model)}
                origin={origin}
              />
            </div>
          </DialogContent>
        )}
      </Dialog>
    </>
  )
}
