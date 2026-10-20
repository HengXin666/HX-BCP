import { SITES } from "./sites.ts";
import type { RawVisit, SiteKind, SiteResult, SiteVerdict } from "./types.ts";

const compact = (text: string) => text.replace(/\s+/g, " ").trim();

export function interpret(visit: RawVisit): SiteResult {
  const meta = SITES.find((site) => site.id === visit.siteId);
  const text = compact(visit.excerpt || "");
  const base = {
    siteId: visit.siteId,
    name: visit.name || meta?.name || visit.siteId,
    url: visit.href || visit.url || meta?.url || "",
    role: meta?.role || "",
  };
  if (!visit.ok || !text) {
    return { ...base, kind: "miss", verdict: "unmeasured", score: null, headline: "页面没打开", detail: visit.error || "没有正文" };
  }
  return readers[visit.siteId]?.(base, text, visit) ?? missed(base, "这个站还没有抽取器");
}

const readers: Record<string, (base: Base, text: string, visit: RawVisit) => SiteResult> = {
  sannysoft: (base, text, visit) => {
    const passed = visit.passed ?? 0;
    const failed = visit.failed ?? 0;
    const total = passed + failed;
    if (total === 0) return missed(base, "结果表是空的");
    const score = Math.round((passed / total) * 1000) / 10;
    const webdriver = visit.webdriver === true || /webdriver/i.test(text) && /failed|present/i.test(text);
    const headless = /HeadlessChrome/i.test(text) || /HeadlessChrome/i.test(visit.ua || "");
    return scored(base, score, webdriver || failed > 0 ? "fail" : "pass", `${passed} 通过 / ${failed} 失败`, [
      webdriver ? "WebDriver present" : "",
      headless ? "UA 是 HeadlessChrome" : "",
    ]);
  },
  creepjs: (base, text) => {
    const headless = text.match(/(\d+)% headless/i);
    const like = text.match(/(\d+)% like headless/i);
    const stealth = text.match(/(\d+)% stealth/i);
    if (!headless || !like) return missed(base, "还没算出 headless 和 like headless");
    const headlessPct = headless ? Number(headless[1]) : 100;
    const score = Math.max(0, 100 - headlessPct);
    const fp = text.match(/FP ID:\s*([a-f0-9]+)/i);
    return scored(
      base,
      score,
      headlessPct >= 50 || (like ? Number(like[1]) >= 50 : false) ? "fail" : "pass",
      [
        headless ? `${headless[1]}% headless` : "",
        like ? `${like[1]}% like headless` : "",
        stealth ? `${stealth[1]}% stealth` : "",
      ].filter(Boolean).join(" · "),
      [fp ? `FP ${fp[1].slice(0, 12)}` : "", "分 = 100 − headless%"],
    );
  },
  iphey: (base, text) => {
    const unreliable = /unreliable/i.test(text);
    const trustworthy = /trustworthy/i.test(text) && !unreliable;
    const mx = text.match(/(\d+)\s*MX SCORE/i);
    if (!mx || (!unreliable && !trustworthy)) return missed(base, "没有同时看到 MX 和结论");
    const score = Number(mx[1]);
    return scored(base, score, unreliable ? "fail" : "pass", unreliable ? "Unreliable" : "Trustworthy", [
      mx ? `MX ${mx[1]}` : "",
    ]);
  },
  browserscan: (base, text) => {
    const auth = text.match(/fingerprint authenticity:\s*(\d+)%/i);
    const botYes = /bot detection:\s*yes/i.test(text);
    const botNo = /bot detection:\s*no/i.test(text);
    if (!botYes && !botNo) return missed(base, "没有看到 Bot Detection 结论");
    const score = auth ? Number(auth[1]) : botYes ? 0 : 100;
    return scored(base, score, botYes ? "fail" : "pass", botYes ? "Bot Detection: Yes" : "Bot Detection: No", [
      auth ? `真实度 ${auth[1]}%` : "",
      /chrome headless/i.test(text) ? "Chrome Headless" : "",
    ]);
  },
  pixelscan: (base, text) => {
    const article = /how to fix|recommended review|best anti-detect browsers|verify your browser fingerprint/i.test(text);
    const finished = /no proxy detected|proxy detected|bot detected|automation detected/i.test(text);
    if (!finished || article && !/scan (complete|finished)|detection result/i.test(text)) {
      return missed(base, "打开了检测页，扫描没有完成");
    }
    const inconsistent = /\binconsistent\b/i.test(text);
    const consistent = /\bconsistent\b/i.test(text) && !inconsistent;
    if (!inconsistent && !consistent) return missed(base, "没有看到一致性结论");
    return scored(base, inconsistent ? 0 : 100, inconsistent ? "fail" : "pass", inconsistent ? "Inconsistent" : "Consistent", []);
  },
  deviceinfo: (base, text) => {
    if (/you are a bot/i.test(text) || /"isBot":\s*true/i.test(text)) {
      return scored(base, 0, "fail", "You are a bot", [
        /webdriver/i.test(text) ? "webdriver" : "",
        /CDP/i.test(text) ? "CDP" : "",
      ].filter(Boolean));
    }
    if (/you are not a bot|you are human/i.test(text)) return scored(base, 100, "pass", "不是机器人", []);
    return missed(base, "没有看到是不是机器人");
  },
  "browserleaks-webrtc": (base, text) => {
    if (/local ip address\s+(?!-|\?|n\/a)([0-9.]+)/i.test(text)) {
      return scored(base, 0, "fail", "漏了本地地址", []);
    }
    if (/no leak/i.test(text)) {
      const ip = text.match(/Public IP Address\s+([0-9.]+)/i);
      return scored(base, 100, "pass", "No Leak", [ip ? `公网 ${ip[1]}` : ""]);
    }
    return missed(base, "泄漏测试还没出结果");
  },
  "browserleaks-canvas": (base, text) => {
    const sig = text.match(/Signature\s+([A-F0-9]{16,})/i);
    const unique = text.match(/Uniqueness\s+([\d.]+%)/i);
    if (!sig) return missed(base, "没有签名");
    return {
      ...base,
      kind: "record",
      verdict: "unmeasured",
      score: null,
      headline: `签名 ${sig[1].slice(0, 12)}`,
      detail: [unique ? `唯一性 ${unique[1]}` : "", "原始值，不进平均分"].filter(Boolean).join(" · "),
    };
  },
  clearcote: (base, text) => {
    const coherence = text.match(/coherence score\s+(\d+)\s+of\s+100/i);
    const agree = text.match(/(\d+)\s+of\s+(\d+)\s+scored checks agree/i);
    if (coherence) {
      const score = Number(coherence[1]);
      const coherent = /\bcoherent\b/i.test(text);
      const gap = agree ? Number(agree[2]) - Number(agree[1]) : 0;
      return scored(base, score, coherent ? "pass" : "fail", coherent ? "Coherent" : "不一致", [
        agree ? `${agree[1]}/${agree[2]} 项一致` : "",
        gap > 0 ? `${gap} 项不一致` : "",
      ]);
    }
    if (/run the audit/i.test(text)) return missed(base, "停在说明页，审计没跑");
    return missed(base, "没有连贯分");
  },
};

type Base = Pick<SiteResult, "siteId" | "name" | "url" | "role">;

function scored(base: Base, score: number, verdict: SiteVerdict, headline: string, extra: string[]): SiteResult {
  return {
    ...base,
    kind: "score" satisfies SiteKind,
    verdict,
    score,
    headline,
    detail: extra.filter(Boolean).join(" · "),
  };
}

function missed(base: Base, headline: string): SiteResult {
  return { ...base, kind: "miss", verdict: "unmeasured", score: null, headline, detail: "抽不到站点自己的结论，这一站不计分" };
}
