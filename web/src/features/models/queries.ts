import { useQuery } from '@tanstack/react-query'

import type { Model } from '@/features/models/types'
import { api } from '@/lib/api'

// useModels 读取各就绪账户模型目录的合集，事件流推送时直接写入
export function useModels() {
  return useQuery({ queryKey: ['models'], queryFn: () => api<{ models: Model[] }>('/api/models') })
}
