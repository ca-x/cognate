import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { Languages, Monitor, Moon, Sun } from "lucide-react"
import i18n, { readPreference, savePreference } from "./i18n"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./components/ui/select"

type Theme = "system" | "light" | "dark"
const storedTheme = readPreference("theme")
const initialTheme: Theme = storedTheme === "light" || storedTheme === "dark" ? storedTheme : "system"
const systemTheme = window.matchMedia("(prefers-color-scheme: dark)")

function applyTheme(theme: Theme) {
  document.documentElement.dataset.theme = theme === "system" ? systemTheme.matches ? "dark" : "light" : theme
}

applyTheme(initialTheme)

export function Preferences() {
  const { t } = useTranslation()
  const [theme, setTheme] = useState<Theme>(() => { const saved = readPreference("theme"); return saved === "light" || saved === "dark" ? saved : "system" })
  useEffect(() => {
    applyTheme(theme)
    const update = () => { if (theme === "system") applyTheme(theme) }
    systemTheme.addEventListener("change", update)
    return () => systemTheme.removeEventListener("change", update)
  }, [theme])
  return <div className="preferences" role="group" aria-label={t("显示与语言偏好")}>
    <Select value={theme} onValueChange={(value) => { if (value === "system" || value === "light" || value === "dark") { setTheme(value); savePreference("theme", value) } }}><SelectTrigger aria-label={t("主题")}><span className="flex items-center gap-2">{theme === "system" ? <Monitor size={15} aria-hidden="true" /> : theme === "dark" ? <Moon size={15} aria-hidden="true" /> : <Sun size={15} aria-hidden="true" />}<SelectValue /></span></SelectTrigger><SelectContent><SelectItem value="system">{t("跟随系统")}</SelectItem><SelectItem value="light">{t("浅色")}</SelectItem><SelectItem value="dark">{t("深色")}</SelectItem></SelectContent></Select>
    <Select value={i18n.resolvedLanguage ?? "zh-CN"} onValueChange={(value) => { if (value === "zh-CN" || value === "en") { savePreference("locale", value); void i18n.changeLanguage(value) } }}><SelectTrigger aria-label={t("界面语言")}><span className="flex items-center gap-2"><Languages size={15} aria-hidden="true" /><SelectValue /></span></SelectTrigger><SelectContent><SelectItem value="zh-CN"><span lang="zh-CN">简体中文</span></SelectItem><SelectItem value="en"><span lang="en">English</span></SelectItem></SelectContent></Select>
  </div>
}
