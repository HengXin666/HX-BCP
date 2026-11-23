import assert from "node:assert/strict"
import { chromium } from "playwright"

const base = process.env.HX_UI_BASE_URL
if (!base) throw new Error("HX_UI_BASE_URL required; all API requests are mocked")
const browser = await chromium.launch({ executablePath: process.env.HX_UI_CHROMIUM || "/usr/bin/chromium" })
try {
for (const width of [1440, 390]) {
 const page = await browser.newPage({ viewport: { width, height: 950 } })
 await page.addInitScript(() => { Object.defineProperty(navigator, "clipboard", { value: {writeText: async value => { window.__copied = value }} }) })
 const errors = []
 page.on("pageerror", error => errors.push(error.stack))
 let releaseNodes
 const gate = new Promise(resolve => { releaseNodes = resolve })
 let nodeFinished = false
 const operations = []
 const dockerApplies = []
 let companion = false
 // Mirrors internal/dockeraccess.Assess for the fixture: a reachable shared
 // Mixed member is ready directly; a WS service needs a companion entry.
 const bridges = [{interface:"docker0",address:"172.17.0.1",subnet:"172.17.0.0/16"},{interface:"br-compose",address:"172.18.0.1",subnet:"172.18.0.0/16"}]
 const assess = gateway => {
  const gw = gateway || "172.17.0.1"
  if (companion) return {listener_id:listener.id,state:"ready",reason:"companion_entry",gateway:gw,bridges,revocable:true,client_landing:true,entry:{listener_id:"companion",kind:"mixed",share_path:"/sub/companion-token",host:gw,port:17890,schemes:["http","socks5h"],companion:true}}
  if (listener.kind === "mixed") return {listener_id:listener.id,state:"ready",reason:"direct",gateway:gw,bridges,revocable:false,client_landing:false,entry:{listener_id:listener.id,kind:"mixed",share_path:listener.share_path,host:gw,port:17890,schemes:["http","socks5h"],companion:false}}
  return {listener_id:listener.id,state:"needs_entry",reason:"websocket_protocol",gateway:gw,bridges,revocable:false,client_landing:true,plan:{id:"plan-"+gw,placement:"shared",name:listener.name+" · Docker",kind:"mixed",bind_address:"0.0.0.0",port:17890,dial_host:gw,gateway:gw,new_port:false}}
 }
 const settings = { tun: { enabled:false, proxy_group_id:"" }, quality:{check_interval_seconds:600,timeout_seconds:5,batch_size:10,probe_concurrency:2,test_url:"https://example.com",health_targets:[]},dns:{enabled:true,ipv6:false,enhanced_mode:"normal",default_nameserver:[],nameserver:["1.1.1.1"],fallback:[]}, performance:{tcp_concurrent:true, unified_delay:true,keep_alive_idle_seconds:30,keep_alive_interval_seconds:30,find_process_mode:"off",log_level:"info"},shared_inbound:{mode:"shared",mixed_bind_address:"0.0.0.0",mixed_port:17890,ws_port:7891},guard:{deny_api_group_create:false},log_retention_days:30 }
 const group = {id:"group-test",name:"Test Exit",enabled:true,version:1,strategy:"select",source_spec:{include_direct:true,node_ids:[],group_ids:[],subscription_ids:[]},empty_behavior:"direct"}
 const listener = {id:"listener-test",name:"Test Docker Entry",kind:"mixed",bind_address:"0.0.0.0",port:19999,shared_inbound:"standard",enabled:true,version:1,proxy_group_id:group.id,public_endpoint:{host:"public.example.com"},transport:{},auth_configured:true,share_path:"/sub/test-fixture-token"}
 await page.route("**/api/v1/**", async route => {
  const path = new URL(route.request().url()).pathname
  let body = {items:[]}
  if (path.endsWith("/auth/status")) body = {configured:true,authenticated:true,username:"test",csrf_token:"test"}
  else if (path.endsWith("/nodes")) { await gate; body={items:[],total:0}; nodeFinished=true }
  else if (path.endsWith("/proxy-groups")) body={items:[group]}
  else if (path.endsWith("/listeners")) body={items:[listener]}
  else if (path === "/api/v1/docker-access") body={items: companion ? [{listener_id:listener.id,entry_listener_id:"companion",bind_target:"shared",created_at:"2026-10-15T00:00:00Z"}] : []}
  else if (path.endsWith("/docker-access")) {
   const method = route.request().method()
   if (method === "POST") { const payload = route.request().postDataJSON(); dockerApplies.push(payload); companion = true; body = assess(payload.gateway) }
   else if (method === "DELETE") { companion = false; await route.fulfill({status:204,body:""}); return }
   else body = assess(new URL(route.request().url()).searchParams.get("gateway"))
  }
  else if (path.endsWith("/settings")) body=settings
  else if (path.endsWith("/routing-rules")) body={rule_sets:[]}
  else if (path.endsWith("/dataplane/status")) body={running:true,healthy:true,proxy_count:0}
  else if (path.endsWith("/system/network")) body={docker_gateway:"172.17.0.1",lan_addresses:["172.17.0.1"]}
  else if (path.endsWith("/network/inspect")) body={tun_available:true,ipv4_forwarding:false,interfaces:[{name:"docker0",kind:"docker",up:true,addresses:["172.17.0.1/16"]},{name:"other-tun",kind:"tun",up:true,addresses:["198.18.0.1/30"]}]}
  else if (path.endsWith("/network/operations")) {operations.push(route.request().postDataJSON());body={exit_code:0,stdout:"diagnostic fixture",stderr:""}}
  else if (path.endsWith("/connections")) body={total:1,sampled_at:new Date().toISOString(),items:[{id:"c",origin:"docker",source_ip:"172.17.0.2",source_port:"40000",destination:"1.1.1.1",destination_port:"443",network:"tcp",inbound:"Mixed",upload:10,download:20}]}
  await route.fulfill({status:200,contentType:"application/json",body:JSON.stringify(body)})
 })
 await page.route("**/sub/companion-token?format=uri", route => route.fulfill({status:200,contentType:"text/plain",body:"http://docker-ab12:gen-secret@public.example.com:17890/#c\nsocks5://docker-ab12:gen-secret@public.example.com:17890/#c\n"}))
 await page.route("**/sub/test-fixture-token?format=uri", route => route.fulfill({status:200,contentType:"text/plain",body:"http://fixture:fixture-password@public.example.com:17890/#test\nsocks5://fixture:fixture-password@public.example.com:17890/#test\n"}))
 await page.goto(base+"/#/routing",{waitUntil:"domcontentloaded"})
 await page.getByText("Test Docker Entry",{exact:true}).waitFor({timeout:10000}).catch(async error => { console.error(errors, await page.locator("body").innerText()); throw error })
 assert.equal(nodeFinished,false,"service rows must not wait for node inventory")
 await page.screenshot({path:`../docs/screenshots/routing-progressive-${width}.png`})
 releaseNodes()
 await page.getByRole("combobox").filter({hasText:"服务操作"}).click()
 await page.getByRole("option",{name:"Docker 使用方式",exact:true}).click()
 await page.waitForFunction(() => window.__copied === "http://fixture:fixture-password@172.17.0.1:17890")
 assert.equal(await page.getByRole("textbox",{name:"ALL_PROXY（SOCKS5，远端解析 DNS）"}).inputValue(), "socks5h://fixture:fixture-password@172.17.0.1:17890")
 await page.getByRole("dialog").getByRole("button",{name:"关闭",exact:true}).click()
 // Permission-denied / insecure-origin clipboard still leaves a usable manual copy.
 await page.evaluate(() => { navigator.clipboard.writeText = async () => { throw new Error("denied") } })
 await page.getByRole("combobox").filter({hasText:"服务操作"}).click()
 await page.getByRole("option",{name:"Docker 使用方式",exact:true}).click()
 await page.getByText(/浏览器未允许自动复制/).waitFor()
 assert.equal(await page.getByRole("textbox",{name:"HTTP_PROXY / HTTPS_PROXY"}).inputValue(), "http://fixture:fixture-password@172.17.0.1:17890")
 await page.getByRole("button",{name:"复制 HTTP URI",exact:true}).click()
 await page.getByText(/复制未获授权/).waitFor()
 await page.screenshot({path:`../docs/screenshots/docker-access-${width}.png`})
 await page.getByRole("dialog").getByRole("button",{name:"关闭",exact:true}).click()
 // Editing a shared member derives the carrier port, never the stale member port.
 await page.getByRole("combobox").filter({hasText:"服务操作"}).click()
 await page.getByRole("option",{name:"编辑服务",exact:true}).click()
 await page.getByText("实时编辑服务",{exact:true}).waitFor()
 assert.equal(await page.locator("label").filter({hasText:/^端口/}).locator("input").count(),0)
 assert.match(await page.locator("label").filter({hasText:/^端口/}).innerText(),/17890/)
 assert.equal(await page.locator("label").filter({hasText:/^公网端口/}).locator("input").count(),0)
 await page.getByRole("button",{name:"取消",exact:true}).click()
 await page.getByRole("button",{name:"新建代理服务",exact:true}).click()
 const dialog = page.getByRole("dialog")
 assert.equal(await dialog.locator("label").filter({hasText:/端口/}).locator("input").count(),0)
 await dialog.getByRole("button",{name:"住宅代理",exact:true}).click()
 await dialog.getByRole("link",{name:"配置住宅供应商账号、密码与渠道"}).waitFor()
 await page.screenshot({path:`../docs/screenshots/residential-create-${width}.png`})
 await page.keyboard.press("Escape")
 await page.getByRole("button",{name:"活跃连接 展开"}).click()
 await page.getByText("172.17.0.2:40000",{exact:true}).waitFor()
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false,"routing horizontal overflow")
 // WS gets actual client landing instructions, never a fake plain proxy URI.
 listener.kind = "vless"
 listener.bind_address = "127.0.0.1"
 listener.shared_inbound = "websocket"
 listener.transport = {type:"ws",ws_path:"/__hx-proxy__/shared"}
 await page.reload({waitUntil:"domcontentloaded"})
 await page.getByText("Test Docker Entry",{exact:true}).waitFor()
 await page.getByRole("combobox").filter({hasText:"服务操作"}).click()
 await page.getByRole("option",{name:"Docker 使用方式",exact:true}).click()
 // WS is not a dead end: a previewed companion entry, no copy button before it exists.
 await page.getByRole("list",{name:"变更预览"}).waitFor()
 assert.equal(await page.getByRole("button",{name:/^复制/}).count(),0,"no copy button before an entry is dialable")
 await page.getByText(/不新开端口/).waitFor()
 await page.screenshot({path:`../docs/screenshots/docker-ws-access-${width}.png`})
 // The client-landing path stays available as the alternative.
 await page.getByText("或者：在容器里用 Mihomo 客户端落地订阅").click()
 await page.getByText(/mixed-port: 7890/).waitFor()
 // Picking the compose bridge re-plans against that gateway.
 await page.getByRole("combobox",{name:"选择 Docker 网桥"}).click()
 await page.getByRole("option",{name:/br-compose/}).click()
 await page.getByText(/172\.18\.0\.1:17890/).first().waitFor()
 await page.getByRole("button",{name:"确认创建 Docker 入口",exact:true}).click()
 await page.waitForFunction(() => window.__copied === "http://docker-ab12:gen-secret@172.18.0.1:17890")
 assert.deepEqual(dockerApplies.at(-1),{plan_id:"plan-172.18.0.1",gateway:"172.18.0.1",confirm:true})
 await page.screenshot({path:`../docs/screenshots/docker-entry-created-${width}.png`})
 await page.getByRole("button",{name:"撤销 Docker 入口",exact:true}).click()
 await page.getByRole("button",{name:"确认创建 Docker 入口",exact:true}).waitFor()
 await page.getByRole("dialog").getByRole("button",{name:"关闭",exact:true}).click()
 await page.goto(base+"/#/settings",{waitUntil:"domcontentloaded"})
 await page.getByRole("tab",{name:"运行",exact:true}).click()
 assert.equal(await page.locator("label").filter({hasText:/端口/}).locator("input").count(),0)
 assert.equal(await page.locator("label").filter({hasText:/端口/}).locator("output").count(),4)
 const tun = page.getByRole("checkbox",{name:"启用当前环境 TUN（保存并应用后生效）"})
 assert.equal(await tun.isChecked(),false)
 await page.getByText(/other-tun/).waitFor()
 await page.getByRole("button",{name:"网络诊断",exact:true}).click()
 await page.getByText(/diagnostic fixture/).waitFor()
 assert.equal(operations[0].action,"diagnose")
 page.once("dialog",dialog=>dialog.dismiss())
 await page.getByRole("button",{name:"修复：开启 IPv4 转发",exact:true}).click()
 assert.equal(operations.length,1,"cancelled repair must not send a request")
 await page.screenshot({path:`../docs/screenshots/network-settings-${width}.png`,fullPage:true})
 assert.deepEqual(errors,[])
 await page.close()
 console.log(`network-routing-regression: ${width}px passed`)
}
} finally {await browser.close()}
