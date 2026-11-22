"use strict";
/* Cache, hosts and access views. Flows live in flows.js. */

HX.views.cache = {
  init: function(){
    $("cReload").onclick = function(){ HX.views.cache.load(); };
    $("cQ").addEventListener("keydown", function(e){ if (e.key === "Enter") HX.views.cache.load(); });
    $("cOrder").onchange = function(){ HX.views.cache.load(); };
    $("cPurge").onclick = function(){
      api("/purge", { method: "POST" }).then(function(r){ toast("清理过期 " + r.purged + " 条"); HX.views.cache.load(); });
    };
  },
  load: function(){
    var q = $("cQ").value.trim();
    return api("/entries?limit=500&order=" + $("cOrder").value + (q ? "&q=" + encodeURIComponent(q) : "")).then(function(d){
      var rows = d.entries || [];
      var tb = $("cRows");
      if (!rows.length){ tb.innerHTML = '<tr><td colspan="7" class="empty">缓存库是空的或没有匹配项</td></tr>'; return; }
      tb.innerHTML = "";
      rows.forEach(function(r){
        var tr = document.createElement("tr");
        var fresh = Number(r.fresh_until || 0) > Date.now() / 1000;
        tr.innerHTML = '<td class="url"><div>' + txt(r.host) + '</div><div class="sub">' + txt(r.url) + "</div></td>" +
          "<td>" + txt(r.type || "-") + "</td><td>" + human(r.size) + "</td><td>" + (r.hit_count || 0) + "</td>" +
          "<td>" + txt(r.source) + (fresh ? "" : ' <span class="badge sens">过期</span>') + "</td>" +
          "<td>" + clock(r.stored_at) + '</td><td><button class="sm danger">删</button></td>';
        tr.onclick = function(){ HX.views.cache.preview(r); };
        tr.querySelector("button").onclick = function(ev){
          ev.stopPropagation();
          api("/entry?url=" + encodeURIComponent(r.url), { method: "DELETE" }).then(function(){ toast("已删除"); tr.remove(); });
        };
        tb.appendChild(tr);
      });
    });
  },
  preview: function(r){
    var d = $("cDetail");
    d.innerHTML = "<h2>" + txt(r.url) + '</h2><dl class="kv"><dt>状态</dt><dd>' + r.status + "</dd><dt>类型</dt><dd>" +
      txt(r.type) + "</dd><dt>大小</dt><dd>" + human(r.size) + "</dd><dt>命中</dt><dd>" + r.hit_count +
      "</dd><dt>新鲜到</dt><dd>" + new Date(r.fresh_until * 1000).toLocaleString() + "</dd><dt>校验</dt><dd>" +
      txt(r.etag || "-") + '</dd></dl><div class="actions"><button id="cpUrl">复制 URL</button></div><div id="cBody" class="note">读取正文...</div>';
    $("cpUrl").onclick = function(){ copy(r.url); };
    api("/body?url=" + encodeURIComponent(r.url)).then(function(b){
      $("cBody").outerHTML = '<div class="note">前 512 字节预览, sha256 ' + txt(b.sha256.slice(0, 16)) + "...</div><pre>" + txt(b.preview) + "</pre>";
    }).catch(function(e){ $("cBody").textContent = "读不到正文: " + e.message; });
  }
};

HX.views.hosts = {
  rows: [], rules: null,
  init: function(){
    var self = this;
    $("hQ").addEventListener("input", function(){ self.render(); });
    $("hAddDec").onclick = function(){ self.set($("hAdd").value, "decrypt"); };
    $("hAddNever").onclick = function(){ self.set($("hAdd").value, "never"); };
    $("hAll").onchange = function(){
      var on = $("hAll").checked;
      if (on && !confirm("全部解密会让所有站点看到代理的 TLS 指纹, 只建议在调试时打开. 继续?")){ $("hAll").checked = false; return; }
      var doc = self.rules || { decrypt: [], never_decrypt: [] };
      doc.decrypt_all = on;
      api("/rules", { method: "PUT", body: doc }).then(function(){ toast(on ? "已开启全部解密" : "已关闭全部解密"); self.load(); });
    };
  },
  set: function(host, mode){
    host = String(host || "").trim();
    if (!host) return;
    var self = this;
    api("/rules/host", { method: "POST", body: { host: host, mode: mode } }).then(function(){
      toast(host + " -> " + ({ decrypt: "解密", tunnel: "隧道", never: "永不解密" })[mode] + " (约 1 秒内对新连接生效)");
      self.load();
    }).catch(function(e){ toast(e.message); });
  },
  load: function(){
    var self = this;
    return Promise.all([api("/hosts"), api("/rules")]).then(function(r){
      self.rows = r[0].hosts || []; self.rules = r[1];
      $("hAll").checked = !!self.rules.decrypt_all;
      (self.rules.decrypt || []).concat(self.rules.never_decrypt || []).forEach(function(h){
        if (!self.rows.some(function(x){ return x.host === h; }))
          self.rows.push({ host: h, conns: 0, clients: [], entries: 0, bytes: 0, saved: 0,
            mode: self.rules.decrypt.indexOf(h) >= 0 ? "decrypt" : "tunnel",
            reason: (self.rules.decrypt.indexOf(h) >= 0 ? "rule:" : "never:") + h });
      });
      self.render();
    });
  },
  render: function(){
    var self = this, q = $("hQ").value.trim().toLowerCase(), tb = $("hRows");
    var rows = self.rows.filter(function(h){ return !q || h.host.indexOf(q) >= 0; });
    if (!rows.length){ tb.innerHTML = '<tr><td colspan="8" class="empty">还没看到任何主机. 让浏览器走代理访问几个页面.</td></tr>'; return; }
    tb.innerHTML = "";
    rows.forEach(function(h){
      var never = String(h.reason || "").indexOf("never:") === 0;
      var cur = h.mode === "decrypt" ? '<span class="badge dec">解密</span>'
        : never ? '<span class="badge sens">永不解密</span>' : '<span class="badge tun">隧道</span>';
      var tr = document.createElement("tr");
      tr.innerHTML = "<td>" + txt(h.host) + '<div class="sub">' + txt(h.reason || "") + "</div></td><td>" + cur + "</td><td>" +
        (h.conns || 0) + "</td><td>" + txt((h.clients || []).join(", ") || "-") + "</td><td>" + (h.entries || 0) + "</td><td>" +
        human(h.bytes) + "</td><td>" + human(h.saved) + "</td><td>" +
        '<button class="sm" data-m="decrypt">解密</button> <button class="sm" data-m="tunnel">隧道</button> <button class="sm" data-m="never">永不</button></td>';
      Array.prototype.forEach.call(tr.querySelectorAll("button"), function(b){
        b.onclick = function(){ self.set(h.host, b.getAttribute("data-m")); };
      });
      tb.appendChild(tr);
    });
  }
};

HX.views.access = {
  load: function(){
    return api("/access").then(function(a){
      var sn = a.snippets || {}, ca = a.ca || {};
      var pacUrl = new URL("proxy.pac", location.href).href;
      var caUrl = new URL("ca.crt", location.href).href;
      var lines = [
        ["Chrome / Edge / Brave", sn.chromium], ["Firefox (about:config)", sn.firefox],
        ["Playwright", sn.playwright], ["Puppeteer", sn.puppeteer], ["curl", sn.curl],
        ["环境变量", sn.env], ["PAC 自动配置", pacUrl], ["系统代理 (Safari 等)", sn.system],
      ];
      var h = "";
      h += '<div class="cards"><div class="card ' + (a.running ? "good" : "amber") + '"><div class="k">代理入口</div><div class="v">' +
        txt(a.running ? a.proxy_url : "未启动") + '</div><div class="s">' + (a.running ? "HTTP 代理, 任何浏览器与程序可用" : "先在宿主上启动代理") +
        '</div></div><div class="card ' + (a.decrypting ? "amber" : "") + '"><div class="k">HTTPS 处理</div><div class="v">' +
        (a.decrypting ? "部分解密" : "全部隧道") + '</div><div class="s">' + (a.decrypting ? (a.rules.decrypt_all ? "全部解密 (调试)" : (a.rules.decrypt || []).length + " 个 host 解密") : "TLS 指纹与直连相同") +
        "</div></div></div>";
      if (!a.running)
        h += '<div class="panel"><h3>启动代理</h3>' + snip("命令", sn.start) +
          '<div class="dim">每个浏览器一个入口可加 <code>--client 名字=端口[,上游代理]</code>, 它们共用同一份缓存, 抓包按客户端区分.</div></div>';
      if ((a.listeners || []).length){
        h += '<div class="panel"><h3>入口列表</h3><table><thead><tr><th>客户端</th><th>协议</th><th>地址</th><th>上游出口</th><th></th></tr></thead><tbody>' +
          a.listeners.map(function(l){
            var url = (l.mode === "socks5" ? "socks5://" : "http://") + l.host + ":" + l.port;
            return "<tr><td>" + txt(l.name) + "</td><td>" + txt(l.mode) + "</td><td>" + txt(url) + "</td><td>" +
              txt(l.upstream || "直连") + (l.upstream_auth ? " (带认证)" : "") + '</td><td><button class="sm" data-copy="' + txt(url) + '">复制</button></td></tr>';
          }).join("") + "</tbody></table></div>";
      }
      h += '<div class="panel"><h3>接入方式</h3>' + lines.map(function(l){ return snip(l[0], l[1]); }).join("") + "</div>";
      h += '<div class="panel"><h3>证书 (只在解密时需要)</h3><ol class="steps">' +
        "<li>下载本地 CA: " + (ca.exists ? '<a href="' + txt(caUrl) + '">hxcp-ca.pem</a>' : "代理启动后自动生成") + "</li>" +
        "<li>Chrome / Edge: 设置 &gt; 隐私和安全 &gt; 管理证书 &gt; 授权机构 &gt; 导入</li>" +
        "<li>Firefox: 设置 &gt; 证书 &gt; 查看证书 &gt; 证书颁发机构 &gt; 导入, 勾选 信任此 CA 标识网站</li>" +
        "<li>macOS Safari: 双击导入钥匙串, 在 钥匙串访问 里把它设为 始终信任</li>" +
        "<li>Linux 系统级: 复制到 /usr/local/share/ca-certificates/ 后执行 update-ca-certificates; 程序也可用 <code>SSL_CERT_FILE</code></li>" +
        '</ol><div class="dim">CA 路径: <code>' + txt(ca.path) + "</code>. 只有 主机与 TLS 页里选成 解密 的 host 才会用到它.</div></div>";
      h += '<div class="panel"><h3>给 Agent</h3>' +
        snip("HTTP API", new URL(".", location.href).href) +
        snip("MCP (stdio)", sn.mcp) +
        '<div class="dim">常用: <code>GET access</code> 拿接入方式, <code>GET flows?q=&amp;client=</code> 查抓包, <code>GET flows.har</code> 导出, ' +
        "<code>GET hosts</code> 看各站点, <code>POST rules/host {host, mode}</code> 切换解密. Agent 走自己的入口端口即可与人分开统计.</div></div>";
      h += '<div class="panel"><h3>三种档位怎么选</h3><ol class="steps"><li>' + txt(a.notes.tunnel) + "</li><li>" + txt(a.notes.decrypt) +
        "</li><li>" + txt(a.notes.cdp) + "</li></ol></div>";
      $("accessBox").innerHTML = h;
      Array.prototype.forEach.call($("accessBox").querySelectorAll("[data-copy]"), function(b){
        b.onclick = function(){ copy(b.getAttribute("data-copy")); };
      });
    });
  }
};

function snip(label, value){
  if (!value) return "";
  return '<div class="snip"><div class="lbl">' + txt(label) + "</div><code>" + txt(value) +
    '</code><button class="sm" data-copy="' + txt(value) + '">复制</button></div>';
}
