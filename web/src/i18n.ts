import i18n from "i18next"
import { initReactI18next, useTranslation } from "react-i18next"
import { useState, type Dispatch, type SetStateAction } from "react"
import { english } from "./locales/en"

export function readPreference(key: "theme" | "locale") {
  try { return localStorage.getItem(`skillhub.${key}`) } catch { return null }
}

export function savePreference(key: "theme" | "locale", value: string) {
  try { localStorage.setItem(`skillhub.${key}`, value) } catch { return }
}

const storedLocale = readPreference("locale")
const locale = storedLocale === "en" || storedLocale === "zh-CN" ? storedLocale : /^en(?:-|$)/i.test(navigator.language) ? "en" : "zh-CN"

void i18n.use(initReactI18next).init({
  resources: { en: { translation: english }, "zh-CN": { translation: Object.fromEntries(Object.keys(english).map((key) => [key, key])) } },
  lng: locale,
  fallbackLng: "en",
  supportedLngs: ["zh-CN", "en"],
  load: "currentOnly",
  initAsync: false,
  keySeparator: false,
  nsSeparator: false,
  returnEmptyString: true,
  interpolation: { escapeValue: false },
})

document.documentElement.lang = locale
document.title = i18n.t("Cognate 工作空间")
i18n.on("languageChanged", (language) => { document.documentElement.lang = language; document.title = i18n.t("Cognate 工作空间") })

export const t = i18n.t.bind(i18n)

type Notice = string | { key: string; values?: Record<string, unknown> }

export function useNotice(): [string, Dispatch<SetStateAction<Notice>>] {
  useTranslation()
  const [notice, setNotice] = useState<Notice>("")
  return [typeof notice === "string" ? t(notice) : t(notice.key, notice.values), setNotice]
}

export function formatNumber(value: number | bigint) {
  return new Intl.NumberFormat(i18n.resolvedLanguage).format(value)
}

export function formatDate(value: number) {
  return new Intl.DateTimeFormat(i18n.resolvedLanguage, { dateStyle: "medium", timeStyle: "short" }).format(value)
}

export default i18n
