"""指纹注入（完整版）：Chromium MV3 扩展 + 随机指纹生成 + Firefox user.js。

覆盖维度（对标常见环境自检检查项）：
- UA / platform / Client Hints(userAgentData) 一致
- 语言 / 时区(TZ env + JS 兜底) / 分辨率 / DPR / 色深
- 硬件并发 / 内存 / 触控点 / 屏幕方向
- window.chrome 存在性 / permissions 一致性 / doNotTrack
- Canvas / WebGL / Audio 噪声；WebGL vendor+renderer 覆写
- WebRTC 出口 IP 遮蔽（SDP 替换 + 启动参数）
- 媒体设备仿真 / 地理位置策略
"""

from __future__ import annotations

import json
import random
import uuid
from pathlib import Path

from . import locale
from .models import Environment

# ---------------- 随机指纹池 ----------------
UA_PLATFORMS = {
    "Windows": {
        "platform": "Win32",
        "ua_platform": "Windows NT 10.0; Win64; x64",
        "hint_platform": "Windows",
        "hint_platform_version": "15.0.0",
        "hint_architecture": "x86",
    },
    "macOS": {
        "platform": "MacIntel",
        "ua_platform": "Macintosh; Intel Mac OS X 10_15_7",
        "hint_platform": "macOS",
        "hint_platform_version": "15.0.0",
        "hint_architecture": "",
    },
    "Linux": {
        "platform": "Linux x86_64",
        "ua_platform": "X11; Linux x86_64",
        "hint_platform": "Linux",
        "hint_platform_version": "",
        "hint_architecture": "x86",
    },
}

TZ_POOL = [
    "America/New_York", "America/Chicago", "America/Los_Angeles", "America/Denver",
    "Europe/London", "Europe/Berlin", "Europe/Paris", "Europe/Amsterdam", "Europe/Madrid",
    "Asia/Tokyo", "Asia/Seoul", "Asia/Singapore", "Asia/Hong_Kong", "Asia/Taipei",
    "Asia/Shanghai", "Australia/Sydney", "America/Sao_Paulo", "America/Toronto",
]

LANG_POOL = {
    "en-US": ["en-US", "en"],
    "en-GB": ["en-GB", "en"],
    "de-DE": ["de-DE", "de"],
    "fr-FR": ["fr-FR", "fr"],
    "ja-JP": ["ja-JP", "ja"],
    "zh-CN": ["zh-CN", "zh"],
    "zh-TW": ["zh-TW", "zh"],
    "ko-KR": ["ko-KR", "ko"],
    "es-ES": ["es-ES", "es"],
    "pt-BR": ["pt-BR", "pt"],
}

RESOLUTION_POOL = [
    (1920, 1080, 1.0), (1920, 1080, 1.25), (1920, 1080, 1.5),
    (1366, 768, 1.0), (1536, 864, 1.0), (1536, 864, 1.25),
    (1440, 900, 1.0), (1440, 900, 1.25), (1280, 800, 1.0),
    (2560, 1440, 1.5), (2560, 1440, 2.0), (1920, 1200, 1.0),
]

# 2026-10-10 按 OS 分池。旧池 6 条里 5 条是 Direct3D11/D3D11 —— 那是 Windows 专属后端,
# 配 os="Linux" 就是新的自相矛盾 (UA/platform 说 Linux, WebGL 说 D3D11)。
# 判据同 cloak_launch.py: 画像必须与宿主同源; 实测 Windows 画像在 Linux 宿主上会被 DataDome 拦。
WEBGL_BY_OS = {
    "Windows": [
        ("Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 Direct3D11 vs_5_0 ps_5_0, D3D11)"),
        ("Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce RTX 2070 SUPER Direct3D11 vs_5_0 ps_5_0, D3D11)"),
        ("Google Inc. (AMD)", "ANGLE (AMD, AMD Radeon RX 6600 Direct3D11 vs_5_0 ps_5_0, D3D11)"),
        ("Google Inc. (Intel)", "ANGLE (Intel, Intel(R) UHD Graphics 630 Direct3D11 vs_5_0 ps_5_0, D3D11)"),
        ("Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce GTX 1660 Ti Direct3D11 vs_5_0 ps_5_0, D3D11)"),
    ],
    "Linux": [
        ("Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 OpenGL 4.5)"),
        ("Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce GTX 1660 Ti OpenGL 4.5)"),
        ("Google Inc. (AMD)", "ANGLE (AMD, AMD Radeon RX 6600 OpenGL 4.5)"),
        ("Google Inc. (Intel)", "ANGLE (Intel, Mesa Intel(R) UHD Graphics 630 (CML), OpenGL 4.6)"),
        ("Google Inc. (Intel)", "ANGLE (Intel, Mesa Intel(R) Xe Graphics (TGL LP), OpenGL 4.6)"),
    ],
    "macOS": [
        ("Google Inc. (Apple)", "ANGLE (Apple, Apple M2 Pro, OpenGL 4.1)"),
        ("Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 Metal 3.1)"),
        ("Google Inc. (AMD)", "ANGLE (AMD, AMD Radeon Pro 5500M Metal 3.1)"),
    ],
}
# 向后兼容: 旧名字仍被外部引用, 指向 Windows 池 (最大的一档)。
WEBGL_POOL = WEBGL_BY_OS["Windows"]


def webgl_for(os_name: str) -> tuple[str, str]:
    """按 os 选一对自洽的 (vendor, renderer) —— 同一事实只此一处定义。"""
    return random.choice(WEBGL_BY_OS.get(os_name, WEBGL_BY_OS["Linux"]))


# 2026-10-10 OS_POOL 加权到宿主平台: 本仓宿主是 Linux, 旧权重 3/5 是 Windows。
OS_POOL = ["Linux", "Linux", "Linux", "Linux", "Linux", "Linux", "macOS", "Windows"]

# ---------------- 注入脚本（完整版） ----------------
_CONTENT_JS = r"""
(() => {
  if (window.__hx_fp_injected__) return;
  window.__hx_fp_injected__ = true;
  const FP = %FP_JSON%;
  const def = (obj, key, getter) => {
    try { Object.defineProperty(obj, key, { configurable: true, get: getter }); } catch (e) {}
  };

  // ---------- navigator 基础 ----------
  if (FP.user_agent) def(Navigator.prototype, 'userAgent', () => FP.user_agent);
  if (FP.app_version) def(Navigator.prototype, 'appVersion', () => FP.app_version);
  if (FP.platform) def(Navigator.prototype, 'platform', () => FP.platform);
  if (FP.language) def(Navigator.prototype, 'language', () => FP.language);
  if (FP.languages) def(Navigator.prototype, 'languages', () => FP.languages.slice());
  if (FP.hardware_concurrency) def(Navigator.prototype, 'hardwareConcurrency', () => FP.hardware_concurrency);
  if (FP.device_memory) def(Navigator.prototype, 'deviceMemory', () => FP.device_memory);
  if (FP.max_touch_points !== undefined) def(Navigator.prototype, 'maxTouchPoints', () => FP.max_touch_points);
  if (FP.do_not_track !== undefined) def(Navigator.prototype, 'doNotTrack', () => FP.do_not_track);
  def(Navigator.prototype, 'webdriver', () => false);

  // ---------- Client Hints (userAgentData) ----------
  try {
    if (FP.user_agent_data && typeof navigator.userAgentData !== 'undefined') {
      const uad = {
        brands: FP.client_hints_brands || [],
        mobile: false,
        platform: FP.hint_platform || 'Windows',
        architecture: FP.hint_architecture || '',
        bitness: '64',
        model: '',
        platformVersion: FP.hint_platform_version || '',
        uaFullVersion: FP.ua_full_version || '',
        fullVersionList: FP.client_hints_brands || [],
        getHighEntropyValues: async () => ({
          architecture: FP.hint_architecture || '',
          bitness: '64', brands: FP.client_hints_brands || [], fullVersionList: FP.client_hints_brands || [],
          model: '', mobile: false, platform: FP.hint_platform || 'Windows', platformVersion: FP.hint_platform_version || '',
          uaFullVersion: FP.ua_full_version || '', wow64: false
        }),
        toJSON: () => ({ brands: FP.client_hints_brands || [], mobile: false, platform: FP.hint_platform || 'Windows' }),
      };
      def(Navigator.prototype, 'userAgentData', () => uad);
    }
  } catch (e) {}

  // ---------- window.chrome 存在性 ----------
  try {
    if (!window.chrome) {
      window.chrome = { loadTimes: () => ({}), csi: () => ({}), app: {}, runtime: {}, webstore: {} };
    }
  } catch (e) {}

  // ---------- permissions 一致性（notifications 与 Notification.permission 对齐） ----------
  try {
    const origQuery = Navigator.prototype.permissions && Navigator.prototype.permissions.query;
    if (origQuery && FP.notification_permission) {
      const fake = { state: 'prompt', onchange: null, addEventListener() {}, removeEventListener() {} };
      const perms = { query: (desc) => {
        if (desc && desc.name === 'notifications') return Promise.resolve({ ...fake, state: FP.notification_permission });
        return origQuery.call(navigator.permissions, desc);
      } };
      def(Navigator.prototype, 'permissions', () => perms);
      def(Notification, 'permission', () => FP.notification_permission);
    }
  } catch (e) {}

  // ---------- screen / viewport ----------
  if (FP.width && FP.height) {
    try {
      def(Screen.prototype, 'width', () => FP.width);
      def(Screen.prototype, 'height', () => FP.height);
      def(Screen.prototype, 'availWidth', () => FP.width);
      def(Screen.prototype, 'availHeight', () => FP.height);
      if (FP.color_depth) def(Screen.prototype, 'colorDepth', () => FP.color_depth);
      if (FP.color_depth) def(Screen.prototype, 'pixelDepth', () => FP.color_depth);
      def(window, 'outerWidth', () => FP.width);
      def(window, 'outerHeight', () => FP.height);
      def(window, 'innerWidth', () => FP.width);
      def(window, 'innerHeight', () => FP.height);
      if (FP.pixel_ratio) def(window, 'devicePixelRatio', () => FP.pixel_ratio);
      if (FP.orientation) {
        try { def(screen.orientation, 'type', () => FP.orientation); } catch (e) {}
      }
    } catch (e) {}
  }

  // ---------- timezone ----------
  if (typeof FP.timezone_offset === 'number') {
    try { def(Date.prototype, 'getTimezoneOffset', () => FP.timezone_offset); } catch (e) {}
  }
  try {
    const tzName = FP.timezone_name;
    if (tzName && Intl.DateTimeFormat) {
      const origResolved = Intl.DateTimeFormat.prototype.resolvedOptions;
      Intl.DateTimeFormat.prototype.resolvedOptions = function () {
        const r = origResolved.call(this);
        try { r.timeZone = tzName; } catch (e) {}
        return r;
      };
    }
  } catch (e) {}

  // ---------- Canvas / measureText / WebGL readPixels / Audio 噪声: 按副本 seed, 见 noise.js ----------
  %NOISE_JS%

  // ---------- WebGL ----------
  try {
    const _getParam = WebGLRenderingContext.prototype.getParameter;
    WebGLRenderingContext.prototype.getParameter = function(p) {
      if (p === 37445 && FP.webgl_vendor) return FP.webgl_vendor;
      if (p === 37446 && FP.webgl_renderer) return FP.webgl_renderer;
      const v = _getParam.call(this, p);
      return v;
    };
    const _getParam2 = WebGL2RenderingContext.prototype.getParameter;
    WebGL2RenderingContext.prototype.getParameter = function(p) {
      if (p === 37445 && FP.webgl_vendor) return FP.webgl_vendor;
      if (p === 37446 && FP.webgl_renderer) return FP.webgl_renderer;
      const v = _getParam2.call(this, p);
      return v;
    };
  } catch (e) {}

  // ---------- WebRTC 出口 IP 遮蔽 (2026-10-10 重写) ----------
  // 旧实现只对 SDP 做字符串替换, 两个致命问题:
  //   1. STUN 包在 createOffer 之前就已发出 —— 真实 IP 早被 STUN 服务器记下,
  //      事后改 SDP 改不了"已经泄露", 只是让 JS 读到的候选变好看;
  //   2. 整段被 (webrtc_public_ip || hide_webrtc) 包着, 默认 webrtc_mode="real"
  //      → hide_webrtc=false → 整段不执行, 一个字节都不改。
  // 实测 (2026-10-10, Chrome 154 + 可用无凭据中继 + 台湾住宅出口 114.33.139.168):
  //   --force-webrtc-ip-handling-policy=disable_non_proxied_udp 与 --proxy-bypass-list=
  //   四组交叉 (仅代理/仅bypass/两者/无代理+rtc) 全部仍吐出 203.10.99.10 与
  //   14.154.58.59 => 该 flag 在本机 google-chrome-stable 上零效果。
  //   对照: 同参数下 CloakBrowser 146 七个 STUN 全 empty (零 srflx 候选)。
  //   => 差异在二进制是否打过补丁, 不在参数。Chrome 154 只能靠 JS 层拦。
  // 正确做法: 在构造那一刻就不让 srflx 候选产生 —— 清空 iceServers,
  // STUN 包根本不发, 泄露面从源头消失。
  // (2026-10-10) 此处原有一条 Agent Note 回链, 指向的 note 从未落盘 —— 悬空回链会让
  // verify-all 的 backlinks 门禁失败。决策内容保留在本注释上方, 待该 note 补齐后恢复回链。
  // ---------- WebRTC: child realms (hx-child-realm, 2026-10-25) ----------
  // PR #12 之后 CreepJS 仍报 typ srflx: 怀疑它从同源 about:blank / srcdoc iframe 的
  // contentWindow 取干净的 RTCPeerConnection, 绕过本 realm 的 hook。拿到子 window 时同步打补丁。
  if (FP.hide_webrtc) {
    try {
      const patched = new WeakSet();
      patched.add(window);
      const patchRealm = function (w) {
        try {
          if (!w || patched.has(w)) return;
          patched.add(w);
          const Real = w.RTCPeerConnection || w.webkitRTCPeerConnection;
          if (!Real) return;
          const Wrapped = function (cfg) {
            const safe = Object.assign({}, cfg || {}, { iceServers: [] });
            try { return new Real(Object.assign({}, safe, { iceTransportPolicy: 'relay' })); }
            catch (e) { return new Real(safe); }
          };
          Wrapped.prototype = Real.prototype;
          ['RTCPeerConnection', 'webkitRTCPeerConnection'].forEach(function (k) {
            if (w[k]) Object.defineProperty(w, k, { configurable: true, writable: true, value: Wrapped });
          });
        } catch (e) {}
      };
      const hookGetter = function (proto, prop) {
        const d = Object.getOwnPropertyDescriptor(proto, prop);
        if (!d || !d.get) return;
        const g = d.get;
        Object.defineProperty(proto, prop, Object.assign({}, d, {
          get: function () {
            const v = g.call(this);
            try { patchRealm(prop === 'contentDocument' ? (v && v.defaultView) : v); } catch (e) {}
            return v;
          },
        }));
      };
      [window.HTMLIFrameElement, window.HTMLFrameElement, window.HTMLObjectElement].forEach(function (C) {
        if (!C) return;
        hookGetter(C.prototype, 'contentWindow');
        hookGetter(C.prototype, 'contentDocument');
      });
      const _open = window.open;
      if (_open) {
        window.open = function () { const w = _open.apply(this, arguments); patchRealm(w); return w; };
      }
    } catch (e) {}
  }

  if (FP.hide_webrtc) {
    try {
      const RealPC = window.RTCPeerConnection || window.webkitRTCPeerConnection;
      if (RealPC) {
        const _PC = function (cfg) {
          const orig = cfg || {};
          const safe = Object.assign({}, orig);
          safe.iceServers = [];
          safe.iceTransportPolicy = 'relay';
          let pc;
          try {
            pc = new RealPC(safe);
          } catch (e) {
            pc = new RealPC(Object.assign({}, orig, { iceServers: [] }));
          }
          return pc;
        };
        _PC.prototype = RealPC.prototype;
        ['createDataChannel','createOffer','createAnswer','setLocalDescription',
         'setRemoteDescription','addIceCandidate','getConfiguration','getStats',
         'close','addEventListener','removeEventListener','dispatchEvent']
          .forEach(function (k) { try { _PC.prototype[k] = RealPC.prototype[k]; } catch (e) {} });
        Object.defineProperty(window, 'RTCPeerConnection', {
          configurable: true, writable: true, value: _PC,
        });
        if (window.webkitRTCPeerConnection) {
          Object.defineProperty(window, 'webkitRTCPeerConnection', {
            configurable: true, writable: true, value: _PC,
          });
        }
      }
    } catch (e) {}
  }

  // ---------- 媒体设备仿真 ----------
  if (FP.fake_media_devices) {
    try {
      const origEnum = navigator.mediaDevices && navigator.mediaDevices.enumerateDevices;
      if (origEnum) {
        const devices = [
          { deviceId: 'default', kind: 'audioinput', label: '', groupId: 'default' },
          { deviceId: 'default', kind: 'videoinput', label: '', groupId: 'default' },
        ];
        def(navigator.mediaDevices, 'enumerateDevices', () => Promise.resolve(devices));
      }
    } catch (e) {}
  }

  // ---------- 暗色主题：JS 层 matchMedia 兜底（CSS 渲染由 CDP / --force-dark-mode 处理） ----------
  if (FP.dark_mode) {
    try {
      const _mm = window.matchMedia.bind(window);
      window.matchMedia = (query) => {
        const m = _mm(query);
        if (/prefers-color-scheme/i.test(query)) {
          try { Object.defineProperty(m, 'matches', { get: () => true }); } catch (e) {}
        }
        return m;
      };
      try {
        const m = _mm('(prefers-color-scheme: dark)');
        Object.defineProperty(m, 'matches', { get: () => true });
        Object.defineProperty(m, 'media', { get: () => '(prefers-color-scheme: dark)' });
      } catch (e) {}
      try { def(document.documentElement.style, 'colorScheme', () => 'dark'); } catch (e) {}
      try { document.documentElement.style.colorScheme = 'dark'; } catch (e) {}
    } catch (e) {}
  }
})();
"""

# ---------------- 随机指纹生成 ----------------
NOISE_JS_FILE = Path(__file__).with_name("noise.js")


def _noise_js() -> str:
    """按副本 seed 的 Canvas / measureText / WebGL readPixels / Audio 噪声 (源码见 noise.js)。"""
    return ";(%s)(FP, window);" % NOISE_JS_FILE.read_text("utf-8").strip()


def ua_for(os_name: str, major: str) -> str:
    """按 os 与内核主版本拼 Chrome UA, 版本只留主版本 (Chrome UA 精简后的格式), 未知 os 按 Windows"""
    plat = UA_PLATFORMS.get(os_name, UA_PLATFORMS["Windows"])["ua_platform"]
    return f"Mozilla/5.0 ({plat}) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/{major}.0.0.0 Safari/537.36"


def gen_random_fingerprint(browser: str = "sun", os_name: str | None = None, country: str | None = None) -> dict:
    """按平台随机生成一套自洽指纹。"""
    os_name = os_name or random.choice(OS_POOL)
    os_cfg = UA_PLATFORMS.get(os_name, UA_PLATFORMS["Windows"])
    # 内核版本（真实探测）→ 构造一致的 UA / Client Hints
    kernel_major = _detect_kernel_major()
    chrome_ver = f"{kernel_major}.0.0.0" if kernel_major else "151.0.0.0"
    ua = ua_for(os_name, kernel_major or "151")

    # 地区倾向：country 命中则用该地区时区/语言
    # country -> tz 的映射在 locale.py —— 那里是**唯一**定义处 (api.py 的 match_ip
    # 对齐用的是同一份; 曾经两份各写一遍, 必然不同步)。
    country_tz = locale.tz_for_country(country or "")
    tz = country_tz or random.choice(TZ_POOL)
    lang = _lang_for_tz(tz)
    langs = LANG_POOL.get(lang, ["en-US", "en"])
    width, height, dpr = random.choice(RESOLUTION_POOL)
    vendor, renderer = webgl_for(os_name)   # 2026-10-10: 跟着 os 走
    concurrency = random.choice([8, 8, 12, 16, 16, 32])
    memory = random.choice([4, 8, 8, 16, 16, 32])
    brands = _brands_for(kernel_major or 151)

    return {
        "browser": browser,
        "kernel_version": "system",
        "os": os_name,
        "device_type": "desktop",
        "user_agent": ua,
        "language": ",".join(langs),
        "timezone": tz,
        "resolution": f"{width}x{height}",
        "color_depth": 24,
        "hardware_concurrency": concurrency,
        "device_memory": memory,
        "webgl_vendor": vendor,
        "webgl_renderer": renderer,
        "canvas_noise": random.random() < 0.9,
        "audio_noise": random.random() < 0.9,
        "webgl_noise": random.random() < 0.7,
        "webrtc_mode": "proxy",
        "webrtc_public_ip": "",
        "headless": False,
        "tls_mode": "none",
        # 注入脚本内部字段
        "_fp": {
            "app_version": f"5.0 ({os_cfg['ua_platform']}) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/{chrome_ver} Safari/537.36",
            "ua_full_version": chrome_ver,
            "hint_platform": os_cfg["hint_platform"],
            "hint_platform_version": os_cfg["hint_platform_version"],
            "hint_architecture": os_cfg["hint_architecture"],
            "client_hints_brands": brands,
            "pixel_ratio": dpr,
            "orientation": "landscape-primary",
            "do_not_track": None,
            "notification_permission": "denied",
            "fake_media_devices": True,
            "hide_webrtc": True,
        },
    }


def _detect_kernel_major() -> str:
    """探测本机 Chromium 内核主版本，保证 UA 与内核一致（最强的指纹一致性）。"""
    try:
        import shutil
        from .config import settings_manager
        exe = settings_manager.load().chromium_path
        if not exe:
            return ""
        import subprocess
        out = subprocess.run([exe, "--version"], capture_output=True, text=True, timeout=5)
        ver = out.stdout.strip().split()[-1]
        return ver.split(".")[0]
    except Exception:
        return ""


def _lang_for_tz(tz: str) -> str:
    mapping = {
        "America/": "en-US", "Europe/London": "en-GB", "Europe/Berlin": "de-DE",
        "Europe/Paris": "fr-FR", "Europe/Madrid": "es-ES", "Asia/Tokyo": "ja-JP",
        "Asia/Seoul": "ko-KR", "Asia/Shanghai": "zh-CN", "Asia/Taipei": "zh-TW",
        "Asia/Hong_Kong": "zh-HK", "Asia/Singapore": "en-SG", "Australia/": "en-AU",
        "America/Sao_Paulo": "pt-BR",
    }
    for prefix, lang in mapping.items():
        if tz.startswith(prefix):
            return lang
    return "en-US"


def _brands_for(major: int) -> list[dict]:
    return [
        {"brand": "Chromium", "version": str(major)},
        {"brand": f"Google Chrome", "version": str(major)},
        {"brand": "Not-A.Brand", "version": "99"},
    ]


# ---------------- 扩展 / user.js ----------------
def build_fingerprint_extension(env: Environment, profile_dir: Path, dark: bool = False) -> Path:
    """生成 MV3 指纹扩展目录，返回路径。"""
    from .guard import _fp_json_for  # 局部导入: guard 依赖本模块, 字典只在 guard 定义一次

    fp_json = _fp_json_for(env, dark, profile_dir)
    ext_dir = profile_dir / "fingerprint-extension"
    ext_dir.mkdir(parents=True, exist_ok=True)
    manifest = {
        "manifest_version": 3,
        "name": "HX Fingerprint",
        "version": "1.0.0",
        "permissions": [],
        "content_scripts": [
            {
                "matches": ["<all_urls>"],
                "js": ["content.js"],
                "run_at": "document_start",
                "world": "MAIN",
                "all_frames": True,
                "match_about_blank": True,
                "match_origin_as_fallback": True,
            }
        ],
    }
    (ext_dir / "manifest.json").write_text(json.dumps(manifest, indent=2), "utf-8")
    content = _CONTENT_JS.replace("%NOISE_JS%", _noise_js()).replace("%FP_JSON%", json.dumps(fp_json))
    (ext_dir / "content.js").write_text(content, "utf-8")
    return ext_dir


def build_firefox_userjs(env: Environment, profile_dir: Path, dark: bool = False) -> None:
    """Firefox：写 user.js（UA/语言/反自动化/WebRTC/暗色主题）。"""
    fp = env.fingerprint
    lines = [
        'user_pref("browser.shell.checkDefaultBrowser", false);',
        'user_pref("toolkit.telemetry.reportingpolicy.firstRun", false);',
        'user_pref("datareporting.healthreport.uploadEnabled", false);',
        'user_pref("dom.webdriver.enabled", false);',
        'user_pref("privacy.resistFingerprinting", false);',
        f'user_pref("intl.accept_languages", {json.dumps((fp.language or "en-US,en").replace(",", ", "))});',
    ]
    if fp.user_agent:
        lines.append(f'user_pref("general.useragent.override", {json.dumps(fp.user_agent)});')
    if fp.webrtc_mode == "disable":
        lines.append('user_pref("media.peerconnection.enabled", false);')
    if dark:
        # 1=暗色：浏览器 UI 暗色 + 网页内容 prefers-color-scheme: dark
        lines.append('user_pref("ui.systemUsesDarkTheme", 1);')
        lines.append('user_pref("layout.css.prefers-color-scheme.content", 1);')
        lines.append('user_pref("browser.theme.content-theme", 1);')
    profile_dir.mkdir(parents=True, exist_ok=True)
    (profile_dir / "user.js").write_text("\n".join(lines) + "\n", "utf-8")


def parse_resolution(res: str) -> tuple[int, int] | None:
    if not res or "x" not in res:
        return None
    try:
        w, h = res.lower().split("x")
        return int(w.strip()), int(h.strip())
    except Exception:
        return None


def platform_for(os_name: str) -> str:
    mapping = {
        "Windows": "Win32",
        "macOS": "MacIntel",
        "Linux": "Linux x86_64",
        "Android": "Linux armv8l",
        "iOS": "iPhone",
    }
    return mapping.get(os_name, "Win32")


def random_extension_id() -> str:
    return uuid.uuid4().hex
