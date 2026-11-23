import { useEffect, useState } from "react"
import { Monitor, Moon, Sun } from "lucide-react"

import { Button } from "@/components/ui/button"
import { applyTheme, savedTheme, setTheme as saveTheme, subscribeTheme, type Theme } from "@/lib/theme"

export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(savedTheme)

  useEffect(() => {
    applyTheme(theme)
    return subscribeTheme(setTheme)
  }, [])

  const next: Theme = theme === "system" ? "light" : theme === "light" ? "dark" : "system"
  const Icon = theme === "light" ? Sun : theme === "dark" ? Moon : Monitor
  const label = theme === "light" ? "明亮主题" : theme === "dark" ? "黑夜主题" : "跟随系统"
  return <Button variant="ghost" size="icon" title={`${label}，点击切换`} aria-label={`${label}，点击切换`} onClick={() => saveTheme(next)}><Icon /></Button>
}
