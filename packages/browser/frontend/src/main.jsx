import React, { useEffect, useMemo, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  Archive,
  BadgePlus,
  BrushCleaning,
  Check,
  ChevronDown,
  CircleX,
  ClipboardList,
  CloudDownload,
  Copy,
  Cpu,
  FileCode2,
  Download,
  Edit3,
  ExternalLink,
  Folder,
  Globe,
  Grid2X2,
  HardDrive,
  Import,
  Layers,
  ListFilter,
  LockKeyhole,
  Monitor,
  Network,
  Package,
  Play,
  Plus,
  Radio,
  RefreshCcw,
  Search,
  Settings,
  Shield,
  Shuffle,
  Split,
  Sparkles,
  Square,
  SlidersHorizontal,
  Trash2,
  Upload,
  WandSparkles,
  Wifi,
  X
} from "lucide-react";
import "./styles.css";

const DEFAULT_GROUP = "默认";
const ALL_GROUPS = "全部";

const emptyProfile = {
  name: "",
  group: DEFAULT_GROUP,
  platform: "custom",
  customNo: "",
  country: "",
  region: "",
  city: "",
  locationMode: "match",
  tags: [],
  account: {
    username: "",
    password: "",
    twoFactor: "",
    cookies: ""
  },
  proxy: {
    type: "no_proxy",
    host: "",
    port: "",
    username: "",
    password: ""
  },
  fingerprint: {
    browser: "sun",
    kernelVersion: "system",
    os: "Windows",
    deviceType: "desktop",
    userAgent: "",
    language: "en-US,en",
    timezone: "UTC",
    resolution: "1280x820",
    colorDepth: 24,
    hardwareConcurrency: 8,
    deviceMemory: 8,
    webglVendor: "Google Inc. (NVIDIA)",
    webglRenderer: "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 Direct3D11 vs_5_0 ps_5_0, D3D11)",
    fonts: ["Arial", "Calibri", "Cambria", "Microsoft YaHei", "Segoe UI", "Tahoma", "Verdana"],
    mediaDevices: "real",
    doNotTrack: false,
    geolocation: "ask",
    ports: [],
    canvasNoise: true,
    audioNoise: true,
    clientRectsNoise: true,
    webglNoise: true,
    webglExtensions: false,
    deterministicRaster: false,
    disableWebRTC: true,
    webrtcMode: "proxy",
    webrtcPublicIp: "",
    tlsMode: "off",
    kernelMode: "auto",
    clientHintsMode: "auto",
    tlsProfile: "real",
    tlsMinVersion: "default",
    tlsHttp2: true,
    tlsQuic: true,
    webgpuMode: "match",
    webgpuVendor: "Google Inc. (NVIDIA)",
    webgpuRenderer: "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 Direct3D11 vs_5_0 ps_5_0, D3D11)",
    mobilePreset: "",
    deviceScaleFactor: 1,
    touchPoints: 0,
    screenOrientation: "portrait"
  },
  launch: {
    urls: [],
    extraArgs: [],
    extensions: []
  },
  storage: {
    cloudSync: false,
    clearCacheAfterClose: false
  },
  notes: ""
};

const randomPools = {
  names: ["FB-Warmup", "Google-Search", "TikTok-Shop", "Amazon-Store", "X-Account", "Shopify-Admin", "Ads-Test", "Creator"],
  groups: ["默认", "运营", "投放", "测试", "客户A", "客户B"],
  platforms: ["facebook", "google", "tiktok", "x", "amazon", "shopify", "custom"],
  countries: [
    ["CN", "Guangdong", "Shenzhen", "Asia/Shanghai", "zh-CN,zh,en-US,en"],
    ["US", "California", "Los Angeles", "America/Los_Angeles", "en-US,en"],
    ["US", "New York", "New York", "America/New_York", "en-US,en"],
    ["GB", "England", "London", "Europe/London", "en-GB,en"],
    ["DE", "Berlin", "Berlin", "Europe/Berlin", "de-DE,de,en-US,en"],
    ["JP", "Tokyo", "Tokyo", "Asia/Tokyo", "ja-JP,ja,en-US,en"],
    ["SG", "Singapore", "Singapore", "Asia/Singapore", "en-SG,en-US,en"]
  ],
  resolutions: ["1280x720", "1365x768", "1440x900", "1536x864", "1600x900", "1680x1050", "1920x1080", "1920x1200"],
  cpu: [4, 6, 8, 10, 12, 16],
  memory: [4, 8, 8],
  webglByOs: {
    Windows: [
      ["Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 Direct3D11 vs_5_0 ps_5_0, D3D11)"],
      ["Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce RTX 4060 Direct3D11 vs_5_0 ps_5_0, D3D11)"],
      ["Google Inc. (Intel)", "ANGLE (Intel, Intel(R) UHD Graphics 770 Direct3D11 vs_5_0 ps_5_0, D3D11)"],
      ["Google Inc. (AMD)", "ANGLE (AMD, AMD Radeon RX 6600 Direct3D11 vs_5_0 ps_5_0, D3D11)"],
      ["Google Inc. (AMD)", "ANGLE (AMD, AMD Radeon(TM) Graphics Direct3D11 vs_5_0 ps_5_0, D3D11)"]
    ],
    macOS: [
      ["Google Inc. (Apple)", "ANGLE (Apple, ANGLE Metal Renderer: Apple M1, Unoriented)"],
      ["Google Inc. (Apple)", "ANGLE (Apple, ANGLE Metal Renderer: Apple M2, Unoriented)"],
      ["Google Inc. (Apple)", "ANGLE (Apple, ANGLE Metal Renderer: Apple M2 Pro, Unoriented)"],
      ["Google Inc. (Apple)", "ANGLE (Apple, ANGLE Metal Renderer: Apple M3, Unoriented)"]
    ],
    Linux: [
      ["Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 (0x2504) OpenGL 4.5.0 NVIDIA)"],
      ["Mesa", "Mesa Intel(R) UHD Graphics 770 (ADL GT2)"],
      ["Google Inc. (AMD)", "ANGLE (AMD, AMD Radeon RX 6600 (radeonsi, navi23, LLVM 15.0.7, DRM 3.54), OpenGL 4.6)"]
    ],
    Android: [
      ["Qualcomm", "Adreno (TM) 740"],
      ["Qualcomm", "Adreno (TM) 730"],
      ["ARM", "Mali-G710"]
    ],
    iOS: [
      ["Apple Inc.", "Apple GPU"],
      ["Apple Inc.", "Apple GPU"]
    ]
  },
  fontsByOs: {
    Windows: [
      ["Arial", "Calibri", "Cambria", "Consolas", "Microsoft YaHei", "Segoe UI", "Tahoma", "Times New Roman", "Verdana"],
      ["Arial", "Calibri", "Courier New", "Segoe UI", "Segoe UI Emoji", "Tahoma", "Verdana"]
    ],
    macOS: [
      ["Helvetica Neue", "Helvetica", "Lucida Grande", "Menlo", "Geneva", "Arial", "Times New Roman", "PingFang SC", "Apple Color Emoji"],
      ["Helvetica Neue", "Menlo", "Monaco", "Arial", "Georgia", "Verdana", "Hiragino Sans GB", "Apple Color Emoji"]
    ],
    Linux: [
      ["DejaVu Sans", "DejaVu Sans Mono", "Liberation Sans", "Noto Sans", "Ubuntu", "FreeSans", "Arial"]
    ],
    Android: [
      ["Roboto", "Noto Sans", "Noto Color Emoji", "Droid Sans"]
    ],
    iOS: [
      ["Helvetica Neue", "Helvetica", "PingFang SC", "Arial", "Georgia", "Times New Roman"]
    ]
  },
  localesByOs: {
    Windows: [
      { country: "US", region: "California", city: "Los Angeles", timezone: "America/Los_Angeles", language: "en-US,en" },
      { country: "US", region: "New York", city: "New York", timezone: "America/New_York", language: "en-US,en-GB,en" },
      { country: "CN", region: "Guangdong", city: "Shenzhen", timezone: "Asia/Shanghai", language: "zh-CN,zh,en-US,en" },
      { country: "CN", region: "Shanghai", city: "Shanghai", timezone: "Asia/Shanghai", language: "zh-CN,en-US,en" },
      { country: "JP", region: "Tokyo", city: "Tokyo", timezone: "Asia/Tokyo", language: "ja-JP,ja,en-US,en" },
      { country: "DE", region: "Berlin", city: "Berlin", timezone: "Europe/Berlin", language: "de-DE,de,en-US,en" },
      { country: "GB", region: "England", city: "London", timezone: "Europe/London", language: "en-GB,en-US,en" }
    ],
    macOS: [
      { country: "US", region: "California", city: "San Francisco", timezone: "America/Los_Angeles", language: "en-US,en" },
      { country: "US", region: "New York", city: "New York", timezone: "America/New_York", language: "en-US,en-GB,en" },
      { country: "CN", region: "Shanghai", city: "Shanghai", timezone: "Asia/Shanghai", language: "zh-CN,zh,en-US,en" },
      { country: "CN", region: "Guangdong", city: "Shenzhen", timezone: "Asia/Shanghai", language: "zh-CN,en-US,en" },
      { country: "JP", region: "Tokyo", city: "Tokyo", timezone: "Asia/Tokyo", language: "ja-JP,ja,en-US,en" },
      { country: "GB", region: "England", city: "London", timezone: "Europe/London", language: "en-GB,en-US,en" },
      { country: "TW", region: "Taipei", city: "Taipei", timezone: "Asia/Taipei", language: "zh-TW,zh,en-US,en" }
    ],
    Linux: [
      { country: "US", region: "California", city: "San Francisco", timezone: "America/Los_Angeles", language: "en-US,en" },
      { country: "DE", region: "Berlin", city: "Berlin", timezone: "Europe/Berlin", language: "de-DE,de,en-US,en" },
      { country: "GB", region: "England", city: "London", timezone: "Europe/London", language: "en-GB,en-US,en" },
      { country: "CN", region: "Beijing", city: "Beijing", timezone: "Asia/Shanghai", language: "zh-CN,zh,en-US,en" }
    ],
    Android: [
      { country: "US", region: "California", city: "Los Angeles", timezone: "America/Los_Angeles", language: "en-US,en" },
      { country: "CN", region: "Guangdong", city: "Guangzhou", timezone: "Asia/Shanghai", language: "zh-CN,zh,en-US,en" },
      { country: "JP", region: "Tokyo", city: "Tokyo", timezone: "Asia/Tokyo", language: "ja-JP,ja,en-US,en" },
      { country: "KR", region: "Seoul", city: "Seoul", timezone: "Asia/Seoul", language: "ko-KR,ko,en-US,en" }
    ],
    iOS: [
      { country: "US", region: "California", city: "Los Angeles", timezone: "America/Los_Angeles", language: "en-US,en" },
      { country: "CN", region: "Shanghai", city: "Shanghai", timezone: "Asia/Shanghai", language: "zh-CN,zh,en-US,en" },
      { country: "JP", region: "Tokyo", city: "Tokyo", timezone: "Asia/Tokyo", language: "ja-JP,ja,en-US,en" },
      { country: "GB", region: "England", city: "London", timezone: "Europe/London", language: "en-GB,en-US,en" }
    ]
  },
  resolutionsByOs: {
    Windows: ["1366x768", "1440x900", "1536x864", "1600x900", "1920x1080", "1920x1200", "2560x1440"],
    macOS: ["1440x900", "1680x1050", "1512x982", "1728x1117", "1920x1080", "2560x1440"],
    Linux: ["1366x768", "1440x900", "1600x900", "1920x1080", "2560x1440"]
  },
  cpuByOs: {
    Windows: [4, 6, 8, 12, 16],
    macOS: [8, 8, 10, 12],
    Linux: [4, 8, 12, 16],
    Android: [6, 8],
    iOS: [6, 6]
  },
  mobileDevices: [
    { name: "Pixel 8 Pro", os: "Android", resolution: "412x915", scale: 3, touch: 5, uaModel: "Pixel 8 Pro" },
    { name: "Pixel 7", os: "Android", resolution: "412x915", scale: 2.625, touch: 5, uaModel: "Pixel 7" },
    { name: "Samsung Galaxy S23", os: "Android", resolution: "360x780", scale: 3, touch: 5, uaModel: "SM-S911B" },
    { name: "iPhone 15", os: "iOS", resolution: "393x852", scale: 3, touch: 5, uaModel: "iPhone" },
    { name: "iPhone 14 Pro", os: "iOS", resolution: "393x852", scale: 3, touch: 5, uaModel: "iPhone" },
    { name: "iPad Pro 11", os: "iOS", resolution: "834x1194", scale: 2, touch: 5, uaModel: "iPad" }
  ],
  tags: ["warmup", "ads", "shop", "social", "api", "manual", "proxy", "stable", "new"]
};

const platformSelectOptions = [
  { value: "custom", label: "自定义" },
  { value: "facebook", label: "Facebook" },
  { value: "google", label: "Google" },
  { value: "tiktok", label: "TikTok" },
  { value: "x", label: "X" },
  { value: "amazon", label: "Amazon" },
  { value: "shopify", label: "Shopify" }
];

const locationModeOptions = [
  { value: "match", label: "跟随出口 IP / 国家" },
  { value: "custom", label: "自定义不变" }
];

const proxyTypeOptions = [
  { value: "no_proxy", label: "不使用代理" },
  { value: "http", label: "HTTP" },
  { value: "https", label: "HTTPS" },
  { value: "socks5", label: "SOCKS5" }
];

const proxyModeOptions = [
  { value: "profile", label: "按环境代理" },
  { value: "global", label: "全局代理" },
  { value: "pool", label: "代理池轮换" }
];

const proxyRotateOptions = [
  { value: "round_robin", label: "按上次使用轮换" },
  { value: "random", label: "随机轮换" },
  { value: "least_used", label: "最少使用优先" }
];

const proxyCheckerOptions = [
  { value: "ip-api", label: "ip-api 地区 / ASN" },
  { value: "ipinfo", label: "ipinfo 地区 / ASN" },
  { value: "ipify", label: "ipify 仅出口 IP" }
];

const defaultProxySettings = {
  enabled: false,
  mode: "profile",
  rotateStrategy: "round_robin",
  checker: "ip-api",
  retryOnFail: true,
  maxRetries: 2,
  globalProxy: { type: "http", host: "", port: "", username: "", password: "" },
  filters: { country: "", region: "", city: "", asn: "", lastIp: "" }
};

const browserTypeOptions = [
  { value: "sun", label: "Chromium" },
  { value: "flower", label: "Firefox" }
];

const sunKernelOptions = [
  { value: "system", label: "跟随系统浏览器" },
  { value: "chrome-stable", label: "Chrome Stable" },
  { value: "chrome-beta", label: "Chrome Beta" },
  { value: "chrome-dev", label: "Chrome Dev" },
  { value: "chrome-canary", label: "Chrome Canary" },
  { value: "edge-stable", label: "Edge Stable" },
  { value: "edge-beta", label: "Edge Beta" },
  { value: "edge-dev", label: "Edge Dev" },
  { value: "edge-canary", label: "Edge Canary" }
];

const flowerKernelOptions = [
  { value: "system", label: "跟随系统浏览器" },
  { value: "firefox-release", label: "Firefox Release" },
  { value: "firefox-esr", label: "Firefox ESR" },
  { value: "firefox-beta", label: "Firefox Beta" },
  { value: "firefox-nightly", label: "Firefox Nightly" }
];

const osOptions = ["Windows", "macOS", "Linux", "Android", "iOS"];
const deviceTypeOptions = ["desktop", "mobile", "tablet"].map((value) => ({ value, label: value[0].toUpperCase() + value.slice(1) }));

const clientHintsOptions = [
  { value: "auto", label: "跟随 UA 自动匹配" },
  { value: "off", label: "关闭 JS UAData 注入" }
];

const tlsProfileOptions = [
  { value: "real", label: "真实内核 TLS" },
  { value: "strict", label: "严格跟随内核版本" }
];

const tlsMinVersionOptions = [
  { value: "default", label: "默认" },
  { value: "tls1.2", label: "TLS 1.2" },
  { value: "tls1.3", label: "TLS 1.3" }
];

const webgpuOptions = [
  { value: "match", label: "跟随 WebGL" },
  { value: "enable", label: "启用" },
  { value: "disable", label: "禁用" }
];

const orientationOptions = [
  { value: "portrait", label: "Portrait" },
  { value: "landscape", label: "Landscape" }
];

const mediaDeviceOptions = [
  { value: "real", label: "真实设备" },
  { value: "fake", label: "仿真设备(稳定,空标签)" },
  { value: "empty", label: "空设备列表" }
];

const webrtcModeOptions = [
  { value: "proxy", label: "可用 + 遮蔽 IP(推荐)" },
  { value: "real", label: "真实(不遮蔽,会暴露 IP)" },
  { value: "disable", label: "禁用(抛错,可被检测)" }
];

const tlsModeOptions = [
  { value: "off", label: "关闭(直连代理)" },
  { value: "chrome", label: "Chrome JA3/JA4 + 头顺序(经本地 tls-proxy)" }
];

const kernelModeOptions = [
  { value: "auto", label: "标准(JS 注入)" },
  { value: "fork", label: "Fork 内核(零 JS 痕迹,需自行编译)" }
];

const geolocationOptions = [
  { value: "ask", label: "询问" },
  { value: "block", label: "阻止" }
];

const defaultLocationShape = {
  os: ["Windows", "Windows", "Windows", "macOS"],
  resolutions: ["1366x768", "1440x900", "1536x864", "1600x900", "1680x1050", "1920x1080"]
};

const locationCountryDefaults = {
  CN: { aliases: ["CN", "CHINA", "中国", "大陆"], timezone: "Asia/Shanghai", language: "zh-CN,zh,en-US,en", os: ["Windows"], resolutions: ["1365x768", "1440x900", "1536x864", "1600x900", "1920x1080"] },
  HK: { aliases: ["HK", "HONGKONG", "HONG KONG", "香港"], timezone: "Asia/Hong_Kong", language: "zh-HK,zh-TW,zh,en-US,en" },
  TW: { aliases: ["TW", "TAIWAN", "台湾", "台灣"], timezone: "Asia/Taipei", language: "zh-TW,zh,en-US,en" },
  US: { aliases: ["US", "USA", "UNITEDSTATES", "UNITED STATES", "美国", "美國"], timezone: "America/New_York", language: "en-US,en" },
  GB: { aliases: ["GB", "UK", "UNITEDKINGDOM", "UNITED KINGDOM", "英国", "英國"], timezone: "Europe/London", language: "en-GB,en-US,en" },
  DE: { aliases: ["DE", "GERMANY", "德国", "德國"], timezone: "Europe/Berlin", language: "de-DE,de,en-US,en", os: ["Windows"] },
  JP: { aliases: ["JP", "JAPAN", "日本"], timezone: "Asia/Tokyo", language: "ja-JP,ja,en-US,en" },
  SG: { aliases: ["SG", "SINGAPORE", "新加坡"], timezone: "Asia/Singapore", language: "en-SG,en-US,en" },
  AU: { aliases: ["AU", "AUSTRALIA", "澳大利亚", "澳洲"], timezone: "Australia/Sydney", language: "en-AU,en-US,en" },
  CA: { aliases: ["CA", "CANADA", "加拿大"], timezone: "America/Toronto", language: "en-CA,en-US,en" },
  FR: { aliases: ["FR", "FRANCE", "法国", "法國"], timezone: "Europe/Paris", language: "fr-FR,fr,en-US,en" },
  IT: { aliases: ["IT", "ITALY", "意大利"], timezone: "Europe/Rome", language: "it-IT,it,en-US,en" },
  ES: { aliases: ["ES", "SPAIN", "西班牙"], timezone: "Europe/Madrid", language: "es-ES,es,en-US,en" },
  NL: { aliases: ["NL", "NETHERLANDS", "荷兰", "荷蘭"], timezone: "Europe/Amsterdam", language: "nl-NL,nl,en-US,en" },
  BR: { aliases: ["BR", "BRAZIL", "巴西"], timezone: "America/Sao_Paulo", language: "pt-BR,pt,en-US,en" },
  MX: { aliases: ["MX", "MEXICO", "墨西哥"], timezone: "America/Mexico_City", language: "es-MX,es,en-US,en" },
  KR: { aliases: ["KR", "KOREA", "SOUTH KOREA", "韩国", "韓國"], timezone: "Asia/Seoul", language: "ko-KR,ko,en-US,en" },
  TH: { aliases: ["TH", "THAILAND", "泰国", "泰國"], timezone: "Asia/Bangkok", language: "th-TH,th,en-US,en" },
  VN: { aliases: ["VN", "VIETNAM", "越南"], timezone: "Asia/Ho_Chi_Minh", language: "vi-VN,vi,en-US,en" },
  MY: { aliases: ["MY", "MALAYSIA", "马来西亚", "馬來西亞"], timezone: "Asia/Kuala_Lumpur", language: "ms-MY,ms,en-US,en" },
  ID: { aliases: ["ID", "INDONESIA", "印度尼西亚", "印尼"], timezone: "Asia/Jakarta", language: "id-ID,id,en-US,en" },
  PH: { aliases: ["PH", "PHILIPPINES", "菲律宾", "菲律賓"], timezone: "Asia/Manila", language: "en-PH,en-US,en" },
  IN: { aliases: ["IN", "INDIA", "印度"], timezone: "Asia/Kolkata", language: "en-IN,hi-IN,en-US,en" }
};

const locationRows = [
  ["CN", "Beijing", "Beijing", ["BEIJING", "北京"]],
  ["CN", "Shanghai", "Shanghai", ["SHANGHAI", "上海"]],
  ["CN", "Tianjin", "Tianjin", ["TIANJIN", "天津"]],
  ["CN", "Chongqing", "Chongqing", ["CHONGQING", "重庆"]],
  ["CN", "Guangdong", "Guangzhou", ["GUANGDONG", "GUANGZHOU", "广东", "广州"]],
  ["CN", "Guangdong", "Shenzhen", ["SHENZHEN", "深圳"]],
  ["CN", "Guangdong", "Dongguan", ["DONGGUAN", "东莞"]],
  ["CN", "Guangdong", "Foshan", ["FOSHAN", "佛山"]],
  ["CN", "Guangdong", "Zhuhai", ["ZHUHAI", "珠海"]],
  ["CN", "Guangdong", "Zhongshan", ["ZHONGSHAN", "中山"]],
  ["CN", "Zhejiang", "Hangzhou", ["ZHEJIANG", "HANGZHOU", "浙江", "杭州"]],
  ["CN", "Zhejiang", "Ningbo", ["NINGBO", "宁波"]],
  ["CN", "Zhejiang", "Wenzhou", ["WENZHOU", "温州"]],
  ["CN", "Zhejiang", "Jinhua", ["JINHUA", "金华"]],
  ["CN", "Zhejiang", "Yiwu", ["YIWU", "义乌"]],
  ["CN", "Jiangsu", "Nanjing", ["JIANGSU", "NANJING", "江苏", "南京"]],
  ["CN", "Jiangsu", "Suzhou", ["SUZHOU", "苏州"]],
  ["CN", "Jiangsu", "Wuxi", ["WUXI", "无锡"]],
  ["CN", "Jiangsu", "Changzhou", ["CHANGZHOU", "常州"]],
  ["CN", "Jiangsu", "Nantong", ["NANTONG", "南通"]],
  ["CN", "Shandong", "Jinan", ["SHANDONG", "JINAN", "山东", "济南"]],
  ["CN", "Shandong", "Qingdao", ["QINGDAO", "青岛"]],
  ["CN", "Shandong", "Yantai", ["YANTAI", "烟台"]],
  ["CN", "Shandong", "Weifang", ["WEIFANG", "潍坊"]],
  ["CN", "Shandong", "Linyi", ["LINYI", "临沂"]],
  ["CN", "Fujian", "Fuzhou", ["FUJIAN", "FUZHOU", "福建", "福州"]],
  ["CN", "Fujian", "Xiamen", ["XIAMEN", "厦门"]],
  ["CN", "Fujian", "Quanzhou", ["QUANZHOU", "泉州"]],
  ["CN", "Fujian", "Zhangzhou", ["ZHANGZHOU", "漳州"]],
  ["CN", "Henan", "Zhengzhou", ["HENAN", "ZHENGZHOU", "河南", "郑州"]],
  ["CN", "Henan", "Luoyang", ["LUOYANG", "洛阳"]],
  ["CN", "Henan", "Nanyang", ["NANYANG", "南阳"]],
  ["CN", "Henan", "Xinxiang", ["XINXIANG", "新乡"]],
  ["CN", "Hubei", "Wuhan", ["HUBEI", "WUHAN", "湖北", "武汉"]],
  ["CN", "Hubei", "Yichang", ["YICHANG", "宜昌"]],
  ["CN", "Hubei", "Xiangyang", ["XIANGYANG", "襄阳"]],
  ["CN", "Hubei", "Jingzhou", ["JINGZHOU", "荆州"]],
  ["CN", "Hunan", "Changsha", ["HUNAN", "CHANGSHA", "湖南", "长沙"]],
  ["CN", "Hunan", "Zhuzhou", ["ZHUZHOU", "株洲"]],
  ["CN", "Hunan", "Xiangtan", ["XIANGTAN", "湘潭"]],
  ["CN", "Hunan", "Yueyang", ["YUEYANG", "岳阳"]],
  ["CN", "Sichuan", "Chengdu", ["SICHUAN", "CHENGDU", "四川", "成都"]],
  ["CN", "Sichuan", "Mianyang", ["MIANYANG", "绵阳"]],
  ["CN", "Sichuan", "Leshan", ["LESHAN", "乐山"]],
  ["CN", "Sichuan", "Nanchong", ["NANCHONG", "南充"]],
  ["CN", "Shaanxi", "Xi'an", ["SHAANXI", "XIAN", "XI'AN", "陕西", "西安"]],
  ["CN", "Shaanxi", "Xianyang", ["XIANYANG", "咸阳"]],
  ["CN", "Shaanxi", "Baoji", ["BAOJI", "宝鸡"]],
  ["CN", "Liaoning", "Shenyang", ["LIAONING", "SHENYANG", "辽宁", "沈阳"]],
  ["CN", "Liaoning", "Dalian", ["DALIAN", "大连"]],
  ["CN", "Liaoning", "Anshan", ["ANSHAN", "鞍山"]],
  ["CN", "Jilin", "Changchun", ["JILIN", "CHANGCHUN", "吉林", "长春"]],
  ["CN", "Jilin", "Jilin", ["JILIN CITY", "吉林市"]],
  ["CN", "Heilongjiang", "Harbin", ["HEILONGJIANG", "HARBIN", "黑龙江", "哈尔滨"]],
  ["CN", "Heilongjiang", "Daqing", ["DAQING", "大庆"]],
  ["CN", "Hebei", "Shijiazhuang", ["HEBEI", "SHIJIAZHUANG", "河北", "石家庄"]],
  ["CN", "Hebei", "Tangshan", ["TANGSHAN", "唐山"]],
  ["CN", "Hebei", "Baoding", ["BAODING", "保定"]],
  ["CN", "Shanxi", "Taiyuan", ["SHANXI", "TAIYUAN", "山西", "太原"]],
  ["CN", "Anhui", "Hefei", ["ANHUI", "HEFEI", "安徽", "合肥"]],
  ["CN", "Anhui", "Wuhu", ["WUHU", "芜湖"]],
  ["CN", "Jiangxi", "Nanchang", ["JIANGXI", "NANCHANG", "江西", "南昌"]],
  ["CN", "Jiangxi", "Ganzhou", ["GANZHOU", "赣州"]],
  ["CN", "Guangxi", "Nanning", ["GUANGXI", "NANNING", "广西", "南宁"]],
  ["CN", "Guangxi", "Guilin", ["GUILIN", "桂林"]],
  ["CN", "Yunnan", "Kunming", ["YUNNAN", "KUNMING", "云南", "昆明"]],
  ["CN", "Yunnan", "Dali", ["DALI", "大理"]],
  ["CN", "Guizhou", "Guiyang", ["GUIZHOU", "GUIYANG", "贵州", "贵阳"]],
  ["CN", "Hainan", "Haikou", ["HAINAN", "HAIKOU", "海南", "海口"]],
  ["CN", "Hainan", "Sanya", ["SANYA", "三亚"]],
  ["CN", "Gansu", "Lanzhou", ["GANSU", "LANZHOU", "甘肃", "兰州"]],
  ["CN", "Qinghai", "Xining", ["QINGHAI", "XINING", "青海", "西宁"]],
  ["CN", "Ningxia", "Yinchuan", ["NINGXIA", "YINCHUAN", "宁夏", "银川"]],
  ["CN", "Xinjiang", "Urumqi", ["XINJIANG", "URUMQI", "新疆", "乌鲁木齐"], { timezone: "Asia/Urumqi" }],
  ["CN", "Inner Mongolia", "Hohhot", ["INNER MONGOLIA", "HOHHOT", "内蒙古", "呼和浩特"]],
  ["CN", "Tibet", "Lhasa", ["TIBET", "LHASA", "西藏", "拉萨"]],
  ["HK", "Hong Kong", "Hong Kong", []],
  ["TW", "Taipei", "Taipei", ["TAIPEI", "台北"]],
  ["TW", "Taichung", "Taichung", ["TAICHUNG", "台中"]],
  ["TW", "Kaohsiung", "Kaohsiung", ["KAOHSIUNG", "高雄"]],
  ["US", "California", "Los Angeles", ["CALIFORNIA", "CA", "LOS ANGELES", "LA"], { timezone: "America/Los_Angeles" }],
  ["US", "California", "San Francisco", ["SAN FRANCISCO", "SF"], { timezone: "America/Los_Angeles" }],
  ["US", "California", "San Jose", ["SAN JOSE"], { timezone: "America/Los_Angeles" }],
  ["US", "New York", "New York", ["NEW YORK", "NY"], { timezone: "America/New_York" }],
  ["US", "Texas", "Dallas", ["TEXAS", "TX", "DALLAS"], { timezone: "America/Chicago" }],
  ["US", "Texas", "Houston", ["HOUSTON"], { timezone: "America/Chicago" }],
  ["US", "Florida", "Miami", ["FLORIDA", "FL", "MIAMI"], { timezone: "America/New_York" }],
  ["US", "Washington", "Seattle", ["WASHINGTON", "SEATTLE"], { timezone: "America/Los_Angeles" }],
  ["US", "Illinois", "Chicago", ["ILLINOIS", "CHICAGO"], { timezone: "America/Chicago" }],
  ["US", "Massachusetts", "Boston", ["MASSACHUSETTS", "BOSTON"], { timezone: "America/New_York" }],
  ["US", "Georgia", "Atlanta", ["GEORGIA", "ATLANTA"], { timezone: "America/New_York" }],
  ["US", "Nevada", "Las Vegas", ["NEVADA", "LAS VEGAS"], { timezone: "America/Los_Angeles" }],
  ["US", "Arizona", "Phoenix", ["ARIZONA", "PHOENIX"], { timezone: "America/Phoenix" }],
  ["GB", "England", "London", ["ENGLAND", "LONDON"]],
  ["GB", "England", "Manchester", ["MANCHESTER"]],
  ["GB", "England", "Birmingham", ["BIRMINGHAM"]],
  ["GB", "Scotland", "Edinburgh", ["SCOTLAND", "EDINBURGH"]],
  ["DE", "Berlin", "Berlin", ["BERLIN"]],
  ["DE", "Bavaria", "Munich", ["BAVARIA", "MUNICH"]],
  ["DE", "Hesse", "Frankfurt", ["HESSE", "FRANKFURT"]],
  ["DE", "North Rhine-Westphalia", "Cologne", ["COLOGNE"]],
  ["JP", "Tokyo", "Tokyo", ["TOKYO", "东京", "東京"]],
  ["JP", "Osaka", "Osaka", ["OSAKA", "大阪"]],
  ["JP", "Kanagawa", "Yokohama", ["YOKOHAMA", "横滨"]],
  ["JP", "Aichi", "Nagoya", ["NAGOYA", "名古屋"]],
  ["JP", "Fukuoka", "Fukuoka", ["FUKUOKA", "福冈"]],
  ["SG", "Singapore", "Singapore", []],
  ["AU", "New South Wales", "Sydney", ["SYDNEY"], { timezone: "Australia/Sydney" }],
  ["AU", "Victoria", "Melbourne", ["MELBOURNE"], { timezone: "Australia/Melbourne" }],
  ["AU", "Queensland", "Brisbane", ["BRISBANE"], { timezone: "Australia/Brisbane" }],
  ["AU", "Western Australia", "Perth", ["PERTH"], { timezone: "Australia/Perth" }],
  ["CA", "Ontario", "Toronto", ["ONTARIO", "TORONTO"], { timezone: "America/Toronto" }],
  ["CA", "British Columbia", "Vancouver", ["VANCOUVER"], { timezone: "America/Vancouver" }],
  ["CA", "Quebec", "Montreal", ["QUEBEC", "MONTREAL"], { timezone: "America/Toronto", language: "fr-CA,en-CA,en-US,en" }],
  ["FR", "Ile-de-France", "Paris", ["PARIS"]],
  ["FR", "Auvergne-Rhone-Alpes", "Lyon", ["LYON"]],
  ["FR", "Provence-Alpes-Cote d'Azur", "Marseille", ["MARSEILLE"]],
  ["IT", "Lazio", "Rome", ["ROME"]],
  ["IT", "Lombardy", "Milan", ["MILAN"]],
  ["ES", "Madrid", "Madrid", ["MADRID"]],
  ["ES", "Catalonia", "Barcelona", ["BARCELONA"]],
  ["NL", "North Holland", "Amsterdam", ["AMSTERDAM"]],
  ["BR", "Sao Paulo", "Sao Paulo", ["SAO PAULO"]],
  ["BR", "Rio de Janeiro", "Rio de Janeiro", ["RIO DE JANEIRO"]],
  ["MX", "Mexico City", "Mexico City", ["MEXICO CITY"]],
  ["KR", "Seoul", "Seoul", ["SEOUL", "首尔", "首爾"]],
  ["KR", "Busan", "Busan", ["BUSAN"]],
  ["TH", "Bangkok", "Bangkok", ["BANGKOK"]],
  ["VN", "Ho Chi Minh", "Ho Chi Minh City", ["HO CHI MINH", "SAIGON"]],
  ["VN", "Hanoi", "Hanoi", ["HANOI"]],
  ["MY", "Kuala Lumpur", "Kuala Lumpur", ["KUALA LUMPUR"]],
  ["ID", "Jakarta", "Jakarta", ["JAKARTA"]],
  ["PH", "Metro Manila", "Manila", ["MANILA"]],
  ["IN", "Maharashtra", "Mumbai", ["MUMBAI"]],
  ["IN", "Karnataka", "Bengaluru", ["BENGALURU", "BANGALORE"]],
  ["IN", "Delhi", "New Delhi", ["DELHI", "NEW DELHI"]]
];

const locationProfiles = locationRows.map(([country, region, city, aliases = [], overrides = {}]) => {
  const defaults = locationCountryDefaults[country] || {};
  return {
    country,
    aliases: [...(defaults.aliases || []), ...aliases],
    region,
    city,
    timezone: overrides.timezone || defaults.timezone || "UTC",
    language: overrides.language || defaults.language || "en-US,en",
    os: overrides.os || defaults.os || defaultLocationShape.os,
    resolutions: overrides.resolutions || defaults.resolutions || defaultLocationShape.resolutions
  };
});

const api = window.localEnv;

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

function pick(items) {
  return items[Math.floor(Math.random() * items.length)];
}

function sample(items, min = 1, max = 3) {
  const copy = [...items].sort(() => Math.random() - 0.5);
  return copy.slice(0, Math.floor(Math.random() * (max - min + 1)) + min);
}

function randomToken(length = 8) {
  const chars = "abcdefghijkmnpqrstuvwxyz23456789";
  return Array.from({ length }, () => pick(chars)).join("");
}

function randomPassword() {
  return `${randomToken(6)}-${randomToken(5)}-${Math.floor(100 + Math.random() * 900)}`;
}

function normalizeBrowserType(value) {
  const browser = String(value || "sun").trim().toLowerCase();
  if (["flower", "flowerbrowser", "firefox", "mozilla"].includes(browser)) return "flower";
  return "sun";
}

function isMobileOs(os) {
  return os === "Android" || os === "iOS";
}

function languageVariants(language) {
  const base = String(language || "en-US,en");
  const first = base.split(",")[0].trim().toLowerCase();
  if (first.startsWith("zh-cn")) return ["zh-CN,zh,en-US,en", "zh-CN,zh-TW,zh,en", "zh-CN,en-US,en", "en-US,en,zh-CN,zh"];
  if (first.startsWith("zh-tw") || first.startsWith("zh-hk")) return [base, `${first},zh,en-US,en`, "en-US,en"];
  if (first.startsWith("ja")) return ["ja-JP,ja,en-US,en", "ja,en-US,en", "en-US,en,ja"];
  if (first.startsWith("ko")) return ["ko-KR,ko,en-US,en", "ko,en-US,en", "en-US,en,ko"];
  if (first.startsWith("de")) return ["de-DE,de,en-US,en", "de,en-US,en", "en-US,en,de"];
  if (first.startsWith("en-gb")) return ["en-GB,en-US,en", "en-GB,en", "en-US,en-GB,en"];
  if (first.startsWith("en")) return ["en-US,en", "en-US,en-GB,en", "en-GB,en-US,en"];
  return [base, `${first},en-US,en`];
}

function pickOsLocale(os, country) {
  const pool = randomPools.localesByOs[os] || randomPools.localesByOs.Windows;
  let code = "";
  if (country) {
    const resolved = locationFromProfile({ country });
    code = resolved.country || normalizeLocationToken(country);
  }
  const matched = code ? pool.filter((item) => item.country === code) : [];
  const locale = pick(matched.length ? matched : pool);
  return {
    ...locale,
    language: pick(languageVariants(locale.language))
  };
}

function pickMobileDevice(os, deviceType) {
  const wantTablet = deviceType === "tablet";
  let pool = randomPools.mobileDevices.filter((item) => item.os === os);
  if (!pool.length) pool = randomPools.mobileDevices.filter((item) => isMobileOs(item.os));
  if (wantTablet) {
    const tablets = pool.filter((item) => /iPad|tablet/i.test(item.name));
    if (tablets.length) pool = tablets;
  } else {
    const phones = pool.filter((item) => !/iPad/i.test(item.name));
    if (phones.length) pool = phones;
  }
  return pick(pool.length ? pool : randomPools.mobileDevices);
}

function pickOsWebgl(os) {
  return pick(randomPools.webglByOs[os] || randomPools.webglByOs.Windows);
}

function pickOsFonts(os) {
  return pick(randomPools.fontsByOs[os] || randomPools.fontsByOs.Windows);
}

function fingerprintFieldsForOs(os, existing = {}, opts = {}) {
  const browser = normalizeBrowserType(existing.browser);
  const requestedType = opts.deviceType || (isMobileOs(os) ? "mobile" : "desktop");
  const mobile = requestedType === "mobile" || requestedType === "tablet" || isMobileOs(os);
  const device = mobile ? pickMobileDevice(isMobileOs(os) ? os : (requestedType === "tablet" ? "iOS" : "Android"), requestedType) : null;
  const finalOs = device?.os || os;
  const locale = pickOsLocale(finalOs, opts.country);
  const webgl = pickOsWebgl(finalOs);
  const cpuPool = randomPools.cpuByOs[finalOs] || randomPools.cpu;
  return {
    geo: locale,
    fingerprint: {
      os: finalOs,
      deviceType: device ? (/iPad/i.test(device.name) ? "tablet" : "mobile") : "desktop",
      userAgent: randomUserAgent(finalOs, browser, device),
      language: locale.language,
      timezone: locale.timezone,
      resolution: device?.resolution || pick(randomPools.resolutionsByOs[finalOs] || randomPools.resolutions),
      colorDepth: finalOs === "macOS" ? pick([24, 24, 30]) : 24,
      hardwareConcurrency: pick(cpuPool),
      deviceMemory: device ? pick([4, 4, 8]) : pick(randomPools.memory),
      webglVendor: webgl[0],
      webglRenderer: webgl[1],
      webgpuVendor: webgl[0],
      webgpuRenderer: webgl[1],
      fonts: pickOsFonts(finalOs),
      mediaDevices: pick(["real", "fake", "fake"]),
      mobilePreset: device?.name || "",
      deviceScaleFactor: device?.scale || (finalOs === "macOS" ? 2 : finalOs === "Windows" ? pick([1, 1, 1.25, 1.5]) : 1),
      touchPoints: device?.touch || 0,
      screenOrientation: device ? "portrait" : "landscape",
      webgpuMode: "match"
    }
  };
}

function applyOsToProfile(profile, os, extra = {}) {
  const next = copyProfile(profile);
  const generated = fingerprintFieldsForOs(os, { ...next.fingerprint, ...extra }, {
    country: next.country,
    deviceType: extra.deviceType
  });
  const keepLocale = next.locationMode === "custom" || Boolean(String(next.country || "").trim());
  const language = next.fingerprint?.language;
  const timezone = next.fingerprint?.timezone;
  if (next.locationMode !== "custom" && !String(next.country || "").trim()) {
    next.country = generated.geo.country;
    next.region = generated.geo.region;
    next.city = generated.geo.city;
  }
  next.fingerprint = {
    ...next.fingerprint,
    ...generated.fingerprint,
    browser: normalizeBrowserType(next.fingerprint?.browser),
    language: keepLocale && language ? language : generated.fingerprint.language,
    timezone: keepLocale && timezone ? timezone : generated.fingerprint.timezone
  };
  return next;
}

function randomUserAgent(os = "Windows", browser = "sun", device = null) {
  const family = normalizeBrowserType(browser);
  const major = family === "flower" ? pick([128, 129, 130, 131, 132, 133]) : pick([136, 137, 138, 139, 140, 141]);
  if (os === "Android") {
    const model = device?.uaModel || device?.name || "Pixel 8 Pro";
    if (family === "flower") return `Mozilla/5.0 (Android 14; Mobile; rv:${major}.0) Gecko/${major}.0 Firefox/${major}.0`;
    return `Mozilla/5.0 (Linux; Android 14; ${model}) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/${major}.0.0.0 Mobile Safari/537.36`;
  }
  if (os === "iOS") {
    const target = device?.uaModel === "iPad" ? "iPad; CPU OS 17_5 like Mac OS X" : "iPhone; CPU iPhone OS 17_5 like Mac OS X";
    if (family === "flower") return `Mozilla/5.0 (${target}) AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/${major}.0 Mobile/15E148 Safari/605.1.15`;
    return `Mozilla/5.0 (${target}) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/${major}.0.0.0 Mobile/15E148 Safari/604.1`;
  }
  if (family === "flower") {
    if (os === "macOS") {
      return `Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:${major}.0) Gecko/20100101 Firefox/${major}.0`;
    }
    if (os === "Linux") {
      return `Mozilla/5.0 (X11; Linux x86_64; rv:${major}.0) Gecko/20100101 Firefox/${major}.0`;
    }
    return `Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:${major}.0) Gecko/20100101 Firefox/${major}.0`;
  }
  if (os === "macOS") {
    return `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/${major}.0.0.0 Safari/537.36`;
  }
  if (os === "Linux") {
    return `Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/${major}.0.0.0 Safari/537.36`;
  }
  return `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/${major}.0.0.0 Safari/537.36`;
}

function normalizeLocationToken(value) {
  return String(value || "").trim().toUpperCase().replace(/[\s_-]+/g, " ");
}

function locationFromProfile(profile = {}) {
  const country = normalizeLocationToken(profile.country);
  const region = normalizeLocationToken(profile.region);
  const city = normalizeLocationToken(profile.city);
  if (!country && !region && !city) {
    return {
      country: "",
      region: "",
      city: "",
      timezone: profile.fingerprint?.timezone || "",
      language: profile.fingerprint?.language || "en-US,en",
      os: defaultLocationShape.os,
      resolutions: defaultLocationShape.resolutions,
      aliases: []
    };
  }
  const joined = [country, region, city].filter(Boolean).join(" ");
  const direct = locationProfiles.find((item) => item.aliases.map(normalizeLocationToken).includes(country));
  const regional = locationProfiles.find((item) => item.aliases.some((alias) => joined.includes(normalizeLocationToken(alias))));
  const matched = regional || direct;
  if (!matched) {
    return {
      country: profile.country || "",
      region: profile.region || "",
      city: profile.city || "",
      timezone: profile.fingerprint?.timezone || "UTC",
      language: profile.fingerprint?.language || "en-US,en",
      os: defaultLocationShape.os,
      resolutions: defaultLocationShape.resolutions,
      aliases: []
    };
  }
  return {
    ...matched,
    country: profile.country || matched.country,
    region: profile.region || matched.region,
    city: profile.city || matched.city
  };
}

function randomLocationForCountry(countryValue) {
  const token = normalizeLocationToken(countryValue);
  if (!token) return pick(locationProfiles);
  const direct = locationProfiles.find((item) => item.aliases.map(normalizeLocationToken).includes(token));
  const countryCode = direct?.country || (/^[a-z]{2}$/i.test(token) ? token.toUpperCase() : "");
  const pool = countryCode ? locationProfiles.filter((item) => item.country === countryCode) : [];
  if (pool.length) return pick(pool);
  const defaults = locationCountryDefaults[countryCode];
  if (defaults) {
    return {
      country: countryCode,
      region: "",
      city: "",
      timezone: defaults.timezone || "UTC",
      language: defaults.language || "en-US,en",
      os: defaults.os || defaultLocationShape.os,
      resolutions: defaults.resolutions || defaultLocationShape.resolutions,
      aliases: defaults.aliases || []
    };
  }
  return pick(locationProfiles);
}

function applyLocationToProfile(profile, location = locationFromProfile(profile), keepCustomLocation = false) {
  return {
    ...profile,
    country: keepCustomLocation ? profile.country : (location.country || profile.country),
    region: keepCustomLocation ? profile.region : (location.region || profile.region),
    city: keepCustomLocation ? profile.city : (location.city || profile.city),
    fingerprint: {
      ...(profile.fingerprint || {}),
      language: location.language || profile.fingerprint?.language,
      timezone: location.timezone || profile.fingerprint?.timezone,
      geolocation: "ask"
    }
  };
}

function locationFromIpGeo(geo = {}) {
  const countryHint = geo.countryCode || geo.country || "";
  const matched = locationFromProfile({
    country: countryHint,
    region: geo.region || "",
    city: geo.city || ""
  });
  const rawCode = String(countryHint).trim().toUpperCase();
  const code = /^[A-Z]{2}$/.test(rawCode) ? rawCode : matched.country;
  const defaults = locationCountryDefaults[code] || {};
  return {
    country: code || matched.country,
    region: geo.region || matched.region,
    city: geo.city || matched.city,
    timezone: geo.timezone || defaults.timezone || matched.timezone,
    language: geo.language || defaults.language || matched.language
  };
}

function applyIpGeoToProfile(profile, geo = {}) {
  if (!geo || (!geo.ok && !geo.country && !geo.countryCode && !geo.timezone && !geo.ip)) return profile;
  const next = copyProfile(profile);
  if (geo.ip) next.fingerprint.webrtcPublicIp = geo.ip;
  if ((next.locationMode || "match") === "custom") return next;
  const applied = applyLocationToProfile(next, locationFromIpGeo(geo), false);
  applied.fingerprint.webrtcPublicIp = geo.ip || applied.fingerprint.webrtcPublicIp || "";
  return applied;
}

function randomFingerprint(existing = {}, location = null) {
  const locationObj = Array.isArray(location)
    ? { country: location[0], timezone: location[3], language: location[4], os: ["Windows", "Windows", "Windows", "macOS", "Linux"] }
    : (location || {});
  const browser = normalizeBrowserType(existing.browser);
  const wantsMobile = existing.deviceType === "mobile" || existing.deviceType === "tablet";
  let os = osOptions.includes(existing.os) ? existing.os : pick(locationObj.os || ["Windows", "Windows", "Windows", "macOS"]);
  if (wantsMobile && !isMobileOs(os)) os = existing.deviceType === "tablet" ? "iOS" : pick(["Android", "Android", "iOS"]);
  if (!wantsMobile && isMobileOs(os)) os = pick(["Windows", "Windows", "macOS", "Linux"]);
  const generated = fingerprintFieldsForOs(os, { ...existing, browser, deviceType: existing.deviceType }, {
    country: locationObj.country || existing.country,
    deviceType: wantsMobile ? existing.deviceType : "desktop"
  });
  return {
    ...emptyProfile.fingerprint,
    ...existing,
    ...generated.fingerprint,
    browser,
    language: locationObj.language || generated.fingerprint.language,
    timezone: locationObj.timezone || generated.fingerprint.timezone,
    clientHintsMode: "auto",
    tlsProfile: "real",
    tlsMinVersion: "default",
    tlsHttp2: true,
    tlsQuic: true,
    canvasNoise: true,
    audioNoise: true,
    clientRectsNoise: true,
    webglNoise: true,
    webglExtensions: false,
    deterministicRaster: false,
    disableWebRTC: true,
    webrtcMode: "proxy",
    webrtcPublicIp: "",
    tlsMode: "off",
    kernelMode: "auto",
    doNotTrack: false,
    geolocation: "ask",
    ports: sample(["3389", "5900", "9222"], 0, 2)
  };
}

function randomProfile(base = {}) {
  const pickedLocation = base.country || base.region || base.city ? locationFromProfile(base) : null;
  const location = pickedLocation || pick(randomPools.countries);
  const locationObj = Array.isArray(location)
    ? { country: location[0], region: location[1], city: location[2], timezone: location[3], language: location[4] }
    : location;
  const platform = pick(randomPools.platforms);
  const name = `${pick(randomPools.names)}-${randomToken(4).toUpperCase()}`;
  return {
    ...clone(emptyProfile),
    ...base,
    name,
    group: base.group || pick(randomPools.groups),
    platform: base.platform || platform,
    customNo: `LP-${Date.now().toString().slice(-6)}-${Math.floor(10 + Math.random() * 90)}`,
    country: locationObj.country,
    region: locationObj.region,
    city: locationObj.city,
    tags: sample(randomPools.tags, 2, 4),
    account: {
      username: `${platform}.${randomToken(7)}@example.local`,
      password: randomPassword(),
      twoFactor: randomToken(16).toUpperCase(),
      cookies: ""
    },
    fingerprint: randomFingerprint(base.fingerprint || {}, locationObj),
    launch: {
      urls: platform === "custom" ? ["about:blank"] : [`https://${platform}.com`],
      extraArgs: ["--disable-notifications"],
      extensions: []
    },
    notes: "随机生成的本地环境"
  };
}

function parseProxyString(value) {
  const raw = String(value || "").trim();
  if (!raw) return null;
  const text = raw.replace(/\s+/g, "");
  const fallback = { type: "http", host: "", port: "", username: "", password: "" };

  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(text)) {
    try {
      const parsed = new URL(text);
      const protocol = parsed.protocol.replace(":", "").toLowerCase();
      return {
        type: protocol === "socks" ? "socks5" : protocol,
        host: parsed.hostname,
        port: parsed.port,
        username: decodeURIComponent(parsed.username || ""),
        password: decodeURIComponent(parsed.password || "")
      };
    } catch {
      return null;
    }
  }

  const typeMatch = text.match(/^(http|https|socks5|socks):(.+)$/i);
  const type = typeMatch ? (typeMatch[1].toLowerCase() === "socks" ? "socks5" : typeMatch[1].toLowerCase()) : fallback.type;
  const body = typeMatch ? typeMatch[2].replace(/^\/\//, "") : text;
  const atIndex = body.lastIndexOf("@");
  if (atIndex > -1) {
    const auth = body.slice(0, atIndex);
    const hostPort = body.slice(atIndex + 1);
    const [host, port] = hostPort.split(":");
    const [username, ...passwordParts] = auth.split(":");
    return {
      type,
      host: host || "",
      port: port || "",
      username: decodeURIComponent(username || ""),
      password: decodeURIComponent(passwordParts.join(":") || "")
    };
  }

  const parts = body.split(":");
  if (parts.length >= 4) {
    const [host, port, username, ...passwordParts] = parts;
    return { type, host, port, username, password: passwordParts.join(":") };
  }
  if (parts.length >= 2) {
    const [host, port] = parts;
    return { ...fallback, type, host, port };
  }
  return null;
}

function normalizeProxySettingsForm(input = {}) {
  return {
    ...defaultProxySettings,
    ...(input || {}),
    globalProxy: { ...defaultProxySettings.globalProxy, ...((input || {}).globalProxy || {}) },
    filters: { ...defaultProxySettings.filters, ...((input || {}).filters || {}) }
  };
}

function proxyToLine(proxy = {}) {
  if (!proxy?.host || !proxy?.port) return "";
  const type = proxy.type && proxy.type !== "no_proxy" ? proxy.type : "http";
  const auth = proxy.username ? `${encodeURIComponent(proxy.username)}:${encodeURIComponent(proxy.password || "")}@` : "";
  return `${type}://${auth}${proxy.host}:${proxy.port}`;
}

function parseProxyImportLine(line) {
  const parts = String(line || "").split(/[|\t,]/).map((item) => item.trim());
  const [proxyString = "", country = "", region = "", city = "", asn = "", refreshUrl = "", group = ""] = parts;
  return {
    proxy_string: proxyString,
    country,
    region,
    city,
    asn,
    refreshUrl,
    group: group || undefined
  };
}

function ensureProfileShape(profile) {
  return {
    ...clone(emptyProfile),
    ...profile,
    account: { ...emptyProfile.account, ...(profile.account || {}) },
    proxy: { ...emptyProfile.proxy, ...(profile.proxy || {}) },
    fingerprint: { ...emptyProfile.fingerprint, ...(profile.fingerprint || {}) },
    launch: { ...emptyProfile.launch, ...(profile.launch || {}) },
    storage: { ...emptyProfile.storage, ...(profile.storage || {}) },
    tags: Array.isArray(profile.tags) ? profile.tags : fromTokens(profile.tags)
  };
}

function normalizeHomepage(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  if (/^(https?:|about:|file:|chrome:)/i.test(text)) return text;
  return `https://${text}`;
}

function toLines(value) {
  return Array.isArray(value) ? value.join("\n") : "";
}

function fromLines(value) {
  return String(value || "")
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function toTokens(value) {
  return Array.isArray(value) ? value.join(", ") : "";
}

function fromTokens(value) {
  return String(value || "")
    .split(/[,，\n]/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function formatDate(value) {
  if (!value) return "-";
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit"
  }).format(new Date(value));
}

class PanelErrorBoundary extends React.Component {
  constructor(props) {
    super(props);
    this.state = { error: null };
  }

  static getDerivedStateFromError(error) {
    return { error };
  }

  render() {
    if (!this.state.error) return this.props.children;
    const message = this.state.error?.message || String(this.state.error);
    return (
      <div className="emptyState">
        <CircleX size={34} />
        <strong>页面出错</strong>
        <span>{message}</span>
        <button className="accent" onClick={() => this.setState({ error: null })}>重试</button>
      </div>
    );
  }
}

function platformName(platform) {
  const map = {
    custom: "自定义",
    facebook: "Facebook",
    google: "Google",
    tiktok: "TikTok",
    x: "X",
    amazon: "Amazon",
    shopify: "Shopify"
  };
  return map[platform] || platform || "自定义";
}

function PlatformIcon({ platform }) {
  if (platform === "facebook") return <span>F</span>;
  if (platform === "google") return <span>G</span>;
  if (platform === "tiktok") return <span>T</span>;
  if (platform === "x") return <span>X</span>;
  if (platform === "amazon") return <span>A</span>;
  if (platform === "shopify") return <span>S</span>;
  return <Globe size={14} />;
}

function App() {
  const [uiTheme, setUiTheme] = useState(() => {
    const raw = window.localStorage.getItem("localPowerUiTheme") || "standard";
    if (raw === "bit" || raw === "workspace") return "workspace";
    return "standard";
  });
  const [colorMode, setColorMode] = useState(() => window.localStorage.getItem("localPowerColorMode") || "dark");
  const [profiles, setProfiles] = useState([]);
  const [groups, setGroups] = useState([DEFAULT_GROUP]);
  const [settings, setSettings] = useState(null);
  const [selected, setSelected] = useState(new Set());
  const [groupFilter, setGroupFilter] = useState(ALL_GROUPS);
  const [statusFilter, setStatusFilter] = useState("all");
  const [platformFilter, setPlatformFilter] = useState("all");
  const [query, setQuery] = useState("");
  const [editor, setEditor] = useState(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [bulkCreateOpen, setBulkCreateOpen] = useState(false);
  const [bulkEditOpen, setBulkEditOpen] = useState(false);
  const [activeSection, setActiveSection] = useState("profiles");
  const [toast, setToast] = useState("");
  const [notices, setNotices] = useState([]);
  const [confirmDialog, setConfirmDialog] = useState(null);
  const [taskDialog, setTaskDialog] = useState(null);
  const [busy, setBusy] = useState(false);
  const [moreMenuOpen, setMoreMenuOpen] = useState(false);
  const [loginStates, setLoginStates] = useState({});

  async function refresh() {
    const [profileResult, settingsResult] = await Promise.all([api.listProfiles(), api.getSettings()]);
    setProfiles(profileResult.profiles.map(ensureProfileShape));
    setGroups(profileResult.groups.length ? profileResult.groups : [DEFAULT_GROUP]);
    setSettings(settingsResult);
    api.listLoginStates?.().then(setLoginStates).catch(() => {});
  }

  useEffect(() => {
    refresh().catch(showError);
    return api.onRuntimeEvent(() => refresh().catch(showError));
  }, []);

  useEffect(() => {
    window.localStorage.setItem("localPowerUiTheme", uiTheme);
  }, [uiTheme]);

  useEffect(() => {
    window.localStorage.setItem("localPowerColorMode", colorMode);
  }, [colorMode]);

  function showToast(message) {
    setToast(message);
    window.clearTimeout(showToast.timer);
    showToast.timer = window.setTimeout(() => setToast(""), 2600);
    const id = `${Date.now()}_${Math.random()}`;
    setNotices((prev) => [...prev.slice(-3), { id, message, type: "success" }]);
    window.setTimeout(() => {
      setNotices((prev) => prev.filter((item) => item.id !== id));
    }, 3200);
  }

  function showError(error) {
    const message = error?.message || String(error);
    setToast(message);
    const id = `${Date.now()}_${Math.random()}`;
    setNotices((prev) => [...prev.slice(-3), { id, message, type: "error" }]);
    window.setTimeout(() => {
      setNotices((prev) => prev.filter((item) => item.id !== id));
    }, 4200);
  }

  function askConfirm(options) {
    return new Promise((resolve) => {
      setConfirmDialog({ ...options, resolve });
    });
  }

  const filteredProfiles = useMemo(() => {
    const text = query.trim().toLowerCase();
    return profiles.filter((profile) => {
      const groupOk = groupFilter === ALL_GROUPS || profile.group === groupFilter;
      const statusOk = statusFilter === "all" || profile.status === statusFilter;
      const platformOk = platformFilter === "all" || profile.platform === platformFilter;
      const textOk =
        !text ||
        [
          profile.name,
          profile.id,
          profile.serialNo,
          profile.customNo,
          profile.group,
          profile.platform,
          profile.account?.username,
          profile.proxy?.host,
          profile.country,
          profile.region,
          profile.city,
          profile.notes,
          ...(profile.tags || [])
        ]
          .filter(Boolean)
          .join(" ")
          .toLowerCase()
          .includes(text);
      return groupOk && statusOk && platformOk && textOk;
    });
  }, [profiles, groupFilter, statusFilter, platformFilter, query]);

  const selectedIds = Array.from(selected);
  const runningCount = profiles.filter((profile) => profile.status === "running").length;
  const proxyCount = profiles.filter((profile) => profile.proxy?.type !== "no_proxy").length;
  const platformOptions = Array.from(new Set(profiles.map((profile) => profile.platform).filter(Boolean)));

  function toggleSelected(id) {
    setSelected((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  }

  function toggleAllVisible() {
    setSelected((prev) => {
      const next = new Set(prev);
      const ids = filteredProfiles.map((profile) => profile.id);
      const allSelected = ids.length && ids.every((id) => next.has(id));
      ids.forEach((id) => (allSelected ? next.delete(id) : next.add(id)));
      return next;
    });
  }

  async function runAction(label, fn) {
    setBusy(true);
    setTaskDialog({ title: label, detail: "正在处理任务", phase: "running" });
    try {
      const result = await fn();
      await refresh();
      setTaskDialog({ title: label, detail: "任务完成", phase: "done" });
      window.setTimeout(() => setTaskDialog(null), 700);
      showToast(label);
      return result;
    } catch (error) {
      setTaskDialog({ title: "操作失败", detail: error?.message || String(error), phase: "error" });
      window.setTimeout(() => setTaskDialog(null), 1600);
      showError(error);
    } finally {
      setBusy(false);
    }
  }

  function openCreate(data = clone(emptyProfile)) {
    setEditor({ mode: "create", data, tab: "base" });
  }

  function openRandomCreate() {
    setEditor({ mode: "create", data: randomProfile(), tab: "base" });
  }

  function openEdit(profile) {
    setEditor({ mode: "edit", data: ensureProfileShape(profile), tab: "base" });
  }

  async function saveEditor(data) {
    await runAction(editor.mode === "create" ? "已创建环境" : "已保存环境", async () => {
      const payload = ensureProfileShape(data);
      if (editor.mode === "create") return api.createProfile(payload);
      return api.updateProfile(payload);
    });
    setEditor(null);
  }

  async function openProfiles(ids) {
    const target = ids?.length ? ids : selectedIds;
    if (!target.length) return showToast("请选择环境");
    if (target.length > 1) {
      const ok = await askConfirm({
        title: "批量打开环境",
        message: `确认打开 ${target.length} 个环境？任务会按本地浏览器进程逐个启动。`,
        confirmText: "打开"
      });
      if (!ok) return;
    }
    await runAction("启动任务已提交", () => api.openProfiles({ ids: target }));
  }

  async function closeProfiles(ids) {
    const target = ids?.length ? ids : selectedIds;
    if (!target.length) return showToast("请选择环境");
    if (target.length > 1) {
      const ok = await askConfirm({
        title: "批量关闭环境",
        message: `确认关闭 ${target.length} 个环境？`,
        confirmText: "关闭"
      });
      if (!ok) return;
    }
    await runAction("已关闭环境", () => api.closeProfiles({ ids: target }));
  }

  async function deleteProfiles(deleteCache = false) {
    if (!selectedIds.length) return showToast("请选择环境");
    const ok = await askConfirm({
      title: deleteCache ? "删除环境和缓存" : "删除环境",
      message: deleteCache ? `确认删除 ${selectedIds.length} 个环境，并清理本地缓存？` : `确认删除 ${selectedIds.length} 个环境？`,
      confirmText: "删除",
      danger: true
    });
    if (!ok) return;
    await runAction(deleteCache ? "已删除环境和缓存" : "已删除环境", () =>
      api.deleteProfiles({ ids: selectedIds, deleteCache })
    );
    setSelected(new Set());
  }

  async function duplicateProfiles() {
    if (!selectedIds.length) return showToast("请选择环境");
    if (selectedIds.length > 1) {
      const ok = await askConfirm({
        title: "批量复制环境",
        message: `确认复制 ${selectedIds.length} 个环境？`,
        confirmText: "复制"
      });
      if (!ok) return;
    }
    await runAction("已复制环境", () => api.duplicateProfiles({ ids: selectedIds }));
  }

  async function clearCache() {
    if (!selectedIds.length) return showToast("请选择环境");
    const ok = await askConfirm({
      title: "清理缓存",
      message: `确认清理 ${selectedIds.length} 个环境的本地浏览器缓存？`,
      confirmText: "清理",
      danger: true
    });
    if (!ok) return;
    await runAction("已清理缓存", () => api.clearCache({ ids: selectedIds }));
  }

  async function exportData() {
    const result = await runAction("导出完成", () => api.exportData());
    if (result?.canceled) showToast("已取消导出");
  }

  async function importData() {
    const result = await runAction("导入完成", () => api.importData());
    if (result?.canceled) showToast("已取消导入");
  }

  async function bulkCreateProfiles(payload) {
    await runAction("批量创建完成", () => api.bulkCreateProfiles(payload));
    setBulkCreateOpen(false);
  }

  async function bulkUpdateProfiles(patch) {
    if (!selectedIds.length) return showToast("请选择环境");
    await runAction("批量编辑完成", () => api.bulkUpdateProfiles({ ids: selectedIds, patch }));
    setBulkEditOpen(false);
  }

  const renderMain = () => {
    if (activeSection === "groups") {
      return <GroupsPanel groups={groups} profiles={profiles} onOpenCreate={openCreate} setGroupFilter={setGroupFilter} setActiveSection={setActiveSection} />;
    }
    if (activeSection === "proxies") {
      return <ProxyPanel profiles={profiles} onEdit={openEdit} onOpenCreate={openCreate} showToast={showToast} showError={showError} />;
    }
    if (activeSection === "extensions") {
      return <ExtensionsPanel profiles={profiles} onEdit={openEdit} showToast={showToast} showError={showError} refreshProfiles={refresh} />;
    }
    if (activeSection === "cache") {
      return <CachePanel profiles={profiles} showToast={showToast} showError={showError} refreshProfiles={refresh} />;
    }
    if (activeSection === "templates") {
      return <TemplatesPanel profiles={profiles} groups={groups} onOpenCreate={openCreate} showToast={showToast} showError={showError} refreshProfiles={refresh} />;
    }
    if (activeSection === "logs") {
      return <LogsPanel showToast={showToast} showError={showError} />;
    }
    if (activeSection === "globalSettings") {
      return <GlobalSettingsPanel settings={settings} setSettings={setSettings} showToast={showToast} showError={showError} />;
    }
    if (activeSection === "skill") {
      return <SkillPanel showToast={showToast} showError={showError} />;
    }
    if (activeSection === "rpa") {
      return <RpaPanel profiles={profiles} showToast={showToast} showError={showError} />;
    }
  if (activeSection === "sync") {
      return <SyncPanel profiles={profiles} showToast={showToast} showError={showError} refreshProfiles={refresh} />;
    }
    if (activeSection === "fingerprintCheck") {
      return <FingerprintCheckPanel profiles={profiles} showToast={showToast} showError={showError} refreshProfiles={refresh} />;
    }
    if (activeSection === "kernels") {
      return <KernelPanel showToast={showToast} showError={showError} />;
    }
    return (
      <section className="adsShell">
        <section className="adsTopbar">
          <SelectMenu
            value={groupFilter}
            options={[ALL_GROUPS, ...groups].map((group) => ({ value: group, label: group }))}
            onChange={setGroupFilter}
          />
          <div className="adsSearch">
            <Search size={20} />
            <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索或新建搜索条件" />
          </div>
          <button className="adsIconBtn" onClick={() => setStatusFilter(statusFilter === "all" ? "running" : "all")} title="筛选">
            <SlidersHorizontal size={18} />
          </button>
        </section>

        <section className="adsToolbar">
          <button className="adsPrimary" disabled={!selected.size || busy} onClick={() => openProfiles()}>
            <Play size={16} />打开
          </button>
          <button className="adsTool" onClick={openRandomCreate} title="随机环境"><WandSparkles size={17} /></button>
          <button className="adsTool" onClick={() => setBulkCreateOpen(true)} title="批量创建"><Grid2X2 size={17} /></button>
          <button className="adsTool" disabled={!selected.size || busy} onClick={() => closeProfiles()} title="关闭"><CircleX size={18} /></button>
          <button className="adsTool" onClick={importData} title="导入"><Upload size={17} /></button>
          <button className="adsTool" onClick={exportData} title="导出"><Download size={17} /></button>
          <button className="adsTool" disabled={!selected.size || busy} onClick={duplicateProfiles} title="复制"><Copy size={17} /></button>
          <button className="adsTool" disabled={!selected.size || busy} onClick={() => deleteProfiles(false)} title="删除"><Trash2 size={17} /></button>
          <div className="adsMoreWrap">
            <button className={moreMenuOpen ? "adsTool active" : "adsTool"} onClick={() => setMoreMenuOpen((value) => !value)} title="更多">
              <span className="dotIcon">•••</span>
            </button>
            {moreMenuOpen ? (
              <div className="adsMoreMenu">
                <button onClick={() => { setMoreMenuOpen(false); selectedIds[0] && openEdit(profiles.find((profile) => profile.id === selectedIds[0])); }} disabled={!selected.size}>
                  <Monitor size={17} />环境信息 <ChevronDown size={15} />
                </button>
                <button onClick={() => { setMoreMenuOpen(false); setActiveSection("proxies"); }}>
                  <Globe size={17} />代理信息 <ChevronDown size={15} />
                </button>
                <button onClick={() => { setMoreMenuOpen(false); setActiveSection("fingerprintCheck"); }}>
                  <Shield size={17} />指纹信息 <ChevronDown size={15} />
                </button>
                <button onClick={() => { setMoreMenuOpen(false); clearCache(); }} disabled={!selected.size}>
                  <BrushCleaning size={17} />清除缓存
                </button>
                <button onClick={() => { setMoreMenuOpen(false); setBulkEditOpen(true); }} disabled={!selected.size}>
                  自定义快捷区
                </button>
              </div>
            ) : null}
          </div>
          <button className="adsRefresh" onClick={refresh}><RefreshCcw size={17} />更新环境</button>
        </section>

        <ProfilesTable
          profiles={filteredProfiles}
          selected={selected}
          toggleSelected={toggleSelected}
          toggleAllVisible={toggleAllVisible}
          openProfiles={openProfiles}
          closeProfiles={closeProfiles}
          openEdit={openEdit}
          openCreate={() => openCreate()}
          loginStates={loginStates}
        />

        <footer className="adsPager">
          <span>总数: {filteredProfiles.length}</span>
          <button disabled>1</button>
          <span>/ 1</span>
          <div className="pagerSize">50条/页</div>
        </footer>
      </section>
    );
  };

  const overlays = (
    <>
      {editor ? (
        <ProfileEditor
          editor={editor}
          groups={groups}
          onClose={() => setEditor(null)}
          onSave={saveEditor}
        />
      ) : null}

      {settingsOpen && settings ? (
        <SettingsPanel
          settings={settings}
          onClose={() => setSettingsOpen(false)}
          onSave={async (next) => {
            await runAction("设置已保存", () => api.updateSettings(next));
            setSettingsOpen(false);
          }}
        />
      ) : null}

      {bulkCreateOpen ? (
        <BulkCreateDialog
          groups={groups}
          onClose={() => setBulkCreateOpen(false)}
          onSubmit={bulkCreateProfiles}
        />
      ) : null}

      {bulkEditOpen ? (
        <BulkEditDialog
          groups={groups}
          selectedCount={selected.size}
          onClose={() => setBulkEditOpen(false)}
          onSubmit={bulkUpdateProfiles}
        />
      ) : null}

      <NoticeStack notices={notices} />

      {confirmDialog ? (
        <ConfirmDialog
          dialog={confirmDialog}
          onClose={(value) => {
            confirmDialog.resolve(value);
            setConfirmDialog(null);
          }}
        />
      ) : null}

      {taskDialog ? <TaskDialog task={taskDialog} /> : null}

      <div className="poweredBy">Powered by HX-Browser</div>

      {toast ? <div className="toast">{toast}</div> : null}
    </>
  );

  if (uiTheme === "workspace") {
    return (
      <div className={`app theme-workspace workspaceApp mode-${colorMode}`}>
        <WorkspaceLayout
          profiles={profiles}
          groups={groups}
          filteredProfiles={filteredProfiles}
          selected={selected}
          selectedIds={selectedIds}
          activeSection={activeSection}
          setActiveSection={setActiveSection}
          groupFilter={groupFilter}
          setGroupFilter={setGroupFilter}
          statusFilter={statusFilter}
          setStatusFilter={setStatusFilter}
          platformFilter={platformFilter}
          setPlatformFilter={setPlatformFilter}
          platformOptions={platformOptions}
          query={query}
          setQuery={setQuery}
          runningCount={runningCount}
          proxyCount={proxyCount}
          busy={busy}
          settings={settings}
          uiTheme={uiTheme}
          setUiTheme={setUiTheme}
          colorMode={colorMode}
          setColorMode={setColorMode}
          toggleSelected={toggleSelected}
          toggleAllVisible={toggleAllVisible}
          openCreate={openCreate}
          openRandomCreate={openRandomCreate}
          openEdit={openEdit}
          openProfiles={openProfiles}
          closeProfiles={closeProfiles}
          duplicateProfiles={duplicateProfiles}
          deleteProfiles={deleteProfiles}
          clearCache={clearCache}
          importData={importData}
          exportData={exportData}
          refresh={refresh}
          setBulkCreateOpen={setBulkCreateOpen}
          setBulkEditOpen={setBulkEditOpen}
          setSettingsOpen={setSettingsOpen}
          renderFallback={renderMain}
        />
        {overlays}
      </div>
    );
  }

  return (
    <div className={`app theme-standard mode-${colorMode}`}>
      <aside className="sidebar">
        <div className="brand">
          <div className="brandMark"><Layers size={22} /></div>
          <div>
            <strong>HX-Browser</strong>
            <span>HX-Browser Workspace</span>
          </div>
        </div>

        <button className="primaryNav" onClick={() => openCreate()}>
          <Plus size={18} />
          新建环境
        </button>

        <nav className="navBlock">
          <NavButton active={activeSection === "profiles"} icon={<Grid2X2 size={17} />} label="环境管理" count={profiles.length} onClick={() => setActiveSection("profiles")} />
          <NavButton active={activeSection === "groups"} icon={<Folder size={17} />} label="分组管理" count={groups.length} onClick={() => setActiveSection("groups")} />
          <NavButton active={activeSection === "proxies"} icon={<Network size={17} />} label="代理管理" count={proxyCount} onClick={() => setActiveSection("proxies")} />
          <NavButton active={activeSection === "extensions"} icon={<BadgePlus size={17} />} label="扩展管理" count={profiles.reduce((sum, item) => sum + (item.launch?.extensions?.length || 0), 0)} onClick={() => setActiveSection("extensions")} />
          <NavButton active={activeSection === "cache"} icon={<BrushCleaning size={17} />} label="缓存管理" count="Clean" onClick={() => setActiveSection("cache")} />
          <NavButton active={activeSection === "templates"} icon={<ClipboardList size={17} />} label="模板管理" count="Tpl" onClick={() => setActiveSection("templates")} />
          <NavButton active={activeSection === "rpa"} icon={<Split size={17} />} label="RPA 流程" count="Auto" onClick={() => setActiveSection("rpa")} />
          <NavButton active={activeSection === "sync"} icon={<Radio size={17} />} label="窗口同步" count="Live" onClick={() => setActiveSection("sync")} />
          <NavButton active={activeSection === "fingerprintCheck"} icon={<Shield size={17} />} label="指纹检测" count="Check" onClick={() => setActiveSection("fingerprintCheck")} />
          <NavButton active={activeSection === "kernels"} icon={<Cpu size={17} />} label="内核管理" count="源" onClick={() => setActiveSection("kernels")} />
          <NavButton active={activeSection === "logs"} icon={<FileCode2 size={17} />} label="操作日志" count="Log" onClick={() => setActiveSection("logs")} />
          <NavButton active={activeSection === "globalSettings"} icon={<Settings size={17} />} label="全局设置" count="Set" onClick={() => setActiveSection("globalSettings")} />
          <NavButton active={activeSection === "skill"} icon={<Sparkles size={17} />} label="Skill 指南" count="AI" onClick={() => setActiveSection("skill")} />
        </nav>

        <div className="groupTitle">快捷分组</div>
        <div className="groupList">
          <button className={groupFilter === ALL_GROUPS ? "active" : ""} onClick={() => { setActiveSection("profiles"); setGroupFilter(ALL_GROUPS); }}>
            <Globe size={16} />
            全部环境
            <span>{profiles.length}</span>
          </button>
          {groups.map((group) => (
            <button key={group} className={groupFilter === group ? "active" : ""} onClick={() => { setActiveSection("profiles"); setGroupFilter(group); }}>
              <Folder size={16} />
              {group}
              <span>{profiles.filter((profile) => profile.group === group).length}</span>
            </button>
          ))}
        </div>

        <div className="sidebarFooter">
          <div className="themeSwitch">
            <span>界面风格</span>
            <Segment
              value={uiTheme}
              onChange={setUiTheme}
              options={[["standard", "标准"], ["workspace", "工作台"]]}
            />
          </div>
          <div className="themeSwitch">
            <span>显示模式</span>
            <Segment
              value={colorMode}
              onChange={setColorMode}
              options={[["dark", "夜间"], ["light", "日间"]]}
            />
          </div>
          <button onClick={openRandomCreate}><Shuffle size={17} />随机环境</button>
          <button onClick={() => setSettingsOpen(true)}><Settings size={17} />设置</button>
          <div className="localApi">
            <span>Local API</span>
            <strong>{settings?.localApiPort || 53225}</strong>
          </div>
        </div>
      </aside>

      <main className="workspace">
        <div className="workspaceView" key={activeSection}>
          <PanelErrorBoundary>
            {renderMain()}
          </PanelErrorBoundary>
        </div>
      </main>

      {overlays}
    </div>
  );
}

function NoticeStack({ notices }) {
  return (
    <div className="noticeStack">
      {notices.map((notice) => (
        <div className={`noticeItem ${notice.type}`} key={notice.id}>
          <span className="noticeIcon">{notice.type === "error" ? "!" : "✓"}</span>
          <div>
            <strong>{notice.type === "error" ? "操作失败" : "操作成功"}</strong>
            <span>{notice.message}</span>
          </div>
        </div>
      ))}
    </div>
  );
}

function ConfirmDialog({ dialog, onClose }) {
  return (
    <div className="modalLayer">
      <div className="confirmBox">
        <div className={dialog.danger ? "modalIcon danger" : "modalIcon"}>{dialog.danger ? "!" : "i"}</div>
        <div className="confirmBody">
          <h3>{dialog.title}</h3>
          <p>{dialog.message}</p>
        </div>
        <footer>
          <button onClick={() => onClose(false)}>取消</button>
          <button className={dialog.danger ? "dangerSolid" : "accent"} onClick={() => onClose(true)}>{dialog.confirmText || "确认"}</button>
        </footer>
      </div>
    </div>
  );
}

function TaskDialog({ task }) {
  return (
    <div className="taskFloat">
      <div className={`taskRing ${task.phase}`} />
      <div>
        <strong>{task.title}</strong>
        <span>{task.detail}</span>
      </div>
    </div>
  );
}

function parseProfileImport(text, group) {
  return String(text || "")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line, index) => {
      const parts = line.split(/[,\t|]/).map((item) => item.trim());
      const [name, proxyText = "", username = "", password = "", platform = "custom", tags = ""] = parts;
      return {
        ...randomProfile(),
        name: name || `Bulk-${index + 1}`,
        group,
        platform: platform || "custom",
        account: { username, password, twoFactor: "", cookies: "" },
        proxy: parseProxyString(proxyText) || emptyProfile.proxy,
        tags: fromTokens(tags)
      };
    });
}

function BulkCreateDialog({ groups, onClose, onSubmit }) {
  const [mode, setMode] = useState("random");
  const [count, setCount] = useState(10);
  const [group, setGroup] = useState(groups[0] || DEFAULT_GROUP);
  const [platform, setPlatform] = useState("custom");
  const [text, setText] = useState("");

  function submit() {
    if (mode === "random") {
      const profiles = Array.from({ length: Math.max(1, Math.min(Number(count) || 1, 500)) }, () => ({
        ...randomProfile({ group, platform }),
        group,
        platform
      }));
      return onSubmit({ profiles });
    }
    return onSubmit({ profiles: parseProfileImport(text, group) });
  }

  return (
    <div className="modalLayer">
      <div className="wideDialog">
        <header className="dialogHead">
          <div>
            <h3>批量创建环境</h3>
            <p>支持按数量随机生成，也支持按行导入账号和代理。</p>
          </div>
          <button className="iconBtn" onClick={onClose}><X size={20} /></button>
        </header>
        <div className="dialogBody">
          <Segment value={mode} onChange={setMode} options={[["random", "随机创建"], ["text", "文本导入"]]} />
          <div className="formGrid">
            <Field label="分组"><Combo value={group} options={groups} onChange={setGroup} /></Field>
            <Field label="平台">
              <SelectMenu value={platform} options={platformSelectOptions} onChange={setPlatform} />
            </Field>
            {mode === "random" ? (
              <Field label="创建数量" wide><input type="number" min="1" max="500" value={count} onChange={(e) => setCount(Number(e.target.value))} /></Field>
            ) : (
              <Field label="导入文本" wide>
                <textarea
                  rows="10"
                  value={text}
                  onChange={(e) => setText(e.target.value)}
                  placeholder="每行一个环境：名称,代理,账号,密码,平台,标签&#10;例如：FB-01,http://user:pass@127.0.0.1:7890,mail@example.com,pwd123,facebook,warmup"
                />
              </Field>
            )}
          </div>
        </div>
        <footer className="dialogFoot">
          <button onClick={onClose}>取消</button>
          <button className="accent" onClick={submit}><Plus size={17} />创建</button>
        </footer>
      </div>
    </div>
  );
}

function BulkEditDialog({ groups, selectedCount, onClose, onSubmit }) {
  const [group, setGroup] = useState("");
  const [tags, setTags] = useState("");
  const [proxyText, setProxyText] = useState("");
  const [timezone, setTimezone] = useState("");
  const [language, setLanguage] = useState("");
  const [resolution, setResolution] = useState("");
  const [urls, setUrls] = useState("");
  const [clearCacheAfterClose, setClearCacheAfterClose] = useState(false);

  function submit() {
    const patch = {};
    if (group) patch.group = group;
    if (tags) patch.tagsAdd = tags;
    if (proxyText) patch.proxy_string = proxyText;
    const fingerprint = {};
    if (timezone) fingerprint.timezone = timezone;
    if (language) fingerprint.language = language;
    if (resolution) fingerprint.resolution = resolution;
    if (Object.keys(fingerprint).length) patch.fingerprint = fingerprint;
    if (urls) patch.launch = { urls: fromLines(urls) };
    patch.storage = { clearCacheAfterClose };
    onSubmit(patch);
  }

  return (
    <div className="modalLayer">
      <div className="wideDialog">
        <header className="dialogHead">
          <div>
            <h3>批量编辑环境</h3>
            <p>将以下字段应用到已选中的 {selectedCount} 个环境，空字段会跳过。</p>
          </div>
          <button className="iconBtn" onClick={onClose}><X size={20} /></button>
        </header>
        <div className="dialogBody">
          <div className="formGrid">
            <Field label="分组"><Combo value={group} options={groups} onChange={setGroup} /></Field>
            <Field label="追加标签"><input value={tags} onChange={(e) => setTags(e.target.value)} placeholder="ads, warmup, client-a" /></Field>
            <Field label="代理自动识别" wide><input value={proxyText} onChange={(e) => setProxyText(e.target.value)} placeholder="host:port:user:pass 或 socks5://user:pass@host:port" /></Field>
            <Field label="时区"><input value={timezone} onChange={(e) => setTimezone(e.target.value)} placeholder="America/New_York" /></Field>
            <Field label="语言"><input value={language} onChange={(e) => setLanguage(e.target.value)} placeholder="en-US,en" /></Field>
            <Field label="窗口尺寸"><input value={resolution} onChange={(e) => setResolution(e.target.value)} placeholder="1920x1080" /></Field>
            <Field label="启动网址" wide><textarea rows="4" value={urls} onChange={(e) => setUrls(e.target.value)} placeholder="每行一个 URL" /></Field>
            <Toggle label="关闭后清理缓存" value={clearCacheAfterClose} onChange={setClearCacheAfterClose} />
          </div>
        </div>
        <footer className="dialogFoot">
          <button onClick={onClose}>取消</button>
          <button className="accent" onClick={submit}><Check size={17} />应用</button>
        </footer>
      </div>
    </div>
  );
}

function NavButton({ active, icon, label, count, onClick }) {
  return (
    <button className={active ? "active" : ""} onClick={onClick}>
      {icon}
      {label}
      <span>{count}</span>
    </button>
  );
}

function WorkspaceLayout({
  profiles,
  groups,
  filteredProfiles,
  selected,
  selectedIds,
  activeSection,
  setActiveSection,
  groupFilter,
  setGroupFilter,
  statusFilter,
  setStatusFilter,
  platformFilter,
  setPlatformFilter,
  platformOptions,
  query,
  setQuery,
  runningCount,
  proxyCount,
  busy,
  settings,
  uiTheme,
  setUiTheme,
  colorMode,
  setColorMode,
  toggleSelected,
  toggleAllVisible,
  openCreate,
  openRandomCreate,
  openEdit,
  openProfiles,
  closeProfiles,
  duplicateProfiles,
  deleteProfiles,
  clearCache,
  importData,
  exportData,
  refresh,
  setBulkCreateOpen,
  setBulkEditOpen,
  setSettingsOpen,
  renderFallback
}) {
  const extensionCount = profiles.reduce((sum, item) => sum + (item.launch?.extensions?.length || 0), 0);
  const totalQuota = Math.max(10, profiles.length || 0);
  const activeTitle = {
    profiles: "浏览器窗口",
    groups: "分组管理",
    proxies: "代理IP推荐",
    extensions: "扩展中心",
    cache: "缓存管理",
    templates: "模板管理",
    rpa: "RPA",
    sync: "群控同步",
    fingerprintCheck: "指纹检测",
    kernels: "浏览器内核",
    logs: "操作日志",
    globalSettings: "系统设置"
  }[activeSection] || "浏览器窗口";

  const sidebarItems = [
    { key: "profiles", label: "首页", icon: <Globe size={16} /> },
    { key: "profiles", label: "浏览器窗口", icon: <Grid2X2 size={16} /> },
    { key: "groups", label: "分组管理", icon: <Folder size={16} /> },
    { key: "proxies", label: "代理IP推荐", icon: <Network size={16} />, badge: "NEW" },
    { key: "extensions", label: "扩展中心", icon: <BadgePlus size={16} /> },
    { key: "sync", label: "窗口同步", icon: <Radio size={16} /> },
    { key: "templates", label: "模板管理", icon: <Sparkles size={16} /> },
    { key: "cache", label: "缓存管理", icon: <Archive size={16} /> },
    { key: "globalSettings", label: "系统设置", icon: <Settings size={16} /> },
    { key: "logs", label: "操作日志", icon: <FileCode2 size={16} /> },
    { key: "rpa", label: "使用帮助", icon: <Split size={16} /> },
    { key: "fingerprintCheck", label: "常见问题", icon: <Shield size={16} /> }
  ];

  function activateGroup(group) {
    setActiveSection("profiles");
    setGroupFilter(group);
  }

  return (
    <>
      <aside className="wsRail">
        <div className="wsLogo compact">
          <div className="wsLogoMark"><Layers size={22} /></div>
          <strong>Workspace</strong>
        </div>
        <nav className="wsModuleNav">
          {sidebarItems.map((item, index) => (
            <button
              key={item.label + "-" + index}
              className={activeSection === item.key ? "active" : ""}
              onClick={() => setActiveSection(item.key)}
            >
              {item.icon}
              <span>{item.label}</span>
              {item.badge ? <small className="wsNewBadge">{item.badge}</small> : null}
            </button>
          ))}
        </nav>
        <div className="wsRailFooter">
          <button onClick={() => setSettingsOpen(true)}><Settings size={17} />设置中心</button>
          <div className="themeSwitch wsThemeSwitch">
            <span>界面风格</span>
            <Segment value={uiTheme} onChange={setUiTheme} options={[["standard", "标准"], ["workspace", "工作台"]]} />
          </div>
          <div className="themeSwitch wsThemeSwitch">
            <span>显示模式</span>
            <Segment value={colorMode} onChange={setColorMode} options={[["dark", "夜间"], ["light", "日间"]]} />
          </div>
        </div>
      </aside>

      <main className="wsWorkspace">
        <header className="wsClientBar">
          <button className="wsLineButton">线路2 <ChevronDown size={14} /></button>
          <div className="wsClientSpacer" />
          <button className="wsUserBtn"><Globe size={16} /> HX-Browser</button>
        </header>

        {activeSection === "profiles" ? (
          <section className="wsWindowPage">
            <div className="wsNoticeBar">
              <button className="wsNoticeIcon"><ListFilter size={16} /></button>
              <button className="wsNoticeIcon" onClick={refresh}><RefreshCcw size={16} /></button>
              <span>已支持 1 控多窗口同步操作，点击「窗口同步」使用。</span>
            </div>

            <section className="wsSearchRow">
              <div className="wsCreateGroup">
                <button className="wsCreateBtn" onClick={() => openCreate()}><Plus size={17} />创建窗口</button>
                <button className="wsCreateDrop" onClick={() => setBulkCreateOpen(true)}><ChevronDown size={15} /></button>
              </div>
              <div className="wsQuotaBox"><strong>已用{profiles.length}</strong><span>/总数{totalQuota}</span></div>
              <div className="wsQuotaBox"><strong>今日打开</strong><span>/总数50</span></div>
              <button className="wsPlanBtn">开通套餐</button>
              <SelectMenu value="all" options={[{ value: "all", label: "选择员工账号搜索" }]} onChange={() => {}} />
              <div className="wsNameSearch">
                <label>窗口名称:</label>
                <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="输入内容搜索" />
              </div>
              <button className="wsAdvancedBtn"><Search size={16} />高级搜索</button>
            </section>

            <section className="wsTabsToolbar">
              <div className="wsTabs">
                <button className={groupFilter === ALL_GROUPS ? "active" : ""} onClick={() => activateGroup(ALL_GROUPS)}>全部</button>
                <button className={groupFilter === DEFAULT_GROUP ? "active" : ""} onClick={() => activateGroup(DEFAULT_GROUP)}>自建</button>
                <button>常用</button>
                <button className={statusFilter === "running" ? "active" : ""} onClick={() => setStatusFilter(statusFilter === "running" ? "all" : "running")}>已打开({runningCount})</button>
                <button>分享</button>
                <button>转移</button>
                <button onClick={() => setActiveSection("groups")}>查看分组 <ListFilter size={14} /></button>
              </div>
              <div className="wsIconToolbar">
                <button title="窗口排列"><Grid2X2 size={17} /></button>
                <button title="刷新" onClick={refresh}><RefreshCcw size={17} /></button>
                <button title="分享"><ExternalLink size={17} /></button>
                <button title="列表"><ListFilter size={17} /></button>
                <button title="回收站"><Archive size={17} />回收站</button>
                <button className="wsMoreBtn" title="更多操作">更多操作 <ChevronDown size={15} /></button>
              </div>
            </section>

            <BitProfilesTable
              profiles={filteredProfiles}
              selected={selected}
              toggleSelected={toggleSelected}
              toggleAllVisible={toggleAllVisible}
              openProfiles={openProfiles}
              closeProfiles={closeProfiles}
              openEdit={openEdit}
              openCreate={openCreate}
            />

            <footer className="wsListFooter">
              <div className="wsFooterActions">
                <button disabled={!selected.size || busy} onClick={() => openProfiles()}><Play size={15} /></button>
                <button disabled={!selected.size || busy} onClick={duplicateProfiles}><Copy size={15} /></button>
                <button disabled={!selected.size || busy} onClick={() => setBulkEditOpen(true)}><Edit3 size={15} /></button>
                <button disabled={!selected.size || busy} onClick={clearCache}><BrushCleaning size={15} /></button>
                <button disabled={!selected.size || busy} onClick={() => deleteProfiles(false)}><Trash2 size={15} /></button>
              </div>
              <span>共 {filteredProfiles.length} 条</span>
              <div className="wsPager">
                <SelectMenu value="100" options={[["50", "50条/页"], ["100", "100条/页"], ["200", "200条/页"]]} onChange={() => {}} />
                <button disabled>{"<"}</button>
                <button className="active">1</button>
                <button disabled>{">"}</button>
                <span>前往</span>
                <input value="1" readOnly />
                <span>页</span>
              </div>
            </footer>
          </section>
        ) : (
          <section className="wsFallback">
            <div className="wsFallbackHead">
              <strong>{activeTitle}</strong>
              <span>{extensionCount} 扩展 · {proxyCount} 代理 · Local API {settings?.localApiPort || 53225}</span>
            </div>
            {renderFallback()}
          </section>
        )}
      </main>
    </>
  );
}

function BitStat({ icon, label, value }) {
  return (
    <div className="wsStat">
      <div>{icon}</div>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function BitProfilesTable({ profiles, selected, toggleSelected, toggleAllVisible, openProfiles, closeProfiles, openEdit, openCreate }) {
  return (
    <section className="wsTablePanel">
      <table className="wsTable">
        <thead>
          <tr>
            <th className="checkCol">
              <button className="check" onClick={toggleAllVisible}>
                {profiles.length && profiles.every((profile) => selected.has(profile.id)) ? <Check size={14} /> : null}
              </button>
            </th>
            <th>序号</th>
            <th>分组</th>
            <th>窗口名称</th>
            <th>账号平台</th>
            <th>代理IP</th>
            <th>备注</th>
            <th>创建时间</th>
            <th>配置</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          {profiles.map((profile, index) => (
            <tr key={profile.id} className={selected.has(profile.id) ? "selected" : ""}>
              <td data-label="选择">
                <button className="check" onClick={() => toggleSelected(profile.id)}>
                  {selected.has(profile.id) ? <Check size={14} /> : null}
                </button>
              </td>
              <td data-label="序号">{profile.serialNo || index + 1}</td>
              <td data-label="分组">{profile.group || "未分组"}</td>
              <td data-label="窗口名称">
                <div className="wsWindowName">
                  <strong>{profile.name || "_" + (index + 1)}</strong>
                  <small>{profile.customNo || profile.id}</small>
                </div>
              </td>
              <td data-label="账号平台">
                <div className="wsPlatformCell">
                  <span className={"platform platform-" + profile.platform}><PlatformIcon platform={profile.platform} /></span>
                  <span>{profile.platform === "custom" ? "其他平台" : platformName(profile.platform) + ".com"}</span>
                </div>
              </td>
              <td data-label="代理IP">
                {profile.proxy?.type === "no_proxy" ? <span className="wsDirectBadge">直</span> : <ProxyView proxy={profile.proxy} />}
              </td>
              <td data-label="备注"><span className="wsRemark">{profile.notes || "-"}</span></td>
              <td data-label="创建时间">{formatDate(profile.createdAt)}</td>
              <td data-label="配置"><button className="wsConfigBtn" onClick={() => openEdit(profile)}><Settings size={16} /></button></td>
              <td data-label="操作">
                <div className="wsRowActions">
                  {profile.status === "running" ? (
                    <button onClick={() => closeProfiles([profile.id])}>关闭</button>
                  ) : (
                    <button className="wsOpenBtn" onClick={() => openProfiles([profile.id])}><Shield size={14} />打开</button>
                  )}
                  <button className="wsStarBtn" onClick={() => openEdit(profile)}>☆</button>
                </div>
              </td>
            </tr>
          ))}
          {!profiles.length ? (
            <tr>
              <td colSpan="10">
                <div className="emptyState">
                  <Layers size={32} />
                  <strong>暂无浏览器窗口</strong>
                  <span>点击创建窗口开始创建本地环境。</span>
                  <button className="wsCreateBtn" onClick={openCreate}><Plus size={17} />创建窗口</button>
                </div>
              </td>
            </tr>
          ) : null}
        </tbody>
      </table>
    </section>
  );
}

function ProfilesTable({ profiles, selected, toggleSelected, toggleAllVisible, openProfiles, closeProfiles, openEdit, openCreate, loginStates }) {
  return (
    <section className="tableWrap adsTableWrap">
      <table className="adsTable">
        <thead>
          <tr>
            <th className="checkCol">
              <button className="check" onClick={toggleAllVisible}>
                {profiles.length && profiles.every((profile) => selected.has(profile.id)) ? <Check size={14} /> : null}
              </button>
            </th>
            <th>序号</th>
            <th>分组</th>
            <th>名称</th>
            <th>编号/ID</th>
            <th>代理</th>
            <th>IP / 地区</th>
            <th>指纹</th>
            <th>已登录</th>
            <th>状态</th>
            <th className="actionCol">操作</th>
          </tr>
        </thead>
        <tbody>
          {profiles.map((profile, index) => (
            <tr key={profile.id} className={selected.has(profile.id) ? "selected" : ""}>
              <td>
                <button className="check" onClick={() => toggleSelected(profile.id)}>
                  {selected.has(profile.id) ? <Check size={14} /> : null}
                </button>
              </td>
              <td>{index + 1}</td>
              <td><span className="pill">{profile.group}</span></td>
              <td>
                <div className="nameCell adsNameCell">
                  <span className={`platform platform-${profile.platform}`}>
                    <PlatformIcon platform={profile.platform} />
                  </span>
                  <div>
                    <strong>{profile.name}</strong>
                    <span>{platformName(profile.platform)} · {profile.fingerprint?.browser === "flower" ? "Firefox" : "Chromium"}</span>
                  </div>
                </div>
              </td>
              <td>
                <div className="idCell">
                  <strong>{profile.customNo || profile.serialNo || "-"}</strong>
                  <span>{profile.id}</span>
                </div>
              </td>
              <td><ProxyView proxy={profile.proxy} /></td>
              <td><LocationView profile={profile} /></td>
              <td>
                <div className="fingerCell">
                  <Shield size={15} />
                  <span>{profile.fingerprint?.resolution}</span>
                  <small>{profile.fingerprint?.timezone}</small>
                </div>
              </td>
              <td>
                <LoginBadges state={loginStates?.[profile.id]} />
              </td>
              <td><StatusBadge status={profile.status} /></td>
              <td>
                <div className="rowActions">
                  {profile.status === "running" ? (
                    <button className="small adsMiniBtn" onClick={() => closeProfiles([profile.id])}><Square size={15} /></button>
                  ) : (
                    <button className="small accent adsMiniBtn" onClick={() => openProfiles([profile.id])}><Play size={15} /></button>
                  )}
                  <button className="iconBtn" onClick={() => openEdit(profile)} title="编辑"><Edit3 size={16} /></button>
                </div>
              </td>
            </tr>
          ))}
          {!profiles.length ? (
            <tr>
              <td colSpan="11">
                <div className="emptyState">
                  <Layers size={32} />
                  <strong>没有环境</strong>
                  <span>新建或随机生成一个本地隔离浏览器环境。</span>
                  <button className="accent" onClick={openCreate}><Plus size={17} />新建环境</button>
                </div>
              </td>
            </tr>
          ) : null}
        </tbody>
      </table>
    </section>
  );
}

function Stat({ icon, label, value }) {
  return (
    <div className="stat">
      <div>{icon}</div>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function Segment({ value, onChange, options }) {
  return (
    <div className="segment">
      {options.map(([key, label]) => (
        <button key={key} className={value === key ? "active" : ""} onClick={() => onChange(key)}>{label}</button>
      ))}
    </div>
  );
}

function StatusBadge({ status }) {
  return <span className={`status ${status}`}>{status === "running" ? "运行中" : "已关闭"}</span>;
}

const LOGIN_PLATFORM_LABEL = {
  google: "Google", youtube: "YouTube", github: "GitHub", cloudflare: "Cloudflare",
  freebuff: "Freebuff", microsoft: "Microsoft", facebook: "FB", tiktok: "TikTok",
  x: "X", amazon: "Amazon", shopify: "Shopify",
};

function LoginBadges({ state }) {
  const platforms = state?.platforms || {};
  const list = Object.entries(platforms).filter(([, v]) => v?.state === "ok");
  if (!list.length) return <span className="muted">-</span>;
  return (
    <div className="loginBadges">
      {list.map(([platform, v]) => (
        <span key={platform} className={`loginBadge loginBadge-${v.state}`} title={v.identity ? `账号: ${v.identity}` : "已登录"}>
          {LOGIN_PLATFORM_LABEL[platform] || platform}
          {v.identity ? `·${v.identity}` : ""}
        </span>
      ))}
    </div>
  );
}

function AccountView({ account }) {
  if (!account?.username) return <span className="muted">未填写</span>;
  return (
    <div className="stackCell">
      <strong>{account.username}</strong>
      <span>{account.password ? "已保存密码" : "无密码"}</span>
    </div>
  );
}

function ProxyView({ proxy }) {
  if (!proxy || proxy.type === "no_proxy") return <span className="muted">无代理</span>;
  return (
    <div className="stackCell">
      <strong>{String(proxy.type).toUpperCase()}</strong>
      <span>{proxy.host}:{proxy.port}</span>
    </div>
  );
}

function LocationView({ profile }) {
  const value = [profile.country, profile.region, profile.city].filter(Boolean).join(" / ");
  return value ? <span>{value}</span> : <span className="muted">未设置</span>;
}

function TagList({ tags }) {
  if (!tags?.length) return <span className="muted">无标签</span>;
  return (
    <div className="tagList">
      {tags.slice(0, 3).map((tag) => <span key={tag}>{tag}</span>)}
      {tags.length > 3 ? <span>+{tags.length - 3}</span> : null}
    </div>
  );
}

function GroupsPanel({ groups, profiles, onOpenCreate, setGroupFilter, setActiveSection }) {
  return (
    <>
      <header className="pageHead">
        <div>
          <h1>分组管理</h1>
          <p>分组来自环境配置，编辑环境时可直接新增分组名。</p>
        </div>
        <button className="accent" onClick={() => onOpenCreate()}><Plus size={17} />新建环境</button>
      </header>
      <section className="miniGrid">
        {groups.map((group) => (
          <button
            className="moduleCard"
            key={group}
            onClick={() => {
              setGroupFilter(group);
              setActiveSection("profiles");
            }}
          >
            <Folder size={22} />
            <strong>{group}</strong>
            <span>{profiles.filter((item) => item.group === group).length} 个环境</span>
          </button>
        ))}
      </section>
    </>
  );
}

function ProxyPanelLegacy({ profiles, onEdit, onOpenCreate, showToast, showError }) {
  const proxyProfiles = profiles.filter((profile) => profile.proxy?.type !== "no_proxy");
  const [testingId, setTestingId] = useState("");
  const [results, setResults] = useState({});
  const [library, setLibrary] = useState([]);
  const [proxyText, setProxyText] = useState("");
  const [settings, setSettings] = useState(null);
  const [proxySettings, setProxySettings] = useState(defaultProxySettings);
  const [globalProxyText, setGlobalProxyText] = useState("");

  async function refreshLibrary() {
    setLibrary(await api.listProxies());
  }

  async function refreshProxySettings() {
    const next = await api.getSettings();
    const normalized = normalizeProxySettingsForm(next.proxySettings);
    setSettings(next);
    setProxySettings(normalized);
    setGlobalProxyText(proxyToLine(normalized.globalProxy));
  }

  useEffect(() => {
    refreshLibrary().catch(showError);
    refreshProxySettings().catch(showError);
  }, []);

  const patchProxySettings = (patch) => setProxySettings((prev) => normalizeProxySettingsForm({ ...prev, ...patch }));
  const patchProxyFilters = (patch) => setProxySettings((prev) => normalizeProxySettingsForm({ ...prev, filters: { ...(prev.filters || {}), ...patch } }));

  async function checkProxy(profile) {
    setTestingId(profile.id);
    try {
      const result = await api.testProxy({ proxy: profile.proxy });
      setResults((prev) => ({ ...prev, [profile.id]: result }));
    } catch (error) {
      setResults((prev) => ({ ...prev, [profile.id]: { ok: false, error: error?.message || String(error) } }));
    } finally {
      setTestingId("");
    }
  }

  async function saveProxyText() {
    const items = String(proxyText || "")
      .split(/\r?\n/)
      .map((line) => line.trim())
      .filter(Boolean)
      .map(parseProxyImportLine)
      .filter((item) => item.proxy_string);
    if (!items.length) return showToast("请粘贴代理");
    await api.saveProxies({ items });
    setProxyText("");
    await refreshLibrary();
    showToast(`已保存 ${items.length} 条代理`);
  }

  async function checkSavedProxy(proxy) {
    setTestingId(proxy.id);
    try {
      await api.checkProxies({ ids: [proxy.id], checker: proxySettings.checker });
      await refreshLibrary();
    } catch (error) {
      showError(error);
    } finally {
      setTestingId("");
    }
  }

  async function saveProxySettings() {
    const globalProxy = parseProxyString(globalProxyText) || proxySettings.globalProxy || {};
    const next = normalizeProxySettingsForm({ ...proxySettings, globalProxy });
    const saved = await api.updateSettings({ ...(settings || {}), proxySettings: next });
    setSettings(saved);
    setProxySettings(normalizeProxySettingsForm(saved.proxySettings));
    setGlobalProxyText(proxyToLine(saved.proxySettings?.globalProxy || globalProxy));
    showToast("代理策略已保存");
  }

  async function checkAllSavedProxies() {
    setTestingId("all");
    try {
      await api.checkProxies({ checker: proxySettings.checker });
      await refreshLibrary();
      showToast("代理池检测完成");
    } catch (error) {
      showError(error);
    } finally {
      setTestingId("");
    }
  }

  async function deleteSavedProxy(proxy) {
    await api.deleteProxies({ ids: [proxy.id] });
    await refreshLibrary();
    showToast("已删除代理");
  }

  return (
    <>
      <header className="pageHead">
        <div>
          <h1>代理管理</h1>
          <p>HTTP、HTTPS、SOCKS5 代理随环境保存，启动时写入浏览器参数。</p>
        </div>
        <button className="accent" onClick={() => onOpenCreate()}><Plus size={17} />新建环境</button>
      </header>
      <section className="noticeBar">
        <Network size={16} />
        <span>代理库支持常见格式自动识别：host:port、host:port:user:pass、user:pass@host:port、http/socks5 URL。</span>
      </section>
      <section className="toolPanel">
        <div className="panelTitle">
          <strong>代理库</strong>
          <span>{library.length} 条保存的代理</span>
        </div>
        <div className="inlineInput">
          <textarea value={proxyText} onChange={(e) => setProxyText(e.target.value)} rows="4" placeholder="每行一个代理，例如 socks5://user:pass@127.0.0.1:7890" />
          <button className="accent" onClick={saveProxyText}><Plus size={17} />保存代理</button>
        </div>
      </section>
      <section className="tableWrap">
        <table className="simpleTable">
          <thead>
            <tr>
              <th>名称</th>
              <th>类型</th>
              <th>主机</th>
              <th>端口</th>
              <th>账号</th>
              <th>状态</th>
              <th>地区</th>
              <th className="actionCol">操作</th>
            </tr>
          </thead>
          <tbody>
            {library.map((proxy) => (
              <tr key={proxy.id}>
                <td>{proxy.name}</td>
                <td>{String(proxy.type).toUpperCase()}</td>
                <td>{proxy.host}</td>
                <td>{proxy.port}</td>
                <td>{proxy.username || "-"}</td>
                <td>{proxy.status === "ok" ? `${proxy.latencyMs}ms` : proxy.status}</td>
                <td>{[proxy.country, proxy.region, proxy.city].filter(Boolean).join(" / ") || "-"}</td>
                <td>
                  <div className="rowActions">
                    <button className="small" disabled={testingId === proxy.id} onClick={() => checkSavedProxy(proxy)}><Shield size={15} />检测</button>
                    <button className="small danger" onClick={() => deleteSavedProxy(proxy)}><Trash2 size={15} />删除</button>
                  </div>
                </td>
              </tr>
            ))}
            {!library.length ? (
              <tr><td colSpan="8"><div className="emptyInline">暂无代理库数据</div></td></tr>
            ) : null}
          </tbody>
        </table>
      </section>
      <section className="tableWrap">
        <table className="simpleTable">
          <thead>
            <tr>
              <th>环境</th>
              <th>类型</th>
              <th>主机</th>
              <th>端口</th>
              <th>账号</th>
              <th className="actionCol">操作</th>
            </tr>
          </thead>
          <tbody>
            {proxyProfiles.map((profile) => (
              <tr key={profile.id}>
                <td>{profile.name}</td>
                <td>{String(profile.proxy.type).toUpperCase()}</td>
                <td>{profile.proxy.host}</td>
                <td>{profile.proxy.port}</td>
                <td>{profile.proxy.username || "-"}</td>
                <td>
                  <div className="rowActions">
                    <ProxyCheckMini result={results[profile.id]} loading={testingId === profile.id} />
                    <button className="small" onClick={() => checkProxy(profile)}><Shield size={15} />检测</button>
                    <button className="small" onClick={() => onEdit(profile)}><Edit3 size={15} />编辑</button>
                  </div>
                </td>
              </tr>
            ))}
            {!proxyProfiles.length ? (
              <tr><td colSpan="6"><div className="emptyState"><Wifi size={30} /><strong>没有代理配置</strong><span>在环境编辑的代理页添加。</span></div></td></tr>
            ) : null}
          </tbody>
        </table>
      </section>
    </>
  );
}

function ExtensionsPanel({ profiles, onEdit, showToast, showError, refreshProfiles }) {
  const rows = profiles.flatMap((profile) => (profile.launch?.extensions || []).map((extension) => ({ profile, extension })));
  const [library, setLibrary] = useState([]);
  const [pathText, setPathText] = useState("");
  const [name, setName] = useState("");
  const [category, setCategory] = useState("默认");
  const [platforms, setPlatforms] = useState("");
  const [autoBind, setAutoBind] = useState(false);
  const [selectedProfiles, setSelectedProfiles] = useState(new Set());
  const [selectedExtensions, setSelectedExtensions] = useState(new Set());
  const categories = Array.from(new Set(library.map((item) => item.category || item.group || "默认")));

  async function refreshLibrary() {
    setLibrary(await api.listExtensions());
  }

  useEffect(() => {
    refreshLibrary().catch(showError);
  }, []);

  function toggleSet(setter, id) {
    setter((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  }

  async function saveExtension() {
    const items = String(pathText || "")
      .split(/\r?\n/)
      .map((line) => line.trim())
      .filter(Boolean)
      .map((pathValue, index) => ({
        path: pathValue,
        name: name || `Extension-${index + 1}`,
        group: category || "默认",
        category: category || "默认",
        platforms: fromTokens(platforms),
        autoBind
      }));
    if (!items.length) return showToast("请输入扩展路径");
    await api.saveExtensions({ items });
    setPathText("");
    setName("");
    await refreshLibrary();
    showToast(`已保存 ${items.length} 个扩展`);
  }

  async function saveExistingExtension(extension, patch) {
    await api.saveExtensions({ items: [{ ...extension, ...patch }] });
    await refreshLibrary();
    showToast("扩展已更新");
  }

  async function bindSelectedExtensions() {
    const extensionIds = Array.from(selectedExtensions);
    const profileIds = Array.from(selectedProfiles);
    if (!extensionIds.length) return showToast("请选择扩展");
    if (!profileIds.length) return showToast("请选择环境");
    const result = await api.bindExtensions({ extensionIds, profileIds });
    await refreshProfiles?.();
    showToast(`已绑定到 ${result.count || 0} 个环境`);
  }

  async function bindCategory(categoryName) {
    const profileIds = Array.from(selectedProfiles);
    if (!profileIds.length) return showToast("请选择环境");
    const result = await api.bindExtensions({ category: categoryName, profileIds });
    await refreshProfiles?.();
    showToast(`分类扩展已绑定到 ${result.count || 0} 个环境`);
  }

  async function deleteExtension(extension) {
    await api.deleteExtensions({ ids: [extension.id] });
    await refreshLibrary();
    showToast("已删除扩展");
  }

  return (
    <>
      <header className="pageHead">
        <div>
          <h1>扩展管理</h1>
          <p>扩展库、分类、平台适用范围和批量绑定到环境。</p>
        </div>
        <div className="topActions">
          <button onClick={() => setSelectedProfiles(new Set(profiles.map((item) => item.id)))}><Check size={17} />全选环境</button>
          <button className="accent" onClick={bindSelectedExtensions}><BadgePlus size={17} />绑定扩展</button>
        </div>
      </header>

      <section className="toolPanel">
        <div className="panelTitle">
          <strong>扩展库</strong>
          <span>{library.length} 个本地扩展</span>
        </div>
        <div className="formGrid">
          <Field label="扩展名称"><input value={name} onChange={(e) => setName(e.target.value)} placeholder="可选" /></Field>
          <Field label="分类"><input value={category} onChange={(e) => setCategory(e.target.value)} placeholder="例如 Facebook / Cookie / 工具" /></Field>
          <Field label="适用平台"><input value={platforms} onChange={(e) => setPlatforms(e.target.value)} placeholder="facebook, google, tiktok" /></Field>
          <Toggle label="新模板默认自动绑定" value={autoBind} onChange={setAutoBind} />
          <Field label="扩展路径" wide><textarea rows="4" value={pathText} onChange={(e) => setPathText(e.target.value)} placeholder="每行一个 unpacked extension 目录路径" /></Field>
        </div>
        <div className="panelActions">
          <button className="accent" onClick={saveExtension}><Plus size={17} />保存扩展</button>
        </div>
      </section>

      <section className="toolPanel">
        <div className="panelTitle">
          <strong>分类快捷绑定</strong>
          <span>{categories.length} 个分类</span>
        </div>
        <div className="chipRow">
          {categories.map((item) => (
            <button key={item} onClick={() => bindCategory(item)}><Package size={15} />{item}</button>
          ))}
          {!categories.length ? <span className="muted">暂无分类</span> : null}
        </div>
      </section>

      <section className="splitPanels">
        <section className="tableWrap">
          <table className="simpleTable">
            <thead>
              <tr>
                <th className="checkCol">选中</th>
                <th>名称</th>
                <th>分类</th>
                <th>平台</th>
                <th>状态</th>
                <th className="actionCol">操作</th>
              </tr>
            </thead>
            <tbody>
              {library.map((extension) => (
                <tr key={extension.id} className={selectedExtensions.has(extension.id) ? "selected" : ""}>
                  <td><button className="check" onClick={() => toggleSet(setSelectedExtensions, extension.id)}>{selectedExtensions.has(extension.id) ? <Check size={14} /> : null}</button></td>
                  <td className="clipCell" title={extension.path}>{extension.name}</td>
                  <td>{extension.category || extension.group}</td>
                  <td>{extension.platforms?.length ? extension.platforms.join(", ") : "全部"}</td>
                  <td>{extension.enabled ? "启用" : "停用"}</td>
                  <td>
                    <div className="rowActions">
                      <button className="small" onClick={() => saveExistingExtension(extension, { enabled: !extension.enabled })}>{extension.enabled ? "停用" : "启用"}</button>
                      <button className="small danger" onClick={() => deleteExtension(extension)}><Trash2 size={15} />删除</button>
                    </div>
                  </td>
                </tr>
              ))}
              {!library.length ? (
                <tr><td colSpan="6"><div className="emptyInline">暂无扩展库数据</div></td></tr>
              ) : null}
            </tbody>
          </table>
        </section>

        <section className="tableWrap">
          <table className="simpleTable">
            <thead>
              <tr>
                <th className="checkCol">选中</th>
                <th>环境</th>
                <th>平台</th>
                <th>扩展数</th>
                <th className="actionCol">操作</th>
              </tr>
            </thead>
            <tbody>
              {profiles.map((profile) => (
                <tr key={profile.id} className={selectedProfiles.has(profile.id) ? "selected" : ""}>
                  <td><button className="check" onClick={() => toggleSet(setSelectedProfiles, profile.id)}>{selectedProfiles.has(profile.id) ? <Check size={14} /> : null}</button></td>
                  <td>{profile.name}</td>
                  <td>{platformName(profile.platform)}</td>
                  <td>{profile.launch?.extensions?.length || 0}</td>
                  <td><button className="small" onClick={() => onEdit(profile)}><Edit3 size={15} />编辑</button></td>
                </tr>
              ))}
              {!profiles.length ? <tr><td colSpan="5"><div className="emptyInline">暂无环境</div></td></tr> : null}
            </tbody>
          </table>
        </section>
      </section>

      <section className="tableWrap">
        <table className="simpleTable">
          <thead>
            <tr>
              <th>环境</th>
              <th>扩展路径</th>
              <th className="actionCol">操作</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(({ profile, extension }) => (
              <tr key={`${profile.id}-${extension}`}>
                <td>{profile.name}</td>
                <td className="clipCell" title={extension}>{extension}</td>
                <td><button className="small" onClick={() => onEdit(profile)}><Edit3 size={15} />编辑</button></td>
              </tr>
            ))}
            {!rows.length ? (
              <tr><td colSpan="3"><div className="emptyState"><BadgePlus size={30} /><strong>没有扩展</strong><span>可从上方扩展库批量绑定。</span></div></td></tr>
            ) : null}
          </tbody>
        </table>
      </section>
    </>
  );
}

function defaultTemplateDraft(groups = []) {
  return {
    name: "Facebook 默认模板",
    platform: "facebook",
    group: groups[0] || DEFAULT_GROUP,
    tags: ["template"],
    defaults: {
      country: "US",
      region: "California",
      city: "Los Angeles",
      storage: { cloudSync: false, clearCacheAfterClose: false }
    },
    proxyRule: {},
    fingerprintRule: {
      browser: "sun",
      os: "Windows",
      deviceType: "desktop",
      language: "en-US,en",
      timezone: "America/Los_Angeles",
      resolution: "1365x768"
    },
    launchRule: {
      urls: ["https://facebook.com"],
      extraArgs: ["--disable-notifications"],
      extensions: []
    },
    extensionIds: [],
    notes: ""
  };
}

function TemplatesPanel({ profiles, groups, onOpenCreate, showToast, showError, refreshProfiles }) {
  const [templates, setTemplates] = useState([]);
  const [extensions, setExtensions] = useState([]);
  const [draft, setDraft] = useState(defaultTemplateDraft(groups));
  const [applyCount, setApplyCount] = useState(1);
  const [selectedTemplateId, setSelectedTemplateId] = useState("");

  async function refreshTemplates() {
    const [templateList, extensionList] = await Promise.all([api.listTemplates(), api.listExtensions().catch(() => [])]);
    setTemplates(templateList || []);
    setExtensions(extensionList || []);
    if (!selectedTemplateId && templateList?.[0]) setSelectedTemplateId(templateList[0].id);
  }

  useEffect(() => {
    refreshTemplates().catch(showError);
  }, []);

  function patch(path, value) {
    setDraft((prev) => {
      const next = clone(prev);
      let target = next;
      for (let index = 0; index < path.length - 1; index += 1) {
        target[path[index]] = target[path[index]] || {};
        target = target[path[index]];
      }
      target[path[path.length - 1]] = value;
      return next;
    });
  }

  function toggleExtension(id) {
    setDraft((prev) => {
      const set = new Set(prev.extensionIds || []);
      set.has(id) ? set.delete(id) : set.add(id);
      return { ...prev, extensionIds: Array.from(set) };
    });
  }

  async function saveTemplate() {
    if (!draft.name.trim()) return showToast("请输入模板名称");
    await api.saveTemplate(draft);
    await refreshTemplates();
    showToast("模板已保存");
  }

  async function deleteTemplate(template) {
    await api.deleteTemplates({ ids: [template.id] });
    await refreshTemplates();
    showToast("模板已删除");
  }

  async function applyTemplate(templateId = selectedTemplateId) {
    if (!templateId) return showToast("请选择模板");
    const created = await api.applyTemplate({ templateId, count: applyCount });
    await refreshProfiles?.();
    showToast(`已按模板创建 ${created.length || 0} 个环境`);
  }

  function loadTemplate(template) {
    setDraft({
      ...defaultTemplateDraft(groups),
      ...template,
      defaults: { ...defaultTemplateDraft(groups).defaults, ...(template.defaults || {}) },
      proxyRule: template.proxyRule || {},
      fingerprintRule: { ...defaultTemplateDraft(groups).fingerprintRule, ...(template.fingerprintRule || {}) },
      launchRule: { ...defaultTemplateDraft(groups).launchRule, ...(template.launchRule || {}) },
      extensionIds: template.extensionIds || []
    });
    setSelectedTemplateId(template.id);
  }

  return (
    <>
      <header className="pageHead">
        <div>
          <h1>模板管理</h1>
          <p>Profile template：默认平台、代理、指纹、启动页、标签和扩展。</p>
        </div>
        <div className="topActions">
          <button onClick={() => onOpenCreate(draft.defaults)}><Plus size={17} />用模板预填</button>
          <button className="accent" onClick={() => applyTemplate()}><ClipboardList size={17} />按模板创建</button>
        </div>
      </header>

      <section className="templateLayout">
        <section className="toolPanel">
          <div className="panelTitle">
            <strong>模板编辑</strong>
            <span>{draft.id ? "编辑模板" : "新模板"}</span>
          </div>
          <div className="formGrid">
            <Field label="模板名称"><input value={draft.name} onChange={(e) => setDraft((prev) => ({ ...prev, name: e.target.value }))} /></Field>
            <Field label="默认平台"><SelectMenu value={draft.platform} options={platformSelectOptions} onChange={(value) => setDraft((prev) => ({ ...prev, platform: value }))} /></Field>
            <Field label="默认分组"><SelectMenu value={draft.group} options={groups.map((group) => ({ value: group, label: group }))} onChange={(value) => setDraft((prev) => ({ ...prev, group: value }))} /></Field>
            <Field label="标签"><input value={toTokens(draft.tags)} onChange={(e) => setDraft((prev) => ({ ...prev, tags: fromTokens(e.target.value) }))} /></Field>
            <Field label="国家 / 地区"><input value={draft.defaults.country || ""} onChange={(e) => patch(["defaults", "country"], e.target.value)} /></Field>
            <Field label="省市"><input value={[draft.defaults.region, draft.defaults.city].filter(Boolean).join(" / ")} onChange={(e) => {
              const [region = "", city = ""] = e.target.value.split("/");
              patch(["defaults", "region"], region.trim());
              patch(["defaults", "city"], city.trim());
            }} /></Field>
            <Field label="内核"><SelectMenu value={draft.fingerprintRule.browser || "sun"} options={browserTypeOptions} onChange={(value) => patch(["fingerprintRule", "browser"], value)} /></Field>
            <Field label="操作系统"><SelectMenu value={draft.fingerprintRule.os || "Windows"} options={osOptions} onChange={(os) => {
              setDraft((prev) => {
                const generated = fingerprintFieldsForOs(os, { browser: prev.fingerprintRule?.browser, os }, { country: prev.defaults?.country });
                const next = clone(prev);
                next.fingerprintRule = {
                  ...next.fingerprintRule,
                  os: generated.fingerprint.os,
                  deviceType: generated.fingerprint.deviceType,
                  language: generated.fingerprint.language,
                  timezone: generated.fingerprint.timezone,
                  resolution: generated.fingerprint.resolution
                };
                next.defaults = {
                  ...(next.defaults || {}),
                  country: generated.geo.country,
                  region: generated.geo.region,
                  city: generated.geo.city
                };
                return next;
              });
            }} /></Field>
            <Field label="语言"><input value={draft.fingerprintRule.language || ""} onChange={(e) => patch(["fingerprintRule", "language"], e.target.value)} /></Field>
            <Field label="时区"><input value={draft.fingerprintRule.timezone || ""} onChange={(e) => patch(["fingerprintRule", "timezone"], e.target.value)} /></Field>
            <Field label="窗口尺寸"><input value={draft.fingerprintRule.resolution || ""} onChange={(e) => patch(["fingerprintRule", "resolution"], e.target.value)} /></Field>
            <Field label="启动页" wide><textarea rows="3" value={toLines(draft.launchRule.urls)} onChange={(e) => patch(["launchRule", "urls"], fromLines(e.target.value))} /></Field>
            <Field label="启动参数" wide><textarea rows="3" value={toLines(draft.launchRule.extraArgs)} onChange={(e) => patch(["launchRule", "extraArgs"], fromLines(e.target.value))} /></Field>
            <Field label="备注" wide><textarea rows="3" value={draft.notes || ""} onChange={(e) => setDraft((prev) => ({ ...prev, notes: e.target.value }))} /></Field>
          </div>
          <div className="extensionPickList">
            {extensions.map((extension) => (
              <button key={extension.id} className={draft.extensionIds?.includes(extension.id) ? "active" : ""} onClick={() => toggleExtension(extension.id)}>
                <Package size={15} />
                <span>{extension.name}</span>
                <small>{extension.category || extension.group}</small>
              </button>
            ))}
          </div>
          <div className="panelActions">
            <button onClick={() => setDraft(defaultTemplateDraft(groups))}>新模板</button>
            <button className="accent" onClick={saveTemplate}><Check size={17} />保存模板</button>
          </div>
        </section>

        <section className="toolPanel">
          <div className="panelTitle">
            <strong>模板库</strong>
            <span>{templates.length} 个模板</span>
          </div>
          <div className="templateApplyBar">
            <SelectMenu
              value={selectedTemplateId}
              options={templates.map((template) => ({ value: template.id, label: template.name }))}
              onChange={setSelectedTemplateId}
              placeholder="选择模板"
            />
            <input type="number" min="1" max="200" value={applyCount} onChange={(e) => setApplyCount(Number(e.target.value) || 1)} />
            <button className="accent" onClick={() => applyTemplate()}><Plus size={17} />创建</button>
          </div>
          <div className="taskList">
            {templates.map((template) => (
              <div className="listCard" key={template.id}>
                <div>
                  <strong>{template.name}</strong>
                  <span>{platformName(template.platform)} · {template.group} · {template.extensionIds?.length || 0} 扩展</span>
                </div>
                <div className="rowActions">
                  <button className="small" onClick={() => loadTemplate(template)}><Edit3 size={15} />编辑</button>
                  <button className="small" onClick={() => applyTemplate(template.id)}><Plus size={15} />创建</button>
                  <button className="small danger" onClick={() => deleteTemplate(template)}><Trash2 size={15} />删除</button>
                </div>
              </div>
            ))}
            {!templates.length ? <div className="emptyInline">暂无模板</div> : null}
          </div>
          <div className="summaryGrid">
            <div><span>环境总数</span><strong>{profiles.length}</strong></div>
            <div><span>扩展库</span><strong>{extensions.length}</strong></div>
            <div><span>模板数</span><strong>{templates.length}</strong></div>
          </div>
        </section>
      </section>
    </>
  );
}

function LogsPanel({ showToast, showError }) {
  const [logs, setLogs] = useState([]);
  const [query, setQuery] = useState("");
  const [action, setAction] = useState("");

  async function refreshLogs(next = {}) {
    const items = await api.listLogs({ query, action, limit: 500, ...next });
    setLogs(items || []);
  }

  useEffect(() => {
    refreshLogs().catch(showError);
  }, []);

  async function exportLogs() {
    const result = await api.exportLogs();
    if (!result?.canceled) showToast(`已导出 ${result.count || 0} 条日志`);
  }

  const actions = Array.from(new Set(logs.map((log) => log.action).filter(Boolean)));

  return (
    <>
      <header className="pageHead">
        <div>
          <h1>操作日志</h1>
          <p>记录环境、代理、扩展、模板、RPA 和同步器的关键操作。</p>
        </div>
        <div className="topActions">
          <button onClick={() => refreshLogs()}><RefreshCcw size={17} />刷新</button>
          <button className="accent" onClick={exportLogs}><Download size={17} />导出</button>
        </div>
      </header>
      <section className="toolPanel">
        <div className="logFilterBar">
          <div className="adsSearch">
            <Search size={18} />
            <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="搜索日志内容、环境 ID、代理 ID" />
          </div>
          <SelectMenu
            value={action}
            options={[{ value: "", label: "全部动作" }, ...actions.map((item) => ({ value: item, label: item }))]}
            onChange={(value) => {
              setAction(value);
              refreshLogs({ action: value }).catch(showError);
            }}
          />
          <button onClick={() => refreshLogs()}><ListFilter size={17} />筛选</button>
        </div>
      </section>
      <section className="tableWrap">
        <table className="simpleTable logTable">
          <thead>
            <tr>
              <th>时间</th>
              <th>动作</th>
              <th>详情</th>
            </tr>
          </thead>
          <tbody>
            {logs.map((log) => (
              <tr key={log.id}>
                <td>{formatDate(log.at)}</td>
                <td><span className="status running">{log.action}</span></td>
                <td><code>{JSON.stringify(log.detail || {})}</code></td>
              </tr>
            ))}
            {!logs.length ? <tr><td colSpan="3"><div className="emptyInline">暂无日志</div></td></tr> : null}
          </tbody>
        </table>
      </section>
    </>
  );
}

function SkillPanel({ showToast, showError }) {
  const [html, setHtml] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  useEffect(() => {
    api.getSkill()
      .then((d) => { setHtml(d.html || ""); setLoading(false); })
      .catch((e) => { setError(String(e.message || e)); setLoading(false); });
  }, []);
  return (
    <section className="skillPanel">
      <header className="panelHeader">
        <div>
          <h1>Skill / 提示词</h1>
          <p>AI 操作手册：API 指南、Python 框架对接、验证凭证处理（CF Turnstile / reCAPTCHA / 2FA / OAuth）、链路探索方法论——全部沉淀自实战。</p>
        </div>
      </header>
      {loading ? <div className="muted">加载手册中…</div> : null}
      {error ? <div className="skillError">{error}（请确认 docs/AI-GUIDE.md 存在）</div> : null}
      {!loading && !error && html ? <div className="skillContent" dangerouslySetInnerHTML={{ __html: html }} /> : null}
    </section>
  );
}

function GlobalSettingsPanel({ settings, setSettings, showToast, showError }) {
  const [form, setForm] = useState(() => settings || {});
  const prefs = {
    blockDevtools: false,
    blockExtensionInstall: false,
    blockImages: false,
    blockMedia: false,
    muteAudio: true,
    autoMatchTimezone: true,
    autoMatchGeolocation: true,
    startupMode: "last",
    restoreSession: true,
    defaultPlatform: "custom",
    virtualCameraPath: "",
    bookmarks: [],
    ...((form || {}).globalPreferences || {})
  };

  useEffect(() => {
    setForm(settings || {});
  }, [settings]);

  const patch = (key, value) => setForm((prev) => ({ ...(prev || {}), [key]: value }));
  const patchPrefs = (key, value) => setForm((prev) => ({
    ...(prev || {}),
    globalPreferences: {
      ...(((prev || {}).globalPreferences) || {}),
      [key]: value
    }
  }));

  async function save() {
    try {
      const saved = await api.updateSettings({ ...form, defaultHomepage: normalizeHomepage(form.defaultHomepage) });
      setSettings(saved);
      showToast("全局设置已保存");
    } catch (error) {
      showError(error);
    }
  }

  return (
    <>
      <header className="pageHead">
        <div>
          <h1>全局设置</h1>
          <p>启动策略、全局偏好、Local API、内核路径和浏览器限制。</p>
        </div>
        <button className="accent" onClick={save}><Check size={17} />保存设置</button>
      </header>

      <section className="settingsGrid">
        <section className="toolPanel">
          <div className="panelTitle"><strong>浏览器路径</strong><span>Chromium / Firefox</span></div>
          <div className="formGrid single">
            <Field label="Chromium 路径" wide><input value={form.sunBrowserPath || ""} onChange={(e) => patch("sunBrowserPath", e.target.value)} /></Field>
            <Field label="Firefox 路径" wide><input value={form.flowerBrowserPath || ""} onChange={(e) => patch("flowerBrowserPath", e.target.value)} /></Field>
            <Field label="Profile 根目录" wide><input value={form.profileRoot || ""} onChange={(e) => patch("profileRoot", e.target.value)} /></Field>
            <Field label="内核下载目录" wide><input value={form.kernelRoot || ""} onChange={(e) => patch("kernelRoot", e.target.value)} /></Field>
            <Field label="默认首页"><input value={form.defaultHomepage || ""} onChange={(e) => patch("defaultHomepage", e.target.value)} onBlur={(e) => patch("defaultHomepage", normalizeHomepage(e.target.value))} placeholder="https://example.com" /></Field>
            <Field label="启动策略"><SelectMenu value={prefs.startupMode} options={[["last", "按环境启动页"], ["homepage", "始终默认首页"], ["blank", "空白页"]]} onChange={(value) => patchPrefs("startupMode", value)} /></Field>
          </div>
        </section>

        <section className="toolPanel">
          <div className="panelTitle"><strong>全局偏好</strong><span>界面开关</span></div>
          <div className="settingsToggleGrid">
            <Toggle label="禁止 DevTools" value={prefs.blockDevtools} onChange={(value) => patchPrefs("blockDevtools", value)} />
            <Toggle label="禁止安装/删除扩展" value={prefs.blockExtensionInstall} onChange={(value) => patchPrefs("blockExtensionInstall", value)} />
            <Toggle label="禁用图片加载" value={prefs.blockImages} onChange={(value) => patchPrefs("blockImages", value)} />
            <Toggle label="限制视频/媒体自动播放" value={prefs.blockMedia} onChange={(value) => patchPrefs("blockMedia", value)} />
            <Toggle label="启动默认静音" value={prefs.muteAudio} onChange={(value) => patchPrefs("muteAudio", value)} />
            <Toggle label="浏览器默认暗色主题" value={form.darkMode} onChange={(value) => patch("darkMode", value)} />
            <Toggle label="按 IP 自动匹配时区" value={prefs.autoMatchTimezone} onChange={(value) => patchPrefs("autoMatchTimezone", value)} />
            <Toggle label="按 IP 自动匹配地理位置" value={prefs.autoMatchGeolocation} onChange={(value) => patchPrefs("autoMatchGeolocation", value)} />
            <Toggle label="恢复上次的标签页" value={prefs.restoreSession} onChange={(value) => patchPrefs("restoreSession", value)} />
          </div>
          <div className="formGrid single">
            <Field label="默认平台"><SelectMenu value={prefs.defaultPlatform} options={platformSelectOptions} onChange={(value) => patchPrefs("defaultPlatform", value)} /></Field>
            <Field label="虚拟摄像头视频文件" wide><input value={prefs.virtualCameraPath || ""} onChange={(e) => patchPrefs("virtualCameraPath", e.target.value)} placeholder="可选：本地 .y4m / .mjpeg 文件" /></Field>
            <Field label="全局书签" wide><textarea rows="4" value={toLines(prefs.bookmarks)} onChange={(e) => patchPrefs("bookmarks", fromLines(e.target.value))} placeholder="每行一个 URL" /></Field>
          </div>
        </section>

        <section className="toolPanel">
          <div className="panelTitle"><strong>Local API</strong><span>本地自动化入口</span></div>
          <div className="formGrid single">
            <Field label="调试端口起点"><input type="number" value={form.baseDebugPort || 43000} onChange={(e) => patch("baseDebugPort", Number(e.target.value))} /></Field>
            <Field label="Local API 端口"><input type="number" value={form.localApiPort || 53225} onChange={(e) => patch("localApiPort", Number(e.target.value))} /></Field>
            <Toggle label="关闭环境时结束进程树" value={form.closeWithTaskTree !== false} onChange={(value) => patch("closeWithTaskTree", value)} />
          </div>
          <div className="apiHint">
            <LockKeyhole size={18} />
            <span>后续可继续补 API Token、白名单和 API 调试页面。</span>
          </div>
        </section>
      </section>
    </>
  );
}

const cacheTypeOptions = [
  { key: "cookies", label: "Cookie" },
  { key: "localStorage", label: "LocalStorage / SessionStorage" },
  { key: "indexedDB", label: "IndexedDB / Databases" },
  { key: "history", label: "History" },
  { key: "cache", label: "Cache / Service Worker" },
  { key: "extensionData", label: "Extension Data" }
];

function CachePanel({ profiles, showToast, showError, refreshProfiles }) {
  const [selected, setSelected] = useState(new Set());
  const [types, setTypes] = useState(new Set(["cache"]));
  const [busy, setBusy] = useState(false);

  function toggleProfile(id) {
    setSelected((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  }

  function toggleType(type) {
    setTypes((prev) => {
      const next = new Set(prev);
      next.has(type) ? next.delete(type) : next.add(type);
      return next;
    });
  }

  async function clearSelectedData() {
    const ids = Array.from(selected);
    const selectedTypes = Array.from(types);
    if (!ids.length) return showToast("请选择环境");
    if (!selectedTypes.length) return showToast("请选择清理类型");
    setBusy(true);
    try {
      await api.clearProfileData({ ids, types: selectedTypes });
      await refreshProfiles();
      showToast("缓存数据已清理");
    } catch (error) {
      showError(error);
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <header className="pageHead">
        <div>
          <h1>缓存管理</h1>
          <p>按环境清理 Cookie、LocalStorage、IndexedDB、History、Cache 和扩展数据。</p>
        </div>
        <div className="topActions">
          <button onClick={() => setSelected(new Set(profiles.map((item) => item.id)))}><Check size={17} />全选环境</button>
          <button className="accent" disabled={busy} onClick={clearSelectedData}><BrushCleaning size={17} />清理所选</button>
        </div>
      </header>

      <section className="toolPanel">
        <div className="panelTitle">
          <strong>清理类型</strong>
          <span>{types.size} 项</span>
        </div>
        <div className="cacheTypeGrid">
          {cacheTypeOptions.map((item) => (
            <Toggle key={item.key} label={item.label} value={types.has(item.key)} onChange={() => toggleType(item.key)} />
          ))}
        </div>
      </section>

      <section className="tableWrap">
        <table className="simpleTable">
          <thead>
            <tr>
              <th className="checkCol">选中</th>
              <th>环境</th>
              <th>分组</th>
              <th>状态</th>
              <th>数据同步</th>
              <th>关闭后清理</th>
            </tr>
          </thead>
          <tbody>
            {profiles.map((profile) => (
              <tr key={profile.id} className={selected.has(profile.id) ? "selected" : ""}>
                <td><button className="check" onClick={() => toggleProfile(profile.id)}>{selected.has(profile.id) ? <Check size={14} /> : null}</button></td>
                <td>{profile.name}</td>
                <td>{profile.group}</td>
                <td>{profile.status === "running" ? "运行中" : "已关闭"}</td>
                <td>{profile.storage?.cloudSync ? "已开启" : "本地"}</td>
                <td>{profile.storage?.clearCacheAfterClose ? "开启" : "关闭"}</td>
              </tr>
            ))}
            {!profiles.length ? (
              <tr><td colSpan="6"><div className="emptyInline">暂无环境</div></td></tr>
            ) : null}
          </tbody>
        </table>
      </section>
    </>
  );
}

function ProxyCheckMini({ result, loading }) {
  if (loading) return <span className="checkMini loading"><span />检测中</span>;
  if (!result) return <span className="muted">未检测</span>;
  if (!result.ok) return <span className="checkMini failed"><span />失败</span>;
  return <span className="checkMini ok"><span />{result.ip} · {result.latencyMs}ms</span>;
}

function ProxyPanel({ profiles, onEdit, onOpenCreate, showToast, showError }) {
  const proxyProfiles = profiles.filter((profile) => profile.proxy?.type !== "no_proxy");
  const [testingId, setTestingId] = useState("");
  const [results, setResults] = useState({});
  const [library, setLibrary] = useState([]);
  const [proxyText, setProxyText] = useState("");
  const [settings, setSettings] = useState(null);
  const [proxySettings, setProxySettings] = useState(defaultProxySettings);
  const [globalProxyText, setGlobalProxyText] = useState("");

  async function refreshLibrary() {
    setLibrary(await api.listProxies());
  }

  async function refreshProxySettings() {
    const next = await api.getSettings();
    const normalized = normalizeProxySettingsForm(next.proxySettings);
    setSettings(next);
    setProxySettings(normalized);
    setGlobalProxyText(proxyToLine(normalized.globalProxy));
  }

  useEffect(() => {
    refreshLibrary().catch(showError);
    refreshProxySettings().catch(showError);
  }, []);

  const patchProxySettings = (patch) => setProxySettings((prev) => normalizeProxySettingsForm({ ...prev, ...patch }));
  const patchProxyFilters = (patch) => setProxySettings((prev) => normalizeProxySettingsForm({ ...prev, filters: { ...(prev.filters || {}), ...patch } }));

  async function checkProxy(profile) {
    setTestingId(profile.id);
    try {
      const result = await api.testProxy({ proxy: profile.proxy, checker: proxySettings.checker });
      setResults((prev) => ({ ...prev, [profile.id]: result }));
    } catch (error) {
      setResults((prev) => ({ ...prev, [profile.id]: { ok: false, error: error?.message || String(error) } }));
    } finally {
      setTestingId("");
    }
  }

  async function saveProxyText() {
    const items = String(proxyText || "")
      .split(/\r?\n/)
      .map((line) => line.trim())
      .filter(Boolean)
      .map(parseProxyImportLine)
      .filter((item) => item.proxy_string);
    if (!items.length) return showToast("请粘贴代理");
    await api.saveProxies({ items });
    setProxyText("");
    await refreshLibrary();
    showToast(`已保存 ${items.length} 条代理`);
  }

  async function checkSavedProxy(proxy) {
    setTestingId(proxy.id);
    try {
      await api.checkProxies({ ids: [proxy.id], checker: proxySettings.checker });
      await refreshLibrary();
    } catch (error) {
      showError(error);
    } finally {
      setTestingId("");
    }
  }

  async function saveProxySettings() {
    const globalProxy = parseProxyString(globalProxyText) || proxySettings.globalProxy || {};
    const next = normalizeProxySettingsForm({ ...proxySettings, globalProxy });
    const saved = await api.updateSettings({ ...(settings || {}), proxySettings: next });
    setSettings(saved);
    setProxySettings(normalizeProxySettingsForm(saved.proxySettings));
    setGlobalProxyText(proxyToLine(saved.proxySettings?.globalProxy || globalProxy));
    showToast("代理策略已保存");
  }

  async function checkAllSavedProxies() {
    setTestingId("all");
    try {
      await api.checkProxies({ checker: proxySettings.checker });
      await refreshLibrary();
      showToast("代理池检测完成");
    } catch (error) {
      showError(error);
    } finally {
      setTestingId("");
    }
  }

  async function deleteSavedProxy(proxy) {
    await api.deleteProxies({ ids: [proxy.id] });
    await refreshLibrary();
    showToast("已删除代理");
  }

  return (
    <>
      <header className="pageHead">
        <div>
          <h1>代理管理</h1>
          <p>支持全局代理、代理池轮换、地区 / ASN 筛选、检测源选择和失败自动换 IP。</p>
        </div>
        <button className="accent" onClick={() => onOpenCreate()}><Plus size={17} />新建环境</button>
      </header>

      <section className="noticeBar">
        <Network size={16} />
        <span>代理库支持常见格式自动识别：host:port、host:port:user:pass、user:pass@host:port、http/socks5 URL。</span>
      </section>

      <section className="toolPanel">
        <div className="panelTitle">
          <strong>全局代理策略</strong>
          <span>{proxySettings.enabled ? "已启用" : "未启用"}</span>
        </div>
        <div className="formGrid proxySettingsGrid">
          <Toggle label="启用全局代理接管" value={proxySettings.enabled} onChange={(value) => patchProxySettings({ enabled: value })} />
          <Toggle label="失败自动重试 / 换 IP" value={proxySettings.retryOnFail} onChange={(value) => patchProxySettings({ retryOnFail: value })} />
          <Field label="代理模式">
            <SelectMenu value={proxySettings.mode} options={proxyModeOptions} onChange={(value) => patchProxySettings({ mode: value })} />
          </Field>
          <Field label="轮换策略">
            <SelectMenu value={proxySettings.rotateStrategy} options={proxyRotateOptions} onChange={(value) => patchProxySettings({ rotateStrategy: value })} />
          </Field>
          <Field label="IP Checker">
            <SelectMenu value={proxySettings.checker} options={proxyCheckerOptions} onChange={(value) => patchProxySettings({ checker: value })} />
          </Field>
          <Field label="最大重试次数">
            <input type="number" min="0" max="10" value={proxySettings.maxRetries} onChange={(event) => patchProxySettings({ maxRetries: Number(event.target.value) })} />
          </Field>
          <Field label="全局代理" wide>
            <input value={globalProxyText} onChange={(event) => setGlobalProxyText(event.target.value)} placeholder="http://user:pass@host:port 或 host:port:user:pass" />
          </Field>
          <Field label="国家筛选"><input value={proxySettings.filters.country} onChange={(event) => patchProxyFilters({ country: event.target.value })} placeholder="US / China" /></Field>
          <Field label="省州筛选"><input value={proxySettings.filters.region} onChange={(event) => patchProxyFilters({ region: event.target.value })} placeholder="California / Guangdong" /></Field>
          <Field label="城市筛选"><input value={proxySettings.filters.city} onChange={(event) => patchProxyFilters({ city: event.target.value })} placeholder="Los Angeles" /></Field>
          <Field label="ASN 筛选"><input value={proxySettings.filters.asn} onChange={(event) => patchProxyFilters({ asn: event.target.value })} placeholder="AS15169" /></Field>
          <Field label="上次 IP 筛选"><input value={proxySettings.filters.lastIp} onChange={(event) => patchProxyFilters({ lastIp: event.target.value })} placeholder="1.2.3." /></Field>
        </div>
        <div className="panelActions">
          <button onClick={checkAllSavedProxies} disabled={testingId === "all"}><Shield size={17} />检测代理池</button>
          <button className="accent" onClick={saveProxySettings}><Check size={17} />保存代理策略</button>
        </div>
      </section>

      <section className="toolPanel">
        <div className="panelTitle">
          <strong>代理库</strong>
          <span>{library.length} 条保存的代理</span>
        </div>
        <div className="inlineInput">
          <textarea value={proxyText} onChange={(e) => setProxyText(e.target.value)} rows="4" placeholder="每行一个代理；可追加 |国家|省州|城市|ASN|换IP链接|分组" />
          <button className="accent" onClick={saveProxyText}><Plus size={17} />保存代理</button>
        </div>
      </section>

      <section className="tableWrap">
        <table className="simpleTable proxyTable">
          <thead>
            <tr>
              <th>名称</th>
              <th>类型</th>
              <th>主机</th>
              <th>端口</th>
              <th>账号</th>
              <th>状态</th>
              <th>地区</th>
              <th>ASN</th>
              <th>上次 IP</th>
              <th>使用 / 失败</th>
              <th className="actionCol">操作</th>
            </tr>
          </thead>
          <tbody>
            {library.map((proxy) => (
              <tr key={proxy.id}>
                <td>{proxy.name}</td>
                <td>{String(proxy.type).toUpperCase()}</td>
                <td>{proxy.host}</td>
                <td>{proxy.port}</td>
                <td>{proxy.username || "-"}</td>
                <td>{proxy.status === "ok" ? `${proxy.latencyMs}ms` : proxy.status}</td>
                <td>{[proxy.country, proxy.region, proxy.city].filter(Boolean).join(" / ") || "-"}</td>
                <td>{proxy.asn || "-"}</td>
                <td>{proxy.lastIp || "-"}</td>
                <td>{Number(proxy.useCount || 0)} / {Number(proxy.failCount || 0)}</td>
                <td>
                  <div className="rowActions">
                    <button className="small" disabled={testingId === proxy.id} onClick={() => checkSavedProxy(proxy)}><Shield size={15} />检测</button>
                    <button className="small danger" onClick={() => deleteSavedProxy(proxy)}><Trash2 size={15} />删除</button>
                  </div>
                </td>
              </tr>
            ))}
            {!library.length ? (
              <tr><td colSpan="11"><div className="emptyInline">暂无代理库数据</div></td></tr>
            ) : null}
          </tbody>
        </table>
      </section>

      <section className="tableWrap">
        <table className="simpleTable">
          <thead>
            <tr>
              <th>环境</th>
              <th>类型</th>
              <th>主机</th>
              <th>端口</th>
              <th>账号</th>
              <th className="actionCol">操作</th>
            </tr>
          </thead>
          <tbody>
            {proxyProfiles.map((profile) => (
              <tr key={profile.id}>
                <td>{profile.name}</td>
                <td>{String(profile.proxy.type).toUpperCase()}</td>
                <td>{profile.proxy.host}</td>
                <td>{profile.proxy.port}</td>
                <td>{profile.proxy.username || "-"}</td>
                <td>
                  <div className="rowActions">
                    <ProxyCheckMini result={results[profile.id]} loading={testingId === profile.id} />
                    <button className="small" onClick={() => checkProxy(profile)}><Shield size={15} />检测</button>
                    <button className="small" onClick={() => onEdit(profile)}><Edit3 size={15} />编辑</button>
                  </div>
                </td>
              </tr>
            ))}
            {!proxyProfiles.length ? (
              <tr><td colSpan="6"><div className="emptyState"><Wifi size={30} /><strong>没有代理配置</strong><span>在环境编辑的代理页添加。</span></div></td></tr>
            ) : null}
          </tbody>
        </table>
      </section>
    </>
  );
}

function ProxyCheckCard({ result, loading }) {
  if (loading) {
    return (
      <div className="proxyCheckCard scanning">
        <div className="scanLine" />
        <div className="scanHeader">
          <span className="pulseDot" />
          <strong>正在检测代理连通性</strong>
        </div>
        <p>正在建立代理隧道、请求出口 IP、解析地区信息。</p>
      </div>
    );
  }
  if (!result) return null;
  if (!result.ok) {
    return (
      <div className="proxyCheckCard failed">
        <div className="scanHeader">
          <span className="pulseDot" />
          <strong>检测失败</strong>
        </div>
        <p>{result.error || "代理不可用或连接超时"}</p>
      </div>
    );
  }
  return (
    <div className="proxyCheckCard ok">
      <div className="scanHeader">
        <span className="pulseDot" />
        <strong>检测通过</strong>
      </div>
      <div className="checkGrid">
        <span>出口 IP</span><strong>{result.ip}</strong>
        <span>延迟</span><strong>{result.latencyMs} ms</strong>
        <span>地区</span><strong>{[result.country, result.region, result.city].filter(Boolean).join(" / ") || "-"}</strong>
        <span>ISP</span><strong>{result.isp || "-"}</strong>
        <span>时区</span><strong>{result.timezone || "-"}</strong>
        <span>语言</span><strong>{result.language || "-"}</strong>
      </div>
    </div>
  );
}

const rpaActionTypes = [
  { type: "openUrl", label: "打开网址", hint: "跳转到指定 URL" },
  { type: "wait", label: "等待", hint: "暂停指定毫秒" },
  { type: "click", label: "点击元素", hint: "CSS selector 点击" },
  { type: "typeText", label: "输入文本", hint: "定位输入框并输入" },
  { type: "scroll", label: "滚动页面", hint: "按像素滚动" },
  { type: "evalJs", label: "执行 JS", hint: "运行页面脚本" },
  { type: "screenshot", label: "截图", hint: "保存 PNG 截图" },
  { type: "closeProfile", label: "关闭环境", hint: "流程结束后关闭" },
  { type: "setVariable", label: "设置变量", hint: "保存变量供后续步骤使用" },
  { type: "condition", label: "条件判断", hint: "按变量执行分支" },
  { type: "loop", label: "循环节点", hint: "重复执行子步骤" }
];

function newRpaStep(type = "wait") {
  const defaults = {
    openUrl: { url: "https://example.com", waitMs: 1200 },
    wait: { ms: 1000 },
    click: { selector: "button", afterMs: 300 },
    typeText: { selector: "input", text: "", afterMs: 300 },
    scroll: { x: 0, y: 600, afterMs: 300 },
    evalJs: { code: "document.title" },
    screenshot: { name: "rpa-shot" },
    closeProfile: {},
    setVariable: { key: "name", value: "" },
    condition: { left: "{{name}}", op: "exists", right: "", thenSteps: [], elseSteps: [] },
    loop: { count: 3, steps: [] }
  };
  const meta = rpaActionTypes.find((item) => item.type === type) || rpaActionTypes[1];
  const params = { ...(defaults[type] || {}) };
  // Build the nested wait step only after defaults exist. Putting newRpaStep("wait") inside
  // the defaults object recursed forever: every call evaluated loop.steps again.
  if (type === "loop" && !params.steps.length) params.steps = [newRpaStep("wait")];
  return {
    id: `step_${Date.now()}_${Math.random().toString(16).slice(2)}`,
    type,
    name: meta.label,
    enabled: true,
    params,
    note: ""
  };
}

function newRpaFlow() {
  return {
    id: `rpa_${Date.now()}_${Math.random().toString(16).slice(2)}`,
    name: "新建 RPA 流程",
    group: DEFAULT_GROUP,
    description: "",
    steps: [newRpaStep("openUrl"), newRpaStep("wait"), newRpaStep("screenshot")],
    lastStatus: "idle",
    runCount: 0
  };
}

function RpaPanel({ profiles, showToast, showError }) {
  const [flows, setFlows] = useState([]);
  const [runs, setRuns] = useState([]);
  const [tasks, setTasks] = useState([]);
  const [activeFlowId, setActiveFlowId] = useState("");
  const [selectedStepId, setSelectedStepId] = useState("");
  const dragRef = useRef({});
  const [targetProfileId, setTargetProfileId] = useState("");
  const [running, setRunning] = useState(false);
  const activeFlow = flows.find((flow) => flow.id === activeFlowId) || flows[0] || null;
  const selectedStep = activeFlow?.steps?.find((step) => step.id === selectedStepId) || activeFlow?.steps?.[0] || null;
  const latestRun = runs[0];

  async function refresh() {
    const data = await api.listRpa();
    const taskData = await api.listRpaTasks().catch(() => ({ tasks: [] }));
    setFlows(data.flows || []);
    setRuns([...(data.runs || [])].reverse());
    setTasks(taskData.tasks || []);
    if (!activeFlowId && data.flows?.[0]) setActiveFlowId(data.flows[0].id);
  }

  useEffect(() => {
    refresh().catch(showError);
  }, []);

  function updateFlow(mutator) {
    setFlows((prev) => prev.map((flow) => {
      if (flow.id !== activeFlow?.id) return flow;
      const next = clone(flow);
      mutator(next);
      return next;
    }));
  }

  async function createFlow() {
    const flow = newRpaFlow();
    const saved = await api.saveRpa(flow);
    setFlows((prev) => [saved, ...prev]);
    setActiveFlowId(saved.id);
    setSelectedStepId(saved.steps[0]?.id || "");
    showToast("已创建 RPA 流程");
  }

  async function saveFlow() {
    if (!activeFlow) return;
    const saved = await api.saveRpa(activeFlow);
    setFlows((prev) => prev.map((flow) => (flow.id === saved.id ? saved : flow)));
    showToast("RPA 流程已保存");
  }

  async function deleteFlow() {
    if (!activeFlow) return;
    await api.deleteRpa({ ids: [activeFlow.id] });
    await refresh();
    showToast("已删除 RPA 流程");
  }

  async function duplicateFlow() {
    if (!activeFlow) return;
    await api.duplicateRpa({ ids: [activeFlow.id] });
    await refresh();
    showToast("已复制 RPA 流程");
  }

  function addStep(type) {
    const step = newRpaStep(type);
    updateFlow((flow) => {
      flow.steps.push(step);
    });
    setSelectedStepId(step.id);
  }

  function deleteStep(stepId) {
    updateFlow((flow) => {
      flow.steps = flow.steps.filter((step) => step.id !== stepId);
    });
    setSelectedStepId("");
  }

  function moveStep(stepId, direction) {
    updateFlow((flow) => {
      const index = flow.steps.findIndex((step) => step.id === stepId);
      const nextIndex = index + direction;
      if (index < 0 || nextIndex < 0 || nextIndex >= flow.steps.length) return;
      const [step] = flow.steps.splice(index, 1);
      flow.steps.splice(nextIndex, 0, step);
    });
  }

  function patchStep(stepId, patch) {
    updateFlow((flow) => {
      flow.steps = flow.steps.map((step) => (step.id === stepId ? { ...step, ...patch } : step));
    });
  }

  function patchStepParam(stepId, key, value) {
    updateFlow((flow) => {
      flow.steps = flow.steps.map((step) => (
        step.id === stepId ? { ...step, params: { ...(step.params || {}), [key]: value } } : step
      ));
    });
  }

  async function runFlow() {
    if (!activeFlow) return;
    await saveFlow();
    setRunning(true);
    try {
      const profileIds = targetProfileId ? [targetProfileId] : profiles.slice(0, 1).map((profile) => profile.id);
      const result = await api.runRpa({ flowId: activeFlow.id, profileIds });
      await refresh();
      if (result.status === "success") showToast("RPA 运行完成");
      else showError(result.error || "RPA 运行失败");
    } catch (error) {
      showError(error);
    } finally {
      setRunning(false);
    }
  }

  return (
    <>
      <header className="pageHead">
        <div>
          <h1>RPA 流程</h1>
          <p>可视化编排浏览器动作，并通过本地 DevTools 调试端口在环境中执行。</p>
        </div>
        <div className="topActions">
          <button onClick={refresh}><RefreshCcw size={17} />刷新</button>
          <button onClick={duplicateFlow} disabled={!activeFlow}><Copy size={17} />复制</button>
          <button onClick={saveFlow} disabled={!activeFlow}><Check size={17} />保存</button>
          <button className="accent" onClick={createFlow}><Plus size={17} />新建流程</button>
        </div>
      </header>

      <section className="rpaShell">
        <aside className="rpaList">
          <div className="panelTitle">
            <strong>流程列表</strong>
            <span>{flows.length}</span>
          </div>
          <div className="rpaFlowList">
            {flows.map((flow) => (
              <button key={flow.id} className={flow.id === activeFlow?.id ? "active" : ""} onClick={() => { setActiveFlowId(flow.id); setSelectedStepId(flow.steps?.[0]?.id || ""); }}>
                <FileCode2 size={16} />
                <span>{flow.name}</span>
                <small>{flow.steps?.length || 0} 步</small>
              </button>
            ))}
            {!flows.length ? <div className="emptyInline">暂无流程</div> : null}
          </div>
          <div className="rpaRunBox">
            <Field label="运行环境">
              <SelectMenu
                value={targetProfileId}
                options={[
                  { value: "", label: "第一个环境" },
                  ...profiles.map((profile) => ({ value: profile.id, label: profile.name || profile.id }))
                ]}
                onChange={setTargetProfileId}
              />
            </Field>
            <button className="accent" disabled={!activeFlow || running || !profiles.length} onClick={runFlow}><Play size={17} />{running ? "运行中" : "运行流程"}</button>
            <button className="danger" disabled={!activeFlow} onClick={deleteFlow}><Trash2 size={17} />删除流程</button>
          </div>
        </aside>

        <section className="rpaCanvas">
          {activeFlow ? (
            <>
              <div className="rpaFlowHead">
                <input value={activeFlow.name} onChange={(e) => updateFlow((flow) => { flow.name = e.target.value; })} />
                <input value={activeFlow.description || ""} onChange={(e) => updateFlow((flow) => { flow.description = e.target.value; })} placeholder="流程描述" />
              </div>
              <div className="rpaActions">
                {rpaActionTypes.map((action) => (
                  <button key={action.type} onClick={() => addStep(action.type)}>
                    <Plus size={14} />
                    <span>{action.label}</span>
                  </button>
                ))}
              </div>
              <div className="rpaSteps" onDragOver={(e) => e.preventDefault()}>
                {activeFlow.steps.map((step, index) => (
                  <button
                    key={step.id}
                    draggable
                    onDragStart={() => { dragRef.current.from = index; }}
                    onDragEnter={() => { dragRef.current.to = index; }}
                    onDrop={() => {
                      const { from, to } = dragRef.current;
                      if (from === undefined || to === undefined || from === to) return;
                      updateFlow((flow) => {
                        const steps = [...flow.steps];
                        const [moved] = steps.splice(from, 1);
                        steps.splice(to, 0, moved);
                        flow.steps = steps;
                      });
                    }}
                    className={`rpaStep ${step.id === selectedStep?.id ? "active" : ""} ${step.enabled === false ? "disabled" : ""}`}
                    onClick={() => setSelectedStepId(step.id)}
                  >
                    <span className="rpaStepNo">{index + 1}</span>
                    <div>
                      <strong>{step.name}</strong>
                      <small>{rpaStepSummary(step)}</small>
                    </div>
                    <span className="rpaStepType">{step.type}</span>
                  </button>
                ))}
              </div>
            </>
          ) : (
            <div className="emptyState">
              <Split size={34} />
              <strong>没有 RPA 流程</strong>
              <span>新建一个流程后开始添加浏览器动作。</span>
              <button className="accent" onClick={createFlow}><Plus size={17} />新建流程</button>
            </div>
          )}
        </section>

        <aside className="rpaInspector">
          <div className="panelTitle">
            <strong>步骤参数</strong>
            <span>{selectedStep?.type || "-"}</span>
          </div>
          {selectedStep ? (
            <RpaStepEditor
              step={selectedStep}
              patchStep={(patch) => patchStep(selectedStep.id, patch)}
              patchParam={(key, value) => patchStepParam(selectedStep.id, key, value)}
              moveUp={() => moveStep(selectedStep.id, -1)}
              moveDown={() => moveStep(selectedStep.id, 1)}
              deleteStep={() => deleteStep(selectedStep.id)}
            />
          ) : <div className="emptyInline">请选择步骤</div>}
          <div className="rpaRunLog">
            <div className="panelTitle">
              <strong>最近运行</strong>
              <span>{latestRun?.status || "无"}</span>
            </div>
            {latestRun?.logs?.slice(-6).map((log) => (
              <div className={`runLog ${log.status}`} key={`${log.stepId}-${log.endedAt}`}>
                <strong>{log.stepName}</strong>
                <span>{log.message}</span>
              </div>
            ))}
            {!latestRun ? <div className="emptyInline">暂无运行记录</div> : null}
          </div>
        </aside>
      </section>
      <RpaTaskPanel
        flows={flows}
        profiles={profiles}
        tasks={tasks}
        refresh={refresh}
        showToast={showToast}
        showError={showError}
      />
    </>
  );
}

function rpaStepSummary(step) {
  const params = step.params || {};
  if (step.type === "openUrl") return params.url || "未设置 URL";
  if (step.type === "wait") return `${params.ms || 1000}ms`;
  if (step.type === "click") return params.selector || "未设置 selector";
  if (step.type === "typeText") return `${params.selector || "input"} -> ${params.text || ""}`;
  if (step.type === "scroll") return `${params.x || 0}, ${params.y || 600}`;
  if (step.type === "evalJs") return String(params.code || "").slice(0, 80);
  if (step.type === "screenshot") return params.name || "screenshot";
  if (step.type === "closeProfile") return "关闭当前环境";
  if (step.type === "setVariable") return `${params.key || "var"} = ${params.value || ""}`;
  if (step.type === "condition") return `${params.left || ""} ${params.op || "exists"} ${params.right || ""}`;
  if (step.type === "loop") return `${params.count || 1} 次`;
  return step.type;
}

function RpaTaskPanel({ flows, profiles, tasks, refresh, showToast, showError }) {
  const [draft, setDraft] = useState({
    name: "RPA Task",
    flowId: "",
    profileIds: [],
    type: "once",
    enabled: true,
    schedule: { runAt: "", intervalMinutes: 60, loopCount: 0, retryCount: 0, retryDelayMs: 3000 }
  });

  useEffect(() => {
    if (!draft.flowId && flows[0]?.id) setDraft((prev) => ({ ...prev, flowId: flows[0].id }));
  }, [flows, draft.flowId]);

  const patch = (key, value) => setDraft((prev) => ({ ...prev, [key]: value }));
  const patchSchedule = (key, value) => setDraft((prev) => ({ ...prev, schedule: { ...(prev.schedule || {}), [key]: value } }));

  function toggleProfile(id) {
    setDraft((prev) => {
      const set = new Set(prev.profileIds || []);
      set.has(id) ? set.delete(id) : set.add(id);
      return { ...prev, profileIds: Array.from(set) };
    });
  }

  async function saveTask() {
    if (!draft.flowId) return showToast("请选择流程");
    if (!draft.profileIds.length) return showToast("请选择环境");
    await api.saveRpaTask(draft);
    await refresh();
    showToast("RPA 任务已保存");
  }

  async function runTask(task) {
    try {
      await api.runRpaTask({ id: task.id });
      await refresh();
      showToast("RPA 任务已执行");
    } catch (error) {
      showError(error);
    }
  }

  async function deleteTask(task) {
    await api.deleteRpaTask({ ids: [task.id] });
    await refresh();
    showToast("RPA 任务已删除");
  }

  return (
    <section className="toolPanel rpaTaskPanel">
      <div className="panelTitle">
        <strong>RPA 任务调度</strong>
        <span>{tasks.length} 个任务</span>
      </div>
      <div className="formGrid">
        <Field label="任务名称"><input value={draft.name} onChange={(e) => patch("name", e.target.value)} /></Field>
        <Field label="选择流程">
          <SelectMenu value={draft.flowId} options={flows.map((flow) => ({ value: flow.id, label: flow.name }))} onChange={(value) => patch("flowId", value)} />
        </Field>
        <Field label="任务类型">
          <SelectMenu
            value={draft.type}
            options={[
              { value: "once", label: "立即执行一次" },
              { value: "scheduled", label: "定时执行" },
              { value: "interval", label: "循环任务" }
            ]}
            onChange={(value) => patch("type", value)}
          />
        </Field>
        <Toggle label="启用任务" value={draft.enabled} onChange={(value) => patch("enabled", value)} />
        <Field label="定时时间"><input type="datetime-local" value={draft.schedule.runAt || ""} onChange={(e) => patchSchedule("runAt", e.target.value)} /></Field>
        <Field label="循环间隔分钟"><input type="number" min="1" value={draft.schedule.intervalMinutes} onChange={(e) => patchSchedule("intervalMinutes", Number(e.target.value))} /></Field>
        <Field label="循环次数 0=不限"><input type="number" min="0" value={draft.schedule.loopCount} onChange={(e) => patchSchedule("loopCount", Number(e.target.value))} /></Field>
        <Field label="失败重试次数"><input type="number" min="0" max="10" value={draft.schedule.retryCount} onChange={(e) => patchSchedule("retryCount", Number(e.target.value))} /></Field>
      </div>
      <div className="miniGrid rpaTargetGrid">
        {profiles.map((profile) => (
          <button key={profile.id} className={draft.profileIds.includes(profile.id) ? "active" : ""} onClick={() => toggleProfile(profile.id)}>
            <Monitor size={16} />
            <strong>{profile.name}</strong>
            <span>{profile.group}</span>
          </button>
        ))}
      </div>
      <div className="panelActions">
        <button className="accent" onClick={saveTask}><Check size={17} />保存任务</button>
      </div>
      <div className="taskList">
        {tasks.map((task) => (
          <div className="runLog" key={task.id}>
            <strong>{task.name}</strong>
            <span>{task.type} · {task.lastStatus} · next {task.nextRunAt ? formatDate(task.nextRunAt) : "-"}</span>
            <div className="rowActions">
              <button className="small" onClick={() => runTask(task)}><Play size={15} />立即执行</button>
              <button className="small danger" onClick={() => deleteTask(task)}><Trash2 size={15} />删除</button>
            </div>
          </div>
        ))}
        {!tasks.length ? <div className="emptyInline">暂无调度任务</div> : null}
      </div>
    </section>
  );
}

function RpaStepEditor({ step, patchStep, patchParam, moveUp, moveDown, deleteStep }) {
  const params = step.params || {};
  return (
    <div className="rpaStepEditor">
      <Field label="步骤名称"><input value={step.name} onChange={(e) => patchStep({ name: e.target.value })} /></Field>
      <Toggle label="启用步骤" value={step.enabled !== false} onChange={(value) => patchStep({ enabled: value })} />
      {step.type === "openUrl" ? (
        <>
          <Field label="URL"><input value={params.url || ""} onChange={(e) => patchParam("url", e.target.value)} /></Field>
          <Field label="打开后等待 ms"><input type="number" value={params.waitMs || 1200} onChange={(e) => patchParam("waitMs", Number(e.target.value))} /></Field>
        </>
      ) : null}
      {step.type === "wait" ? (
        <Field label="等待 ms"><input type="number" value={params.ms || 1000} onChange={(e) => patchParam("ms", Number(e.target.value))} /></Field>
      ) : null}
      {step.type === "click" ? (
        <>
          <Field label="CSS Selector"><input value={params.selector || ""} onChange={(e) => patchParam("selector", e.target.value)} /></Field>
          <Field label="点击后等待 ms"><input type="number" value={params.afterMs || 300} onChange={(e) => patchParam("afterMs", Number(e.target.value))} /></Field>
        </>
      ) : null}
      {step.type === "typeText" ? (
        <>
          <Field label="CSS Selector"><input value={params.selector || ""} onChange={(e) => patchParam("selector", e.target.value)} /></Field>
          <Field label="输入文本"><textarea rows="4" value={params.text || ""} onChange={(e) => patchParam("text", e.target.value)} /></Field>
          <Field label="输入后等待 ms"><input type="number" value={params.afterMs || 300} onChange={(e) => patchParam("afterMs", Number(e.target.value))} /></Field>
        </>
      ) : null}
      {step.type === "scroll" ? (
        <>
          <Field label="X"><input type="number" value={params.x || 0} onChange={(e) => patchParam("x", Number(e.target.value))} /></Field>
          <Field label="Y"><input type="number" value={params.y || 600} onChange={(e) => patchParam("y", Number(e.target.value))} /></Field>
          <Field label="滚动后等待 ms"><input type="number" value={params.afterMs || 300} onChange={(e) => patchParam("afterMs", Number(e.target.value))} /></Field>
        </>
      ) : null}
      {step.type === "evalJs" ? (
        <Field label="JavaScript"><textarea rows="7" value={params.code || ""} onChange={(e) => patchParam("code", e.target.value)} /></Field>
      ) : null}
      {step.type === "screenshot" ? (
        <Field label="文件名前缀"><input value={params.name || ""} onChange={(e) => patchParam("name", e.target.value)} /></Field>
      ) : null}
      {step.type === "setVariable" ? (
        <>
          <Field label="变量名"><input value={params.key || ""} onChange={(e) => patchParam("key", e.target.value)} /></Field>
          <Field label="变量值"><input value={params.value || ""} onChange={(e) => patchParam("value", e.target.value)} placeholder="支持 {{profileId}}" /></Field>
        </>
      ) : null}
      {step.type === "condition" ? (
        <>
          <Field label="左值"><input value={params.left || ""} onChange={(e) => patchParam("left", e.target.value)} placeholder="{{name}}" /></Field>
          <Field label="操作符">
            <SelectMenu
              value={params.op || "exists"}
              options={["exists", "not_exists", "equals", "not_equals", "contains", "not_contains", "gt", "lt", "regex"]}
              onChange={(value) => patchParam("op", value)}
            />
          </Field>
          <Field label="右值"><input value={params.right || ""} onChange={(e) => patchParam("right", e.target.value)} /></Field>
          <Field label="Then 子步骤 JSON" wide><textarea rows="4" value={JSON.stringify(params.thenSteps || [], null, 2)} onChange={(e) => {
            try { patchParam("thenSteps", JSON.parse(e.target.value || "[]")); } catch {}
          }} /></Field>
          <Field label="Else 子步骤 JSON" wide><textarea rows="4" value={JSON.stringify(params.elseSteps || [], null, 2)} onChange={(e) => {
            try { patchParam("elseSteps", JSON.parse(e.target.value || "[]")); } catch {}
          }} /></Field>
        </>
      ) : null}
      {step.type === "loop" ? (
        <>
          <Field label="循环次数"><input type="number" min="1" max="100" value={params.count || 1} onChange={(e) => patchParam("count", Number(e.target.value))} /></Field>
          <Field label="子步骤 JSON" wide><textarea rows="6" value={JSON.stringify(params.steps || [], null, 2)} onChange={(e) => {
            try { patchParam("steps", JSON.parse(e.target.value || "[]")); } catch {}
          }} /></Field>
        </>
      ) : null}
      {step.type === "closeProfile" ? (
        <div className="noticeBar"><Square size={16} /><span>该步骤会关闭正在运行的目标环境。</span></div>
      ) : null}
      <Field label="备注"><textarea rows="3" value={step.note || ""} onChange={(e) => patchStep({ note: e.target.value })} /></Field>
      <div className="rpaStepControls">
        <button onClick={moveUp}>上移</button>
        <button onClick={moveDown}>下移</button>
        <button className="danger" onClick={deleteStep}><Trash2 size={16} />删除</button>
      </div>
    </div>
  );
}

function SyncPanelLegacy({ profiles, showToast, showError, refreshProfiles }) {
  const [masterId, setMasterId] = useState("");
  const [followerIds, setFollowerIds] = useState(new Set());
  const [sessions, setSessions] = useState([]);
  const [busy, setBusy] = useState(false);
  const [captureMouse, setCaptureMouse] = useState(true);
  const [captureKeyboard, setCaptureKeyboard] = useState(true);
  const [captureWheel, setCaptureWheel] = useState(true);
  const running = sessions[0] || null;

  async function refreshSync() {
    const result = await api.syncStatus();
    setSessions(result.sessions || []);
  }

  useEffect(() => {
    refreshSync().catch(showError);
  }, []);

  function toggleFollower(id) {
    setFollowerIds((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  }

  async function start() {
    const followers = Array.from(followerIds).filter((id) => id !== masterId);
    if (!masterId) return showToast("请选择主控环境");
    if (!followers.length) return showToast("请选择跟随环境");
    setBusy(true);
    try {
      await api.startSync({ masterId, followerIds: followers, captureMouse, captureKeyboard, captureWheel });
      await refreshSync();
      await refreshProfiles();
      showToast("窗口同步已启动");
    } catch (error) {
      showError(error);
    } finally {
      setBusy(false);
    }
  }

  async function stop() {
    setBusy(true);
    try {
      await api.stopSync({ id: running?.id });
      await refreshSync();
      showToast("窗口同步已停止");
    } catch (error) {
      showError(error);
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <header className="pageHead">
        <div>
          <h1>窗口同步</h1>
          <p>选择一个主控环境，把鼠标、滚轮和键盘事件同步到多个跟随环境。</p>
        </div>
        <div className="topActions">
          <button onClick={refreshSync}><RefreshCcw size={17} />刷新</button>
          {running ? (
            <button className="danger" disabled={busy} onClick={stop}><Square size={17} />停止同步</button>
          ) : (
            <button className="accent" disabled={busy || profiles.length < 2} onClick={start}><Radio size={17} />开始同步</button>
          )}
        </div>
      </header>

      <section className="noticeBar">
        <Radio size={16} />
        <span>启动后会自动打开所选环境。请在主控浏览器窗口操作，跟随窗口会同步点击、输入、滚轮和鼠标移动。</span>
      </section>

      <section className="syncShell">
        <section className="syncPanel">
          <div className="panelTitle">
            <strong>主控环境</strong>
            <span>{masterId ? "已选择" : "未选择"}</span>
          </div>
          <div className="syncProfileList">
            {profiles.map((profile) => (
              <button key={profile.id} className={masterId === profile.id ? "active" : ""} onClick={() => setMasterId(profile.id)}>
                <Monitor size={16} />
                <span>{profile.name}</span>
                <small>{profile.status === "running" ? "运行中" : "未启动"}</small>
              </button>
            ))}
            {!profiles.length ? <div className="emptyInline">暂无环境</div> : null}
          </div>
        </section>

        <section className="syncPanel">
          <div className="panelTitle">
            <strong>跟随环境</strong>
            <span>{followerIds.size} 个</span>
          </div>
          <div className="syncProfileList">
            {profiles.map((profile) => (
              <button key={profile.id} className={followerIds.has(profile.id) ? "active" : ""} disabled={profile.id === masterId} onClick={() => toggleFollower(profile.id)}>
                <Check size={16} />
                <span>{profile.name}</span>
                <small>{profile.id === masterId ? "主控" : profile.status === "running" ? "运行中" : "未启动"}</small>
              </button>
            ))}
            {!profiles.length ? <div className="emptyInline">暂无环境</div> : null}
          </div>
        </section>

        <aside className="syncPanel syncStatusPanel">
          <div className="panelTitle">
            <strong>同步设置</strong>
            <span>{running ? "同步中" : "待启动"}</span>
          </div>
          <div className="formGrid single">
            <Toggle label="同步鼠标点击和移动" value={captureMouse} onChange={setCaptureMouse} />
            <Toggle label="同步键盘输入" value={captureKeyboard} onChange={setCaptureKeyboard} />
            <Toggle label="同步滚轮" value={captureWheel} onChange={setCaptureWheel} />
          </div>

          <div className={running ? "syncLive on" : "syncLive"}>
            <span />
            <strong>{running ? "会话运行中" : "未启动同步"}</strong>
            <small>{running ? `已转发 ${running.eventCount || 0} 个事件` : "选择主控和跟随后开始"}</small>
          </div>

          {running ? (
            <div className="checkGrid">
              <span>主控</span><strong>{profiles.find((item) => item.id === running.masterId)?.name || running.masterId}</strong>
              <span>跟随</span><strong>{running.followerIds?.length || 0} 个窗口</strong>
              <span>启动</span><strong>{formatDate(running.startedAt)}</strong>
              <span>最近事件</span><strong>{formatDate(running.lastEventAt)}</strong>
            </div>
          ) : null}

          <div className="syncErrors">
            <div className="panelTitle">
              <strong>错误日志</strong>
              <span>{running?.errors?.length || 0}</span>
            </div>
            {running?.errors?.slice(-5).map((error) => (
              <div className="runLog error" key={`${error.at}-${error.profileId}`}>
                <strong>{error.profileId}</strong>
                <span>{error.message}</span>
              </div>
            ))}
            {!running?.errors?.length ? <div className="emptyInline">暂无错误</div> : null}
          </div>
        </aside>
      </section>
    </>
  );
}

function SyncPanel({ profiles, showToast, showError, refreshProfiles }) {
  const groups = useMemo(() => Array.from(new Set(profiles.map((item) => item.group).filter(Boolean))), [profiles]);
  const [masterId, setMasterId] = useState("");
  const [followerIds, setFollowerIds] = useState(new Set());
  const [sessions, setSessions] = useState([]);
  const [busy, setBusy] = useState(false);
  const [groupFilter, setGroupFilter] = useState("all");
  const [captureMouse, setCaptureMouse] = useState(true);
  const [captureKeyboard, setCaptureKeyboard] = useState(true);
  const [captureWheel, setCaptureWheel] = useState(true);
  const [autoArrange, setAutoArrange] = useState(true);
  const [layout, setLayout] = useState({ monitor: "primary", columns: 2, width: 960, height: 640, left: 0, top: 0, gap: 12 });
  const [displays, setDisplays] = useState([]);
  const [settings, setSettings] = useState(null);
  const running = sessions[0] || null;

  const visibleProfiles = useMemo(
    () => groupFilter === "all" ? profiles : profiles.filter((profile) => profile.group === groupFilter),
    [profiles, groupFilter]
  );

  async function refreshSync() {
    const result = await api.syncStatus();
    setSessions(result.sessions || []);
    setDisplays(await api.listDisplays().catch(() => []));
    setSettings(await api.getSettings().catch(() => null));
  }

  useEffect(() => {
    refreshSync().catch(showError);
  }, []);

  useEffect(() => {
    if (masterId && !profiles.some((profile) => profile.id === masterId)) setMasterId("");
    setFollowerIds((prev) => new Set(Array.from(prev).filter((id) => profiles.some((profile) => profile.id === id))));
  }, [profiles, masterId]);

  function patchLayout(key, value) {
    setLayout((prev) => ({ ...prev, [key]: key === "monitor" ? value : Number(value) || 0 }));
  }

  async function saveHotkeys(patch) {
    const next = { ...(settings || {}), syncHotkeys: { ...((settings || {}).syncHotkeys || {}), ...patch } };
    const saved = await api.updateSettings(next);
    setSettings(saved);
    showToast("同步快捷键已保存");
  }

  function toggleFollower(id) {
    setFollowerIds((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  }

  function selectVisibleFollowers() {
    setFollowerIds((prev) => {
      const next = new Set(prev);
      for (const profile of visibleProfiles) {
        if (profile.id !== masterId) next.add(profile.id);
      }
      return next;
    });
  }

  function clearFollowers() {
    setFollowerIds(new Set());
  }

  async function arrangeSelectedWindows() {
    const ids = [masterId, ...Array.from(followerIds).filter((id) => id !== masterId)].filter(Boolean);
    if (ids.length < 2) return showToast("请选择主控和跟随环境");
    setBusy(true);
    try {
      const result = await api.arrangeSync({ ids, layout });
      await refreshProfiles();
      const failed = (result.results || []).filter((item) => !item.ok).length;
      showToast(failed ? `窗口平铺完成，${failed} 个窗口未支持` : "窗口已平铺");
    } catch (error) {
      showError(error);
    } finally {
      setBusy(false);
    }
  }

  async function start() {
    const followers = Array.from(followerIds).filter((id) => id !== masterId);
    if (!masterId) return showToast("请选择主控环境");
    if (!followers.length) return showToast("请选择跟随环境");
    setBusy(true);
    try {
      await api.startSync({
        masterId,
        followerIds: followers,
        captureMouse,
        captureKeyboard,
        captureWheel,
        layout: { ...layout, enabled: autoArrange }
      });
      await refreshSync();
      await refreshProfiles();
      showToast("窗口同步已启动");
    } catch (error) {
      showError(error);
    } finally {
      setBusy(false);
    }
  }

  async function stop() {
    setBusy(true);
    try {
      await api.stopSync({ id: running?.id });
      await refreshSync();
      showToast("窗口同步已停止");
    } catch (error) {
      showError(error);
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <header className="pageHead">
        <div>
          <h1>窗口同步</h1>
          <p>选择主控窗口，把鼠标、滚轮和键盘事件同步到多个跟随窗口，并支持启动前自动平铺。</p>
        </div>
        <div className="topActions">
          <button onClick={refreshSync}><RefreshCcw size={17} />刷新</button>
          <button disabled={busy || profiles.length < 2} onClick={arrangeSelectedWindows}><Grid2X2 size={17} />平铺窗口</button>
          {running ? (
            <button className="danger" disabled={busy} onClick={stop}><Square size={17} />停止同步</button>
          ) : (
            <button className="accent" disabled={busy || profiles.length < 2} onClick={start}><Radio size={17} />开始同步</button>
          )}
        </div>
      </header>

      <section className="noticeBar">
        <Radio size={16} />
        <span>同步器会自动打开所选环境。平铺窗口通过 Chromium DevTools 设置窗口位置，Firefox 不支持时会保留同步但返回提示。</span>
      </section>

      <section className="syncFilterBar">
        <SelectMenu
          value={groupFilter}
          options={[{ value: "all", label: "全部分组" }, ...groups.map((group) => ({ value: group, label: group }))]}
          onChange={setGroupFilter}
        />
        <button onClick={selectVisibleFollowers}><Check size={16} />选中当前分组</button>
        <button onClick={clearFollowers}><CircleX size={16} />清空跟随</button>
      </section>

      <section className="syncShell">
        <section className="syncPanel">
          <div className="panelTitle">
            <strong>主控环境</strong>
            <span>{masterId ? "已选择" : "未选择"}</span>
          </div>
          <div className="syncProfileList">
            {visibleProfiles.map((profile) => (
              <button key={profile.id} className={masterId === profile.id ? "active" : ""} onClick={() => setMasterId(profile.id)}>
                <Monitor size={16} />
                <span>{profile.name}</span>
                <small>{profile.status === "running" ? "运行中" : "未启动"} · {profile.group}</small>
              </button>
            ))}
            {!visibleProfiles.length ? <div className="emptyInline">暂无环境</div> : null}
          </div>
        </section>

        <section className="syncPanel">
          <div className="panelTitle">
            <strong>跟随环境</strong>
            <span>{followerIds.size} 个</span>
          </div>
          <div className="syncProfileList">
            {visibleProfiles.map((profile) => (
              <button key={profile.id} className={followerIds.has(profile.id) ? "active" : ""} disabled={profile.id === masterId} onClick={() => toggleFollower(profile.id)}>
                <Check size={16} />
                <span>{profile.name}</span>
                <small>{profile.id === masterId ? "主控" : profile.status === "running" ? "运行中" : "未启动"} · {profile.group}</small>
              </button>
            ))}
            {!visibleProfiles.length ? <div className="emptyInline">暂无环境</div> : null}
          </div>
        </section>

        <aside className="syncPanel syncStatusPanel">
          <div className="panelTitle">
            <strong>同步设置</strong>
            <span>{running ? "同步中" : "待启动"}</span>
          </div>
          <div className="formGrid single">
            <Toggle label="同步鼠标点击和移动" value={captureMouse} onChange={setCaptureMouse} />
            <Toggle label="同步键盘输入" value={captureKeyboard} onChange={setCaptureKeyboard} />
            <Toggle label="同步滚轮" value={captureWheel} onChange={setCaptureWheel} />
            <Toggle label="启动同步时自动平铺窗口" value={autoArrange} onChange={setAutoArrange} />
          </div>

          <div className="syncLayoutGrid">
            <Field label="屏幕">
              <SelectMenu
                value={layout.monitor}
                options={[
                  { value: "primary", label: "内置 / 主屏" },
                  { value: "secondary", label: "扩展屏" },
                  { value: "all", label: "全部屏幕区域" },
                  { value: "custom", label: "自定义坐标" }
                ]}
                onChange={(value) => patchLayout("monitor", value)}
              />
            </Field>
            <Field label="列数"><input type="number" min="1" max="8" value={layout.columns} onChange={(event) => patchLayout("columns", event.target.value)} /></Field>
            <Field label="宽度"><input type="number" min="360" value={layout.width} onChange={(event) => patchLayout("width", event.target.value)} /></Field>
            <Field label="高度"><input type="number" min="280" value={layout.height} onChange={(event) => patchLayout("height", event.target.value)} /></Field>
            <Field label="间距"><input type="number" min="0" value={layout.gap} onChange={(event) => patchLayout("gap", event.target.value)} /></Field>
            <Field label="左偏移"><input type="number" min="0" value={layout.left} onChange={(event) => patchLayout("left", event.target.value)} /></Field>
            <Field label="上偏移"><input type="number" min="0" value={layout.top} onChange={(event) => patchLayout("top", event.target.value)} /></Field>
          </div>

          <div className="noticeBar">
            <Monitor size={16} />
            <span>{displays.length ? displays.map((item) => `${item.label}: ${item.workArea.width}x${item.workArea.height}`).join(" · ") : "未读取到屏幕信息"}</span>
          </div>

          <div className="syncLayoutGrid">
            <Field label="开始/停止快捷键"><input value={settings?.syncHotkeys?.toggle || ""} onChange={(e) => setSettings((prev) => ({ ...(prev || {}), syncHotkeys: { ...((prev || {}).syncHotkeys || {}), toggle: e.target.value } }))} /></Field>
            <Field label="停止快捷键"><input value={settings?.syncHotkeys?.stop || ""} onChange={(e) => setSettings((prev) => ({ ...(prev || {}), syncHotkeys: { ...((prev || {}).syncHotkeys || {}), stop: e.target.value } }))} /></Field>
          </div>
          <div className="panelActions">
            <button onClick={() => saveHotkeys(settings?.syncHotkeys || {})}><Check size={16} />保存快捷键</button>
          </div>

          <div className={running ? "syncLive on" : "syncLive"}>
            <span />
            <strong>{running ? "会话运行中" : "未启动同步"}</strong>
            <small>{running ? `已转发 ${running.eventCount || 0} 个事件` : "选择主控和跟随后开始"}</small>
          </div>

          {running ? (
            <div className="checkGrid">
              <span>主控</span><strong>{profiles.find((item) => item.id === running.masterId)?.name || running.masterId}</strong>
              <span>跟随</span><strong>{running.followerIds?.length || 0} 个窗口</strong>
              <span>启动</span><strong>{formatDate(running.startedAt)}</strong>
              <span>最近事件</span><strong>{formatDate(running.lastEventAt)}</strong>
              <span>平铺</span><strong>{running.layout?.enabled ? "已启用" : "未启用"}</strong>
            </div>
          ) : null}

          <div className="syncErrors">
            <div className="panelTitle">
              <strong>错误日志</strong>
              <span>{running?.errors?.length || 0}</span>
            </div>
            {running?.errors?.slice(-5).map((error) => (
              <div className="runLog error" key={`${error.at}-${error.profileId}`}>
                <strong>{error.profileId}</strong>
                <span>{error.message}</span>
              </div>
            ))}
            {!running?.errors?.length ? <div className="emptyInline">暂无错误</div> : null}
          </div>
        </aside>
      </section>
    </>
  );
}

const fingerprintSites = [
  { id: "pixelscan", name: "Pixelscan", url: "https://pixelscan.net/" },
  { id: "creepjs", name: "CreepJS", url: "https://abrahamjuliot.github.io/creepjs/" },
  { id: "browserleaks", name: "BrowserLeaks", url: "https://browserleaks.com/" },
  { id: "iphey", name: "IPhey", url: "https://iphey.com/" }
];

function resultText(status) {
  if (status === "pass") return "通过";
  if (status === "warn") return "警告";
  return "失败";
}

function FingerprintCheckPanel({ profiles, showToast, showError, refreshProfiles }) {
  const [selectedId, setSelectedId] = useState(profiles[0]?.id || "");
  const [result, setResult] = useState(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!selectedId && profiles[0]?.id) setSelectedId(profiles[0].id);
  }, [profiles, selectedId]);

  const selectedProfile = profiles.find((item) => item.id === selectedId) || profiles[0];
  const checks = result?.checks || [];
  const grouped = {
    fail: checks.filter((item) => item.status === "fail"),
    warn: checks.filter((item) => item.status === "warn"),
    pass: checks.filter((item) => item.status === "pass")
  };

  async function runCheck(id = selectedProfile?.id) {
    if (!id) return showToast("请选择环境");
    setBusy(true);
    try {
      const response = await api.checkFingerprint({ ids: [id] });
      setResult(response.results?.[0] || null);
      showToast("指纹检测已完成");
    } catch (error) {
      showError(error);
    } finally {
      setBusy(false);
    }
  }

  async function openSite(site) {
    if (!selectedProfile?.id) return showToast("请选择环境");
    setBusy(true);
    try {
      await api.openFingerprintSite({ id: selectedProfile.id, url: site.url });
      await refreshProfiles();
      showToast(`已用当前环境打开 ${site.name}`);
    } catch (error) {
      showError(error);
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <header className="pageHead">
        <div>
          <h1>指纹检测</h1>
          <p>本地一致性检测 + Pixelscan / CreepJS / BrowserLeaks 外部检测入口。</p>
        </div>
        <div className="topActions">
          <button onClick={() => runCheck()} disabled={busy || !selectedProfile}><Shield size={17} />开始检测</button>
          {fingerprintSites.map((site) => (
            <button key={site.id} disabled={busy || !selectedProfile} onClick={() => openSite(site)}><ExternalLink size={16} />{site.name}</button>
          ))}
        </div>
      </header>

      <section className="noticeBar">
        <Shield size={16} />
        <span>检测面板会先检查本地指纹一致性；外部站点会用所选环境窗口打开，结果以网站显示为准。</span>
      </section>

      <section className="checkShell">
        <aside className="syncPanel">
          <div className="panelTitle">
            <strong>选择环境</strong>
            <span>{profiles.length} 个环境</span>
          </div>
          <div className="syncProfileList">
            {profiles.map((profile) => (
              <button key={profile.id} className={selectedProfile?.id === profile.id ? "active" : ""} onClick={() => {
                setSelectedId(profile.id);
                setResult(null);
              }}>
                <StatusBadge status={profile.status} />
                <span>{profile.name}</span>
                <small>{[profile.country, profile.region, profile.city].filter(Boolean).join(" / ") || profile.id}</small>
              </button>
            ))}
            {!profiles.length ? <div className="emptyInline">暂无环境</div> : null}
          </div>
        </aside>

        <section className="checkMain">
          <div className={`scoreCard ${result?.verdict || "idle"}`}>
            <div className="scoreRing">
              <strong>{result ? result.score : "--"}</strong>
              <span>{result ? resultText(result.verdict) : "未检测"}</span>
            </div>
            <div>
              <h2>{selectedProfile?.name || "未选择环境"}</h2>
              <p>{result ? `通过 ${result.passCount} / 警告 ${result.warnCount} / 失败 ${result.failCount}` : "点击开始检测生成本地一致性报告。"}</p>
              <div className="checkTags">
                <span>{selectedProfile?.fingerprint?.browser === "flower" ? "Firefox" : "Chromium"}</span>
                <span>{selectedProfile?.fingerprint?.deviceType || "desktop"}</span>
                <span>{selectedProfile?.fingerprint?.timezone || "timezone"}</span>
                <span>{selectedProfile?.proxy?.type || "proxy"}</span>
              </div>
            </div>
          </div>

          <div className="checkGroups">
            {["fail", "warn", "pass"].map((status) => (
              <div className="checkGroup" key={status}>
                <div className="panelTitle">
                  <strong>{resultText(status)}</strong>
                  <span>{grouped[status].length}</span>
                </div>
                <div className="checkRows">
                  {grouped[status].map((item) => (
                    <div className={`checkRow ${item.status}`} key={item.id}>
                      <span>{resultText(item.status)}</span>
                      <div>
                        <strong>{item.label}</strong>
                        <small>{item.detail}</small>
                        <em>{item.actual || item.expected}</em>
                      </div>
                    </div>
                  ))}
                  {result && !grouped[status].length ? <div className="emptyInline">无项目</div> : null}
                </div>
              </div>
            ))}
          </div>
        </section>

        <aside className="syncPanel">
          <div className="panelTitle">
            <strong>检测站点</strong>
            <span>外部验证</span>
          </div>
          <div className="siteList">
            {fingerprintSites.map((site) => (
              <button key={site.id} onClick={() => openSite(site)} disabled={busy || !selectedProfile}>
                <ExternalLink size={16} />
                <div>
                  <strong>{site.name}</strong>
                  <span>{site.url.replace(/^https?:\/\//, "")}</span>
                </div>
              </button>
            ))}
          </div>
          <div className="checkSummary">
            <strong>检测范围</strong>
            <span>UA / Client Hints</span>
            <span>TLS / HTTP2 / QUIC</span>
            <span>WebGL / WebGPU</span>
            <span>Screen / DPR / Touch</span>
            <span>Proxy / Geo / WebRTC</span>
          </div>
        </aside>
      </section>
    </>
  );
}

function KernelPanel({ showToast, showError }) {
  const [data, setData] = useState({ sources: [], local: [], kernelRoot: "" });
  const [loading, setLoading] = useState(false);
  const [downloading, setDownloading] = useState("");
  const [task, setTask] = useState(null);

  async function refresh() {
    setLoading(true);
    setTask({ title: "刷新内核源", detail: "正在请求官方版本源", phase: "running" });
    try {
      setData(await api.listKernelSources());
      setTask({ title: "刷新内核源", detail: "源数据已更新", phase: "done" });
      window.setTimeout(() => setTask(null), 700);
      showToast("内核源已刷新");
    } catch (error) {
      setTask({ title: "刷新失败", detail: error?.message || String(error), phase: "error" });
      window.setTimeout(() => setTask(null), 1600);
      showError(error);
    } finally {
      setLoading(false);
    }
  }

  async function download(item) {
    setDownloading(item.id);
    setTask({ title: `下载 ${item.family} ${item.channel}`, detail: item.version, phase: "running" });
    try {
      const result = await api.downloadKernel(item);
      setTask({ title: "下载完成", detail: result.path, phase: "done" });
      window.setTimeout(() => setTask(null), 900);
      showToast(`已下载到 ${result.path}`);
      setData(await api.listKernelSources());
    } catch (error) {
      setTask({ title: "下载失败", detail: error?.message || String(error), phase: "error" });
      window.setTimeout(() => setTask(null), 1600);
      showError(error);
    } finally {
      setDownloading("");
    }
  }

  useEffect(() => {
    refresh();
  }, []);

  return (
    <>
      <header className="pageHead">
        <div>
          <h1>内核管理</h1>
          <p>从官方更新源获取 Chrome for Testing 和 Microsoft Edge 的 Windows x64 下载地址。</p>
        </div>
        <div className="topActions">
          <button onClick={refresh} disabled={loading}><RefreshCcw size={17} />刷新源</button>
          <button onClick={() => api.openKernelFolder()}><ExternalLink size={17} />打开目录</button>
        </div>
      </header>

      <section className="noticeBar">
        <Package size={16} />
        <span>下载目录：{data.kernelRoot || "kernels"}。下载后可在设置里分别配置 Chromium 的 chrome.exe/msedge.exe，或 Firefox 的 firefox.exe。</span>
      </section>

      <section className="tableWrap">
        <table className="simpleTable">
          <thead>
            <tr>
              <th>浏览器</th>
              <th>通道</th>
              <th>版本</th>
              <th>平台</th>
              <th>格式</th>
              <th>大小</th>
              <th>来源</th>
              <th className="actionCol">操作</th>
            </tr>
          </thead>
          <tbody>
            {data.sources.map((item) => (
              <tr key={item.id}>
                <td>{item.family}</td>
                <td>{item.channel}</td>
                <td>{item.version}</td>
                <td>{item.platform}</td>
                <td>{item.artifact}</td>
                <td>{item.sizeLabel || "-"}</td>
                <td>{item.source}</td>
                <td>
                  <button className="small accent" disabled={downloading === item.id} onClick={() => download(item)}>
                    <CloudDownload size={15} />{downloading === item.id ? "下载中" : "下载"}
                  </button>
                </td>
              </tr>
            ))}
            {!data.sources.length ? (
              <tr><td colSpan="8"><div className="emptyState"><Cpu size={30} /><strong>没有源数据</strong><span>点击刷新源获取官方下载地址。</span></div></td></tr>
            ) : null}
          </tbody>
        </table>
      </section>

      <section className="tableWrap">
        <table className="simpleTable">
          <thead>
            <tr>
              <th>本地文件</th>
              <th>大小</th>
              <th>更新时间</th>
              <th>路径</th>
            </tr>
          </thead>
          <tbody>
            {data.local.map((item) => (
              <tr key={item.path}>
                <td>{item.name}</td>
                <td>{item.sizeLabel}</td>
                <td>{formatDate(item.updatedAt)}</td>
                <td>{item.path}</td>
              </tr>
            ))}
            {!data.local.length ? (
              <tr><td colSpan="4"><div className="emptyInline">暂无本地下载文件</div></td></tr>
            ) : null}
          </tbody>
        </table>
      </section>

      {task ? <TaskDialog task={task} /> : null}
    </>
  );
}

function copyProfile(profile) {
  return typeof structuredClone === "function" ? structuredClone(profile) : clone(profile);
}

function ProfileEditor({ editor, groups, onClose, onSave }) {
  const [profile, setProfile] = useState(() => ensureProfileShape(editor.data));
  const [tab, setTab] = useState(editor.tab || "base");
  const [proxyText, setProxyText] = useState("");
  const [proxyTesting, setProxyTesting] = useState(false);
  const [proxyResult, setProxyResult] = useState(null);

  function patch(path, value) {
    setProfile((prev) => {
      const next = copyProfile(prev);
      let target = next;
      for (let i = 0; i < path.length - 1; i += 1) target = target[path[i]];
      target[path[path.length - 1]] = value;
      return next;
    });
  }

  function randomizeAll() {
    setProfile((current) => {
      const keepCustomLocation = current.locationMode === "custom";
      const hasLocation = Boolean(current.country || current.region || current.city);
      const base = {
        locationMode: current.locationMode,
        group: current.group,
        platform: current.platform,
        fingerprint: { browser: current.fingerprint.browser, deviceType: current.fingerprint.deviceType }
      };
      if (keepCustomLocation || hasLocation) {
        base.country = current.country;
        base.region = current.region;
        base.city = current.city;
      }
      const next = randomProfile(base);
      if (!hasLocation && !keepCustomLocation) return next;
      return applyLocationToProfile(next, locationFromProfile(next), keepCustomLocation);
    });
    setTab("base");
  }

  function randomizeFingerprint() {
    setProfile((current) => {
      const hasLocation = Boolean(current.country || current.region || current.city);
      const location = hasLocation
        ? locationFromProfile(current)
        : {
            country: current.country,
            region: current.region,
            city: current.city,
            language: current.fingerprint.language,
            timezone: current.fingerprint.timezone
          };
      const next = copyProfile(current);
      next.fingerprint = randomFingerprint(next.fingerprint, location);
      return next;
    });
  }

  function applyLocationGenerate() {
    setProfile((current) => {
      const location = current.country ? randomLocationForCountry(current.country) : pick(locationProfiles);
      const nextProfile = randomProfile({
        country: current.country || location.country,
        locationMode: current.locationMode || "match",
        group: current.group,
        platform: current.platform,
        fingerprint: { browser: current.fingerprint.browser, deviceType: current.fingerprint.deviceType }
      });
      nextProfile.region = current.region || location.region;
      nextProfile.city = current.city || location.city;
      return applyLocationToProfile(nextProfile, {
        ...location,
        language: current.fingerprint?.language || location.language,
        timezone: current.fingerprint?.timezone || location.timezone
      }, true);
    });
    setTab("base");
  }

  function applyProxyText(value = proxyText) {
    const parsed = parseProxyString(value);
    if (!parsed?.host || !parsed?.port) return null;
    setProfile((prev) => ({
      ...prev,
      proxy: {
        ...prev.proxy,
        type: parsed.type || "http",
        host: parsed.host,
        port: parsed.port,
        username: parsed.username || "",
        password: parsed.password || ""
      }
    }));
    return parsed;
  }

  async function checkProxyConfig(proxy) {
    if (!proxy?.host || !proxy?.port) return;
    setProxyTesting(true);
    setProxyResult(null);
    try {
      const result = await api.testProxy({ proxy });
      setProxyResult(result);
      if (result?.ok) setProfile((prev) => applyIpGeoToProfile(prev, result));
    } catch (error) {
      setProxyResult({ ok: false, error: error?.message || String(error) });
    } finally {
      setProxyTesting(false);
    }
  }

  async function checkCurrentProxy() {
    await checkProxyConfig(profile.proxy);
  }

  async function applyProxyTextAndMatch(value = proxyText) {
    const parsed = applyProxyText(value);
    if (!parsed) return;
    await checkProxyConfig({
      type: parsed.type || "http",
      host: parsed.host,
      port: parsed.port,
      username: parsed.username || "",
      password: parsed.password || ""
    });
  }

  return (
    <div className="overlay">
      <div className="drawer">
        <header className="drawerHead">
          <div>
            <h2>{editor.mode === "create" ? "新建环境" : "编辑环境"}</h2>
            <p>{profile.id || "保存后生成本地 ID"}</p>
          </div>
          <div className="drawerTools">
            <button onClick={randomizeAll}><WandSparkles size={16} />随机全部</button>
            <button onClick={randomizeFingerprint}><Shuffle size={16} />随机指纹</button>
            <button className="iconBtn" onClick={onClose}><X size={20} /></button>
          </div>
        </header>

        <div className="tabs">
          {[
            ["base", "基础"],
            ["account", "账号"],
            ["proxy", "代理"],
            ["fingerprint", "指纹"],
            ["launch", "启动"],
            ["storage", "存储"]
          ].map(([key, label]) => (
            <button key={key} className={tab === key ? "active" : ""} onClick={() => setTab(key)}>{label}</button>
          ))}
        </div>

        <div className="drawerBody">
          {tab === "base" ? (
            <div className="formGrid">
              <Field label="环境名称"><input value={profile.name} onChange={(e) => patch(["name"], e.target.value)} placeholder="环境名称" /></Field>
              <Field label="自定义编号"><input value={profile.customNo} onChange={(e) => patch(["customNo"], e.target.value)} placeholder="可选" /></Field>
              <Field label="分组"><Combo value={profile.group} options={groups} onChange={(value) => patch(["group"], value)} /></Field>
              <Field label="平台">
                <SelectMenu value={profile.platform} options={platformSelectOptions} onChange={(value) => patch(["platform"], value)} />
              </Field>
              <Field label="国家 / 地区"><input value={profile.country} onChange={(e) => patch(["country"], e.target.value)} onBlur={() => {
                setProfile((prev) => {
                  if ((prev.locationMode || "match") === "custom" || !String(prev.country || "").trim()) return prev;
                  return applyLocationToProfile(prev, locationFromProfile(prev), false);
                });
              }} placeholder="CN / US / HK" /></Field>
              <Field label="省市"><input value={[profile.region, profile.city].filter(Boolean).join(" / ")} onChange={(e) => {
                const [region = "", city = ""] = e.target.value.split("/").map((item) => item.trim());
                patch(["region"], region);
                patch(["city"], city);
              }} placeholder="Guangdong / Shenzhen" /></Field>
              <Field label="地区生成模式">
                <SelectMenu value={profile.locationMode || "match"} options={locationModeOptions} onChange={(value) => patch(["locationMode"], value)} />
              </Field>
              <Field label="生成指纹">
                <button type="button" onClick={applyLocationGenerate}><Sparkles size={15} />保留国家随机全部</button>
              </Field>
              <Field label="标签" wide><input value={toTokens(profile.tags)} onChange={(e) => patch(["tags"], fromTokens(e.target.value))} placeholder="逗号分隔，例如 fb, warmup, test" /></Field>
              <Field label="备注" wide><textarea value={profile.notes} onChange={(e) => patch(["notes"], e.target.value)} rows="4" /></Field>
            </div>
          ) : null}

          {tab === "account" ? (
            <div className="formGrid">
              <Field label="账号"><input value={profile.account.username} onChange={(e) => patch(["account", "username"], e.target.value)} /></Field>
              <Field label="密码"><input type="password" value={profile.account.password} onChange={(e) => patch(["account", "password"], e.target.value)} /></Field>
              <Field label="2FA 密钥"><input value={profile.account.twoFactor} onChange={(e) => patch(["account", "twoFactor"], e.target.value)} /></Field>
              <Field label="Cookies" wide><textarea value={profile.account.cookies} onChange={(e) => patch(["account", "cookies"], e.target.value)} rows="8" placeholder="JSON / Netscape / 原始 Cookie 字符串" /></Field>
            </div>
          ) : null}

          {tab === "proxy" ? (
            <div className="formGrid">
              <Field label="粘贴代理自动识别" wide>
                <div className="inlineInput">
                  <input
                    value={proxyText}
                    onChange={(e) => {
                      setProxyText(e.target.value);
                      applyProxyText(e.target.value);
                    }}
                    onPaste={(e) => {
                      const text = e.clipboardData.getData("text");
                      if (!text) return;
                      e.preventDefault();
                      setProxyText(text);
                      applyProxyTextAndMatch(text);
                    }}
                    placeholder="支持 host:port、host:port:user:pass、user:pass@host:port、socks5://user:pass@host:port"
                  />
                  <button type="button" onClick={() => applyProxyTextAndMatch()}><Sparkles size={15} />识别</button>
                  <button type="button" onClick={checkCurrentProxy} disabled={proxyTesting}><Shield size={15} />{proxyTesting ? "检测中" : "检测代理"}</button>
                </div>
              </Field>
              <div className="wide">
                <ProxyCheckCard result={proxyResult} loading={proxyTesting} />
              </div>
              {proxyResult?.ok ? (
                <Field label="匹配指纹" wide>
                  <button type="button" onClick={() => setProfile((prev) => applyIpGeoToProfile({ ...prev, locationMode: "match" }, proxyResult))}>
                    <Sparkles size={15} />按出口 IP 写入国家 / 时区 / 语言
                  </button>
                </Field>
              ) : null}
              <Field label="类型">
                <SelectMenu value={profile.proxy.type} options={proxyTypeOptions} onChange={(value) => patch(["proxy", "type"], value)} />
              </Field>
              <Field label="主机"><input value={profile.proxy.host} onChange={(e) => patch(["proxy", "host"], e.target.value)} placeholder="127.0.0.1" /></Field>
              <Field label="端口"><input value={profile.proxy.port} onChange={(e) => patch(["proxy", "port"], e.target.value)} placeholder="7890" /></Field>
              <Field label="用户名"><input value={profile.proxy.username} onChange={(e) => patch(["proxy", "username"], e.target.value)} /></Field>
              <Field label="密码"><input type="password" value={profile.proxy.password} onChange={(e) => patch(["proxy", "password"], e.target.value)} /></Field>
            </div>
          ) : null}

          {tab === "fingerprint" ? (
            <div className="formGrid">
              <Field label="浏览器类型">
                <SelectMenu value={profile.fingerprint.browser || "sun"} options={browserTypeOptions} onChange={(browser) => {
                  setProfile((prev) => {
                    const next = copyProfile(prev);
                    const os = next.fingerprint.os || "Windows";
                    const device = isMobileOs(os) ? pickMobileDevice(os, next.fingerprint.deviceType) : null;
                    next.fingerprint.browser = browser;
                    next.fingerprint.kernelVersion = "system";
                    next.fingerprint.userAgent = randomUserAgent(os, browser, device);
                    return next;
                  });
                }} />
              </Field>
              <Field label="浏览器内核">
                <SelectMenu
                  value={profile.fingerprint.kernelVersion}
                  options={profile.fingerprint.browser === "flower" ? flowerKernelOptions : sunKernelOptions}
                  onChange={(value) => patch(["fingerprint", "kernelVersion"], value)}
                />
              </Field>
              <Field label="操作系统">
                <SelectMenu value={profile.fingerprint.os} options={osOptions} onChange={(os) => {
                  setProfile((prev) => applyOsToProfile(prev, os));
                }} />
              </Field>
              <Field label="设备类型">
                <SelectMenu value={profile.fingerprint.deviceType || "desktop"} options={deviceTypeOptions} onChange={(deviceType) => {
                  setProfile((prev) => {
                    let os = prev.fingerprint.os || "Windows";
                    if (deviceType === "desktop" && isMobileOs(os)) os = "Windows";
                    if (deviceType !== "desktop" && !isMobileOs(os)) os = deviceType === "tablet" ? "iOS" : "Android";
                    return applyOsToProfile(prev, os, { deviceType });
                  });
                }} />
              </Field>
              <Field label="User-Agent" wide><textarea value={profile.fingerprint.userAgent} onChange={(e) => patch(["fingerprint", "userAgent"], e.target.value)} rows="3" placeholder="留空使用浏览器默认 UA" /></Field>
              <Field label="Client Hints">
                <SelectMenu value={profile.fingerprint.clientHintsMode || "auto"} options={clientHintsOptions} onChange={(value) => patch(["fingerprint", "clientHintsMode"], value)} />
              </Field>
              <Field label="TLS 指纹">
                <SelectMenu value={profile.fingerprint.tlsProfile || "real"} options={tlsProfileOptions} onChange={(value) => patch(["fingerprint", "tlsProfile"], value)} />
              </Field>
              <Field label="TLS 最低版本">
                <SelectMenu value={profile.fingerprint.tlsMinVersion || "default"} options={tlsMinVersionOptions} onChange={(value) => patch(["fingerprint", "tlsMinVersion"], value)} />
              </Field>
              <Field label="WebGPU">
                <SelectMenu value={profile.fingerprint.webgpuMode || "match"} options={webgpuOptions} onChange={(value) => patch(["fingerprint", "webgpuMode"], value)} />
              </Field>
              <Field label="语言"><input value={profile.fingerprint.language} onChange={(e) => patch(["fingerprint", "language"], e.target.value)} /></Field>
              <Field label="时区"><input value={profile.fingerprint.timezone} onChange={(e) => patch(["fingerprint", "timezone"], e.target.value)} /></Field>
              <Field label="窗口尺寸"><input value={profile.fingerprint.resolution} onChange={(e) => patch(["fingerprint", "resolution"], e.target.value)} /></Field>
              <Field label="设备缩放"><input type="number" step="0.125" value={profile.fingerprint.deviceScaleFactor} onChange={(e) => patch(["fingerprint", "deviceScaleFactor"], Number(e.target.value))} /></Field>
              <Field label="色深"><input type="number" value={profile.fingerprint.colorDepth} onChange={(e) => patch(["fingerprint", "colorDepth"], Number(e.target.value))} /></Field>
              <Field label="CPU 核心"><input type="number" value={profile.fingerprint.hardwareConcurrency} onChange={(e) => patch(["fingerprint", "hardwareConcurrency"], Number(e.target.value))} /></Field>
              <Field label="内存 GB"><input type="number" value={profile.fingerprint.deviceMemory} onChange={(e) => patch(["fingerprint", "deviceMemory"], Number(e.target.value))} /></Field>
              <Field label="触控点"><input type="number" value={profile.fingerprint.touchPoints} onChange={(e) => patch(["fingerprint", "touchPoints"], Number(e.target.value))} /></Field>
              <Field label="屏幕方向">
                <SelectMenu value={profile.fingerprint.screenOrientation || "portrait"} options={orientationOptions} onChange={(value) => patch(["fingerprint", "screenOrientation"], value)} />
              </Field>
              <Field label="移动机型"><input value={profile.fingerprint.mobilePreset || ""} onChange={(e) => patch(["fingerprint", "mobilePreset"], e.target.value)} placeholder="Pixel 8 Pro / iPhone 15" /></Field>
              <Field label="媒体设备">
                <SelectMenu value={profile.fingerprint.mediaDevices} options={mediaDeviceOptions} onChange={(value) => patch(["fingerprint", "mediaDevices"], value)} />
              </Field>
              <Field label="地理位置">
                <SelectMenu value={profile.fingerprint.geolocation} options={geolocationOptions} onChange={(value) => patch(["fingerprint", "geolocation"], value)} />
              </Field>
              <Field label="WebGL Vendor"><input value={profile.fingerprint.webglVendor} onChange={(e) => patch(["fingerprint", "webglVendor"], e.target.value)} /></Field>
              <Field label="WebGL Renderer"><input value={profile.fingerprint.webglRenderer} onChange={(e) => patch(["fingerprint", "webglRenderer"], e.target.value)} /></Field>
              <Field label="WebGPU Vendor"><input value={profile.fingerprint.webgpuVendor} onChange={(e) => patch(["fingerprint", "webgpuVendor"], e.target.value)} /></Field>
              <Field label="WebGPU Renderer"><input value={profile.fingerprint.webgpuRenderer} onChange={(e) => patch(["fingerprint", "webgpuRenderer"], e.target.value)} /></Field>
              <Field label="字体列表" wide><textarea value={toLines(profile.fingerprint.fonts)} onChange={(e) => patch(["fingerprint", "fonts"], fromLines(e.target.value))} rows="3" /></Field>
              <Field label="端口保护" wide><input value={toTokens(profile.fingerprint.ports)} onChange={(e) => patch(["fingerprint", "ports"], fromTokens(e.target.value))} placeholder="逗号分隔，如 3389, 5900, 9222" /></Field>
              <Toggle label="Do Not Track" value={profile.fingerprint.doNotTrack} onChange={(value) => patch(["fingerprint", "doNotTrack"], value)} />
              <Toggle label="Canvas 噪声" value={profile.fingerprint.canvasNoise} onChange={(value) => patch(["fingerprint", "canvasNoise"], value)} />
              <Toggle label="Audio 噪声" value={profile.fingerprint.audioNoise} onChange={(value) => patch(["fingerprint", "audioNoise"], value)} />
              <Toggle label="ClientRects 噪声" value={profile.fingerprint.clientRectsNoise} onChange={(value) => patch(["fingerprint", "clientRectsNoise"], value)} />
              <Toggle label="WebGL 噪声" value={profile.fingerprint.webglNoise} onChange={(value) => patch(["fingerprint", "webglNoise"], value)} />
              <Toggle label="WebGL 扩展列表覆盖" value={profile.fingerprint.webglExtensions} onChange={(value) => patch(["fingerprint", "webglExtensions"], value)} />
              <Toggle label="确定性光栅 (SwiftShader)" value={profile.fingerprint.deterministicRaster} onChange={(value) => patch(["fingerprint", "deterministicRaster"], value)} />
              <Toggle label="HTTP/2" value={profile.fingerprint.tlsHttp2} onChange={(value) => patch(["fingerprint", "tlsHttp2"], value)} />
              <Toggle label="QUIC / HTTP3" value={profile.fingerprint.tlsQuic} onChange={(value) => patch(["fingerprint", "tlsQuic"], value)} />
              <Field label="WebRTC">
                <SelectMenu value={profile.fingerprint.webrtcMode || (profile.fingerprint.disableWebRTC ? "proxy" : "real")} options={webrtcModeOptions} onChange={(value) => patch(["fingerprint", "webrtcMode"], value)} />
              </Field>
              <Field label="WebRTC 公网 IP"><input value={profile.fingerprint.webrtcPublicIp || ""} onChange={(e) => patch(["fingerprint", "webrtcPublicIp"], e.target.value)} placeholder="代理出口 IP；留空=丢弃泄露候选" /></Field>
              <Field label="TLS 改写代理">
                <SelectMenu value={profile.fingerprint.tlsMode || "off"} options={tlsModeOptions} onChange={(value) => patch(["fingerprint", "tlsMode"], value)} />
              </Field>
              <Field label="内核模式">
                <SelectMenu value={profile.fingerprint.kernelMode || "auto"} options={kernelModeOptions} onChange={(value) => patch(["fingerprint", "kernelMode"], value)} />
              </Field>
            </div>
          ) : null}

          {tab === "launch" ? (
            <div className="formGrid">
              <Field label="启动网址" wide><textarea value={toLines(profile.launch.urls)} onChange={(e) => patch(["launch", "urls"], fromLines(e.target.value))} rows="5" placeholder="每行一个 URL" /></Field>
              <Field label="额外参数" wide><textarea value={toLines(profile.launch.extraArgs)} onChange={(e) => patch(["launch", "extraArgs"], fromLines(e.target.value))} rows="5" placeholder="每行一个 Chromium 参数" /></Field>
              <Field label="扩展路径" wide><textarea value={toLines(profile.launch.extensions)} onChange={(e) => patch(["launch", "extensions"], fromLines(e.target.value))} rows="4" placeholder="每行一个 unpacked extension 路径" /></Field>
            </div>
          ) : null}

          {tab === "storage" ? (
            <div className="formGrid single">
              <Toggle label="保存本地缓存和登录态" value={!profile.storage.clearCacheAfterClose} onChange={(value) => patch(["storage", "clearCacheAfterClose"], !value)} />
              <Toggle label="标记为云同步环境" value={profile.storage.cloudSync} onChange={(value) => patch(["storage", "cloudSync"], value)} />
              <Field label="缓存说明" wide><textarea value={profile.notes} onChange={(e) => patch(["notes"], e.target.value)} rows="5" /></Field>
            </div>
          ) : null}
        </div>

        <footer className="drawerFoot">
          <button onClick={onClose}>取消</button>
          <button className="accent" onClick={() => onSave(profile)}><Check size={17} />保存</button>
        </footer>
      </div>
    </div>
  );
}

function Field({ label, children, wide }) {
  return (
    <div className={wide ? "field wide" : "field"}>
      <span>{label}</span>
      {children}
    </div>
  );
}

function normalizeOptions(options) {
  return options.map((option) => {
    if (Array.isArray(option)) return { value: option[0], label: option[1] };
    if (typeof option === "object") return option;
    return { value: option, label: option };
  });
}

function SelectMenu({ value, options, onChange, placeholder = "请选择" }) {
  const [open, setOpen] = useState(false);
  const items = normalizeOptions(options);
  const current = items.find((item) => item.value === value);

  return (
    <div className={open ? "selectMenu open" : "selectMenu"} tabIndex={-1} onBlur={(event) => {
      if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false);
    }}>
      <button type="button" className="selectButton" onClick={() => setOpen((next) => !next)}>
        <span>{current?.label || value || placeholder}</span>
        <ChevronDown size={16} />
      </button>
      {open ? (
        <div className="selectDropdown">
          {items.map((item) => (
            <button
              type="button"
              key={item.value}
              className={item.value === value ? "active" : ""}
              onMouseDown={(event) => {
                event.preventDefault();
                onChange(item.value);
                setOpen(false);
              }}
            >
              {item.label}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function Combo({ value, options, onChange }) {
  const [open, setOpen] = useState(false);
  const items = options.filter(Boolean);
  const filtered = items.filter((item) => !value || item.toLowerCase().includes(value.toLowerCase()));

  return (
    <div className={open ? "combo open" : "combo"} tabIndex={-1} onBlur={(event) => {
      if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false);
    }}>
      <input
        value={value}
        onFocus={() => setOpen(true)}
        onChange={(e) => {
          onChange(e.target.value);
          setOpen(true);
        }}
      />
      <ChevronDown
        className="comboArrow"
        size={16}
        onMouseDown={(event) => {
          event.preventDefault();
          setOpen((next) => !next);
        }}
      />
      {open && filtered.length ? (
        <div className="selectDropdown comboDropdown">
          {filtered.map((item) => (
            <button
              type="button"
              key={item}
              className={item === value ? "active" : ""}
              onMouseDown={(event) => {
                event.preventDefault();
                onChange(item);
                setOpen(false);
              }}
            >
              {item}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function Toggle({ label, value, onChange }) {
  return (
    <label className="toggle">
      <input type="checkbox" checked={value} onChange={(e) => onChange(e.target.checked)} />
      <span>{label}</span>
    </label>
  );
}

function SettingsPanel({ settings, onClose, onSave }) {
  const [form, setForm] = useState(settings);
  const patch = (key, value) => setForm((prev) => ({ ...prev, [key]: value }));

  return (
    <div className="overlay">
      <div className="settingsPanel">
        <header className="drawerHead">
          <div>
            <h2>设置</h2>
            <p>本地启动参数、Profile 目录、内核下载目录和 Local API</p>
          </div>
          <button className="iconBtn" onClick={onClose}><X size={20} /></button>
        </header>
        <div className="drawerBody">
          <div className="formGrid single">
            <Field label="Chromium 路径" wide><input value={form.sunBrowserPath || ""} onChange={(e) => patch("sunBrowserPath", e.target.value)} placeholder="chrome.exe / msedge.exe / brave.exe" /></Field>
            <Field label="Firefox 路径" wide><input value={form.flowerBrowserPath || ""} onChange={(e) => patch("flowerBrowserPath", e.target.value)} placeholder="firefox.exe" /></Field>
            <Field label="浏览器路径" wide><input value={form.browserPath || ""} onChange={(e) => patch("browserPath", e.target.value)} /></Field>
            <Field label="Profile 根目录" wide><input value={form.profileRoot || ""} onChange={(e) => patch("profileRoot", e.target.value)} /></Field>
            <Field label="内核下载目录" wide><input value={form.kernelRoot || ""} onChange={(e) => patch("kernelRoot", e.target.value)} /></Field>
            <Field label="调试端口起点"><input type="number" value={form.baseDebugPort} onChange={(e) => patch("baseDebugPort", Number(e.target.value))} /></Field>
            <Field label="Local API 端口"><input type="number" value={form.localApiPort} onChange={(e) => patch("localApiPort", Number(e.target.value))} /></Field>
            <Field label="默认首页"><input value={form.defaultHomepage} onChange={(e) => patch("defaultHomepage", e.target.value)} onBlur={(e) => patch("defaultHomepage", normalizeHomepage(e.target.value))} placeholder="https://example.com" /></Field>
            <Toggle label="关闭环境时结束进程树" value={form.closeWithTaskTree} onChange={(value) => patch("closeWithTaskTree", value)} />
          </div>
        </div>
        <footer className="drawerFoot">
          <button onClick={onClose}>取消</button>
          <button className="accent" onClick={() => onSave({ ...form, defaultHomepage: normalizeHomepage(form.defaultHomepage) })}><Check size={17} />保存</button>
        </footer>
      </div>
    </div>
  );
}

createRoot(document.getElementById("root")).render(<App />);
