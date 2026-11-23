import { useCallback, useEffect, useState } from "react"
import {
	Check,
	Copy,
  Gauge,
  Globe2,
  KeyRound,
  LoaderCircle,
  LogOut,
  Monitor,
  Moon,
  Palette,
  Play,
  Plus,
  RotateCcw,
  Save,
  Settings2,
  ShieldCheck,
  Sun,
  Trash2,
  UserRound,
} from "lucide-react"

import { SiteIcon } from "@/components/site-icon"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { NetworkSettings } from "@/components/network-settings"
import { Textarea } from "@/components/ui/textarea"
import { api, type APIKeyItem, type TwoFactorSetup, type TwoFactorStatus } from "@/lib/api"
import {
  defaultThemeColor,
  savedTheme,
  savedThemeColor,
  setTheme,
  setThemeColor,
  subscribeTheme,
  subscribeThemeColor,
  type Theme,
} from "@/lib/theme"
import type { GlobalSettings, HealthTarget } from "@/lib/types"
import { cn } from "@/lib/utils"

interface SettingsPageProps {
  onNotice: (message: string, tone?: "success" | "error") => void
  onSignedOut: () => void
  username?: string
}

type SiteResult = { success: number; tested: number }
type SettingsTab = "appearance" | "quality" | "dns" | "runtime" | "account"

const themeOptions: Array<{ value: Theme; label: string; description: string; icon: typeof Sun }> = [
  { value: "light", label: "明亮", description: "始终使用浅色界面", icon: Sun },
  { value: "dark", label: "黑夜", description: "始终使用深色界面", icon: Moon },
  { value: "system", label: "跟随系统", description: "随设备外观自动切换", icon: Monitor },
]

const themeColorOptions = [
  { value: "#0f766e", label: "青绿" },
  { value: "#2563eb", label: "蓝色" },
  { value: "#be123c", label: "玫红" },
  { value: "#b45309", label: "琥珀" },
  { value: "#15803d", label: "绿色" },
  { value: "#475569", label: "石墨" },
]

export function SettingsPage({ onNotice, onSignedOut, username }: SettingsPageProps) {
  const [activeTab, setActiveTab] = useState<SettingsTab>("appearance")
  const [theme, setCurrentTheme] = useState<Theme>(savedTheme)
  const [themeColor, setCurrentThemeColor] = useState(savedThemeColor)
  const [themeColorDraft, setThemeColorDraft] = useState(savedThemeColor)
  const [settings, setSettings] = useState<GlobalSettings | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [testingSites, setTestingSites] = useState(false)
  const [siteResults, setSiteResults] = useState<Record<string, SiteResult>>({})
  const [newUsername, setNewUsername] = useState(username ?? "")
  const [usernamePassword, setUsernamePassword] = useState("")
  const [currentPassword, setCurrentPassword] = useState("")
  const [newPassword, setNewPassword] = useState("")
  const [confirmPassword, setConfirmPassword] = useState("")
  const [changingAccount, setChangingAccount] = useState<"username" | "password" | null>(null)
  const [twoFactorStatus, setTwoFactorStatus] = useState<TwoFactorStatus | null>(null)
  const [twoFactorSetup, setTwoFactorSetup] = useState<TwoFactorSetup | null>(null)
  const [twoFactorCode, setTwoFactorCode] = useState("")
  const [twoFactorBusy, setTwoFactorBusy] = useState<"setup" | "enable" | "disable" | null>(null)
  const [apiKeys, setAPIKeys] = useState<APIKeyItem[]>([])
  const [apiKeyName, setApiKeyName] = useState("")
  const [creatingAPIKey, setCreatingAPIKey] = useState(false)
  const [newAPIKey, setNewAPIKey] = useState<APIKeyItem | null>(null)
  const [revokingAPIKey, setRevokingAPIKey] = useState<string | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try { setSettings(await api.globalSettings()) }
    catch (error) { onNotice(error instanceof Error ? error.message : "加载全局配置失败", "error") }
    finally { setLoading(false) }
  }, [onNotice])

  useEffect(() => { void load() }, [load])
  const loadTwoFactor = useCallback(async () => {
    try { setTwoFactorStatus(await api.twoFactorStatus()) }
    catch (error) { onNotice(error instanceof Error ? error.message : "加载 2FA 状态失败", "error") }
  }, [onNotice])
  useEffect(() => { void loadTwoFactor() }, [loadTwoFactor])
  const loadAPIKeys = useCallback(async () => {
    try { setAPIKeys((await api.apiKeys()).items) }
    catch (error) { onNotice(error instanceof Error ? error.message : "加载 API 密钥失败", "error") }
  }, [onNotice])
  useEffect(() => { void loadAPIKeys() }, [loadAPIKeys])
  useEffect(() => subscribeTheme(setCurrentTheme), [])
  useEffect(() => subscribeThemeColor((color) => { setCurrentThemeColor(color); setThemeColorDraft(color) }), [])
  useEffect(() => { setNewUsername(username ?? "") }, [username])

  async function save() {
    if (!settings) return
    setSaving(true)
    try { setSettings(await api.updateGlobalSettings(settings)); onNotice("全局配置已校验并应用") }
    catch (error) { onNotice(error instanceof Error ? error.message : "应用全局配置失败", "error") }
    finally { setSaving(false) }
  }

  async function testHealthTargets() {
    if (!settings) return
    const enabledTargets = settings.quality.health_targets.filter((target) => target.enabled)
    if (!enabledTargets.length) { onNotice("请先启用至少一个站点", "error"); return }
    setTestingSites(true)
    try {
      const saved = await api.updateGlobalSettings(settings)
      setSettings(saved)
      const result = await api.checkNodes([])
      const summary: Record<string, SiteResult> = Object.fromEntries(enabledTargets.map((target) => [target.id, { success: 0, tested: 0 }]))
      for (const item of result.items) for (const check of item.node.health_checks) {
        const target = summary[check.target_id]
        if (!target) continue
        target.tested++
        if (check.success) target.success++
      }
      setSiteResults(summary)
      onNotice(`站点测试完成：已通过 ${result.items.length} 个节点执行 URL Test`)
    } catch (error) { onNotice(error instanceof Error ? error.message : "站点测试失败", "error") }
    finally { setTestingSites(false) }
  }

  async function changeUsername() {
    const next = newUsername.trim()
    if (next.length < 3 || next.length > 64) { onNotice("用户名需要包含 3 到 64 个字符", "error"); return }
    setChangingAccount("username")
    try {
      await api.changeUsername(usernamePassword, next)
      onNotice("管理员账号已修改，请使用新账号重新登录")
      onSignedOut()
    } catch (error) { onNotice(error instanceof Error ? error.message : "修改管理员账号失败", "error") }
    finally { setChangingAccount(null) }
  }

  async function changePassword() {
    if (newPassword !== confirmPassword) { onNotice("两次输入的新密码不一致", "error"); return }
    if (newPassword.length < 10) { onNotice("新密码至少需要 10 个字符", "error"); return }
    setChangingAccount("password")
    try {
      await api.changePassword(currentPassword, newPassword)
      onNotice("管理员密码已修改，请重新登录")
      onSignedOut()
    } catch (error) { onNotice(error instanceof Error ? error.message : "修改密码失败", "error") }
    finally { setChangingAccount(null) }
  }

  async function logoutAll() {
    try { await api.logoutAll(); onSignedOut() }
    catch (error) { onNotice(error instanceof Error ? error.message : "注销会话失败", "error") }
  }

  async function beginTwoFactorSetup() {
    setTwoFactorBusy("setup")
    try {
      setTwoFactorSetup(await api.setupTwoFactor())
      setTwoFactorStatus((current) => current ? { ...current, configured: true, enabled: false, verified: false } : current)
      setTwoFactorCode("")
      onNotice("2FA 密钥已生成，请先添加到验证器，再输入当前验证码启用")
    } catch (error) { onNotice(error instanceof Error ? error.message : "生成 2FA 密钥失败", "error") }
    finally { setTwoFactorBusy(null) }
  }

  async function enableTwoFactor() {
    if (!/^\d{6}$/.test(twoFactorCode.trim())) { onNotice("请输入 6 位 2FA 验证码", "error"); return }
    setTwoFactorBusy("enable")
    try {
      await api.enableTwoFactor(twoFactorCode.trim())
      setTwoFactorSetup(null)
      setTwoFactorCode("")
      await loadTwoFactor()
      onNotice("2FA 已启用，浏览器终端现在需要验证码解锁")
    } catch (error) { onNotice(error instanceof Error ? error.message : "启用 2FA 失败", "error") }
    finally { setTwoFactorBusy(null) }
  }

  async function disableTwoFactor() {
    if (!/^\d{6}$/.test(twoFactorCode.trim())) { onNotice("请输入 6 位 2FA 验证码", "error"); return }
    setTwoFactorBusy("disable")
    try {
      await api.disableTwoFactor(twoFactorCode.trim())
      setTwoFactorCode("")
      await loadTwoFactor()
      onNotice("2FA 已关闭，终端在重新配置 2FA 前不可用")
    } catch (error) { onNotice(error instanceof Error ? error.message : "关闭 2FA 失败", "error") }
    finally { setTwoFactorBusy(null) }
  }

  async function copyTwoFactorValue(value: string, label: string) {
    try {
      await navigator.clipboard.writeText(value)
      onNotice(`${label}已复制`)
    } catch { onNotice(`无法复制${label}，请手动选择文本`, "error") }
  }

  async function createAPIKey() {
    const name = apiKeyName.trim()
    if (!name) return
    setCreatingAPIKey(true)
    try {
      const created = await api.apiKeyCreate(name)
      setNewAPIKey(created)
      setApiKeyName("")
      await loadAPIKeys()
    } catch (error) { onNotice(error instanceof Error ? error.message : "创建 API 密钥失败", "error") }
    finally { setCreatingAPIKey(false) }
  }

  async function revokeAPIKey(id: string) {
    setRevokingAPIKey(id)
    try {
      await api.apiKeyRevoke(id)
      setNewAPIKey(null)
      await loadAPIKeys()
    } catch (error) { onNotice(error instanceof Error ? error.message : "吊销 API 密钥失败", "error") }
    finally { setRevokingAPIKey(null) }
  }

  async function copyAPIKeyValue(value: string) {
    try {
      await navigator.clipboard.writeText(value)
      onNotice("密钥已复制")
    } catch { onNotice("无法复制密钥，请手动选择文本", "error") }
  }

  function changeThemeColor(color: string) {
    setThemeColor(color)
  }

  function commitThemeColor() {
    if (!/^#[0-9a-fA-F]{6}$/.test(themeColorDraft.trim())) {
      setThemeColorDraft(themeColor)
      onNotice("主题颜色需要使用 #RRGGBB 格式", "error")
      return
    }
    changeThemeColor(themeColorDraft)
  }

  if (loading) return <div className="flex min-h-72 items-center justify-center gap-2 text-sm text-muted-foreground"><LoaderCircle className="size-4 animate-spin" />正在加载全局配置</div>
  if (!settings) return <div className="py-20 text-center text-sm text-muted-foreground">全局配置不可用</div>

  const updateTarget = (index: number, patch: Partial<HealthTarget>) => {
    const targets = [...settings.quality.health_targets]
    if (!targets[index]) return
    targets[index] = { ...targets[index], ...patch }
    setSettings({ ...settings, quality: { ...settings.quality, health_targets: targets } })
  }
  const serverSettingsTab = activeTab === "quality" || activeTab === "dns" || activeTab === "runtime"

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div><h1 className="text-xl font-semibold">全局配置</h1><p className="mt-1 text-sm text-muted-foreground">外观、节点检测、DNS、数据面运行与管理员安全</p></div>
        {serverSettingsTab && <Button onClick={() => void save()} disabled={saving}>{saving ? <LoaderCircle className="animate-spin" /> : <Save />}保存并应用</Button>}
      </div>

      <Tabs value={activeTab} onValueChange={(value) => setActiveTab(value as SettingsTab)}>
        <TabsList className="grid h-auto w-full grid-cols-2 gap-0.5 sm:grid-cols-5" aria-label="设置分类">
          <TabsTrigger value="appearance"><Palette className="mr-1.5 size-3.5" />主题</TabsTrigger>
          <TabsTrigger value="quality"><Gauge className="mr-1.5 size-3.5" />节点检测</TabsTrigger>
          <TabsTrigger value="dns"><Globe2 className="mr-1.5 size-3.5" />DNS</TabsTrigger>
          <TabsTrigger value="runtime"><Settings2 className="mr-1.5 size-3.5" />运行</TabsTrigger>
          <TabsTrigger value="account"><UserRound className="mr-1.5 size-3.5" />账号安全</TabsTrigger>
        </TabsList>

        <TabsContent value="appearance">
          <SettingsPanel icon={Palette} title="显示主题" description="主题模式和颜色仅保存在当前浏览器，立即应用到整个管理面板。">
            <div className="grid gap-3 md:grid-cols-3">
              {themeOptions.map((option) => {
                const Icon = option.icon
                const selected = theme === option.value
                return <button key={option.value} type="button" aria-pressed={selected} onClick={() => setTheme(option.value)} className={cn("flex min-h-24 items-start gap-3 rounded-md border bg-card p-4 text-left transition-colors hover:bg-muted", selected && "border-primary bg-accent ring-1 ring-primary")}><span className={cn("flex size-9 shrink-0 items-center justify-center rounded-md border bg-background", selected && "border-primary text-primary")}><Icon className="size-4" /></span><span><span className="block text-sm font-semibold">{option.label}</span><span className="mt-1 block text-xs leading-5 text-muted-foreground">{option.description}</span></span></button>
              })}
            </div>
            <div className="mt-5 border-t pt-4">
              <div className="text-xs font-medium">主题颜色</div>
              <div className="mt-3 grid gap-4 lg:grid-cols-[96px_minmax(240px,360px)_1fr] lg:items-start">
                <label className="block text-xs font-medium">颜色画板<input type="color" aria-label="主题颜色选择器" value={themeColor} onChange={(event) => changeThemeColor(event.target.value)} className="mt-1 block h-16 w-24 cursor-pointer rounded-md border bg-card p-1" /></label>
                <div><Field label="十六进制颜色"><div className="flex gap-2"><Input aria-label="主题颜色十六进制" value={themeColorDraft} maxLength={7} onChange={(event) => setThemeColorDraft(event.target.value)} onBlur={commitThemeColor} onKeyDown={(event) => { if (event.key === "Enter") { event.preventDefault(); commitThemeColor() } }} className="font-mono uppercase" /><Button type="button" variant="outline" onClick={() => changeThemeColor(defaultThemeColor)} title="恢复默认主题色"><RotateCcw />恢复默认</Button></div></Field><div className="mt-3 flex flex-wrap gap-2" aria-label="主题颜色预设">{themeColorOptions.map((option) => <button key={option.value} type="button" aria-label={`${option.label}主题色`} title={option.label} onClick={() => changeThemeColor(option.value)} className={cn("relative size-8 rounded-md border shadow-sm transition-transform hover:scale-105", themeColor === option.value && "ring-2 ring-ring ring-offset-2 ring-offset-background")} style={{ backgroundColor: option.value }}>{themeColor === option.value && <Check className="absolute inset-0 m-auto size-4 text-white drop-shadow" />}</button>)}</div></div>
                <div className="rounded-md border bg-muted/50 p-3"><div className="text-xs font-medium">效果预览</div><div className="mt-3 flex flex-wrap items-center gap-2"><Button type="button" size="sm">主要操作</Button><Badge className="border-primary/40 bg-accent text-accent-foreground">选中状态</Badge><span className="inline-flex items-center gap-2 text-xs text-muted-foreground"><span className="size-3 rounded-full bg-primary" />{themeColor.toUpperCase()}</span></div></div>
              </div>
            </div>
            <div className="mt-5 border-t pt-4">
              <div className="text-xs font-medium">状态色板</div>
              <div className="mt-2 flex flex-wrap gap-3" aria-label="当前状态色板">
                <Swatch className="bg-info" label="信息" /><Swatch className="bg-success" label="正常" /><Swatch className="bg-warning" label="注意" /><Swatch className="bg-destructive" label="危险" />
              </div>
            </div>
          </SettingsPanel>
        </TabsContent>

        <TabsContent value="quality">
          <SettingsPanel icon={Gauge} title="节点检测" description="URL 延迟结果包含代理握手和 HTTP 往返，不是 ICMP ping。" action={<div className="flex gap-2"><Button variant="outline" size="sm" onClick={() => void testHealthTargets()} disabled={testingSites}>{testingSites ? <LoaderCircle className="animate-spin" /> : <Play />}测试站点</Button><Button variant="outline" size="sm" onClick={() => setSettings({ ...settings, quality: { ...settings.quality, health_targets: [...settings.quality.health_targets, newTarget(settings.quality.health_targets)] } })}><Plus />添加站点</Button></div>}>
            <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-5">
              <Field label="主测试 URL"><Input value={settings.quality.test_url} onChange={(event) => setSettings({ ...settings, quality: { ...settings.quality, test_url: event.target.value } })} /></Field>
              <Field label="复测间隔（秒）"><Input type="number" min={60} max={86400} value={settings.quality.check_interval_seconds} onChange={(event) => setSettings({ ...settings, quality: { ...settings.quality, check_interval_seconds: Number(event.target.value) } })} /></Field>
              <Field label="单次超时（秒）"><Input type="number" min={1} max={30} value={settings.quality.timeout_seconds} onChange={(event) => setSettings({ ...settings, quality: { ...settings.quality, timeout_seconds: Number(event.target.value) } })} /></Field>
              <Field label="每轮节点数"><Input type="number" min={1} max={200} value={settings.quality.batch_size} onChange={(event) => setSettings({ ...settings, quality: { ...settings.quality, batch_size: Number(event.target.value) } })} /></Field>
              <Field label="节点并发数"><Input type="number" min={1} max={16} value={settings.quality.probe_concurrency} onChange={(event) => setSettings({ ...settings, quality: { ...settings.quality, probe_concurrency: Number(event.target.value) } })} /></Field>
            </div>
            <div className="mt-5 max-h-[calc(100vh-360px)] min-h-36 overflow-y-auto rounded-md border">
              {settings.quality.health_targets.map((target, index) => <div key={`${target.id}-${index}`} className="grid gap-3 border-b p-3 last:border-b-0 lg:grid-cols-[auto_40px_120px_minmax(260px,1fr)_110px_auto] lg:items-end">
                <label className="flex h-8 items-center gap-2 text-xs font-medium"><Checkbox aria-label={`启用 ${target.name}`} checked={target.enabled} onCheckedChange={(value) => updateTarget(index, { enabled: value === true })} />启用</label>
                <div className="flex size-8 items-center justify-center overflow-hidden rounded-md border bg-card"><SiteIcon url={target.url} /></div>
                <Field label="标识"><Input value={target.id} maxLength={32} onChange={(event) => updateTarget(index, { id: event.target.value })} /></Field>
                <div className="grid gap-3 sm:grid-cols-[140px_minmax(220px,1fr)]"><Field label="名称"><Input value={target.name} maxLength={40} onChange={(event) => updateTarget(index, { name: event.target.value })} /></Field><Field label="测试 URL"><Input value={target.url} onChange={(event) => updateTarget(index, { url: event.target.value })} /></Field></div>
                <div className="flex h-8 items-center"><SiteResultBadge result={siteResults[target.id]} /></div>
                <Button variant="ghost" size="icon" title="删除站点" aria-label={`删除 ${target.name}`} onClick={() => setSettings({ ...settings, quality: { ...settings.quality, health_targets: settings.quality.health_targets.filter((_, targetIndex) => targetIndex !== index) } })}><Trash2 /></Button>
              </div>)}
            </div>
          </SettingsPanel>
        </TabsContent>

        <TabsContent value="dns">
          <SettingsPanel icon={Globe2} title="DNS" description="配置由 Mihomo 数据面读取，保存前会经过完整配置校验。">
            <div className="mb-5 flex flex-wrap gap-5"><Toggle label="启用 Mihomo DNS" checked={settings.dns.enabled} onChange={(enabled) => setSettings({ ...settings, dns: { ...settings.dns, enabled } })} /><Toggle label="IPv6 解析" checked={settings.dns.ipv6} onChange={(ipv6) => setSettings({ ...settings, dns: { ...settings.dns, ipv6 } })} /><Field label="增强模式"><Select value={settings.dns.enhanced_mode} onValueChange={(value) => setSettings({ ...settings, dns: { ...settings.dns, enhanced_mode: value as GlobalSettings["dns"]["enhanced_mode"] } })}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="normal">Normal</SelectItem><SelectItem value="fake-ip">Fake IP</SelectItem><SelectItem value="redir-host">Redir Host</SelectItem></SelectContent></Select></Field></div>
            <div className="grid gap-4 lg:grid-cols-3"><ListField label="Bootstrap DNS" values={settings.dns.default_nameserver} onChange={(default_nameserver) => setSettings({ ...settings, dns: { ...settings.dns, default_nameserver } })} /><ListField label="主 DNS" values={settings.dns.nameserver} onChange={(nameserver) => setSettings({ ...settings, dns: { ...settings.dns, nameserver } })} /><ListField label="Fallback DNS" values={settings.dns.fallback} onChange={(fallback) => setSettings({ ...settings, dns: { ...settings.dns, fallback } })} /></div>
          </SettingsPanel>
        </TabsContent>

        <TabsContent value="runtime">
          <NetworkSettings settings={settings} onChange={setSettings} />
          <SettingsPanel icon={Settings2} title="性能与运行" description="这些选项会影响 Mihomo 的连接与日志行为。">
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
              <Toggle label="共享入口（推荐）" checked={settings.shared_inbound.mode === "shared"} onChange={(enabled) => setSettings({ ...settings, shared_inbound: { ...settings.shared_inbound, mode: enabled ? "shared" : "per_service", mixed_port: settings.shared_inbound.mixed_port || 7890, ws_port: settings.shared_inbound.ws_port || 7891, mixed_bind_address: settings.shared_inbound.mixed_bind_address || "0.0.0.0" } })} />
              <Field label="共享 Mixed 端口"><output className="block rounded-md border bg-muted px-3 py-2 font-mono text-xs">{settings.shared_inbound.mixed_port ?? 7890}（固定）</output></Field>
              <Field label="共享 WS 内部端口"><output className="block rounded-md border bg-muted px-3 py-2 font-mono text-xs">{settings.shared_inbound.ws_port ?? 7891}（固定）</output></Field>
              <Field label="共享 Mixed 绑定 IP"><Input disabled={settings.shared_inbound.mode !== "shared"} value={settings.shared_inbound.mixed_bind_address ?? "0.0.0.0"} onChange={(event) => setSettings({ ...settings, shared_inbound: { ...settings.shared_inbound, mixed_bind_address: event.target.value } })} /></Field>
              <Field label="共享 WS 公网域名（反代）"><Input disabled={settings.shared_inbound.mode !== "shared"} placeholder="ws.example.com" value={settings.shared_inbound.ws_public_host ?? ""} onChange={(event) => setSettings({ ...settings, shared_inbound: { ...settings.shared_inbound, ws_public_host: event.target.value } })} /></Field>
              <Field label="共享 WS 公网端口"><output className="block rounded-md border bg-muted px-3 py-2 font-mono text-xs">{settings.shared_inbound.ws_public_port ?? 443}（固定）</output></Field>
              <Field label="共享 Mixed 公网主机名 / IP（可选）"><Input disabled={settings.shared_inbound.mode !== "shared"} placeholder="VPS 公网地址" value={settings.shared_inbound.mixed_public_host ?? ""} onChange={(event) => setSettings({ ...settings, shared_inbound: { ...settings.shared_inbound, mixed_public_host: event.target.value } })} /></Field>
              <Field label="共享 Mixed 公网端口"><output className="block rounded-md border bg-muted px-3 py-2 font-mono text-xs">{settings.shared_inbound.mixed_public_port ?? settings.shared_inbound.mixed_port ?? 7890}（固定）</output></Field>
            </div>
            <p className="mt-3 text-xs leading-5 text-muted-foreground">共享入口开启后，所有 HTTP / SOCKS5 / Mixed 服务复用同一个端口，所有 VLESS / VMess / Trojan 服务复用同一个内部端口，全部由 Mihomo 按用户名（IN-USER）分流。这样只需要 1 个客户端进程、2 个 URL 就能同时使用所有代理组与协议，不再需要为每个组单独绑定端口。端口由部署配置固定管理，页面仅展示；WebSocket 经固定公网入口和 URL Path 路由，HTTP / SOCKS 按认证用户名分流。</p>
            <div className="mt-5 rounded-md border p-3">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="max-w-2xl">
                  <div className="text-sm font-semibold">禁止 API 新增服务组</div>
                  <p className="mt-1 text-xs leading-5 text-muted-foreground">开启后，使用 API Key 调用 <span className="font-mono">POST /api/v1/proxy-groups</span>、<span className="font-mono">/proxy-services</span>、<span className="font-mono">/quickstart</span> 一律返回 403，只能在本页面手动新建。浏览器登录会话不受影响，编辑与删除现有对象也不受影响。这样脚本无法再把「一个节点」包装成一个服务组；要让某个服务走指定节点，请在节点上直接指定，不要为它建组。</p>
                </div>
                {/* 只有在这里点过开关才会写入该字段; 没点过就保持 undefined, 保存时后端按"未提供"处理而不会重置。 */}
                <Toggle label="禁止 API 新增服务组" checked={settings.guard?.deny_api_group_create ?? false} onChange={(deny_api_group_create) => setSettings({ ...settings, guard: { ...settings.guard, deny_api_group_create } })} />
              </div>
            </div>
            <div className="mt-5 grid gap-4 sm:grid-cols-2 lg:grid-cols-4"><Toggle label="TCP 并发建连" checked={settings.performance.tcp_concurrent} onChange={(tcp_concurrent) => setSettings({ ...settings, performance: { ...settings.performance, tcp_concurrent } })} /><Toggle label="统一延迟计算" checked={settings.performance.unified_delay} onChange={(unified_delay) => setSettings({ ...settings, performance: { ...settings.performance, unified_delay } })} /><Field label="Keepalive 空闲（秒）"><Input type="number" min={0} max={600} value={settings.performance.keep_alive_idle_seconds} onChange={(event) => setSettings({ ...settings, performance: { ...settings.performance, keep_alive_idle_seconds: Number(event.target.value) } })} /></Field><Field label="Keepalive 间隔（秒）"><Input type="number" min={0} max={600} value={settings.performance.keep_alive_interval_seconds} onChange={(event) => setSettings({ ...settings, performance: { ...settings.performance, keep_alive_interval_seconds: Number(event.target.value) } })} /></Field><Field label="进程识别"><Select value={settings.performance.find_process_mode} onValueChange={(value) => setSettings({ ...settings, performance: { ...settings.performance, find_process_mode: value as GlobalSettings["performance"]["find_process_mode"] } })}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent><SelectItem value="off">关闭</SelectItem><SelectItem value="strict">严格</SelectItem><SelectItem value="always">始终</SelectItem></SelectContent></Select></Field><Field label="数据面日志"><Select value={settings.performance.log_level} onValueChange={(value) => setSettings({ ...settings, performance: { ...settings.performance, log_level: value as GlobalSettings["performance"]["log_level"] } })}><SelectTrigger><SelectValue /></SelectTrigger><SelectContent>{["silent", "error", "warning", "info", "debug"].map((value) => <SelectItem key={value} value={value}>{value}</SelectItem>)}</SelectContent></Select></Field><Field label="日志保留（天）"><Input type="number" min={0} max={3650} value={settings.log_retention_days ?? 30} onChange={(event) => setSettings({ ...settings, log_retention_days: Number(event.target.value) })} /></Field></div>
            {/* Retention covers the files the product writes itself: the managed
                Mihomo log and its byte-rotated backups, plus the stale short
                runtime instance directories under ~/.hx-pxy-runtime. 0 disables
                the cleanup; a nightly pass deletes what is older than the
                window. */}
            <p className="mt-3 text-xs leading-5 text-muted-foreground">日志保留只清理产品自己写下的文件（受管 Mihomo 的 <span className="font-mono">mihomo.log</span> 轮转日志与 <span className="font-mono">~/.hx-pxy-runtime</span> 下的陈旧实例目录），判定按最后修改时间，过期即删；设置为 0 表示关闭清理。数据库、快照、artifacts 与 active.yaml 永不在清理范围内。</p>
          </SettingsPanel>
        </TabsContent>

          <TabsContent value="account">
            <SettingsPanel icon={UserRound} title="管理员账号" description="修改账号或密码都会注销全部现有会话。" action={<Button variant="outline" size="sm" onClick={() => void logoutAll()}><LogOut />注销所有会话</Button>}>
              <div className="divide-y rounded-md border">
                <div className="grid gap-3 p-4 lg:grid-cols-[minmax(180px,0.6fr)_minmax(220px,1fr)_minmax(220px,1fr)_auto] lg:items-end"><div><div className="text-sm font-semibold">登录账号</div><div className="mt-1 text-xs text-muted-foreground">当前账号：{username || "管理员"}</div></div><Field label="新账号"><Input aria-label="新管理员账号" autoComplete="username" value={newUsername} onChange={(event) => setNewUsername(event.target.value)} /></Field><Field label="当前密码"><Input aria-label="修改账号的当前密码" type="password" autoComplete="current-password" value={usernamePassword} onChange={(event) => setUsernamePassword(event.target.value)} /></Field><Button onClick={() => void changeUsername()} disabled={changingAccount !== null || !usernamePassword || newUsername.trim() === username}>{changingAccount === "username" ? <LoaderCircle className="animate-spin" /> : <UserRound />}修改账号</Button></div>
                <div className="grid gap-3 p-4 lg:grid-cols-[minmax(180px,0.6fr)_minmax(180px,1fr)_minmax(180px,1fr)_minmax(180px,1fr)_auto] lg:items-end"><div><div className="text-sm font-semibold">登录密码</div><div className="mt-1 text-xs text-muted-foreground">密码长度为 10 到 128 个字符</div></div><Field label="当前密码"><Input aria-label="当前密码" type="password" autoComplete="current-password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} /></Field><Field label="新密码"><Input aria-label="新密码" type="password" autoComplete="new-password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} /></Field><Field label="确认新密码"><Input aria-label="确认新密码" type="password" autoComplete="new-password" value={confirmPassword} onChange={(event) => setConfirmPassword(event.target.value)} /></Field><Button onClick={() => void changePassword()} disabled={changingAccount !== null || !currentPassword || !newPassword || !confirmPassword}>{changingAccount === "password" ? <LoaderCircle className="animate-spin" /> : <KeyRound />}修改密码</Button></div>
              </div>
              <div className="mt-4 border-t pt-4">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div><div className="text-sm font-semibold">终端 2FA</div><div className="mt-1 max-w-2xl text-xs leading-5 text-muted-foreground">浏览器终端默认开启，但必须使用验证器的一次性验证码解锁。密钥只在生成时显示，服务端以加密形式保存。</div></div>
                  <span className={cn("rounded-full border px-2 py-0.5 text-[11px]", twoFactorStatus?.enabled ? "border-success-border bg-success-muted text-success-foreground" : "border-warning-border bg-warning-muted text-warning-foreground")}>{twoFactorStatus?.enabled ? "已启用" : "未启用"}</span>
                </div>
                {!twoFactorStatus?.enabled && !twoFactorSetup && <div className="mt-3 flex flex-wrap items-center gap-3"><Button variant="outline" size="sm" onClick={() => void beginTwoFactorSetup()} disabled={twoFactorBusy !== null}>{twoFactorBusy === "setup" ? <LoaderCircle className="animate-spin" /> : <ShieldCheck />}生成 2FA 密钥</Button><span className="text-xs text-muted-foreground">支持 Google Authenticator、Authy、1Password 等 TOTP 验证器。</span></div>}
                {twoFactorSetup && <div className="mt-3 space-y-3 rounded-md border bg-muted/40 p-3"><div className="text-xs font-medium">将密钥添加到验证器</div><div className="grid gap-3 lg:grid-cols-2"><CopyField label="密钥" value={twoFactorSetup.secret} onCopy={() => void copyTwoFactorValue(twoFactorSetup.secret, "密钥")} /><CopyField label="otpauth 地址" value={twoFactorSetup.otpauth_url} onCopy={() => void copyTwoFactorValue(twoFactorSetup.otpauth_url, "otpauth 地址")} /></div><div className="flex flex-wrap items-end gap-2"><Field label="验证器当前验证码"><Input aria-label="启用 2FA 的验证码" inputMode="numeric" maxLength={6} value={twoFactorCode} onChange={(event) => setTwoFactorCode(event.target.value.replace(/\D/g, "").slice(0, 6))} /></Field><Button size="sm" onClick={() => void enableTwoFactor()} disabled={twoFactorBusy !== null}>{twoFactorBusy === "enable" ? <LoaderCircle className="animate-spin" /> : <ShieldCheck />}启用 2FA</Button></div></div>}
                {twoFactorStatus?.enabled && <div className="mt-3 flex flex-wrap items-end gap-2"><Field label="关闭前的当前验证码"><Input aria-label="关闭 2FA 的验证码" inputMode="numeric" maxLength={6} value={twoFactorCode} onChange={(event) => setTwoFactorCode(event.target.value.replace(/\D/g, "").slice(0, 6))} /></Field><Button variant="outline" size="sm" onClick={() => void disableTwoFactor()} disabled={twoFactorBusy !== null}>{twoFactorBusy === "disable" ? <LoaderCircle className="animate-spin" /> : <ShieldCheck />}关闭 2FA</Button></div>}
              </div>
              <div className="mt-4 border-t pt-4">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div><div className="text-sm font-semibold">API 密钥</div><div className="mt-1 max-w-2xl text-xs leading-5 text-muted-foreground">用于脚本或自动化调用管理 API，无需暴露账号密码。请求时携带 <code className="rounded bg-muted px-1">Authorization: Bearer &lt;密钥&gt;</code> 或 <code className="rounded bg-muted px-1">X-API-Key</code> 请求头即可。</div></div>
                </div>
                <div className="mt-3 space-y-2">
                  {apiKeys.map((key) => (
                    <div key={key.id} className="flex flex-wrap items-center gap-3 rounded-md border bg-muted/40 px-3 py-2">
                      <KeyRound className="size-4 shrink-0 text-muted-foreground" />
                      <div className="min-w-0 flex-1"><div className="text-xs font-medium">{key.name}</div><div className="text-[11px] text-muted-foreground">创建于 {new Date(key.created_at).toLocaleString()}</div></div>
                      <Button variant="ghost" size="icon" title="吊销密钥" aria-label={`吊销 ${key.name}`} disabled={revokingAPIKey !== null} onClick={() => void revokeAPIKey(key.id)}>{revokingAPIKey === key.id ? <LoaderCircle className="animate-spin" /> : <Trash2 />}</Button>
                    </div>
                  ))}
                  {apiKeys.length === 0 && <p className="text-xs text-muted-foreground">尚未创建 API 密钥。</p>}
                </div>
                {newAPIKey && <div className="mt-3 space-y-3 rounded-md border bg-muted/40 p-3"><div className="text-xs font-medium">新密钥（仅显示一次，请立即保存）</div><CopyField label="密钥" value={newAPIKey.key ?? ""} onCopy={() => void copyAPIKeyValue(newAPIKey.key ?? "")} /></div>}
                <div className="mt-3 flex flex-wrap items-end gap-2"><Field label="名称"><Input aria-label="API 密钥名称" maxLength={64} placeholder="如 ci-runner" value={apiKeyName} onChange={(event) => setApiKeyName(event.target.value)} /></Field><Button size="sm" onClick={() => void createAPIKey()} disabled={creatingAPIKey || !apiKeyName.trim()}>{creatingAPIKey ? <LoaderCircle className="animate-spin" /> : <Plus />}创建密钥</Button></div>
              </div>
            </SettingsPanel>
          </TabsContent>
      </Tabs>
    </div>
  )
}

function newTarget(targets: HealthTarget[]): HealthTarget { let suffix = targets.length + 1; while (targets.some((target) => target.id === `site-${suffix}`)) suffix++; return { id: `site-${suffix}`, name: `站点 ${suffix}`, url: "https://example.com/", enabled: false } }
function SiteResultBadge({ result }: { result?: SiteResult }) { return result ? <Badge variant={result.success === result.tested && result.tested > 0 ? "success" : "warning"}>{result.success}/{result.tested} 可访问</Badge> : <span className="text-xs text-muted-foreground">尚未测试</span> }
function SettingsPanel({ icon: Icon, title, description, action, children }: { icon: typeof Gauge; title: string; description: string; action?: React.ReactNode; children: React.ReactNode }) { return <section className="overflow-hidden rounded-lg border bg-card"><div className="flex min-h-12 flex-wrap items-center gap-3 border-b bg-muted/60 px-4 py-2"><Icon className="size-4" /><div><h2 className="text-sm font-semibold">{title}</h2><p className="mt-0.5 text-[11px] text-muted-foreground">{description}</p></div><div className="ml-auto">{action}</div></div><div className="p-4">{children}</div></section> }
function Swatch({ className, label }: { className: string; label: string }) { return <div className="flex items-center gap-2 text-xs text-muted-foreground"><span className={cn("size-4 rounded border border-black/10", className)} />{label}</div> }
function Field({ label, children }: { label: string; children: React.ReactNode }) { return <label className="block text-xs"><span className="mb-1 block font-medium">{label}</span>{children}</label> }
function Toggle({ label, checked, onChange }: { label: string; checked: boolean; onChange: (checked: boolean) => void }) { return <label className="flex h-8 items-center gap-2 text-xs font-medium"><Checkbox checked={checked} onCheckedChange={(value) => onChange(value === true)} />{label}</label> }
function ListField({ label, values, onChange }: { label: string; values: string[]; onChange: (values: string[]) => void }) { return <Field label={label}><Textarea className="min-h-28 font-mono text-xs" value={values.join("\n")} onChange={(event) => onChange(event.target.value.split("\n").map((value) => value.trim()).filter(Boolean))} /></Field> }
function CopyField({ label, value, onCopy }: { label: string; value: string; onCopy: () => void }) { return <Field label={label}><div className="flex gap-2"><Input readOnly value={value} className="min-w-0 font-mono text-xs" /><Button type="button" variant="outline" size="icon" title={`复制${label}`} aria-label={`复制${label}`} onClick={onCopy}><Copy /></Button></div></Field> }
