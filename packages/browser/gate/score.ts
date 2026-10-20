import { readFileSync, writeFileSync } from "node:fs";
import { scoreVisits } from "./engine.ts";
import { formatScore, scorecardSvg, showScore, verdictLabel } from "./svg.ts";
import type { RawVisit } from "./types.ts";

const noFail = process.argv.includes("--no-fail");
const positional = process.argv.slice(2).filter((arg) => arg !== "--no-fail");
const input = positional[0];
const out = positional[1];
if (!input) {
  console.error("usage: node score.ts <capture.json> [scorecard.svg]");
  process.exit(2);
}

const data = JSON.parse(readFileSync(input, "utf8"));
const report = scoreVisits(data.visits as RawVisit[], data);
for (const result of report.results) {
  const score = result.score === null ? "    —" : formatScore(result.score).padStart(5);
  console.log(`${score}  ${result.verdict.padEnd(11)}  ${result.name}  ${result.headline}`);
}
console.log(`${showScore(report.score)}  ${verdictLabel(report.verdict)}`);
console.log("RESULT " + JSON.stringify({
  score: report.score === null ? null : Math.round(report.score * 10) / 10,
  verdict: report.verdict,
  rows: report.results.map((result) => ({
    siteId: result.siteId,
    name: result.name,
    score: result.score,
    verdict: result.verdict,
    headline: result.headline,
  })),
}));
if (out) writeFileSync(out, scorecardSvg(report));
process.exit(noFail || report.verdict === "pass" ? 0 : 1);
