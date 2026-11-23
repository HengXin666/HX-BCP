import { StrictMode } from "react"
import { createRoot } from "react-dom/client"

import App from "@/App"
import "@/index.css"
import { applyTheme, applyThemeColor, savedTheme, savedThemeColor } from "@/lib/theme"

applyThemeColor(savedThemeColor())
applyTheme(savedTheme())

const root = document.getElementById("root")
if (!root) {
  throw new Error("root element is missing")
}

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
