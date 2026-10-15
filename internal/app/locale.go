package app

import "strings"

// electronLocales 是官方客户端 Electron 44 随附的界面语言包
var electronLocales = []string{
	"af", "am", "ar", "bg", "bn", "ca", "cs", "da", "de", "el", "en-GB", "en-US", "es", "es-419", "et", "fa", "fi", "fil", "fr", "gu",
	"he", "hi", "hr", "hu", "id", "it", "ja", "kn", "ko", "lt", "lv", "ml", "mr", "ms", "nb", "nl", "pl", "pt-BR", "pt-PT", "ro",
	"ru", "sk", "sl", "sr", "sv", "sw", "ta", "te", "th", "tr", "uk", "ur", "vi", "zh-CN", "zh-TW",
}

// localeAliases 是 Chromium 对只有语言代码的名称的映射
var localeAliases = map[string]string{"en": "en-US", "iw": "he", "no": "nb", "pt": "pt-BR", "tl": "fil", "zh": "zh-CN"}

// acceptLanguage 按 Electron 的规则返回更新清单请求的 Accept-Language：宿主系统语言依次解析为随附语言包之一，都无法解析时为 en-US
func acceptLanguage() string {
	for _, name := range systemLocales() {
		if locale := resolveLocale(canonicalLocale(name)); locale != "" {
			return locale
		}
	}
	return "en-US"
}

// canonicalLocale 去掉 POSIX 语言名中的字符集并以连字符分隔，例如 zh_TW.UTF-8 为 zh-TW
func canonicalLocale(name string) string {
	base, modifier, _ := strings.Cut(name, "@")
	base, _, _ = strings.Cut(base, ".")
	if modifier != "" {
		base += "@" + modifier
	}
	return strings.ReplaceAll(base, "_", "-")
}

// availableLocale 返回与 name 大小写无关地相同的随附语言包，没有时返回空串
func availableLocale(name string) string {
	for _, locale := range electronLocales {
		if strings.EqualFold(locale, name) {
			return locale
		}
	}
	return ""
}

// resolveLocale 按 Chromium 的 CheckAndResolveLocale 把语言名解析为随附语言包：先精确匹配，再按地区回退，最后查语言代码别名
func resolveLocale(name string) string {
	if locale := availableLocale(name); locale != "" {
		return locale
	}
	if strings.Contains(name, "@") {
		return ""
	}
	language, region, found := strings.Cut(strings.ToLower(name), "-")
	if found {
		fallback := language
		switch language {
		case "es":
			if region != "es" {
				fallback = "es-419"
			}
		case "pt":
			if region != "br" {
				fallback = "pt-PT"
			}
		case "zh":
			fallback = "zh-CN"
			if region == "hk" || region == "mo" {
				fallback = "zh-TW"
			}
		case "en":
			fallback = "en-GB"
			if region == "lr" || region == "ph" {
				fallback = "en-US"
			}
		}
		if locale := availableLocale(fallback); locale != "" {
			return locale
		}
	}
	return availableLocale(localeAliases[language])
}
