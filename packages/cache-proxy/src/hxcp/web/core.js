"use strict";
/* Shared helpers, hash router and header status for the hxcp console.
   API paths are relative to the page, so it works at / and under a proxy sub path. */
var HX = { views: {}, stats: null, current: "" };

function $(id){ return document.getElementById(id); }
function txt(s){ var d = document.createElement("div"); d.textContent = String(s == null ? "" : s); return d.innerHTML; }
function human(n){
  n = Number(n || 0);
  if (n < 1024) return n + " B";
  if (n < 1048576) return (n / 1024).toFixed(1) + " KB";
  if (n < 1073741824) return (n / 1048576).toFixed(2) + " MB";
  return (n / 1073741824).toFixed(2) + " GB";
}
function pct(x){ return (Number(x || 0) * 100).toFixed(1) + "%"; }
function z(n){ return (n < 10 ? "0" : "") + n; }
function hhmmss(t){ var d = new Date(t * 1000); return z(d.getHours()) + ":" + z(d.getMinutes()) + ":" + z(d.getSeconds()); }
function clock(t){
  var d = new Date(t * 1000), now = new Date();
  var same = d.toDateString() === now.toDateString();
  return (same ? "" : z(d.getMonth() + 1) + "-" + z(d.getDate()) + " ") + hhmmss(t);
}
function ago(t){
  var s = Math.max(0, Date.now() / 1000 - Number(t || 0));
  if (!t) return "-";
  if (s < 60) return Math.round(s) + " 秒前";
  if (s < 3600) return Math.round(s / 60) + " 分钟前";
  if (s < 86400) return Math.round(s / 3600) + " 小时前";
  return Math.round(s / 86400) + " 天前";
}
function toast(msg){
  var t = $("toast"); t.textContent = msg; t.className = "on";
  clearTimeout(toast._t); toast._t = setTimeout(function(){ t.className = ""; }, 1800);
}
function copy(text){
  if (navigator.clipboard && window.isSecureContext){
    navigator.clipboard.writeText(text).then(function(){ toast("已复制"); });
    return;
  }
  var ta = document.createElement("textarea"); ta.value = text; document.body.appendChild(ta);
  ta.select(); try { document.execCommand("copy"); toast("已复制"); } catch (e) { toast("复制失败"); }
  document.body.removeChild(ta);
}

/* Bearer token kept per tab, asked once when the API answers 401. */
function token(){ try { return sessionStorage.getItem("hxcp-token") || ""; } catch (e) { return ""; } }
function api(path, opt){
  opt = opt || {};
  var h = { "content-type": "application/json" };
  if (token()) h.authorization = "Bearer " + token();
  return fetch(path.replace(/^\//, ""), { method: opt.method || "GET", headers: h,
    body: opt.body == null ? undefined : JSON.stringify(opt.body) }).then(function(r){
    if (r.status === 401){
      var t = prompt("这个 API 需要 token (Bearer), 输入后只保存在本标签页:");
      if (t){ try { sessionStorage.setItem("hxcp-token", t.trim()); } catch (e) {} return api(path, opt); }
    }
    if (!r.ok) return r.text().then(function(b){ throw new Error("HTTP " + r.status + " " + b.slice(0, 120)); });
    return r.json();
  });
}

function bars(el, rows, opt){
  opt = opt || {};
  if (!rows.length){ el.innerHTML = '<div class="empty">' + (opt.empty || "暂无数据") + "</div>"; return; }
  var mx = Math.max.apply(null, rows.map(function(r){ return r.value; })) || 1;
  el.innerHTML = rows.map(function(r){
    return '<div class="row"><div class="lbl" title="' + txt(r.label) + '">' + txt(r.label) + "</div>" +
      '<div class="track"><div class="fill' + (opt.alt ? " alt" : "") + '" style="width:' +
      Math.max(2, Math.round(r.value / mx * 100)) + '%"></div></div><div class="num">' + txt(r.text) + "</div></div>";
  }).join("");
}

function refreshHead(){
  return api("/stats").then(function(s){
    HX.stats = s;
    var e = s.engine || {}, st = s.store || {}, on = s.engine_state === "running";
    $("pulse").className = "pulse" + (on ? " on" : "");
    $("headStat").innerHTML = (on ? "运行中 <b>" + (s.running || 1) + "</b> 个实例" : "引擎 " + txt(s.engine_state)) +
      " · 缓存 <b>" + (st.entries || 0) + "</b> 条 / " + human(st.bytes) +
      " · 累计已省 <b>" + human(st.bytes_served) + "</b>";
    return s;
  }).catch(function(){ $("headStat").textContent = "API 不可达"; });
}

/* Overview view */
HX.views.overview = {
  load: function(){
    return Promise.all([refreshHead(), api("/hosts"), api("/clients")]).then(function(r){
      var s = r[0] || {}, hosts = (r[1] || {}).hosts || [], clients = (r[2] || {}).clients || [];
      var e = s.engine || {}, st = s.store || {}, fl = s.flows || {};
      var served = Number(e.bytes_served || 0), net = Number(e.bytes_network || 0);
      var req = Number(e.hits || 0) + Number(e.misses || 0);
      var cards = [
        ["本轮已省流量", human(served), "命中直接本地应答, 不出网", "good"],
        ["本轮字节节省率", pct(e.saved_ratio), "出网 " + human(net), ""],
        ["本轮命中率", req ? pct(Number(e.hits || 0) / req) : "-", (e.hits || 0) + " / " + req + " 个可缓存请求", ""],
        ["累计已省", human(st.bytes_served), "缓存库历史命中 " + (st.hits || 0) + " 次", "good"],
        ["缓存库", (st.entries || 0) + " 条", human(st.bytes) + " · 所有浏览器共用", ""],
        ["隧道 / 解密", (e.tunnels || 0) + " / " + (e.decrypted || 0), "TLS 连接数", (e.decrypted ? "amber" : "")],
        ["flow 记录", String(fl.records != null ? fl.records : (fl.written || 0)), fl.enabled ? "记录中" : "未开启落盘", ""],
        ["错误", String(e.errors || 0), "抓取或上游错误", e.errors ? "amber" : ""],
      ];
      $("kpis").innerHTML = cards.map(function(c){
        return '<div class="card ' + c[3] + '"><div class="k">' + c[0] + '</div><div class="v">' + txt(c[1]) +
          '</div><div class="s">' + txt(c[2]) + "</div></div>";
      }).join("");
      bars($("topHosts"), hosts.filter(function(h){ return h.saved > 0; }).slice(0, 10).map(function(h){
        return { label: h.host, value: h.saved, text: human(h.saved) };
      }), { empty: "还没有命中. 第二次访问同一资源时开始省流量." });
      var rs = e.reasons || {};
      bars($("reasons"), Object.keys(rs).sort(function(a, b){ return rs[b] - rs[a]; }).slice(0, 10).map(function(k){
        return { label: k, value: rs[k], text: String(rs[k]) };
      }), { alt: true, empty: "引擎未运行" });
      $("clientList").innerHTML = clients.length ? "<table><thead><tr><th>客户端</th><th>入口</th><th>状态</th><th>最近</th><th></th></tr></thead><tbody>" +
        clients.map(function(c){
          return "<tr><td>" + txt(c.client) + "</td><td>" + txt((c.kinds || []).join(", ")) + '</td><td class="' +
            (c.state === "running" ? "s2" : "s0") + '">' + txt(c.state) + "</td><td>" + ago(c.updated_at) +
            '</td><td><a href="#flows" data-client="' + txt(c.client) + '">看抓包</a></td></tr>';
        }).join("") + "</tbody></table>"
        : '<div class="empty">还没有客户端接入. 去 <a href="#access">接入</a> 页拿代理地址.</div>';
      Array.prototype.forEach.call($("clientList").querySelectorAll("a[data-client]"), function(a){
        a.onclick = function(){ HX.pendingClient = a.getAttribute("data-client"); };
      });
    });
  }
};

/* Router */
function show(name){
  if (!HX.views[name]) name = "overview";
  HX.current = name;
  Array.prototype.forEach.call(document.querySelectorAll(".view"), function(v){
    v.className = "view" + (v.id === "view-" + name ? " on" : "");
  });
  Array.prototype.forEach.call(document.querySelectorAll("#nav a"), function(a){
    a.className = a.getAttribute("data-view") === name ? "on" : "";
  });
  var v = HX.views[name];
  if (v.init && !v._inited){ v._inited = true; v.init(); }
  Promise.resolve(v.load && v.load()).catch(function(e){ toast("加载失败: " + e.message); });
}
window.addEventListener("hashchange", function(){ show(location.hash.slice(1).split("?")[0]); });
window.addEventListener("load", function(){
  show(location.hash.slice(1).split("?")[0] || "overview");
  setInterval(function(){
    refreshHead();
    if (HX.current === "overview") HX.views.overview.load();
  }, 5000);
});
