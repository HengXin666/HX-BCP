import { interpret } from "./interpret.ts";
import { SITES } from "./sites.ts";
import type { RawVisit, SiteReport, Verdict } from "./types.ts";
import { GATE_MIN } from "./types.ts";

export function scoreVisits(visits: RawVisit[], meta: { browser?: string; capturedAt?: string } = {}): SiteReport {
  const seen = new Set(visits.map((visit) => visit.siteId));
  const absent: RawVisit[] = SITES.filter((site) => !seen.has(site.id)).map((site) => ({
    siteId: site.id,
    name: site.name,
    url: site.url,
    capturedAt: meta.capturedAt || "",
    browser: meta.browser || visits[0]?.browser || "",
    ok: false,
    status: 0,
    excerpt: "",
    passed: null,
    failed: null,
    error: "这一站没有被打开",
  }));
  const results = [...visits, ...absent].map(interpret);
  const scored = results.filter((result) => result.kind === "score" && result.score !== null);
  const failed = scored.filter((result) => result.verdict === "fail");
  const missed = results.filter((result) => result.kind === "miss");
  const score = scored.length === 0 ? null : scored.reduce((sum, result) => sum + (result.score ?? 0), 0) / scored.length;
  return {
    results,
    scored,
    failed,
    missed,
    score,
    verdict: gate(failed.length, missed.length, scored.length, score),
    browser: meta.browser || visits[0]?.browser || "",
    capturedAt: meta.capturedAt || visits[0]?.capturedAt || "",
  };
}

function gate(failed: number, missed: number, scored: number, score: number | null): Verdict {
  if (failed > 0) return "block";
  if (scored === 0 || missed > 0) return "incomplete";
  if (score !== null && score < GATE_MIN) return "block";
  return "pass";
}
