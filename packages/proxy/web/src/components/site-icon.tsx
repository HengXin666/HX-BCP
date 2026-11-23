import { useEffect, useState } from "react"
import { Globe2 } from "lucide-react"

import { cn } from "@/lib/utils"

export function SiteIcon({ url, compact = false }: { url: string; compact?: boolean }) {
  const [failed, setFailed] = useState(false)
  useEffect(() => setFailed(false), [url])

  let source = ""
  try { source = `${new URL(url).origin}/favicon.ico` } catch { source = "" }
  if (!source || failed) return <Globe2 className={cn("text-muted-foreground", compact ? "size-3" : "size-4")} aria-hidden="true" />
  return <img src={source} alt="" className={cn("rounded-sm", compact ? "size-3" : "size-5")} loading="lazy" onError={() => setFailed(true)} />
}
