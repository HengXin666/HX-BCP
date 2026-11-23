import { useCallback, useEffect, useState } from "react"
import { BookOpen, LoaderCircle, Plus, Save, Trash2 } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { api } from "@/lib/api"
import type { RoutingRule, RoutingRulesConfig, RoutingRuleSet } from "@/lib/types"

interface RulesPageProps { onNotice: (message: string, tone?: "success" | "error") => void }

const ruleTypes: Array<{ value: RoutingRule["type"]; label: string }> = [
  { value: "domain_suffix", label: "域名后缀" }, { value: "domain", label: "完整域名" },
  { value: "domain_keyword", label: "域名关键词" }, { value: "ip_cidr", label: "IP CIDR" },
  { value: "geosite", label: "GeoSite" }, { value: "geoip", label: "GeoIP" },
  { value: "process_name", label: "进程名" }, { value: "network", label: "TCP / UDP" },
  { value: "dst_port", label: "目标端口" },
]

export function RulesPage({ onNotice }: RulesPageProps) {
  const [config, setConfig] = useState<RoutingRulesConfig>({ rule_sets: [] })
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try { setConfig(await api.routingRules()) }
    catch (error) { onNotice(error instanceof Error ? error.message : "加载站点别名失败", "error") }
    finally { setLoading(false) }
  }, [onNotice])

  useEffect(() => { void load() }, [load])

  const updateSet = (index: number, patch: Partial<RoutingRuleSet>) => {
    const rule_sets = [...config.rule_sets]
    if (!rule_sets[index]) return
    rule_sets[index] = { ...rule_sets[index], ...patch }
    setConfig({ ...config, rule_sets })
  }

  const updateRule = (setIndex: number, ruleIndex: number, patch: Partial<RoutingRule>) => {
    const set = config.rule_sets[setIndex]
    const current = set?.rules[ruleIndex]
    if (!set || !current) return
    const rules = [...set.rules]
    rules[ruleIndex] = { type: patch.type ?? current.type, value: patch.value ?? current.value }
    updateSet(setIndex, { rules })
  }

  async function save() {
    setSaving(true)
    try { setConfig(await api.updateRoutingRules({ rule_sets: config.rule_sets })); onNotice("站点别名已保存并应用") }
    catch (error) { onNotice(error instanceof Error ? error.message : "保存站点别名失败", "error") }
    finally { setSaving(false) }
  }

  if (loading) return <div className="flex min-h-72 items-center justify-center gap-2 text-sm text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />正在加载站点别名</div>

  return <div className="space-y-4">
    <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
      <div><h1 className="text-xl font-semibold">站点别名</h1><p className="mt-1 max-w-3xl text-sm leading-5 text-muted-foreground">集中维护可复用的网页匹配集合。具体走直连、阻断或代理组，由每个代理服务单独选择。</p></div>
      <div className="flex gap-2"><Button variant="outline" onClick={() => setConfig({ ...config, rule_sets: [...config.rule_sets, createRuleSet(config.rule_sets)] })}><Plus />新增别名</Button><Button onClick={() => void save()} disabled={saving}>{saving ? <LoaderCircle className="animate-spin" /> : <Save />}保存并应用</Button></div>
    </div>

    {config.rule_sets.length === 0 ? <div className="flex min-h-64 flex-col items-center justify-center rounded-lg border bg-card text-center"><BookOpen className="mb-3 size-8 text-muted-foreground" /><div className="font-medium">尚未创建站点别名</div><div className="mt-1 text-xs text-muted-foreground">创建后可在各代理组的“路由策略”中引用。</div></div> : config.rule_sets.map((set, setIndex) => <section key={`${set.id}-${setIndex}`} className="overflow-hidden rounded-lg border bg-card">
      <div className="flex flex-wrap items-center gap-3 border-b bg-muted/70 px-4 py-3">
        <label className="flex items-center gap-2 text-xs font-medium"><Checkbox checked={set.enabled} onCheckedChange={(value) => updateSet(setIndex, { enabled: value === true })} />启用</label>
        <Input className="w-36" value={set.id} maxLength={40} onChange={(event) => updateSet(setIndex, { id: event.target.value })} aria-label="站点别名标识" />
        <Input className="min-w-44 flex-1" value={set.name} maxLength={60} onChange={(event) => updateSet(setIndex, { name: event.target.value })} aria-label="站点别名名称" />
        <label className="flex items-center gap-2 text-xs">优先级<Input className="w-24" type="number" min={0} max={10000} value={set.priority} onChange={(event) => updateSet(setIndex, { priority: Number(event.target.value) })} /></label>
        <Button variant="ghost" size="icon" title="删除站点别名" aria-label={`删除 ${set.name}`} onClick={() => setConfig({ ...config, rule_sets: config.rule_sets.filter((_, index) => index !== setIndex) })}><Trash2 /></Button>
      </div>
      <div className="p-4">
        <div className="mb-2 flex items-center justify-between"><h2 className="text-xs font-semibold">网页匹配项 <Badge variant="secondary">{set.rules.length}</Badge></h2><Button variant="outline" size="sm" onClick={() => updateSet(setIndex, { rules: [...set.rules, { type: "domain_suffix", value: "example.com" }] })}><Plus />添加</Button></div>
        <div className="space-y-2">{set.rules.map((rule, ruleIndex) => <div key={ruleIndex} className="grid grid-cols-[140px_minmax(0,1fr)_32px] gap-2">
          <Select value={rule.type} onValueChange={(value) => updateRule(setIndex, ruleIndex, { type: value as RoutingRule["type"] })}><SelectTrigger aria-label="匹配类型"><SelectValue /></SelectTrigger><SelectContent>{ruleTypes.map((type) => <SelectItem key={type.value} value={type.value}>{type.label}</SelectItem>)}</SelectContent></Select>
          <Input value={rule.value} onChange={(event) => updateRule(setIndex, ruleIndex, { value: event.target.value })} aria-label={`${set.name} 匹配值`} />
          <Button variant="ghost" size="icon" title="删除匹配项" aria-label="删除匹配项" onClick={() => updateSet(setIndex, { rules: set.rules.filter((_, index) => index !== ruleIndex) })}><Trash2 /></Button>
        </div>)}</div>
      </div>
    </section>)}
  </div>
}

function createRuleSet(existing: RoutingRuleSet[]): RoutingRuleSet {
  let suffix = existing.length + 1
  while (existing.some((set) => set.id === `sites-${suffix}`)) suffix++
  return { id: `sites-${suffix}`, name: `站点组 ${suffix}`, enabled: true, priority: suffix * 10, routes: [], rules: [{ type: "domain_suffix", value: "example.com" }] }
}
