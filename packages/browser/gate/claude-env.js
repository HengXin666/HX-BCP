async () => {
  const ro = Intl.DateTimeFormat().resolvedOptions();
  const out = {
    timeZone: ro.timeZone || "",
    intlLocale: ro.locale || "",
    tzOffset: new Date().getTimezoneOffset(),
    languages: Array.from(navigator.languages || [navigator.language]),
    ua: navigator.userAgent,
    platform: navigator.platform || "",
    brands: ((navigator.userAgentData && navigator.userAgentData.brands) || []).map((b) => b.brand),
    cnFonts: [],
    vendorFonts: [],
    webglRenderer: "",
    webrtcIps: [],
  };
  const ctx = document.createElement("canvas").getContext("2d");
  const sample = "中文字体测试mmmwwwlli";
  const has = (font) => ["monospace", "serif", "sans-serif"].some((base) => {
    ctx.font = "72px 'hx-no-such-font-0', " + base;
    const plain = ctx.measureText(sample).width;
    ctx.font = "72px '" + font + "', " + base;
    return ctx.measureText(sample).width !== plain;
  });
  const CN = ["Microsoft YaHei", "SimSun", "SimHei", "KaiTi", "FangSong", "PingFang SC", "PingFang TC", "Hiragino Sans GB", "Heiti SC", "STHeiti", "Songti SC", "Noto Sans CJK SC", "Noto Sans SC", "Source Han Sans SC", "WenQuanYi Micro Hei", "WenQuanYi Zen Hei", "Microsoft JhengHei", "PMingLiU", "MingLiU"];
  const VENDOR = ["MiSans", "HarmonyOS Sans", "HarmonyOS Sans SC", "OPPO Sans", "vivo Sans", "Alibaba PuHuiTi", "FZShuTi", "FZYaoTi", "FZLanTingHei", "DengXian"];
  out.cnFonts = CN.filter(has);
  out.vendorFonts = VENDOR.filter(has);
  try {
    const gl = document.createElement("canvas").getContext("webgl");
    const info = gl && gl.getExtension("WEBGL_debug_renderer_info");
    out.webglRenderer = info ? String(gl.getParameter(info.UNMASKED_RENDERER_WEBGL)) : gl ? String(gl.getParameter(gl.RENDERER)) : "";
  } catch (e) {}
  try {
    const ips = new Set();
    const pc = new RTCPeerConnection({ iceServers: [] });
    pc.createDataChannel("x");
    pc.onicecandidate = (ev) => {
      const m = ev.candidate && ev.candidate.candidate.match(/ ([0-9]{1,3}(?:[.][0-9]{1,3}){3}|[0-9a-f:]{6,}) [0-9]+ typ /i);
      if (m) ips.add(m[1]);
    };
    await pc.setLocalDescription(await pc.createOffer());
    await new Promise((r) => setTimeout(r, 1500));
    pc.close();
    out.webrtcIps = Array.from(ips);
  } catch (e) {}
  return out;
}
