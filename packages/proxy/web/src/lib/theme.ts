export type Theme = "light" | "dark" | "system"

const themeEvent = "hx-theme-change"
const themeColorEvent = "hx-theme-color-change"
const themeColorKey = "hx-theme-color"
export const defaultThemeColor = "#0f766e"

function normalizeColor(color: string): string | null {
  const value = color.trim().toLowerCase()
  return /^#[0-9a-f]{6}$/.test(value) ? value : null
}

function foregroundFor(color: string): string {
  const red = Number.parseInt(color.slice(1, 3), 16) / 255
  const green = Number.parseInt(color.slice(3, 5), 16) / 255
  const blue = Number.parseInt(color.slice(5, 7), 16) / 255
  const linear = (value: number) => value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
  const luminance = 0.2126 * linear(red) + 0.7152 * linear(green) + 0.0722 * linear(blue)
  return luminance > 0.42 ? "#101413" : "#ffffff"
}

export function savedTheme(): Theme {
  const value = localStorage.getItem("hx-theme")
  return value === "light" || value === "dark" ? value : "system"
}

export function savedThemeColor(): string {
  return normalizeColor(localStorage.getItem(themeColorKey) ?? "") ?? defaultThemeColor
}

export function applyThemeColor(color: string): string {
  const normalized = normalizeColor(color) ?? defaultThemeColor
  document.documentElement.style.setProperty("--brand", normalized)
  document.documentElement.style.setProperty("--brand-on-color", foregroundFor(normalized))
  return normalized
}

export function setThemeColor(color: string) {
  const normalized = applyThemeColor(color)
  if (normalized === defaultThemeColor) localStorage.removeItem(themeColorKey)
  else localStorage.setItem(themeColorKey, normalized)
  window.dispatchEvent(new CustomEvent<string>(themeColorEvent, { detail: normalized }))
}

export function applyTheme(theme: Theme) {
  const dark = theme === "dark" || (theme === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches)
  document.documentElement.classList.toggle("dark", dark)
  document.documentElement.dataset.theme = theme
  document.querySelector('meta[name="theme-color"]')?.setAttribute("content", dark ? "#0f1413" : "#f3f7f5")
  if (theme === "system") localStorage.removeItem("hx-theme")
  else localStorage.setItem("hx-theme", theme)
}

export function setTheme(theme: Theme) {
  applyTheme(theme)
  window.dispatchEvent(new CustomEvent<Theme>(themeEvent, { detail: theme }))
}

export function subscribeTheme(listener: (theme: Theme) => void) {
  const onTheme = (event: Event) => listener((event as CustomEvent<Theme>).detail)
  const media = window.matchMedia("(prefers-color-scheme: dark)")
  const onSystemTheme = () => {
    if (savedTheme() === "system") applyTheme("system")
  }
  window.addEventListener(themeEvent, onTheme)
  media.addEventListener("change", onSystemTheme)
  return () => {
    window.removeEventListener(themeEvent, onTheme)
    media.removeEventListener("change", onSystemTheme)
  }
}

export function subscribeThemeColor(listener: (color: string) => void) {
  const onColor = (event: Event) => listener((event as CustomEvent<string>).detail)
  window.addEventListener(themeColorEvent, onColor)
  return () => window.removeEventListener(themeColorEvent, onColor)
}
