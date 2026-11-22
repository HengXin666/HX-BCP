"use strict";
/* Flow capture view: filters in the URL hash, live tail, detail with headers, body and curl. */
var F = { rows: [], offset: 0, page: 300, sel: null, tab: "overview", body: null, timer: null };

function fq(){
  var p = [], v;
  [["host", "fHost"], ["status", "fStatus"], ["q", "fQ"], ["client", "fClient"], ["kind", "fKind"], ["via", "fVia"]].forEach(function(x){
    v = $(x[1]).value.trim(); if (v) p.push(x[0] + "=" + encodeURIComponent(v));
  });
  v = $("fDays").value; if (v && v !== "0") p.push("days=" + v);
  return p.join("&");
}
function syncHash(){
  var q = fq();
  history.replaceState(null, "", "#flows" + (q ? "?" + q : ""));
  $("harLink").href = "flows.har" + (q ? "?" + q : "");
}
function fromHash(){
  var i = location.hash.indexOf("?"), sp = new URLSearchParams(i >= 0 ? location.hash.slice(i + 1) : "");
  [["host", "fHost"], ["status", "fStatus"], ["q", "fQ"], ["client", "fClient"], ["kind", "fKind"], ["via", "fVia"], ["days", "fDays"]].forEach(function(x){
    var v = sp.get(x[0]); if (v != null) $(x[1]).value = v;
  });
}
function stCls(s){ s = Number(s || 0); return !s ? "s0" : s < 300 ? "s2" : s < 400 ? "s3" : s < 500 ? "s4" : "s5"; }
function verdictBadge(r){
  var v = String(r.verdict || r.kind || "-");
  var cls = r.kind === "hit" ? "hit" : r.kind === "tunnel" ? "tun" : v.indexOf("fill") === 0 ? "fill"
    : /sensitive|no_store|set_cookie|vary|error/.test(v) ? "sens" : "";
  return '<span class="badge ' + cls + '">' + txt(v.length > 34 ? v.slice(0, 34) + "..." : v) + "</span>";
}
function tlsBadge(r){
  var t = r.tls || (r.via === "cdp" || !r.via ? "cdp" : "");
  var m = { passthrough: ["隧道", "tun"], decrypted: ["解密", "dec"], plain: ["明文", ""], cdp: ["原生", "hit"] }[t] || [t || "-", ""];
  return '<span class="badge ' + m[1] + '" title="' + txt(t) + '">' + m[0] + "</span>";
}
function sizeOf(r){
  var n = Number(r.size || 0) || Number(r.encoded || 0);
  if (n) return human(n);
  if (r.post_data) return "↑" + human(r.post_data.length);
  return "-";
}
function splitUrl(u){
  try { var x = new URL(u); return [x.host, x.pathname + x.search]; } catch (e) { return ["", u]; }
}

function renderSpark(rows){
  var el = $("spark"); el.innerHTML = "";
  if (!rows.length){ $("tMin").textContent = ""; $("tMax").textContent = ""; return; }
  var ts = rows.map(function(r){ return Number(r.at || 0); });
  var lo = Math.min.apply(null, ts), hi = Math.max.apply(null, ts), span = Math.max(hi - lo, 1), N = 140;
  var b = [], e = [], i;
  for (i = 0; i < N; i++){ b.push(0); e.push(0); }
  rows.forEach(function(r){
    var k = Math.min(N - 1, Math.floor((Number(r.at || 0) - lo) / span * N));
    b[k]++; if (Number(r.status || 0) >= 400 || String(r.verdict || "").indexOf("error") === 0) e[k]++;
  });
  var mx = Math.max.apply(null, b) || 1;
  for (i = 0; i < N; i++){
    var it = document.createElement("i");
    it.style.height = Math.max(1, Math.round(b[i] / mx * 30)) + "px";
    if (e[i]) it.className = "err";
    it.title = b[i] + " 个请求";
    el.appendChild(it);
  }
  $("tMin").textContent = clock(lo);
  $("tMax").textContent = clock(hi) + " · 跨度 " + Math.round(span) + " 秒";
}

function renderRows(){
  var tb = $("rows");
  if (!F.rows.length){
    tb.innerHTML = '<tr><td colspan="10" class="empty">没有匹配的请求. ' +
      (fq() ? "当前过滤: " + txt(fq()) + ". 试试放宽范围." : "让浏览器走代理, 或给 CDP 档加 --record-flows.") + "</td></tr>";
    return;
  }
  tb.innerHTML = "";
  F.rows.forEach(function(r, i){
    var tr = document.createElement("tr"), u = splitUrl(r.url);
    if (F.sel === r) tr.className = "sel";
    tr.innerHTML = '<td class="dim">' + (i + 1) + "</td><td>" + clock(r.at) + "</td><td>" + txt(r.client || "-") + "</td><td>" +
      txt(r.method || "GET") + '</td><td class="url"><div>' + txt(u[0]) + '</div><div class="sub">' + txt(u[1]) + "</div></td>" +
      '<td class="' + stCls(r.status) + '">' + (r.status || "-") + "</td><td class=\"nw\">" + sizeOf(r) + "</td><td class=\"nw\">" +
      (r.ms != null ? r.ms + " ms" : "-") + "</td><td>" + tlsBadge(r) + "</td><td>" + verdictBadge(r) + "</td>";
    tr.onclick = function(){ F.sel = r; F.tab = "overview"; F.body = null; renderRows(); renderDetail(); };
    tb.appendChild(tr);
  });
}

function hdTable(obj, hot){
  var keys = Object.keys(obj || {});
  if (!keys.length) return '<div class="empty">无</div>';
  return '<div class="hd">' + keys.map(function(k){
    return '<div class="k' + (hot.indexOf(k.toLowerCase()) >= 0 ? " hot" : "") + '">' + txt(k) + '</div><div class="v">' + txt(obj[k]) + "</div>";
  }).join("") + "</div>";
}
function shq(s){ return "'" + String(s).replace(/'/g, "'\\''") + "'"; }
function asCurl(r){
  var parts = ["curl", "-X", r.method || "GET", shq(r.url)];
  Object.keys(r.req_headers || {}).forEach(function(k){
    var lk = k.toLowerCase();
    if (lk === "host" || lk === "content-length" || lk.indexOf("proxy-") === 0 || lk.charAt(0) === ":") return;
    parts.push("-H", shq(k + ": " + r.req_headers[k]));
  });
  if (r.post_data) parts.push("--data-raw", shq(r.post_data));
  return parts.join(" ");
}
function asFetch(r){
  var h = {};
  Object.keys(r.req_headers || {}).forEach(function(k){
    var lk = k.toLowerCase();
    if (lk === "host" || lk === "content-length" || lk.indexOf("proxy-") === 0 || lk.charAt(0) === ":" || lk === "cookie") return;
    h[k] = r.req_headers[k];
  });
  var o = { method: r.method || "GET", headers: h };
  if (r.post_data) o.body = r.post_data;
  return "fetch(" + JSON.stringify(r.url) + ", " + JSON.stringify(o, null, 2) + ");";
}

function renderDetail(){
  var d = $("detail"), r = F.sel;
  if (!r){ d.innerHTML = '<div class="empty">选一条请求看头与体</div>'; return; }
  var h = "<h2>" + txt(r.method || "GET") + " " + txt(r.url) + "</h2>";
  h += '<dl class="kv"><dt>时间</dt><dd>' + new Date(Number(r.at || 0) * 1000).toLocaleString() + "</dd>" +
    "<dt>客户端</dt><dd>" + txt(r.client || "-") + " · " + txt(r.via || "cdp") + " 档</dd>" +
    "<dt>状态 / 类型</dt><dd>" + (r.status || "-") + " · " + txt(r.type || r.mime || "-") + "</dd>" +
    "<dt>判定</dt><dd>" + txt(r.verdict || r.kind || "-") + "</dd>" +
    "<dt>TLS</dt><dd>" + tlsBadge(r) + (r.alpn ? " ALPN " + txt(r.alpn.join(",")) : "") + (r.http_version ? " " + txt(r.http_version) : "") + "</dd>" +
    "<dt>大小 / 耗时</dt><dd>" + sizeOf(r) + " · " + (r.ms != null ? r.ms + " ms" : "-") + "</dd></dl>";
  if (r.kind === "tunnel")
    h += '<div class="note">这是一条隧道连接: 浏览器自己与站点握手, 代理只转发字节, 看不到路径与内容. 想缓存或看明文, 去 主机与 TLS 页把这个 host 切到 解密.</div>';
  var ref = r.body_ref;
  if (r.post_data && String(r.post_data).indexOf("...[truncated]") >= 0)
    h += '<div class="warn">请求体是 8K 摘要. ' + (ref && ref.sha256 ? "完整体在取证池, 可在 请求体 页取回." : "这条没有完整体: 当时未开 --forensic.") + "</div>";
  h += '<div class="actions"><button id="dCurl">复制 curl</button><button id="dFetch">复制 fetch</button><button id="dUrl">复制 URL</button>' +
    '<button id="dHost">只看这个 host</button>' + (r.client ? '<button id="dClient">只看这个客户端</button>' : "") + "</div>";
  h += '<div class="tabs">' + [["overview", "概览"], ["req", "请求头"], ["resp", "响应头"], ["body", "请求体"], ["raw", "原始 JSON"]].map(function(t){
    return '<button data-tab="' + t[0] + '" class="' + (F.tab === t[0] ? "on" : "") + '">' + t[1] + "</button>";
  }).join("") + '</div><div id="dPane"></div>';
  d.innerHTML = h;
  Array.prototype.forEach.call(d.querySelectorAll(".tabs button"), function(b){
    b.onclick = function(){ F.tab = b.getAttribute("data-tab"); renderDetail(); };
  });
  $("dCurl").onclick = function(){ copy(asCurl(r)); };
  $("dFetch").onclick = function(){ copy(asFetch(r)); };
  $("dUrl").onclick = function(){ copy(r.url); };
  $("dHost").onclick = function(){ $("fHost").value = splitUrl(r.url)[0].split(":")[0]; loadFlows(true); };
  if ($("dClient")) $("dClient").onclick = function(){ setClient(r.client); loadFlows(true); };
  var p = $("dPane");
  if (F.tab === "req") p.innerHTML = hdTable(r.req_headers, ["cookie", "authorization"]);
  else if (F.tab === "resp") p.innerHTML = hdTable(r.resp_headers, ["set-cookie"]);
  else if (F.tab === "raw") p.innerHTML = "<pre>" + txt(JSON.stringify(r, null, 2)) + "</pre>";
  else if (F.tab === "body") p.innerHTML = bodyView(r);
  else p.innerHTML = "<pre>" + txt(asCurl(r)) + "</pre>";
  if ($("bLoad")) $("bLoad").onclick = function(){ loadBody(0); };
  if ($("bMore")) $("bMore").onclick = function(){ loadBody(F.body.offset + F.body.returned); };
}
function bodyView(r){
  var h = "";
  if (r.body_ref && r.body_ref.sha256)
    h += '<div class="note">完整体 ' + human(r.body_ref.size) + ' 在取证池 <button id="bLoad">取回</button>' +
      (F.body && !F.body.eof ? ' <button id="bMore">续读</button>' : "") + "</div>";
  if (F.body) return h + "<pre>" + txt(F.body.text) + "</pre>";
  return h + (r.post_data ? "<pre>" + txt(r.post_data) + "</pre>" : '<div class="empty">这条请求没有请求体</div>');
}
function loadBody(off){
  var r = F.sel;
  api("/flow/body?sha256=" + encodeURIComponent(r.body_ref.sha256) + "&offset=" + off + "&limit=131072").then(function(b){
    if (off && F.body){ F.body.text += b.text; F.body.returned += b.returned; F.body.eof = b.eof; }
    else F.body = { text: b.text, returned: b.returned, eof: b.eof, offset: b.offset };
    renderDetail();
  }).catch(function(e){ toast("取回失败: " + e.message); });
}

function setClient(name){
  var sel = $("fClient");
  if (name && !Array.prototype.some.call(sel.options, function(o){ return o.value === name; })){
    var o = document.createElement("option"); o.value = name; o.textContent = name; sel.appendChild(o);
  }
  sel.value = name || "";
}
function loadClients(){
  return api("/clients").then(function(d){
    var cur = $("fClient").value;
    (d.clients || []).forEach(function(c){ setClient(c.client); });
    $("fClient").value = cur;
  }).catch(function(){});
}

function loadFlows(reset){
  if (reset){ F.offset = 0; F.rows = []; F.sel = null; }
  syncHash();
  var q = fq();
  return api("/flows?limit=" + F.page + "&offset=" + F.offset + (q ? "&" + q : "")).then(function(d){
    var rows = d.flows || [];
    F.rows = F.rows.concat(rows);
    $("btnMore").className = rows.length >= F.page ? "" : "hidden";
    $("listStat").textContent = "已载 " + F.rows.length + " 条";
    renderSpark(F.rows); renderRows(); renderDetail();
  });
}
function tail(){
  if (!F.rows.length) return loadFlows(true);
  var q = fq(), since = Number(F.rows[0].at || 0);
  return api("/flows?limit=500&since=" + since + (q ? "&" + q : "")).then(function(d){
    var fresh = d.flows || [];
    if (!fresh.length) return;
    F.rows = fresh.concat(F.rows).slice(0, 3000);
    $("listStat").textContent = "已载 " + F.rows.length + " 条 · 实时";
    renderSpark(F.rows); renderRows();
  });
}

HX.views.flows = {
  init: function(){
    fromHash();
    $("btnReload").onclick = function(){ loadFlows(true); };
    $("btnMore").onclick = function(){ F.offset += F.page; loadFlows(false); };
    ["fHost", "fStatus", "fQ"].forEach(function(id){
      $(id).addEventListener("keydown", function(e){ if (e.key === "Enter") loadFlows(true); });
    });
    ["fClient", "fKind", "fVia", "fDays"].forEach(function(id){ $(id).onchange = function(){ loadFlows(true); }; });
    $("live").onchange = function(){
      clearInterval(F.timer); F.timer = null;
      if ($("live").checked) F.timer = setInterval(function(){ if (HX.current === "flows") tail(); }, 2000);
    };
  },
  load: function(){
    return loadClients().then(function(){
      if (HX.pendingClient){ setClient(HX.pendingClient); HX.pendingClient = ""; }
      return loadFlows(true);
    });
  }
};
