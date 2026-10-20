/* fp-insight 注入探针 (MAIN world) — 观测站点实际调用了哪些指纹采集面
 * 交付物: 每次调用的 参数/返回值/耗时/调用栈/调用次数, 经 WebSocket 回传。
 * 纪律: 只观测不改写 —— 伪造返回值会污染被测对象, 取证工具不碰证物。
 * 保真: 计数永远精确; 只有"事件上报"受令牌桶限制 (防止 getBoundingClientRect 这类
 *       高频面淹没传输), 被抑制的量会以 meta/suppressed 显式上报。
 */
(() => {
  "use strict";
  if (window.__FPI__) return;
  var CFG = window.__FPI_CFG__ || {};
  var SELF = "fp-insight";
  var INST = "i" + Math.random().toString(36).slice(2, 10) + Date.now().toString(36).slice(-4);

  /* 探针内务必须走**原始引用**。原因: 探针会 hook Performance.now 等高频面,
     若自己的计时还走 RAW_NOW(), 就会递归进自己的包装函数 —— 实测直接爆栈,
     而爆栈发生在 hook 包装体的第一行 (try 之外), 于是异常逃逸到站点脚本,
     整个装配过程静默失败 (页面照跑, 但一个钩子都没装上)。 */
  var RAW_NOW = (function () {
    try { var p = performance.now; if (typeof p === "function") return p.bind(performance); } catch (e) {}
    return function () { return Date.now(); };
  })();
  var RAW_ERR = Error;
  var T0 = RAW_NOW();
  var SEQ = 0, TOTAL = 0, DROPPED = 0, SUPPRESSED = 0;
  var WS = null, RETRY = 0, Q = [], FLUSHING = false, LAST_FLUSH = 0;
  var NODE = false;
  try { NODE = !!(window.process && window.process.versions && window.process.versions.node); } catch (e) {}

  /* ---------------------------------------------------- 探针自身的内务快照
   * 必须在任何 hook 安装**之前**读取。否则探针自己读 navigator.userAgent / document.cookie
   * 时会命中自己的钩子, 在证据里留下"站点读了一次 UA"的假记录。 */
  var RAW = (function () {
    var o = {};
    try { o.ua = navigator.userAgent; } catch (e) { o.ua = ""; }
    try { o.lang = navigator.language; } catch (e) { o.lang = ""; }
    try { o.tz = Intl.DateTimeFormat().resolvedOptions().timeZone; } catch (e) { o.tz = ""; }
    /* 同时冻结 Document.prototype.cookie 的原始读取器 —— 否则探针自己读 cookie
       会命中探针自己装的钩子, 把"探针在读数"记成"站点在采指纹"。 */
    try { o.ckd = Object.getOwnPropertyDescriptor(Document.prototype, "cookie"); } catch (e) { o.ckd = null; }
    return o;
  })();
  function rawCookie() {
    try { return RAW.ckd && RAW.ckd.get ? (RAW.ckd.get.call(document) || "") : ""; } catch (e) { return ""; }
  }

  /* ---------------------------------------------------------------- 传输 */
  var FLUSH_MS = 120, MAX_Q = 20000, MAX_BATCH = 800;
  function flush() {
    if (!Q.length || !WS || WS.readyState !== 1) return;
    var batch = Q.splice(0, MAX_BATCH);
    try { WS.send(JSON.stringify({ ch: "fp", kind: "ev", sid: CFG.sid, frame: CFG.frame, payload: batch })); }
    catch (e) { Q = batch.concat(Q); }
  }
  function schedule() {
    if (Q.length >= MAX_Q) { DROPPED += Q.length; Q = []; push({ kind: "meta", type: "overflow", dropped: DROPPED }); }
    var now = RAW_NOW();
    if (now - LAST_FLUSH >= FLUSH_MS) { LAST_FLUSH = now; flush(); return; }
    if (!FLUSHING) { FLUSHING = true; setTimeout(function () { FLUSHING = false; LAST_FLUSH = RAW_NOW(); flush(); }, FLUSH_MS); }
  }
  function push(ev) { Q.push(ev); schedule(); }
  function emitRaw(ev) { ev.t = Math.round(RAW_NOW() - T0); push(ev); }
  function send(o) { try { if (WS && WS.readyState === 1) WS.send(JSON.stringify(o)); } catch (e) {} }
  function connect() {
    if (!CFG.report || NODE) return;
    try { WS = new WebSocket(CFG.report); } catch (e) { WS = null; }
    if (!WS) return;
    WS.onopen = function () {
      RETRY = 0;
      send({ ch: "fp", kind: "hello", sid: CFG.sid, inst: INST, payload: {
        inst: INST, url: location.href, href0: CFG.href0, ua: RAW.ua,
        top: (function () { try { return window.top === window; } catch (e) { return false; } })(),
        tz: RAW.tz, now: Date.now(), lang: RAW.lang, uaData: !!N.userAgentData } });
      emitRaw({ kind: "meta", type: "online" });
      flush();
    };
    WS.onclose = function () { WS = null; setTimeout(connect, Math.min(4000, 250 * Math.pow(2, RETRY++))); };
    WS.onerror = function () { try { WS.close(); } catch (e) {} };
  }
  function tzName() { return RAW.tz || ""; }

  /* ------------------------------------------------------------ 栈与格式化 */
  var PAGE_URL_RE = /^(https?|blob|file|data):/;
  function parseStack(depth) {
    var s;
    try { s = new RAW_ERR().stack || ""; } catch (e) { return []; }
    var lines = s.split("\n"), all = [];
    for (var i = 0; i < lines.length; i++) {
      var L = lines[i].trim();
      if (L.indexOf("at ") !== 0) continue;
      var m = /^at\s+(.*?)\s*\(?(.*?):(\d+):(\d+)\)?$/.exec(L);
      if (m) all.push({ fn: m[1] || "(anon)", url: m[2], line: +m[3], col: +m[4], raw: L });
      else {
        var m2 = /^at\s+(.*)$/.exec(L);
        if (m2) all.push({ fn: m2[1], url: "", line: 0, col: 0, raw: L });
      }
    }
    /* 关键切分: 探针的注入脚本是**匿名**的 (addScriptToEvaluateOnNewDocument 不带源 URL),
       它在栈里和页面内联脚本长得一样, 都是 <anonymous>。按函数名过滤不可靠 ——
       唯一可靠的锚点是第一个带真实资源 URL 的帧, 它必定属于被测站点。
       探针自己的帧 (wrapped -> note -> parseStack) 全部排在它之前, 整体丢弃。 */
    var start = -1;
    for (var j = 0; j < all.length; j++) { if (PAGE_URL_RE.test(all[j].url)) { start = j; break; } }
    if (start < 0) return [];
    return all.slice(start, start + (depth || 8));
  }
  function topOf(st) { return st.length ? st[0] : null; }
  function brief(v, d) {
    d = d === undefined ? 1 : d;
    try {
      if (v === null) return "null";
      if (v === undefined) return "undefined";
      var t = typeof v;
      if (t === "string") return JSON.stringify(v.length > 200 ? v.slice(0, 200) + "\u2026" : v);
      if (t === "number" || t === "boolean" || t === "bigint") return String(v);
      if (t === "function") return "[fn " + (v.name || "anon") + "]";
      if (t === "symbol") return String(v);
      if (v instanceof Error) return v.name + ": " + String(v.message).slice(0, 120);
      if (typeof ArrayBuffer !== "undefined" && (v instanceof ArrayBuffer || ArrayBuffer.isView(v)))
        return "[" + (v.constructor ? v.constructor.name : "buf") + " " + (v.byteLength || 0) + "B]";
      if (typeof Node !== "undefined" && v instanceof Node) return "<" + v.nodeName.toLowerCase() + (v.id ? "#" + v.id : "") + ">";
      if (Array.isArray(v)) {
        if (d <= 0) return "[Array(" + v.length + ")]";
        var a = []; for (var i = 0; i < Math.min(v.length, 6); i++) a.push(brief(v[i], d - 1));
        return "[" + a.join(", ") + (v.length > 6 ? ", \u2026+" + (v.length - 6) : "") + "]";
      }
      if (t === "object") {
        if (d <= 0) return "{Object}";
        var ks = [], k = Object.keys(v);
        for (var j = 0; j < Math.min(k.length, 6); j++) { var s2; try { s2 = brief(v[k[j]], d - 1); } catch (e) { s2 = "?"; } ks.push(k[j] + ": " + s2); }
        var ctor = (v.constructor && v.constructor.name) || "Object";
        return ctor + "{" + ks.join(", ") + (k.length > 6 ? ", \u2026+" + (k.length - 6) : "") + "}";
      }
      return String(v);
    } catch (e) { return "[unreadable]"; }
  }

  /* ------------------------------------------------- 计数 + 事件上报令牌桶 */
  var COUNT = Object.create(null), BUCKET = Object.create(null), EMIT_PER_5S = 60;
  var HOOKED = 0, HOOKFAIL = 0;
  var IN_NOTE = false;   /* 重入守卫: 记录过程自身若触发被 hook 的 API, 直接短路, 否则无限递归 */
  function counter(path, surface) { var c = path + "|" + surface; return COUNT[c] = (COUNT[c] || 0) + 1; }
  function bump(path, surface) { return counter(path, surface); }
  function allow(path) {
    var now = RAW_NOW(), b = BUCKET[path];
    if (!b) b = BUCKET[path] = { t: now, n: 0, sup: 0 };
    if (now - b.t > 5000) {
      if (b.sup) { SUPPRESSED += b.sup; emitRaw({ kind: "meta", type: "suppressed", path: path, count: b.sup }); }
      b.t = now; b.n = 0; b.sup = 0;
    }
    b.n++;
    if (b.n > EMIT_PER_5S) { b.sup++; return false; }
    return true;
  }
  function note(path, surface, cat, data) {
    var n = bump(path, surface);
    TOTAL++;
    if (!allow(path)) return;
    if (IN_NOTE) return;
    IN_NOTE = true;
    try {
      var st = parseStack(8);
      var ev = { seq: SEQ++, path: path, surface: surface, cat: cat || "", n: n, ms: 0, stack: st, top: topOf(st) };
      for (var k in data) ev[k] = data[k];
      emitRaw(ev);
    } finally { IN_NOTE = false; }
  }

  /* ------------------------------------------------------- 变量纠察 (取样) */
  var INV = Object.create(null), INV_DIRTY = 0;
  function sample(path, value) {
    if (IN_NOTE) return;
    var r = INV[path];
    if (!r) r = INV[path] = { path: path, n: 0, vals: [], distinct: 0, first: null, last: null, err: 0 };
    r.n++;
    var s;
    try { s = brief(value, 1); } catch (e) { s = "?"; r.err++; }
    r.last = s;
    if (r.first === null) r.first = s;
    if (r.vals.length < 12 && r.vals.indexOf(s) < 0) r.vals.push(s);
    r.distinct = r.vals.length > 11 ? 99 : r.vals.length;
    INV_DIRTY++;
  }
  function invList() {
    var out = [], k;
    for (k in INV) {
      var r = INV[k];
      if (!r.n || k.indexOf("__") === 0) continue;
      out.push({ path: r.path, n: r.n, distinct: r.vals.length > 11 ? 99 : r.vals.length,
        stable: r.vals.length <= 1, vals: r.vals.slice(0, 6), first: r.first, last: r.last, err: r.err });
    }
    return out;
  }
  function reportInv() {
    if (!INV_DIRTY) return;
    INV_DIRTY = 0;
    send({ ch: "fp", kind: "inv", sid: CFG.sid, frame: CFG.frame, payload: invList() });
  }
  setInterval(reportInv, 2500);

  /* ----------------------------------------------------------- hook 原语 */
  var _dp = Object.defineProperty, _gopd = Object.getOwnPropertyDescriptor, _apply = Function.prototype.apply;
  function hookMethod(proto, key, cat, label) {
    var d;
    try { d = _gopd(proto, key); } catch (e) { return; }
    if (!d || typeof d.value !== "function") return;
    var orig = d.value, path = label || key;
    function wrapped() {
      var t0 = RAW_NOW(), self = this, callArgs = arguments, res, err = null;
      try { res = _apply.call(orig, self, callArgs); }
      catch (e) { err = e; throw e; }
      finally {
        try {
          var args = [];
          for (var i = 0; i < callArgs.length && i < 6; i++) args.push(brief(callArgs[i], 1));
          note(path, "method", cat, { key: key, args: args,
            ret: err ? "throw " + err.name : brief(res, 1),
            ms: Math.round((RAW_NOW() - t0) * 1000) / 1000 });
        } catch (e2) {}
      }
      return res;
    }
    try { _dp(proto, key, { value: wrapped, writable: d.writable, enumerable: d.enumerable, configurable: d.configurable }); HOOKED++; }
    catch (e) { HOOKFAIL++; }
  }
  function hookGetter(proto, key, cat, label, opts) {
    var d;
    try { d = _gopd(proto, key); } catch (e) { return; }
    if (!d || !d.get) return;
    var g = d.get, path = label || key;
    try {
      _dp(proto, key, { get: function () {
        var t0 = RAW_NOW(), v = _apply.call(g, this, []);
        try {
          var r = { key: key, ret: brief(v, 1), ms: Math.round((RAW_NOW() - t0) * 1000) / 1000 };
          if (opts && opts.coll && v && typeof v.length === "number" && typeof v.item === "function") r.ret = "[len " + v.length + "]";
          note(path, "getter", cat, r);
          sample(path, v);
        } catch (e) {}
        return v;
      }, set: d.set, enumerable: d.enumerable, configurable: d.configurable });
      HOOKED++;
    } catch (e) { HOOKFAIL++; }
  }
  function hookCtor(scope, name, cat, label) {
    var O = scope[name];
    if (typeof O !== "function") return;
    var path = label || name;
    var P2 = new Proxy(O, {
      construct: function (t, a, nt) {
        var t0 = RAW_NOW(), res = Reflect.construct(t, a, nt);
        try {
          var args = []; for (var i = 0; i < a.length && i < 5; i++) args.push(brief(a[i], 1));
          note(path, "ctor", cat, { key: name, args: args, ret: "new " + name, ms: Math.round((RAW_NOW() - t0) * 1000) / 1000 });
        } catch (e) {}
        return res;
      },
      apply: function (t, tt, a) { return Reflect.apply(t, tt, a); }
    });
    try { scope[name] = P2; } catch (e) {}
  }
  /* 注入时刻的环境快照。冠 env: 前缀是刻意的 —— 它是**探针自己的读数**,
     与"站点读了哪个 API"是两类证据, 不允许混在同一个列表里冒充后者。 */
  function scan(pairs, cat) {
    for (var i = 0; i < pairs.length; i++) {
      try {
        var v = pairs[i][0], parts = pairs[i][1].split(".");
        for (var j = 0; j < parts.length; j++) v = v[parts[j]];
        sample("env:" + pairs[i][1], v);
      } catch (e) {}
    }
  }
  var C = window, D = document;
  /* P(x)  = 构造函数 -> 它的 prototype。用于 HTMLCanvasElement 这类"类"。
     PR(x) = **实例** -> 它所属的原型对象。navigator/screen 这类单例必须走 PR:
             navigator.prototype 是 undefined, 用 P(navigator) 静默装不上任何钩子
             (实测踩过: 所有 Navigator.* / Screen.* 钩子一个都没生效, 页面却毫无报错)。 */
  var P = function (o) { return o ? o.prototype : null; };
  var PR = function (o) { try { return o ? Object.getPrototypeOf(o) : null; } catch (e) { return null; } };

  /* 中性视图: 读 N/S 上的属性**不经过**本探针装的钩子。
     站点的读取走原型钩子, 探针自己的内务走这里 —— 两条路必须分开,
     否则证据里会混进探针自己的脚印。 */
  function neutralise(real, canon) {
    var cache = Object.create(null);
    for (var i = 0; i < canon.length; i++) { try { cache[canon[i]] = real[canon[i]]; } catch (e) { cache[canon[i]] = undefined; } }
    try {
      return new Proxy(real, {
        get: function (t, k) {
          if (typeof k === "string" && k in cache) return cache[k];
          try { return t[k]; } catch (e) { return undefined; }
        },
        has: function (t, k) { try { return k in t; } catch (e) { return false; } },
        getPrototypeOf: function (t) { return Object.getPrototypeOf(t); }
      });
    } catch (e) { return real; }
  }
  var N = neutralise(navigator, ["userAgent", "platform", "vendor", "product", "productSub", "appVersion",
    "appName", "appCodeName", "language", "languages", "hardwareConcurrency", "deviceMemory", "maxTouchPoints",
    "doNotTrack", "cookieEnabled", "webdriver", "onLine", "oscpu", "buildID", "pdfViewerEnabled",
    "globalPrivacyControl", "userAgentData", "connection", "plugins", "mimeTypes", "permissions",
    "mediaDevices", "geolocation", "storage", "devicePosture", "gpu", "xr", "bluetooth", "usb", "hid",
    "credentials", "clipboard", "serviceWorker", "getBattery"]);
  var S = neutralise(screen, ["width", "height", "availWidth", "availHeight", "colorDepth", "pixelDepth",
    "orientation", "isExtended"]);

  /* --------------------------------------------------------------- Canvas */
  if (P(C.HTMLCanvasElement)) {
    hookMethod(P(C.HTMLCanvasElement), "toDataURL", "canvas", "HTMLCanvasElement.toDataURL");
    hookMethod(P(C.HTMLCanvasElement), "toBlob", "canvas", "HTMLCanvasElement.toBlob");
    hookMethod(P(C.HTMLCanvasElement), "getContext", "canvas", "HTMLCanvasElement.getContext");
  }
  if (P(C.CanvasRenderingContext2D)) {
    ["getImageData", "fillText", "strokeText", "measureText", "isPointInPath", "putImageData",
     "createLinearGradient", "drawImage", "fillRect"].forEach(function (m) {
      hookMethod(P(C.CanvasRenderingContext2D), m, "canvas", "CanvasRenderingContext2D." + m);
    });
    hookGetter(P(C.CanvasRenderingContext2D), "font", "canvas", "CanvasRenderingContext2D.font");
  }
  if (C.OffscreenCanvas) {
    hookMethod(P(C.OffscreenCanvas), "getContext", "canvas", "OffscreenCanvas.getContext");
    hookMethod(P(C.OffscreenCanvas), "convertToBlob", "canvas", "OffscreenCanvas.convertToBlob");
  }

  /* ---------------------------------------------------------------- WebGL */
  ["WebGLRenderingContext", "WebGL2RenderingContext"].forEach(function (g) {
    if (!C[g] || !P(C[g])) return;
    var pr = P(C[g]);
    ["getParameter", "getExtension", "getSupportedExtensions", "getShaderPrecisionFormat", "getContextAttributes",
     "readPixels", "getProgramParameter", "getShaderParameter", "getInternalformatParameter",
     "getUniformLocation", "getAttribLocation", "getError", "getActiveUniform", "getActiveAttrib"].forEach(function (m) {
      if (typeof pr[m] === "function") hookMethod(pr, m, "webgl", g + "." + m);
    });
    ["drawingBufferWidth", "drawingBufferHeight", "canvas"].forEach(function (k) { hookGetter(pr, k, "webgl", g + "." + k); });
  });
  if (N.gpu) {
    sample("env:navigator.gpu", N.gpu ? "present" : "absent");
    if (P(C.GPU)) hookMethod(P(C.GPU), "requestAdapter", "webgl", "GPU.requestAdapter");
    if (P(C.GPUAdapter)) hookMethod(P(C.GPUAdapter), "requestDevice", "webgl", "GPUAdapter.requestDevice");
  }

  /* ---------------------------------------------------------------- Audio */
  ["AudioContext", "OfflineAudioContext", "webkitOfflineAudioContext", "webkitAudioContext"].forEach(function (g) {
    if (C[g]) hookCtor(C, g, "audio", g);
  });
  if (C.BaseAudioContext && P(C.BaseAudioContext)) {
    hookMethod(P(C.BaseAudioContext), "createAnalyser", "audio", "BaseAudioContext.createAnalyser");
    hookMethod(P(C.BaseAudioContext), "createOscillator", "audio", "BaseAudioContext.createOscillator");
    hookMethod(P(C.BaseAudioContext), "createDynamicsCompressor", "audio", "BaseAudioContext.createDynamicsCompressor");
    hookMethod(P(C.BaseAudioContext), "createGain", "audio", "BaseAudioContext.createGain");
  }
  if (C.AnalyserNode && P(C.AnalyserNode)) {
    ["getFloatFrequencyData", "getByteFrequencyData", "getFloatTimeDomainData", "getByteTimeDomainData"].forEach(function (m) {
      hookMethod(P(C.AnalyserNode), m, "audio", "AnalyserNode." + m);
    });
  }
  if (C.AudioBuffer && P(C.AudioBuffer)) {
    hookMethod(P(C.AudioBuffer), "getChannelData", "audio", "AudioBuffer.getChannelData");
    hookMethod(P(C.AudioBuffer), "copyFromChannel", "audio", "AudioBuffer.copyFromChannel");
  }
  if (C.OscillatorNode && P(C.OscillatorNode)) {
    ["start", "stop"].forEach(function (m) { hookMethod(P(C.OscillatorNode), m, "audio", "OscillatorNode." + m); });
  }
  if (C.DynamicsCompressorNode && P(C.DynamicsCompressorNode))
    ["threshold", "knee", "ratio", "reduction", "attack", "release"].forEach(function (k) { hookGetter(P(C.DynamicsCompressorNode), k, "audio", "DynamicsCompressorNode." + k); });

  /* ------------------------------------------------------------ navigator */
  scan([[N, "userAgent"], [N, "platform"], [N, "vendor"], [N, "product"], [N, "productSub"], [N, "appVersion"],
        [N, "appName"], [N, "appCodeName"], [N, "language"], [N, "languages"], [N, "hardwareConcurrency"],
        [N, "deviceMemory"], [N, "maxTouchPoints"], [N, "doNotTrack"], [N, "cookieEnabled"], [N, "webdriver"],
        [N, "onLine"], [N, "oscpu"], [N, "buildID"], [N, "pdfViewerEnabled"], [N, "plugins.length"],
        [N, "mimeTypes.length"], [N, "userAgentData"], [N, "globalPrivacyControl"], [N, "devicePosture"]], "navigator");
  ["userAgent", "platform", "vendor", "product", "productSub", "appVersion", "hardwareConcurrency",
   "deviceMemory", "languages", "language", "maxTouchPoints", "webdriver", "pdfViewerEnabled",
   "doNotTrack", "plugins", "mimeTypes"].forEach(function (k) {
    try { var np = PR(N); if (np && k in np) hookGetter(np, k, "navigator", "Navigator." + k, { coll: true }); } catch (e) {}
  });
  if (N.userAgentData && PR(N.userAgentData)) {
    var uad = PR(N.userAgentData);
    ["brands", "mobile", "platform", "architecture", "bitness", "model", "platformVersion"].forEach(function (k) {
      try { if (k in uad) hookGetter(uad, k, "navigator", "NavigatorUAData." + k); } catch (e) {}
    });
    hookMethod(uad, "getHighEntropyValues", "navigator", "NavigatorUAData.getHighEntropyValues");
    hookMethod(uad, "toJSON", "navigator", "NavigatorUAData.toJSON");
  }
  if (P(C.Navigator) && "connection" in P(C.Navigator)) hookGetter(P(C.Navigator), "connection", "network", "Navigator.connection");
  if (N.connection) {
    scan([[N.connection, "effectiveType"], [N.connection, "rtt"], [N.connection, "downlink"],
          [N.connection, "saveData"], [N.connection, "type"]], "network");
    var NC = N.connection.constructor;
    if (NC && P(NC)) ["rtt", "downlink", "effectiveType", "saveData", "type"].forEach(function (k) {
      try { if (k in P(NC)) hookGetter(P(NC), k, "network", "NetworkInformation." + k); } catch (e) {}
    });
  }

  /* --------------------------------------------------------------- screen */
  scan([[S, "width"], [S, "height"], [S, "availWidth"], [S, "availHeight"], [S, "colorDepth"], [S, "pixelDepth"],
        [S, "orientation.type"], [S, "orientation.angle"], [S, "isExtended"],
        [C, "devicePixelRatio"], [C, "innerWidth"], [C, "innerHeight"], [C, "outerWidth"], [C, "outerHeight"],
        [C, "screenX"], [C, "screenY"], [C, "screenLeft"], [C, "screenTop"], [C, "scrollX"], [C, "scrollY"],
        [D.documentElement, "clientWidth"], [D.documentElement, "clientHeight"]], "screen");
  if (P(C.Screen)) ["width", "height", "availWidth", "availHeight", "colorDepth", "pixelDepth", "orientation"].forEach(function (k) {
    try { if (k in P(C.Screen)) hookGetter(P(C.Screen), k, "screen", "Screen." + k); } catch (e) {}
  });
  if (C.ScreenOrientation && P(C.ScreenOrientation)) {
    hookGetter(P(C.ScreenOrientation), "type", "screen", "ScreenOrientation.type");
    hookGetter(P(C.ScreenOrientation), "angle", "screen", "ScreenOrientation.angle");
  }
  if (P(C.Window)) ["devicePixelRatio", "innerWidth", "innerHeight", "outerWidth", "outerHeight"].forEach(function (k) {
    try { if (k in P(C.Window)) hookGetter(P(C.Window), k, "screen", "Window." + k); } catch (e) {}
  });

  /* ------------------------------------------------------- locale / time */
  if (C.Intl) {
    ["DateTimeFormat", "NumberFormat", "Collator", "RelativeTimeFormat", "PluralRules", "DisplayNames", "ListFormat"].forEach(function (n2) {
      if (C.Intl[n2]) hookCtor(C.Intl, n2, "locale", "Intl." + n2);
    });
    if (C.Intl.DateTimeFormat && P(C.Intl.DateTimeFormat))
      hookMethod(P(C.Intl.DateTimeFormat), "resolvedOptions", "locale", "Intl.DateTimeFormat.resolvedOptions");
    if (C.Intl.NumberFormat && P(C.Intl.NumberFormat))
      hookMethod(P(C.Intl.NumberFormat), "resolvedOptions", "locale", "Intl.NumberFormat.resolvedOptions");
  }
  /* 只留 getTimezoneOffset (时区指纹的核心判据); getTime/getHours/toString 高频且无区分力。 */
  if (P(C.Date)) hookMethod(P(C.Date), "getTimezoneOffset", "locale", "Date.getTimezoneOffset");
  if (P(C.Performance)) hookMethod(P(C.Performance), "now", "timing", "Performance.now");
  if (P(C.PerformanceTiming)) hookGetter(P(C.PerformanceTiming), "timeOrigin", "timing", "PerformanceTiming.timeOrigin");
  /* Math.* 刻意不挂: 浮点误差是**经 canvas/audio 通道**体现的, 直接读 Math.sin 没有区分力,
     而 V8 会把 Math.* 内联, 挂钩子只制造噪声与递归风险。 */

  /* ------------------------------------------- 权限 / 媒体 / 传感器 / 设备 */
  if (N.permissions) sample("env:navigator.permissions", "present");
  var Perm = N.permissions;
  if (Perm && P(Perm) && typeof P(Perm).query === "function") {
    var _q = P(Perm).query;
    try {
      _dp(P(Perm), "query", { value: function (d) {
        var r, self = this;
        try { r = _apply.call(_q, self, arguments); } catch (e) { note("Permissions.query", "method", "permissions", { key: "query", args: [brief(d, 1)], ret: "throw" }); throw e; }
        try {
          var pname = d && d.name;
          var n = bump("Permissions.query:" + pname, "method"); TOTAL++;
          if (r && r.then) r.then(function (st) {
            bump("Permissions.query:" + pname, "resolved");
            if (allow("Permissions.query:" + pname))
              push({ t: Math.round(RAW_NOW() - T0), seq: SEQ++, path: "Permissions.query:" + pname, surface: "resolved",
                cat: "permissions", n: n, key: "query", args: [brief(d, 1)], ret: st && st.state, stack: [], top: null });
          });
        } catch (e) {}
        return r;
      }, writable: true, configurable: true });
    } catch (e) {}
  }
  if (N.mediaDevices) {
    sample("env:navigator.mediaDevices", N.mediaDevices ? "present" : "absent");
    var MD = PR(N.mediaDevices);
    if (MD) ["enumerateDevices", "getUserMedia", "getDisplayMedia", "getSupportedConstraints"].forEach(function (m) {
      if (typeof MD[m] === "function") hookMethod(MD, m, "permissions", "MediaDevices." + m);
    });
  }
  if (N.getBattery) hookMethod(PR(N), "getBattery", "battery", "Navigator.getBattery");
  if (N.storage && PR(N.storage) && PR(N.storage).estimate) hookMethod(PR(N.storage), "estimate", "storage", "StorageManager.estimate");
  if (N.geolocation) sample("env:navigator.geolocation", "present");
  if (C.DeviceOrientationEvent) hookCtor(C, "DeviceOrientationEvent", "sensor", "DeviceOrientationEvent");
  if (C.DeviceMotionEvent) hookCtor(C, "DeviceMotionEvent", "sensor", "DeviceMotionEvent");
  if (C.Notification) hookCtor(C, "Notification", "permissions", "Notification");

  /* ------------------------------------------------------------ 字体检测 */
  if (D.fonts) {
    sample("env:document.fonts.size", D.fonts.size);
    if (P(C.FontFaceSet)) {
      hookMethod(P(C.FontFaceSet), "check", "font", "FontFaceSet.check");
      hookMethod(P(C.FontFaceSet), "load", "font", "FontFaceSet.load");
      hookGetter(P(C.FontFaceSet), "size", "font", "FontFaceSet.size");
    }
    if (C.FontFace) hookCtor(C, "FontFace", "font", "FontFace");
  }

  /* --------------------------------------------------------- WebRTC / 网络 */
  if (C.RTCPeerConnection) {
    var wrapRTC = function (Orig) {
      function R() {
        var pc = Reflect.construct(Orig, arguments, Orig);
        try {
          var args = []; for (var i = 0; i < arguments.length && i < 3; i++) args.push(brief(arguments[i], 1));
          note("RTCPeerConnection", "ctor", "webrtc", { key: "RTCPeerConnection", args: args, ret: "RTCPeerConnection" });
          if (pc.createDataChannel) {
            var o1 = pc.createDataChannel.bind(pc);
            pc.createDataChannel = function () { note("RTCPeerConnection.createDataChannel", "method", "webrtc", { key: "createDataChannel", args: [brief(arguments[0], 1)], ret: "" }); return o1.apply(null, arguments); };
          }
          ["createOffer", "createAnswer", "setLocalDescription", "addIceCandidate", "setConfiguration"].forEach(function (m) {
            if (typeof pc[m] !== "function") return;
            var o2 = pc[m].bind(pc);
            pc[m] = function () {
              var r = o2.apply(null, arguments);
              try {
                if (r && r.then) r.then(function (d) {
                  if (m === "createOffer" || m === "createAnswer") note("RTCPeerConnection." + m, "method", "webrtc", { key: m, args: [], ret: "sdp " + (d && d.sdp ? d.sdp.length : 0) + "B" });
                });
              } catch (e) {}
              return r;
            };
          });
        } catch (e) {}
        return pc;
      }
      R.prototype = Orig.prototype;
      try { Object.setPrototypeOf(R, Orig); } catch (e) {}
      return R;
    };
    try { C.RTCPeerConnection = wrapRTC(C.RTCPeerConnection); } catch (e) {}
    if (C.webkitRTCPeerConnection) { try { C.webkitRTCPeerConnection = wrapRTC(C.webkitRTCPeerConnection); } catch (e) {} }
  }

  /* ------------------------------------------------------ DOM / 自动化痕迹 */
  if (P(C.Function)) hookMethod(P(C.Function), "toString", "detect", "Function.prototype.toString");
  if (P(C.Element)) hookMethod(P(C.Element), "getAttribute", "detect", "Element.getAttribute");
  if (P(C.Document)) {
    hookMethod(P(C.Document), "createElement", "dom", "Document.createElement");
    hookMethod(P(C.Document), "write", "dom", "Document.write");
    if ("visibilityState" in P(C.Document)) hookGetter(P(C.Document), "visibilityState", "detect", "Document.visibilityState");
    if ("hidden" in P(C.Document)) hookGetter(P(C.Document), "hidden", "detect", "Document.hidden");
    if ("hasFocus" in P(C.Document)) hookMethod(P(C.Document), "hasFocus", "detect", "Document.hasFocus");
  }
  if (P(C.HTMLIFrameElement)) hookGetter(P(C.HTMLIFrameElement), "contentWindow", "dom", "HTMLIFrameElement.contentWindow");
  if (C.Worker) hookCtor(C, "Worker", "worker", "Worker");
  if (C.SharedWorker) hookCtor(C, "SharedWorker", "worker", "SharedWorker");
  if (N.serviceWorker && P(N.serviceWorker)) hookMethod(P(N.serviceWorker), "register", "worker", "ServiceWorkerContainer.register");
  if (C.WebAssembly) /* 指纹极少用 */ 0;

  /* ------------------------------------------------------ 存储 / Cookie */
  function snapStores() {
    /* 只走原始读取器 —— 这三行若走 D.cookie, 就会触发探针自己的 cookie 钩子,
       在证据里凭空多出一条"站点读了 cookie"。 */
    try { sample("store:document.cookie", rawCookie()); } catch (e) {}
    try { sample("store:localStorage.length", C.localStorage.length); } catch (e) {}
    try { sample("store:sessionStorage.length", C.sessionStorage.length); } catch (e) {}
    try { sample("store:indexedDB", D.defaultView.indexedDB ? "present" : "absent"); } catch (e) {}
    try { sample("store:window.name", C.name); } catch (e) {}
  }
  snapStores();
  setInterval(snapStores, 2000);
  if (C.Storage && P(C.Storage)) ["setItem", "getItem", "removeItem", "clear", "key"].forEach(function (m) {
    if (typeof P(C.Storage)[m] === "function") hookMethod(P(C.Storage), m, "storage", "Storage." + m);
  });
  try {
    var cd = _gopd(P(C.Document), "cookie");
    if (cd && cd.get && cd.set) _dp(P(C.Document), "cookie", {
      get: function () { var v = _apply.call(cd.get, this, []); sample("store:document.cookie", v); return v; },
      set: function (v) {
        try { note("document.cookie", "setter", "storage", { key: "cookie", args: [brief(v, 1)], ret: "set" }); } catch (e) {}
        return _apply.call(cd.set, this, [v]);
      }, enumerable: cd.enumerable, configurable: true });
  } catch (e) {}

  /* --------------------------------------------------------- 心跳 / 快照 */
  setInterval(function () {
    var cookieLen = 0, ck = [];
    try { var c = rawCookie(); cookieLen = c.length; ck = c.split(";").map(function (x) { return x.split("=")[0].trim(); }).filter(Boolean).slice(0, 40); } catch (e) {}
    var ls = [], ss = [];
    try { for (var i = 0; i < C.localStorage.length; i++) ls.push(C.localStorage.key(i)); } catch (e) {}
    try { for (var j = 0; j < C.sessionStorage.length; j++) ss.push(C.sessionStorage.key(j)); } catch (e) {}
    /* 绝对计数随心跳推送。为什么不用"增量": 绝对快照是幂等的, 断线重连/重复上报
       都不会把次数算重; 服务端用 MAX 合并, 天然容忍乱序。 */
    try { send({ ch: "fp", kind: "counts", sid: CFG.sid, frame: CFG.frame, payload: COUNT }); } catch (e) {}
    send({ ch: "fp", kind: "snap", sid: CFG.sid, frame: CFG.frame, payload: {
      url: location.href, title: D.title, cookieLen: cookieLen, cookieKeys: ck, ls: ls.slice(0, 40), ss: ss.slice(0, 40),
      total: TOTAL, seq: SEQ, suppressed: SUPPRESSED, dropped: DROPPED, tz: tzName(),
      hc: N.hardwareConcurrency, dm: N.deviceMemory, ua: RAW.ua, platform: N.platform,
      t: Math.round(RAW_NOW() - T0) } });
  }, 3000);

  window.addEventListener("unload", function () { reportInv(); try { flush(); } catch (e) {} });
  window.addEventListener("pagehide", function () { reportInv(); try { flush(); } catch (e) {} });
  try {
    window.__FPI__ = {
      hooked: function () { return HOOKED; },
      hookfail: function () { return HOOKFAIL; },
      total: function () { return TOTAL; },
      seq: function () { return SEQ; },
      counts: function () { var o = {}, k; for (k in COUNT) o[k] = COUNT[k]; return o; },
      diag: function () { return { hooked: HOOKED, hookfail: HOOKFAIL, total: TOTAL, seq: SEQ, dropped: DROPPED, suppressed: SUPPRESSED, ws: WS ? WS.readyState : -1, cfg: !!CFG.report }; },
      inv: function () { return invList(); },
      note: note, sample: sample, flush: flush
    };
  } catch (e) {}
  connect();
})();
