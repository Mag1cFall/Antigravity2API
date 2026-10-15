import { createContext, type ReactNode, use, useEffect, useMemo, useState } from 'react'

import type { Locale } from '@/lib/format'

// Language 是界面语言：简体中文、繁体中文（台湾）与英文
export type Language = 'zh' | 'zh-TW' | 'en'

// languages 是语言菜单中的界面语言与各自的名称
export const languages: { value: Language; name: string; short: string }[] = [
  { value: 'zh', name: '简体中文', short: '简' },
  { value: 'zh-TW', name: '繁體中文（台灣）', short: '繁' },
  { value: 'en', name: 'English', short: 'EN' },
]

// Messages 是一组按语言区分的文案，英文与繁体中文必须覆盖简体中文的全部键
export interface Messages<T extends Record<string, string>> {
  zh: T
  en: Record<keyof T, string>
  tw: Record<keyof T, string>
}

// defineMessages 声明一个功能的简体中文、英文与繁体中文（台湾）文案
export function defineMessages<T extends Record<string, string>>(
  zh: T,
  en: Record<keyof T, string>,
  tw: Record<keyof T, string>,
): Messages<T> {
  return { zh, en, tw }
}

// fill 用 vars 替换文案中的 {名称} 占位
export function fill(template: string, vars: Record<string, string | number>) {
  return template.replace(/\{(\w+)\}/g, (match, name: string) =>
    name in vars ? String(vars[name]) : match,
  )
}

// languageKey 是本地存储中保存界面语言的键
const languageKey = 'antigravity2api.language'

// initialLanguage 取已保存的语言，否则按浏览器语言选择
function initialLanguage(): Language {
  try {
    const saved = localStorage.getItem(languageKey)
    const known = languages.find((item) => item.value === saved)
    if (known) return known.value
  } catch {
    // 本地存储不可用时按浏览器语言选择
  }
  return browserLanguage(navigator.languages.length ? navigator.languages : [navigator.language])
}

// browserLanguage 按浏览器语言列表选择界面语言：台湾、香港、澳门与繁体中文用繁体中文，其余中文用简体中文
function browserLanguage(preferred: readonly string[]): Language {
  for (const tag of preferred) {
    const id = tag.toLowerCase()
    if (!id.startsWith('zh')) {
      if (id.startsWith('en')) return 'en'
      continue
    }
    return /^zh-(tw|hk|mo|hant)/.test(id) ? 'zh-TW' : 'zh'
  }
  return 'en'
}

interface I18nValue {
  language: Language
  locale: Locale
  setLanguage: (language: Language) => void
}

const I18nContext = createContext<I18nValue | null>(null)

// localeOf 是各界面语言对应的 Intl 区域
const localeOf: Record<Language, Locale> = { zh: 'zh-CN', 'zh-TW': 'zh-TW', en: 'en' }

// I18nProvider 提供当前界面语言并同步到 html lang
export function I18nProvider({ children }: { children: ReactNode }) {
  const [language, setLanguageState] = useState(initialLanguage)
  useEffect(() => {
    document.documentElement.lang = localeOf[language]
  }, [language])
  const value = useMemo<I18nValue>(
    () => ({
      language,
      locale: localeOf[language],
      setLanguage: (next) => {
        setLanguageState(next)
        try {
          localStorage.setItem(languageKey, next)
        } catch {
          // 本地存储不可用时只在本次会话生效
        }
      },
    }),
    [language],
  )
  return <I18nContext value={value}>{children}</I18nContext>
}

// useI18n 返回当前语言与区域
export function useI18n() {
  const value = use(I18nContext)
  if (!value) throw new Error('useI18n 必须在 I18nProvider 内使用')
  return value
}

// useMessages 返回当前语言的文案
export function useMessages<T extends Record<string, string>>(messages: Messages<T>): T {
  const { language } = useI18n()
  if (language === 'en') return messages.en as T
  if (language === 'zh-TW') return messages.tw as T
  return messages.zh
}
