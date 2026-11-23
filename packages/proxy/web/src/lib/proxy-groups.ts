import type { ListenerKind, ProxyGroupStrategy } from "@/lib/types"

export const strategyMeta: Record<ProxyGroupStrategy, { label: string; short: string; detail: string }> = {
  "url-test": { label: "自动测速", short: "始终走最快成员", detail: "定期测速并选择当前最快的可用节点。" },
  fallback: { label: "故障切换", short: "按顺序使用可用成员", detail: "主节点失效后切换到下一成员，恢复后自动切回。" },
  "round-robin": { label: "轮询", short: "请求依次分配", detail: "请求依次分给每个可用成员。" },
  "consistent-hashing": { label: "按站点哈希", short: "同一网站固定出口", detail: "按目标域名进行一致性哈希。" },
  "sticky-sessions": { label: "会话粘滞", short: "同一会话短期固定", detail: "相同来源与目标在一段时间内固定出口。" },
  manual: { label: "手动选择", short: "由客户端选择", detail: "保留成员列表供客户端手动切换。" },
}

/**
 * 出站策略的可选项，顺序即界面上的顺序。
 * 代理服务的创建表单与列表徽章共用它，避免两处各维护一份策略清单。
 */
export const strategyChoices: Array<{ value: ProxyGroupStrategy; label: string }> = (
  ["url-test", "fallback", "round-robin", "consistent-hashing", "sticky-sessions", "manual"] as const
).map((value) => ({ value, label: strategyMeta[value].label }))

export function strategyLabel(strategy: ProxyGroupStrategy): string {
  return strategyMeta[strategy]?.label || strategy
}

// 入口协议。WS 系列走共享入口的内部端口，其余直接监听自己的端口。
export const listenerKinds: Array<{ value: ListenerKind; label: string }> = [
  { value: "mixed", label: "Mixed" }, { value: "http", label: "HTTP" }, { value: "socks", label: "SOCKS5" },
  { value: "vless", label: "VLESS WS" }, { value: "vmess", label: "VMess WS" }, { value: "trojan", label: "Trojan WS" },
]

export const regionChoices = [
  { value: "jp", label: "日本" }, { value: "hk", label: "香港" }, { value: "tw", label: "台湾" },
  { value: "sg", label: "新加坡" }, { value: "us", label: "美国" }, { value: "kr", label: "韩国" },
]

export function regionLabel(region: string): string {
  return regionChoices.find((item) => item.value === region)?.label || region
}

/**
 * 按节点显示名匹配地区。机场命名不统一（"JP" / "日本" / "东京" 都指日本），
 * 所以每个地区是一组别名，命中任一即可。
 */
export function matchesRegion(name: string, region: string): boolean {
  const lower = name.toLowerCase()
  const aliases: Record<string, string[]> = {
    jp: ["jp", "japan", "日本", "东京", "大阪", "tokyo", "osaka"],
    hk: ["hk", "香港", "hong kong"],
    tw: ["tw", "台湾", "台灣", "taipei"],
    sg: ["sg", "新加坡", "singapore"],
    us: ["us", "美国", "usa", "los angeles"],
    kr: ["kr", "韩国", "korea", "seoul"],
  }
  return (aliases[region] || [region]).some((value) => lower.includes(value))
}
