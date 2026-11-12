import { createContext, Fragment, type ReactNode, use, useMemo, useState } from 'react'

import type { Locale } from '@/lib/format'
import { Translator } from '@/lib/translator'
import type { Catalog } from '@/locales/zh-CN'

// languages 是语言菜单中的界面语言、名称与顶栏按钮上的缩写
export const languages: { value: Locale; name: string; short: string }[] = [
  { value: 'zh-CN', name: '简体中文', short: '简' },
  { value: 'zh-TW', name: '繁體中文（台灣）', short: '繁' },
  { value: 'en', name: 'English', short: 'EN' },
  { value: 'ja', name: '日本語', short: '日' },
  { value: 'ko', name: '한국어', short: '한' },
  { value: 'fr', name: 'Français', short: 'FR' },
  { value: 'de', name: 'Deutsch', short: 'DE' },
]

// LocaleModule 是一种语言的界面文案与服务文案翻译表，服务文案以简体中文写成，简体中文没有翻译表
interface LocaleModule {
  messages: Catalog
  serverMessages?: Record<string, string>
}

// localeModules 按需加载各语言的文案，每种语言打包为单独的文件
const localeModules: Record<Locale, () => Promise<LocaleModule>> = {
  'zh-CN': () => import('@/locales/zh-CN'),
  'zh-TW': () => import('@/locales/zh-TW'),
  en: () => import('@/locales/en'),
  ja: () => import('@/locales/ja'),
  ko: () => import('@/locales/ko'),
  fr: () => import('@/locales/fr'),
  de: () => import('@/locales/de'),
}

// catalogs 是已加载语言的界面文案
const catalogs: Partial<Record<Locale, Catalog>> = {}
// serverTables 是已加载语言的服务文案翻译表
const serverTables: Partial<Record<Locale, Record<string, string>>> = {}
// translators 是首次翻译服务文案时由翻译表建立的翻译器
const translators: Partial<Record<Locale, Translator>> = {}

// loadLocale 加载一种语言的文案，已加载时直接返回
async function loadLocale(locale: Locale) {
  if (catalogs[locale]) return
  const module = await localeModules[locale]()
  catalogs[locale] = module.messages
  if (module.serverMessages) serverTables[locale] = module.serverMessages
}

// fill 用 vars 替换文案中的 {名称} 占位
export function fill(template: string, vars: Record<string, string | number>) {
  return template.replace(/\{(\w+)\}/g, (match, name: string) =>
    name in vars ? String(vars[name]) : match,
  )
}

// fillNodes 用 vars 替换文案中的 {名称} 占位，占位可以是元素
export function fillNodes(template: string, vars: Record<string, ReactNode>) {
  return template
    .split(/\{(\w+)\}/g)
    .map((part, index) =>
      index % 2 ? (
        <Fragment key={index}>{part in vars ? vars[part] : '{' + part + '}'}</Fragment>
      ) : (
        part
      ),
    )
}

// codeText 把文案中反引号括起的片段显示为不换行的等宽代码
export function codeText(text: string) {
  return text.split('`').map((part, index) =>
    index % 2 ? (
      <code key={index} className="font-mono whitespace-nowrap">
        {part}
      </code>
    ) : (
      part
    ),
  )
}

// languageKey 是本地存储中保存界面语言的键
const languageKey = 'antigravity2api.language'

// matchLocale 返回与语言标签最接近的界面语言：台湾、香港、澳门与繁体中文为繁体中文，其余中文为简体中文，其他按语言主标签选择，与 internal/i18n 的 Match 相同
function matchLocale(tag: string): Locale | undefined {
  const lower = tag.toLowerCase()
  if (['zh-tw', 'zh-hk', 'zh-mo'].includes(lower) || lower.startsWith('zh-hant')) return 'zh-TW'
  if (lower === 'zh' || lower.startsWith('zh-')) return 'zh-CN'
  const primary = lower.split('-')[0]
  return languages.find((item) => item.value === primary)?.value
}

// initialLocale 取已保存的语言，否则按浏览器首选语言选择，都不支持时为英文
function initialLocale(): Locale {
  try {
    const saved = localStorage.getItem(languageKey)
    const known = languages.find((item) => item.value === saved)
    if (known) return known.value
  } catch {
    // 本地存储不可用时按浏览器语言选择
  }
  const preferred = navigator.languages.length ? navigator.languages : [navigator.language]
  for (const tag of preferred) {
    const match = matchLocale(tag)
    if (match) return match
  }
  return 'en'
}

// current 是当前界面语言，供组件树之外的服务文案翻译使用
let current = initialLocale()
document.documentElement.lang = current

// loadInitialLocale 加载启动时界面语言的文案，渲染应用前调用
export function loadInitialLocale() {
  return loadLocale(current)
}

// serverText 把服务生成的简体中文日志、错误与状态说明换成当前界面语言，上游原文、账户、模型名与数值保持原样
export function serverText(text: string) {
  if (current === 'zh-CN') return text
  const translator = (translators[current] ??= new Translator(serverTables[current] ?? {}, current))
  return translator.translate(text)
}

// I18nValue 是界面语言上下文的值
interface I18nValue {
  locale: Locale
  setLocale: (locale: Locale) => Promise<void>
}

// I18nContext 向组件树提供当前界面语言
const I18nContext = createContext<I18nValue | null>(null)

// I18nProvider 提供当前界面语言；切换时先加载该语言的文案，再保存并同步到 html lang
export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState(current)
  const value = useMemo<I18nValue>(
    () => ({
      locale,
      setLocale: async (next) => {
        await loadLocale(next)
        current = next
        document.documentElement.lang = next
        setLocaleState(next)
        try {
          localStorage.setItem(languageKey, next)
        } catch {
          // 本地存储不可用时只在本次会话生效
        }
      },
    }),
    [locale],
  )
  return <I18nContext value={value}>{children}</I18nContext>
}

// useI18n 返回当前界面语言与切换函数
export function useI18n() {
  const value = use(I18nContext)
  if (!value) throw new Error('useI18n 必须在 I18nProvider 内使用')
  return value
}

// useMessages 返回当前语言中一个页面或组件的文案
export function useMessages<N extends keyof Catalog>(namespace: N): Catalog[N] {
  const { locale } = useI18n()
  const catalog = catalogs[locale]
  if (!catalog) throw new Error(locale + ' 的文案尚未加载')
  return catalog[namespace]
}
