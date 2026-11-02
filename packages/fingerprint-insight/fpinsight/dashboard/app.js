/* fp-insight dashboard — 纯前端, 只用 server 的 /api/* 与 /ws/ui。 */
"use strict";
const $ = (s, r) => (r || document).querySelector(s);
const $$ = (s, r) => Array.from((r || document).querySelectorAll(s));
const esc = (s) => String(s == null ? "" : s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
const pad = (n, w) => String(n).padStart(w, "0");
function hhmmss(t) { const d = new Date(t * 1000); return pad(d.getHours(), 2) + ":" + pad(d.getMinutes(), 2) + ":" + pad(d.getSeconds(), 2); }
function msLabel(ms) { return ms == null ? "-" : (ms >= 1000 ? (ms / 1000).toFixed(2) + "s" : Math.round(ms * 100) / 100 + "ms"); }
function shorten(u, n) {
  if (!u) return "";
  try { const x = new URL(u); const p = x.pathname + x.search; return (x.host + p).slice(0, n || 70); }
  catch (e) { return String(u).slice(0, n || 70); }
}
async function api(path, opt) {
  const r = await fetch(path, Object.assign({ headers: { "Content-Type": "application/json" } }, opt || {}));
  if (!r.ok) throw new Error((await r.text()).slice(0, 240));
  return r.json();
}

const S = {
  sid: null, session: null, calls: [], lastId: 0, selected: null,
  filter: "", cat: "", view: "calls", counters: {}, counts: {}, live: true, tab: "live",
};

/* ---------------------------------------------------------------- 启动 */
function boot() {
  $("#launch").onclick = launch;
  $("#stop").onclick = stop;
  $("#nav").onclick = doNav;
  $("#mark").onclick = doMark;
  $("#shot").onclick = doShot;
  $("#clear").onclick = doClear;
  $("#applyFilter").onclick = () => { S.filter = $("#q").value.trim(); S.cat = $("#cat").value; S.lastId = 0; S.calls = []; reloadCalls(); };
  $("#q").addEventListener("keydown", (e) => { if (e.key === "Enter") $("#applyFilter").click(); });
  $("#cat").onchange = () => $("#applyFilter").click();
  $("#liveToggle").onchange = (e) => { S.live = e.target.checked; renderBadges(); };
  $$(".tabs button").forEach((b) => b.onclick = () => {
    S.tab = b.dataset.tab;
    $$(".tabs button").forEach((x) => x.classList.toggle("on", x === b));
    if (S.tab === "inv") loadInv();
    renderRight();
  });
  connectUI();
  refreshState();
  setInterval(() => { if (S.live) pollNew(); }, 1400);
  setInterval(refreshState, 6000);
}

function connectUI() {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  let ws;
  try { ws = new WebSocket(proto + "//" + location.host + "/ws/ui"); } catch (e) { return; }
  ws.onmessage = (ev) => {
    let m; try { m = JSON.parse(ev.data); } catch (e) { return; }
    if (m.kind === "ev" && S.live) {
      (m.payload || []).forEach((e) => { if (e.kind !== "meta") appendCall(e); });
    } else if (m.kind === "inv") {
      renderInvBadge(m.payload || []);
      if (S.tab === "inv") loadInv();
    } else if (m.kind === "counts" && m.payload) {
      // 精确计数是**绝对快照**, 取 MAX 合并即幂等 —— 与后端同一条判据。
      for (const k of Object.keys(m.payload)) {
        if (k.indexOf("env:") === 0 || k.indexOf("store:") === 0) continue;
        S.counts[k] = Math.max(S.counts[k] || 0, m.payload[k] || 0);
      }
      renderApis();
    } else if (m.kind === "snap" && m.payload) {
      $("#snapUrl").textContent = shorten(m.payload.url, 40);
      $("#snapTz").textContent = m.payload.tz || "-";
      $("#snapTotal").textContent = m.payload.total != null ? m.payload.total : "-";
      if (m.payload.tz) $("#curTz").textContent = m.payload.tz;
    } else if (m.kind === "hello" && m.payload && m.payload.url && m.payload.url !== "about:blank") {
      $("#curUrl").textContent = shorten(m.payload.url, 60);
    } else if (m.kind === "meta" && m.payload && m.payload.type === "probe_disconnected") {
      $("#liveDot").className = "dot";
    }
  };
  ws.onclose = () => setTimeout(connectUI, 1500);
}

async function refreshState() {
  try {
    const s = await api("/api/state");
    $("#hooked").textContent = (s.sessions && s.sessions.length) ? String(s.sessions.length) : "0";
    if (s.sessions && s.sessions.length && !S.sid) {
      S.sid = s.sessions[0].sid; S.session = s.sessions[0];
      afterAttach();
    }
    if (S.sid && s.sessions) {
      const cur = s.sessions.find((x) => x.sid === S.sid);
      if (cur) { S.session = cur; $("#curUrl").textContent = shorten(cur.url, 60); }
    }
    $("#hubStats").textContent = s.stats ? (s.stats.calls + s.stats.snaps) : "0";
  } catch (e) { /* 服务未起 */ }
}

/* ---------------------------------------------------------------- 动作 */
async function launch() {
  const body = {
    target: $("#url").value.trim(),
    label: $("#label").value.trim(),
    headless: $("#headless").checked,
    timezone: $("#tz").value.trim() || null,
    proxy: $("#proxy").value.trim() || null,
    width: parseInt($("#w").value || "1440", 10),
    height: parseInt($("#h").value || "900", 10),
    wait_ms: 3200,
  };
  btn($("#launch"), true, "启动中…");
  try {
    const r = await api("/api/sessions", { method: "POST", body: JSON.stringify(body) });
    S.sid = r.session.sid; S.session = r.session;
    S.calls = []; S.lastId = 0;
    afterAttach();
    setStatus("会话 " + S.sid + " 已启动", "ok");
  } catch (e) { setStatus("启动失败: " + e.message, "bad"); }
  btn($("#launch"), false, "开始观测");
}
async function stop() {
  if (!S.sid) return;
  try { await api("/api/sessions/" + S.sid, { method: "DELETE" }); setStatus("会话已关闭 (证据已落库)", "ok"); }
  catch (e) { setStatus("关闭失败: " + e.message, "bad"); }
  S.sid = null;
}
async function doNav() {
  if (!S.sid) return setStatus("先启动会话", "bad");
  setStatus("导航中…", "");
  try {
    const r = await api("/api/sessions/" + S.sid + "/navigate", { method: "POST", body: JSON.stringify({ url: $("#navUrl").value.trim(), wait_ms: 3000 }) });
    setStatus("已导航 -> " + shorten(r.url, 60), "ok");
    setTimeout(() => { S.lastId = 0; S.calls = []; reloadCalls(); }, 1200);
  } catch (e) { setStatus("导航失败: " + e.message, "bad"); }
}
async function doMark() {
  if (!S.sid) return setStatus("先启动会话", "bad");
  const label = $("#markLabel").value || "mark";
  try {
    const r = await api("/api/sessions/" + S.sid + "/mark", { method: "POST", body: JSON.stringify({ label, note: "dashboard 手动标记" }) });
    setStatus("已打点 " + r.label + " (API 计数 " + r.counter_paths + " 项)", "ok");
    pushMoment(label);
  } catch (e) { setStatus("打点失败: " + e.message, "bad"); }
}
async function doShot() {
  if (!S.sid) return;
  try {
    const r = await api("/api/sessions/" + S.sid + "/shot", { method: "POST" });
    $("#shotBox").innerHTML = '<img class="shot" src="' + esc(r.url) + "?t=" + Date.now() + '">';
    setStatus("截图已取", "ok");
  } catch (e) { setStatus("截图失败: " + e.message, "bad"); }
}
async function doClear() {
  if (!S.sid) return;
  try {
    await api("/api/sessions/" + S.sid + "/clear", { method: "POST", body: JSON.stringify({ what: $("#clearWhat").value }) });
    setStatus("已清 " + $("#clearWhat").value + " —— 可用来观察采集是否随存储重建而变", "ok");
  } catch (e) { setStatus("清理失败: " + e.message, "bad"); }
}
function pushMoment(label) {
  const el = document.createElement("div");
  el.className = "iv";
  el.innerHTML = '<div class="k"><span class="path">' + esc(label) + '</span><span class="cnt">' + hhmmss(Date.now() / 1000) + "</span></div>";
  $("#moments").prepend(el);
}

function btn(el, busy, text) { el.disabled = busy; el.textContent = text; }
function setStatus(msg, kind) {
  const el = $("#status");
  el.textContent = msg;
  el.style.color = kind === "bad" ? "var(--rs)" : kind === "ok" ? "var(--cy)" : "var(--dim)";
}
function afterAttach() {
  $("#curSid").textContent = S.sid || "-";
  $("#stop").disabled = false;
  $("#nav").disabled = false; $("#mark").disabled = false;
  $("#shot").disabled = false; $("#clear").disabled = false;
  $("#navUrl").value = ($("#url").value || "").trim();
  S.lastId = 0; S.calls = []; S.counts = {};
  reloadCalls(); loadInv(); renderApis();
  const t = $("#tz").value.trim();
  if (t) $("#curTz").textContent = t;
}

/* ---------------------------------------------------------------- 事件流 */
function appendCall(e) {
  S.calls.push(e);
  if (S.calls.length > 4000) S.calls = S.calls.slice(-3000);
  const tb = $("#calls");
  const tr = document.createElement("tr");
  tr.className = "fresh";
  tr.dataset.path = e.path;
  paint(tr, e);
  tr.onclick = () => selectCall(e, tr);
  tb.appendChild(tr);
  if (tb.children.length > 900) tb.removeChild(tb.firstChild);
  setTimeout(() => tr.classList.remove("fresh"), 900);
  S.counters[e.path] = (S.counters[e.path] || 0) + 1;
  renderBadges();
}

function paint(tr, e) {
  const st = (e.stack && e.stack[0]) || e.top || {};
  tr.innerHTML =
    '<td class="t">' + hhmmss((Date.now() - 0) / 1000).slice(0, 8) + "</td>" +
    '<td class="t">' + (e.n || "") + "</td>" +
    '<td><span class="tag ' + esc(e.cat || "") + '">' + esc(e.cat || "-") + "</span></td>" +
    '<td class="a">' + esc(e.path) + "</td>" +
    '<td class="r">' + esc(fmt(e.args)) + "</td>" +
    '<td class="r">' + esc(String(e.ret == null ? "" : e.ret).slice(0, 80)) + "</td>" +
    '<td class="s" title="' + esc(st.url || "") + '">' + esc((st.fn || "") + " @ " + shorten(st.url, 46) + (st.line ? ":" + st.line : "")) + "</td>";
}
function fmt(a) {
  if (a == null) return "";
  if (typeof a === "string") return a;
  try { return JSON.stringify(a).replace(/^\[|\]$/g, "").slice(0, 90); } catch (e) { return String(a); }
}

async function pollNew() {
  if (!S.sid || S.tab !== "live") return;
  const p = new URLSearchParams({ after: String(S.lastId), limit: "400" });
  if (S.filter) p.set("q", S.filter);
  if (S.cat) p.set("cat", S.cat);
  try {
    const r = await api("/api/sessions/" + S.sid + "/calls?" + p.toString());
    (r.calls || []).forEach((c) => {
      S.lastId = Math.max(S.lastId, c.id);
      if (S.calls.some((x) => x._dbid === c.id)) return;
      let e;
      try { e = Object.assign({}, c, { args: JSON.parse(c.args || "[]"), stack: JSON.parse(c.stack || "[]"), _dbid: c.id }); }
      catch (err) { e = Object.assign({}, c, { _dbid: c.id }); }
      appendCall(e);
    });
    if (r.calls && r.calls.length) loadInv();
  } catch (e) { /* ignore */ }
}

async function reloadCalls() {
  if (!S.sid) return;
  $("#calls").innerHTML = "";
  S.counters = {};
  const p = new URLSearchParams({ after: "0", limit: "500" });
  if (S.filter) p.set("q", S.filter);
  if (S.cat) p.set("cat", S.cat);
  try {
    const r = await api("/api/sessions/" + S.sid + "/calls?" + p.toString());
    (r.calls || []).forEach((c) => {
      let e;
      try { e = Object.assign({}, c, { args: JSON.parse(c.args || "[]"), stack: JSON.parse(c.stack || "[]"), _dbid: c.id }); }
      catch (err) { e = Object.assign({}, c, { _dbid: c.id }); }
      S.lastId = Math.max(S.lastId, c.id);
      S.calls.push(e);
      S.counters[e.path] = (S.counters[e.path] || 0) + 1;
      const tr = document.createElement("tr");
      paint(tr, e);
      tr.onclick = () => selectCall(e, tr);
      $("#calls").appendChild(tr);
    });
    renderBadges();
    // 库里已有记录的会话 (刷新面板后重新接管) 也要能补出精确计数
    try {
      const s = await api("/api/sessions/" + S.sid + "/summary");
      (s.apis || []).forEach((a) => {
        const k = a.path + "|" + a.surface;
        S.counts[k] = Math.max(S.counts[k] || 0, a.reads_precise || 0);
      });
      renderApis();
    } catch (err) { /* ignore */ }
  } catch (e) { /* ignore */ }
}

function renderBadges() {
  $("#evCount").textContent = String(S.calls.length);
  const live = S.sid && S.live;
  $("#liveDot").className = "dot" + (live ? " live" : "");
  $("#livePill").className = "pill" + (live ? " on" : "");
}

/* 技术画像 = 精确读取次数排行。这是面板的主结论面, 必须真的渲染出来 ——
   它曾经只是个静态占位符 (测试抓到的真实缺口, 不是样式问题)。 */
function renderApis() {
  const items = Object.keys(S.counts).map((k) => {
    const i = k.lastIndexOf("|");
    return { path: i < 0 ? k : k.slice(0, i), surf: i < 0 ? "" : k.slice(i + 1), reads: S.counts[k] };
  }).filter((x) => x.reads > 0).sort((a, b) => b.reads - a.reads);
  $("#apiCount").textContent = String(items.length);
  const box = $("#apis");
  if (!items.length) {
    box.innerHTML = '<div class="empty">尚未观测到任何指纹面</div>';
    return;
  }
  const mx = items[0].reads;
  box.innerHTML = items.slice(0, 100).map((x) =>
    '<div class="api" data-path="' + esc(x.path) + '" title="' + esc(x.path + " · " + x.surf) + '">' +
      '<span class="v">' + x.reads + "</span>" +
      '<span class="b" style="width:' + Math.max(2, Math.round((x.reads / mx) * 66)) + 'px"></span>' +
      '<span class="p">' + esc(x.path) + "</span>" +
    "</div>").join("");
  box.querySelectorAll(".api").forEach((el) => {
    el.onclick = () => {
      const p = el.dataset.path || "";
      box.querySelectorAll(".api").forEach((y) => y.classList.toggle("sel", y === el));
      $("#q").value = p;
      S.filter = p; S.cat = ""; S.lastId = 0; S.calls = [];
      reloadCalls();
    };
  });
}

/* ---------------------------------------------------------------- 详情 */
function selectCall(e, tr) {
  $$("#calls tr").forEach((x) => x.classList.remove("sel"));
  if (tr) tr.classList.add("sel");
  S.selected = e;
  const st = e.stack || [];
  const top = st[0] || e.top || {};
  let html = '<div class="hd">' + esc(e.path) + "</div>";
  html += '<div class="kv">' +
    "<i>面</i><span>" + esc(e.surface || "-") + "</span>" +
    "<i>类别</i><span>" + esc(e.cat || "-") + "</span>" +
    "<i>次数</i><span>" + esc(e.n == null ? "-" : e.n) + "</span>" +
    "<i>耗时</i><span>" + esc(msLabel(e.ms)) + "</span>" +
    "<i>参数</i><span>" + esc(fmt(e.args)) + "</span>" +
    "<i>返回</i><span>" + esc(String(e.ret == null ? "" : e.ret).slice(0, 400)) + "</span>" +
    "</div>";
  html += '<div class="kv"><i>调用栈</i><span>' + (st.length ? "" : "无 (无页面帧, 可能是浏览器内部调用)") + "</span></div>";
  html += '<div class="stack">' + st.map((f, i) =>
    '<div class="fr"><span class="i">' + i + '</span><div><div class="fx">' + esc(f.fn || "(anon)") + "</div>" +
    '<div class="fu" title="' + esc(f.url) + '">' + esc(shorten(f.url, 52)) + (f.line ? ":" + f.line : "") + "</div></div></div>"
  ).join("") + "</div>";
  if (top && top.url) {
    html += '<div class="hint">采集发生在 <b>' + esc(shorten(top.url, 60)) + ":" + esc(top.line) +
      "</b> 的 <b>" + esc(top.fn) + "</b> 里 —— 分析时从这一行往回找。</div>";
  }
  $("#detail").innerHTML = html;
}

/* ---------------------------------------------------------------- 纠察 */
let INV_STABLE = [];
function loadInv() {
  if (!S.sid) return;
  api("/api/sessions/" + S.sid + "/inv").then((r) => {
    INV_STABLE = r.stable || [];
    renderInvBadge(INV_STABLE);
    if (S.tab === "inv") renderRight();
  }).catch(() => {});
}
function renderInvBadge(rows) {
  $("#invCount").textContent = String((rows || []).length);
}

function renderRight() {
  const box = $("#invBody");
  if (!S.sid) { box.innerHTML = '<div class="empty">尚无会话</div>'; return; }
  if (S.tab === "live") {
    box.innerHTML = '<div class="hint" style="border:0">事件流在中间那栏。切到 <b>恒定量</b> 看运行期不变的数据, 切到 <b>全部</b> 看含变化的量。</div>';
    return;
  }
  api("/api/sessions/" + S.sid + "/inv").then((r) => {
    const rows = S.tab === "inv" ? r.stable : (r.stable || []).concat(r.varying || []);
    renderInvBadge(r.stable || []);
    if (!rows.length) { box.innerHTML = '<div class="empty">暂无数据</div>'; return; }
    box.innerHTML = rows.map((x) => {
      let vals = [];
      try { vals = JSON.parse(x.vals || "[]"); } catch (e) {}
      const multi = vals.length > 1;
      return '<div class="iv' + (x.stable ? " stable" : "") + '" title="' + esc(x.path) + '">' +
        '<div class="k"><span class="path">' + esc(x.path) + '</span>' +
        '<span class="cnt">n=' + x.n + (x.stable ? " 恒定" : " " + x.distinct + "变体") + "</span></div>" +
        (multi
          ? '<div class="multi">' + vals.slice(0, 8).map((v) => "<span>" + esc(String(v).slice(0, 40)) + "</span>").join("") + "</div>"
          : '<div class="val">' + esc(String(x.last == null ? x.first : x.last).slice(0, 110)) + "</div>") +
        "</div>";
    }).join("");
  }).catch((e) => { box.innerHTML = '<div class="empty">' + esc(e.message) + "</div>"; });
}

document.addEventListener("DOMContentLoaded", boot);
