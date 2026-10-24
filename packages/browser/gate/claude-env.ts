import { readFileSync } from "node:fs";

/** Signals read by gate/claude-env.js: the list FuckClaude publishes, which NodeCloak's README scores against. */
export type ClaudeEnv = {
  timeZone: string;
  intlLocale: string;
  tzOffset: number;
  languages: string[];
  ua: string;
  platform: string;
  brands: string[];
  cnFonts: string[];
  vendorFonts: string[];
  webglRenderer: string;
  webrtcIps: string[];
};

/** First five weights are FuckClaude's published values. The rest are not published; these are our estimates. */
export const WEIGHTS = {
  timezone: 24,
  language: 18,
  cnFonts: 14,
  vendorFonts: 10,
  webrtc: 10,
  cnBrowser: 8,
  cnDevice: 8,
  intlLocale: 6,
  tzOffset: 6,
  webgl: 1,
} as const;

const CN_TZ = new Set(["Asia/Shanghai", "Asia/Urumqi", "Asia/Chongqing", "Asia/Chungking", "Asia/Harbin", "Asia/Kashgar", "PRC"]);
const CN_BROWSER = /MicroMessenger|QQBrowser|Quark|UCBrowser|baiduboxapp|BIDUBrowser|360SE|360EE|QihooBrowser|SogouMobileBrowser|MetaSr|aweme|Douyin/i;
const CN_DEVICE = /HarmonyOS|OpenHarmony|HUAWEI|Honor|Xiaomi|Redmi|OPPO|vivo|OnePlus/i;
const SOFT_GL = /SwiftShader|llvmpipe|softpipe/i;

export type Hit = { id: keyof typeof WEIGHTS; points: number; why: string };
export type ClaudeEnvReport = { score: number; level: "low" | "medium" | "high"; exitCode: 0 | 1 | 2; hits: Hit[] };

export function scoreClaudeEnv(env: ClaudeEnv): ClaudeEnvReport {
  const hits: Hit[] = [];
  const add = (id: keyof typeof WEIGHTS, points: number, why: string) => {
    if (points > 0) hits.push({ id, points, why });
  };
  add("timezone", CN_TZ.has(env.timeZone) ? WEIGHTS.timezone : 0, env.timeZone);
  const langs = env.languages.map((l) => l.toLowerCase());
  const lang = langs.some((l) => l === "zh-cn" || l.startsWith("zh-hans")) ? WEIGHTS.language : langs.some((l) => l.startsWith("zh")) ? WEIGHTS.language / 2 : 0;
  add("language", lang, langs.join(", "));
  add("cnFonts", env.cnFonts.length ? WEIGHTS.cnFonts : 0, env.cnFonts.join(", "));
  add("vendorFonts", env.vendorFonts.length ? WEIGHTS.vendorFonts : 0, env.vendorFonts.join(", "));
  add("webrtc", env.webrtcIps.length ? WEIGHTS.webrtc : 0, env.webrtcIps.join(", "));
  add("cnBrowser", CN_BROWSER.test(env.ua + " " + env.brands.join(" ")) ? WEIGHTS.cnBrowser : 0, "UA / brands");
  add("cnDevice", CN_DEVICE.test(env.ua) ? WEIGHTS.cnDevice : 0, "UA");
  add("intlLocale", env.intlLocale.toLowerCase().startsWith("zh") ? WEIGHTS.intlLocale : 0, env.intlLocale);
  add("tzOffset", env.tzOffset === -480 ? WEIGHTS.tzOffset : 0, "UTC+8");
  add("webgl", SOFT_GL.test(env.webglRenderer) ? WEIGHTS.webgl : 0, env.webglRenderer);
  const score = hits.reduce((sum, hit) => sum + hit.points, 0);
  const level = score <= 10 ? "low" : score <= 30 ? "medium" : "high";
  return { score, level, exitCode: level === "low" ? 0 : level === "medium" ? 1 : 2, hits };
}

if (process.argv[1]?.endsWith("/claude-env.ts")) {
  const file = process.argv[2];
  if (!file) {
    console.error("usage: node claude-env.ts <claude-env.json>");
    process.exit(3);
  }
  const report = scoreClaudeEnv(JSON.parse(readFileSync(file, "utf8")) as ClaudeEnv);
  for (const hit of report.hits) console.log("+" + hit.points + "  " + hit.id + "  " + hit.why);
  console.log("CLAUDE-ENV " + report.score + " " + report.level.toUpperCase() + " RISK");
  console.log("RESULT " + JSON.stringify(report));
  process.exit(report.exitCode);
}
