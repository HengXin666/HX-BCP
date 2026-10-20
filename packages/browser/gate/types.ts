export const GATE_MIN = 75;

export type Verdict = "block" | "incomplete" | "pass";

export type SiteKind = "score" | "record" | "miss";

export type SiteVerdict = "pass" | "fail" | "unmeasured";

export type RawVisit = {
  siteId: string;
  name: string;
  url: string;
  capturedAt: string;
  browser: string;
  ok: boolean;
  status: number;
  title?: string;
  excerpt: string;
  passed: number | null;
  failed: number | null;
  ua?: string;
  webdriver?: boolean | null;
  error?: string;
  href?: string;
};

export type SiteResult = {
  siteId: string;
  name: string;
  url: string;
  role: string;
  kind: SiteKind;
  verdict: SiteVerdict;
  score: number | null;
  headline: string;
  detail: string;
};

export type SiteReport = {
  results: SiteResult[];
  scored: SiteResult[];
  failed: SiteResult[];
  missed: SiteResult[];
  score: number | null;
  verdict: Verdict;
  browser: string;
  capturedAt: string;
};
