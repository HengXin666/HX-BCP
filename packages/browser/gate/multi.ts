import { existsSync, readFileSync } from "node:fs";
import { scoreVisits } from "./engine.ts";
import { SITES } from "./sites.ts";
import { formatScore, showScore, verdictLabel } from "./svg.ts";
import type { RawVisit, SiteReport, SiteResult } from "./types.ts";

export const NO_CDP = "该内核不说 CDP, 当前采集器取不到";
export const NOT_PRODUCT = "本机裸 Chromium 的分不是产品分, 不收";
export const NO_CAPTURE = "这一轮没有产出采集结果";

export type Capture = {
  kernel?: string;
  kernelLabel?: string;
  family?: string;
  browser?: string;
  capturedAt?: string;
  unmeasured?: string;
  available?: boolean;
  status?: string;
  error?: string;
  requestedHeaded?: boolean;
  visits?: RawVisit[];
  results?: SiteResult[];
};

export type ProfileInput = { kernel: string; label?: string; report?: Capture | null; reason?: string; ua?: string };

export type Bundle = { profiles: ProfileInput[] };

export type CellState = "pass" | "fail" | "unmeasured" | "record";

export type KernelCard = {
  kernel: string;
  label: string;
  ua: string;
  uaNote: string;
  capturedAt: string;
  collector: string;
  reason: string;
  report: SiteReport;
};

/** True when the parsed JSON is a multi kernel input with a profiles array */
export function isBundle(data: unknown): data is Bundle {
  return Array.isArray((data as Bundle | null)?.profiles);
}

/** Turns every profile into a card, rejects a kernel listed twice, flags kernels whose UA is identical */
export function loadCards(bundle: Bundle): KernelCard[] {
  const seen = new Set<string>();
  const cards = bundle.profiles.map((profile) => {
    if (!profile.kernel) throw new Error("profile 缺 kernel");
    if (seen.has(profile.kernel)) throw new Error(`kernel ${profile.kernel} 出现了两次`);
    seen.add(profile.kernel);
    return toCard(profile);
  });
  for (const card of cards) {
    const twin = cards.find((other) => other !== card && card.ua !== "" && other.ua === card.ua);
    if (twin) card.uaNote = [card.uaNote, `UA 与 ${twin.kernel} 完全相同`].filter(Boolean).join(", ");
  }
  return cards;
}

/** Builds one card, a missing capture, an unmeasured capture or a local Chromium capture keeps every site unmeasured */
function toCard(profile: ProfileInput): KernelCard {
  const cap = profile.report ?? null;
  if (cap?.kernel && cap.kernel !== profile.kernel) {
    throw new Error(`profile ${profile.kernel} 里装的是 ${cap.kernel} 的采集`);
  }
  const uas = [...new Set((cap?.visits ?? []).map((visit) => visit.ua || "").filter(Boolean))];
  const gap = cap?.available === false ? cap.error || (cap.status === "unmeasured" ? "未测" : "未安装") : "";
  const reason = profile.reason || cap?.unmeasured || gap || (cap === null ? NO_CAPTURE : "") || (cap?.browser === "local-chromium" ? NOT_PRODUCT : "");
  const capturedAt = cap?.capturedAt || "";
  const collector = cap?.browser || "";
  let report: SiteReport;
  if (reason || cap === null) report = blank(reason || NO_CAPTURE, capturedAt, collector);
  else if (Array.isArray(cap.results)) report = cap as unknown as SiteReport;
  else report = scoreVisits(cap.visits ?? [], { browser: collector, capturedAt });
  const order = (result: SiteResult) => SITES.findIndex((site) => site.id === result.siteId);
  report = { ...report, results: [...report.results].sort((a, b) => order(a) - order(b)) };
  return {
    kernel: profile.kernel,
    label: profile.label || cap?.kernelLabel || profile.kernel,
    ua: uas[0] || profile.ua || "",
    uaNote: uas.length > 1 ? `${uas.length} 个站读到的 UA 不一样` : "",
    capturedAt: capturedAt || report.capturedAt || "",
    collector,
    reason,
    report,
  };
}

/** A report where every known site is unmeasured with the given reason */
function blank(reason: string, capturedAt: string, browser: string): SiteReport {
  const results: SiteResult[] = SITES.map((site) => ({
    siteId: site.id,
    name: site.name,
    url: site.url,
    role: site.role,
    kind: "miss",
    verdict: "unmeasured",
    score: null,
    headline: reason,
    detail: reason,
  }));
  return { results, scored: [], failed: [], missed: results, score: null, verdict: "incomplete", browser, capturedAt };
}

/** Cell state of one site for one kernel, a canvas record is its own state */
export function stateAt(card: KernelCard, siteId: string): CellState {
  const result = card.report.results.find((item) => item.siteId === siteId);
  if (!result) return "unmeasured";
  return result.kind === "record" ? "record" : result.verdict;
}

/** Where this kernel differs from the others on one site, empty when it does not */
export function diffAt(card: KernelCard, cards: KernelCard[], siteId: string): string {
  const mine = stateAt(card, siteId);
  const others = cards.filter((other) => other !== card);
  const names = (state: CellState) => others.filter((other) => stateAt(other, siteId) === state).map((other) => other.kernel);
  if (mine === "pass" && names("fail").length) return `它过, ${names("fail").join(" ")} 不过`;
  if (mine === "fail" && names("pass").length) return `它不过, ${names("pass").join(" ")} 过`;
  return "";
}

/** Counts of sites only this kernel passes and sites only this kernel fails */
export function diffCounts(card: KernelCard, cards: KernelCard[]): { wins: number; losses: number } {
  let wins = 0;
  let losses = 0;
  for (const site of SITES) {
    const note = diffAt(card, cards, site.id);
    if (note.startsWith("它过")) wins += 1;
    if (note.startsWith("它不过")) losses += 1;
  }
  return { wins, losses };
}

/** Reads probe.py captures or compare.py cells into one multi kernel input, expected kernels without a file become unmeasured */
export function bundleCaptures(paths: string[], expect: string[]): Bundle {
  const profiles: ProfileInput[] = paths.filter((path) => existsSync(path)).map((path) => {
    const cap = JSON.parse(readFileSync(path, "utf8")) as Capture;
    const stem = (path.split("/").pop() || path).replace(/\.json$/, "").replace(/^sites-/, "");
    const mode = cap.requestedHeaded === undefined ? "" : cap.requestedHeaded ? "-headed" : "-headless";
    const kernel = cap.kernel ? cap.kernel + mode : stem;
    const label = cap.kernelLabel || (cap.available !== undefined ? cap.browser : "") || kernel;
    return { kernel, label, report: { ...cap, kernel } };
  });
  for (const kernel of expect) {
    if (!profiles.some((profile) => profile.kernel === kernel)) profiles.push({ kernel, reason: NO_CAPTURE });
  }
  return { profiles };
}

/** Prints the text scorecard of every kernel and one RESULT line, returns true when every kernel passes */
export function printCards(cards: KernelCard[]): boolean {
  for (const card of cards) {
    console.log(`== ${card.kernel} ${card.label} ${showScore(card.report.score)} ${verdictLabel(card.report.verdict)}`);
    console.log(`   UA ${card.ua || "未取到"}${card.uaNote ? ", " + card.uaNote : ""}`);
    console.log(`   采集时间 ${card.capturedAt || "未记录"}${card.reason ? ", 未测原因 " + card.reason : ""}`);
    for (const result of card.report.results) {
      const score = result.score === null ? "    -" : formatScore(result.score).padStart(5);
      console.log(`${score}  ${result.verdict.padEnd(11)}  ${result.name}  ${result.headline}`);
    }
  }
  console.log("RESULT " + JSON.stringify({
    kernels: cards.map((card) => ({
      kernel: card.kernel,
      ua: card.ua,
      capturedAt: card.capturedAt,
      reason: card.reason,
      score: card.report.score === null ? null : Math.round(card.report.score * 10) / 10,
      verdict: card.report.verdict,
      rows: card.report.results.map((result) => ({ siteId: result.siteId, score: result.score, verdict: result.verdict })),
    })),
  }));
  return cards.length > 0 && cards.every((card) => card.report.verdict === "pass");
}
