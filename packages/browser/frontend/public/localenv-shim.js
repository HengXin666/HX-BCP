/* HX-Browser: local-browser 前端 → REST API 桥接层
 * 前端通过 window.localEnv.* (原 Electron IPC) 调用，这里映射到 /api/v1/*
 */
(function () {
  const API = "/api/v1";

  async function req(method, path, body) {
    const opt = { method, headers: {} };
    if (body !== undefined) {
      opt.headers["Content-Type"] = "application/json";
      opt.body = JSON.stringify(body);
    }
    const r = await fetch(API + path, opt);
    let j = null;
    try { j = await r.json(); } catch (e) { j = { code: -1, msg: "bad response" }; }
    if (j.code !== 0) throw new Error(j.msg || "请求失败");
    return j.data;
  }

  function firstArr(v) {
    if (Array.isArray(v)) return v;
    if (v && typeof v === "object" && Array.isArray(v.ids)) return v.ids;
    return [];
  }

  /* ---------- 数据映射 ---------- */
  const FP_DEFAULTS = {
    kernelVersion: "system", deviceType: "desktop", colorDepth: 24,
    clientHintsMode: "", clientRectsNoise: false, deterministicRaster: false,
    fonts: [], mediaDevices: [], doNotTrack: "", geolocation: "", ports: "",
    kernelMode: "js", tlsHttp: true, tlsMinVersion: "default", tlsMode: "none",
    tlsProfile: "", tlsQuic: true, webglExtensions: [], webgpuMode: "default",
    webgpuVendor: "", webgpuRenderer: "", mobilePreset: "", deviceScaleFactor: 1,
    touchPoints: 0, screenOrientation: "", disableWebRTC: false, headless: false,
  };

  function toUiProfile(p) {
    const fp = p.fingerprint || {};
    const rt = p.runtime || null;
    return {
      ...p,
      id: p.id, name: p.name || "", group: p.group || "默认", platform: p.platform || "custom",
      country: p.country || "", region: p.region || "", city: p.city || "",
      customNo: p.customNo || "", serialNo: String(p.serial_no || 0),
      locationMode: p.location_mode || "match_ip", notes: p.notes || "",
      tags: p.tags || [], createdAt: p.created_at || 0,
      status: rt ? "running" : "closed",
      runtime: rt ? {
        pid: rt.pid, debugPort: rt.debug_port, profileDir: rt.profile_dir,
        startedAt: rt.started_at, endpoint: rt.cdp_http || "",
        cdpHttp: rt.cdp_http || "", cdpWs: rt.cdp_ws || "", loginState: rt.login_state || "unknown",
      } : null,
      account: { username: "", password: "", twoFactor: "", cookies: "", ...(p.account || {}) },
      proxy: { type: "no_proxy", host: "", port: 0, username: "", password: "", ...(p.proxy || {}) },
      fingerprint: {
        browser: fp.browser || "sun", os: fp.os || "Windows",
        kernel: fp.kernel || "",
        userAgent: fp.user_agent || "", language: fp.language || "",
        timezone: fp.timezone || "", resolution: fp.resolution || "1280x820",
        hardwareConcurrency: fp.hardware_concurrency || 0, deviceMemory: fp.device_memory || 0,
        webglVendor: fp.webgl_vendor || "", webglRenderer: fp.webgl_renderer || "",
        canvasNoise: !!fp.canvas_noise, audioNoise: !!fp.audio_noise, webglNoise: !!fp.webgl_noise,
        webrtcMode: fp.webrtc_mode || "real", webrtcPublicIp: fp.webrtc_public_ip || "",
        ...FP_DEFAULTS,
        deviceScaleFactor: fp.device_scale_factor || Number(FP_DEFAULTS.deviceScaleFactor) || 1,  // 放在 spread 后才能盖回真实值
      },
      launch: { urls: [], extraArgs: [], extensions: [], ...(p.launch || {}) },
      storage: { cloudSync: false, clearCacheAfterClose: false, ...(p.storage || {}) },
    };
  }

  /* 前端 payload → 后端参数（只挑认识字段，宽容忽略其余） */
  function fromUiProfile(payload) {
    const fp = payload.fingerprint || {};
    const out = {
      name: payload.name, group: payload.group, platform: payload.platform,
      country: payload.country, region: payload.region, city: payload.city,
      location_mode: payload.locationMode, notes: payload.notes, tags: payload.tags,
    };
    if (payload.account) out.account = {
      username: payload.account.username || "", password: payload.account.password || "",
      twoFactor: payload.account.twoFactor || "", cookies: payload.account.cookies || "",
    };
    if (payload.proxy) out.proxy = {
      type: payload.proxy.type || "no_proxy", host: payload.proxy.host || "",
      port: Number(payload.proxy.port) || 0, username: payload.proxy.username || "",
      password: payload.proxy.password || "",
    };
    if (payload.fingerprint) out.fingerprint = {
      browser: fp.browser || "sun", os: fp.os, device_type: fp.deviceType,
      kernel: fp.kernel || "",
      user_agent: fp.userAgent, language: fp.language, timezone: fp.timezone,
      resolution: fp.resolution, color_depth: fp.colorDepth,
      hardware_concurrency: fp.hardwareConcurrency, device_memory: fp.deviceMemory,
      webgl_vendor: fp.webglVendor, webgl_renderer: fp.webglRenderer,
      device_scale_factor: fp.browser === "safari" ? Number(fp.deviceScaleFactor) || 0 : undefined,  // sun/flower 不送, 保持脚本按 1.0
      canvas_noise: fp.canvasNoise, audio_noise: fp.audioNoise, webgl_noise: fp.webglNoise,
      webrtc_mode: fp.webrtcMode || "real", webrtc_public_ip: fp.webrtcPublicIp || "",
      headless: !!fp.headless,
    };
    if (payload.launch) out.launch = {
      urls: payload.launch.urls || [], extra_args: payload.launch.extraArgs || [],
      extensions: payload.launch.extensions || [],
    };
    if (payload.storage) out.storage = {
      cloud_sync: !!payload.storage.cloudSync,
      clear_cache_after_close: !!payload.storage.clearCacheAfterClose,
    };
    return out;
  }

  /* ---------- 设置映射 ---------- */
  function toUiSettings(s) {
    return {
      ...s,
      sunBrowserPath: s.chromium_path || "", flowerBrowserPath: s.firefox_path || "",
      safariBrowserPath: (s.kernel_paths && s.kernel_paths.safari) || "",
      kernelPaths: { ...(s.kernel_paths || {}) },
      browserPath: s.chromium_path || "", baseDebugPort: s.cdp_base_port || 43000,
      defaultHomepage: s.default_homepage || "about:blank", localApiPort: 53225,
      closeWithTaskTree: true, profileRoot: "", kernelRoot: "",
      globalPreferences: {}, proxySettings: { globalProxy: null },
      syncHotkeys: { enabled: false, toggle: "CommandOrControl+Alt+S", stop: "CommandOrControl+Alt+X", lastPayload: null },
      darkMode: !!s.dark_mode,
    };
  }

  function fromUiSettings(s) {
    const out = {};
    if (s.sunBrowserPath !== undefined) out.chromium_path = s.sunBrowserPath;
    if (s.flowerBrowserPath !== undefined) out.firefox_path = s.flowerBrowserPath;
    // kernel_paths 是 dict 且后端整字段覆盖: 带上完整 kernelPaths, 只改 safari; 空串=跟随自动探测, 不写键
    if (s.safariBrowserPath !== undefined || s.kernelPaths !== undefined) {
      const kp = { ...(s.kernelPaths || {}) };
      const safari = s.safariBrowserPath ?? (s.kernelPaths?.safari ?? "");
      delete kp.safari;
      if (safari) kp.safari = safari;
      out.kernel_paths = kp;
    }
    if (s.baseDebugPort !== undefined) out.cdp_base_port = Number(s.baseDebugPort) || 43000;
    if (s.defaultHomepage !== undefined) out.default_homepage = s.defaultHomepage;
    if (s.localApiPort !== undefined) out.api_port = Number(s.localApiPort) || 8325;
    if (s.darkMode !== undefined) out.dark_mode = !!s.darkMode;
    return out;
  }

  /* ---------- 代理映射 ---------- */
  function toUiProxy(p) {
    return { id: p.id, label: p.label || "", type: p.type || "http", host: p.host || "",
      port: p.port || 0, username: p.username || "", password: p.password || "",
      country: p.country || "", asn: p.asn || "", latency: p.latency_ms || 0 };
  }

  /* ---------- 指纹检查映射 ---------- */
  async function checkFingerprintProfile(id) {
    const d = await req("GET", `/environments/${id}/fingerprint/check`);
    const ua = d.ua || "", tz = d.tz || "";
    const checks = [
      { name: "User-Agent", pass: !!ua, detail: ua },
      { name: "时区", pass: !!tz, detail: tz },
      { name: "语言", pass: !!d.language, detail: d.language },
      { name: "Screen", pass: !!d.screen, detail: d.screen },
      { name: "WebGL", pass: !/error|none/.test(d.webgl || ""), detail: d.webgl },
      { name: "webdriver", pass: d.webdriver === false, detail: String(d.webdriver) },
    ];
    const ok = checks.every(c => c.pass);
    return { id, profileId: id, ok, checks };
  }

  /* ---------- 桥对象 ---------- */
  const api = {
    /* 环境 */
    listProfiles: async () => {
      const d = await req("GET", "/environments?page_size=500");
      const groups = await req("GET", "/groups").catch(() => ({ groups: ["默认"] }));
      return { groups: groups.groups || ["默认"], profiles: (d.list || []).map(toUiProfile) };
    },
    /* 已登录账号 (登录态批量扫描: 离线读 cookie 库 / 运行中走 CDP 明文) */
    listLoginStates: async () => req("GET", "/environments/login-states"),
    createProfile: async (payload) => toUiProfile(await req("POST", "/environments", fromUiProfile(payload))),
    updateProfile: async (payload) => {
      const id = payload.id;
      return toUiProfile(await req("PUT", `/environments/${id}`, fromUiProfile(payload)));
    },
    bulkCreateProfiles: async (payload = {}) => {
      const created = [];
      for (const item of (payload.profiles || [])) {
        created.push(toUiProfile(await req("POST", "/environments", fromUiProfile(item))));
      }
      if (payload.count) {
        const d = await req("POST", "/environments/batch", { count: payload.count, base: fromUiProfile(payload.base || {}) });
        const list = await req("GET", "/environments?page_size=500");
        for (const e of list.list || []) if (d.ids.includes(e.id)) created.push(toUiProfile(e));
      }
      return created;
    },
    bulkUpdateProfiles: async (payload = {}) => {
      const out = [];
      for (const id of firstArr(payload)) {
        out.push(toUiProfile(await req("PUT", `/environments/${id}`, fromUiProfile(payload.patch || {}))));
      }
      return out;
    },
    duplicateProfiles: async (payload = {}) => {
      const out = [];
      for (const id of firstArr(payload)) out.push(toUiProfile(await req("POST", `/environments/${id}/duplicate`)));
      return out;
    },
    deleteProfiles: async (payload = {}) => {
      for (const id of firstArr(payload)) await req("DELETE", `/environments/${id}`);
      return { ok: true };
    },
    openProfiles: async (payload = {}) => {
      const out = [];
      for (const id of firstArr(payload)) {
        try { out.push(await req("POST", `/environments/${id}/start`)); }
        catch (e) { console.warn("open", id, e); }
      }
      return out;
    },
    closeProfiles: async (payload = {}) => {
      for (const id of firstArr(payload)) await req("POST", `/environments/${id}/stop`);
      return { ok: true };
    },
    clearCache: async (payload = {}) => {
      for (const id of firstArr(payload)) {
        await req("POST", `/environments/${id}/cache/clear`, { types: ["cookies", "localstorage", "indexeddb", "cache"] });
      }
      return { ok: true };
    },
    clearProfileData: async (payload = {}) => {
      for (const id of firstArr(payload)) await req("DELETE", `/environments/${id}`);
      return { ok: true };
    },
    checkFingerprint: async (payload = {}) => {
      const ids = firstArr(payload);
      const list = await req("GET", "/environments?page_size=500");
      const targets = ids.length ? (list.list || []).filter(e => ids.includes(e.id)) : (list.list || []).slice(0, 1);
      const results = [];
      for (const e of targets) {
        try { results.push(await checkFingerprintProfile(e.id)); }
        catch (err) { results.push({ id: e.id, ok: false, error: String(err) }); }
      }
      return { results };
    },
    openFingerprintSite: async (payload = {}) => {
      const url = payload.url || "https://pixelscan.net/";
      window.open(url, "_blank");
      return { ok: true };
    },

    /* 代理 */
    listProxies: async () => (await req("GET", "/proxies")).list.map(toUiProxy),
    saveProxies: async (payload) => {
      const items = Array.isArray(payload) ? payload : (payload.list || []);
      for (const p of items) {
        if (p.id) await req("DELETE", `/proxies/${p.id}`).catch(() => {});
        await req("POST", "/proxies", { label: p.label || "", type: p.type || "http", host: p.host || "", port: Number(p.port) || 0, username: p.username || "", password: p.password || "" });
      }
      return (await req("GET", "/proxies")).list.map(toUiProxy);
    },
    deleteProxies: async (payload = {}) => {
      for (const id of firstArr(payload)) await req("DELETE", `/proxies/${id}`);
      return { ok: true };
    },
    checkProxies: async (payload = {}) => {
      const list = await req("GET", "/proxies");
      const targets = firstArr(payload).length ? list.list.filter(p => firstArr(payload).includes(p.id)) : list.list;
      const results = [];
      for (const p of targets) {
        try {
          const r = await req("POST", "/proxies/check", { type: p.type, host: p.host, port: p.port, username: p.username, password: p.password });
          results.push({ id: p.id, ok: !!r.ok, ip: r.ip || "", country: r.country || "", asn: r.asn || "", latency: r.latency_ms || 0, error: r.error || "" });
        } catch (e) { results.push({ id: p.id, ok: false, error: String(e) }); }
      }
      return { results };
    },
    testProxy: async (payload) => {
      const p = payload.proxy || payload;
      const r = await req("POST", "/proxies/check", { type: p.type || "http", host: p.host || "", port: p.port || 0, username: p.username || "", password: p.password || "" });
      return { ok: !!r.ok, ip: r.ip || "", country: r.country || "", region: r.region || "", city: r.city || "", asn: r.asn || "", isp: r.isp || "", latency: r.latency_ms || 0, error: r.error || "" };
    },

    /* 扩展（存后端 + 绑定） */
    listExtensions: async () => (await req("GET", "/extensions")).list,
    saveExtensions: async (payload) => {
      const items = Array.isArray(payload) ? payload : (payload.list || []);
      for (const e of items) {
        if (e.id) await req("DELETE", `/extensions/${e.id}`).catch(() => {});
        await req("POST", "/extensions", { name: e.name || "", path: e.path || "", categories: e.categories || [], platforms: e.platforms || [], bind_env_ids: e.bindEnvIds || [] });
      }
      return (await req("GET", "/extensions")).list;
    },
    deleteExtensions: async (payload = {}) => {
      for (const id of firstArr(payload)) await req("DELETE", `/extensions/${id}`);
      return { ok: true };
    },
    bindExtensions: async (payload = {}) => {
      const id = payload.id || (firstArr(payload)[0]);
      const envIds = payload.envIds || payload.env_ids || [];
      if (id) await req("POST", `/extensions/${id}/bind`, envIds);
      return { ok: true };
    },

    /* 模板 */
    listTemplates: async () => ({ list: (await req("GET", "/templates")).list }),
    saveTemplate: async (payload) => {
      if (payload.id) await req("DELETE", `/templates/${payload.id}`).catch(() => {});
      const t = await req("POST", "/templates", { name: payload.name || "", description: payload.description || "", fingerprint: payload.fingerprint || {}, proxy_strategy: payload.proxyStrategy || {}, extensions: payload.extensions || [], launch: payload.launch || {} });
      return t;
    },
    deleteTemplates: async (payload = {}) => {
      for (const id of firstArr(payload)) await req("DELETE", `/templates/${id}`);
      return { ok: true };
    },
    applyTemplate: async (payload = {}) => {
      const id = payload.id || (firstArr(payload)[0]);
      const count = payload.count || 1;
      return await req("POST", `/templates/${id}/apply?count=${count}`);
    },

    /* 设置 */
    getSettings: async () => toUiSettings(await req("GET", "/settings")),
    updateSettings: async (payload) => toUiSettings(await req("PUT", "/settings", fromUiSettings(payload))),

    /* 日志 */
    listLogs: async (payload = {}) => {
      const d = await req("GET", `/logs?limit=${payload.limit || 100}`);
      return { list: (d.list || []).map(l => ({ at: l.at, action: l.action, detail: l.detail })) };
    },
    exportLogs: async () => {
      const d = await req("GET", "/logs?limit=500");
      return { list: d.list || [] };
    },

    /* 数据导出/导入 */
    exportData: async () => {
      const envs = await req("GET", "/environments?page_size=500");
      const proxies = await req("GET", "/proxies");
      const templates = await req("GET", "/templates");
      return { groups: ["默认"], profiles: (envs.list || []).map(e => ({ ...toUiProfile(e), runtime: null, status: "closed" })),
        proxies: (proxies.list || []).map(toUiProxy), templates: templates.list || [], extensions: [], rpaFlows: [], rpaTasks: [], rpaRuns: [], actionLogs: [], profileTemplates: [] };
    },
    importData: async () => {
      const input = document.createElement("input");
      input.type = "file"; input.accept = ".json";
      const file = await new Promise(res => { input.onchange = () => res(input.files[0]); input.click(); });
      if (!file) return { canceled: true };
      const data = JSON.parse(await file.text());
      let count = 0;
      for (const p of (data.profiles || [])) {
        try { await req("POST", "/environments", fromUiProfile(p)); count++; } catch (e) { console.warn(e); }
      }
      return { count };
    },

    /* RPA（v0.1 未实现，返回空结构） */
    listRpa: async () => ({ flows: [], runs: [] }),
    saveRpa: async () => { throw new Error("RPA 流程编排在 HX-Browser v0.1 未实现，请直接用 Python 爬虫框架接入 CDP"); },
    deleteRpa: async () => ({ ok: true }),
    duplicateRpa: async () => ({ ok: true }),
    runRpa: async () => ({ ok: true, runs: [] }),
    listRpaTasks: async () => ({ list: [] }),
    saveRpaTask: async () => { throw new Error("RPA 任务在 HX-Browser v0.1 未实现"); },
    deleteRpaTask: async () => ({ ok: true }),
    runRpaTask: async () => ({ ok: true }),

    /* 窗口同步（无 GUI，stub） */
    syncStatus: async () => ({ sessions: [] }),
    startSync: async () => ({ ok: false, error: "HX-Browser 为无头 Web 服务，不支持窗口同步" }),
    stopSync: async () => ({ ok: true }),
    arrangeSync: async () => ({ ok: true }),
    listDisplays: async () => ({ displays: [] }),

    /* 内核管理（用系统浏览器，stub） */
    listKernelSources: async () => ({ sources: [], local: [], kernelRoot: "" }),
    downloadKernel: async () => { throw new Error("HX-Browser 使用系统已装浏览器，请在设置页配置路径"); },
    openKernelFolder: async () => ({ ok: true }),

    /* Skill 手册 */
    getSkill: async () => req("GET", "/skill"),

    /* 运行时事件（轮询模拟） */
    onRuntimeEvent: (callback) => {      let last = JSON.stringify({});
      const timer = setInterval(async () => {
        try {
          const d = await req("GET", "/environments?page_size=500");
          const now = JSON.stringify((d.list || []).map(e => [e.id, e.runtime ? e.runtime.debug_port : null, e.runtime ? e.runtime.pid : null]));
          if (now !== last) {
            last = now;
            const running = {};
            for (const e of d.list || []) if (e.runtime) running[e.id] = { pid: e.runtime.pid, debugPort: e.runtime.debug_port, endpoint: e.runtime.cdp_http };
            callback({ at: Date.now(), event: "update", running });
          }
        } catch (e) { /* ignore */ }
      }, 4000);
      return () => clearInterval(timer);
    },
  };

  window.localEnv = api;
})();
