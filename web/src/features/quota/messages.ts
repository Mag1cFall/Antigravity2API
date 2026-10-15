import { defineMessages } from '@/lib/i18n'

// quotaMessages 是额度组、窗口与额度显示的文案
export const quotaMessages = defineMessages(
  {
    gemini: 'Gemini',
    '3p': 'Claude 与 GPT',
    '5h': '5 小时',
    weekly: '每周',
    remaining: '剩余 {percent}',
    resetAt: '{time} 重置',
  },
  {
    gemini: 'Gemini',
    '3p': 'Claude & GPT',
    '5h': '5h',
    weekly: 'Weekly',
    remaining: '{percent} left',
    resetAt: 'Resets {time}',
  },
  {
    gemini: 'Gemini',
    '3p': 'Claude 與 GPT',
    '5h': '5 小時',
    weekly: '每週',
    remaining: '剩餘 {percent}',
    resetAt: '{time} 重設',
  },
)
