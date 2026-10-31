import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { scoreVisits } from "./engine.ts";
import { bundleCaptures, isBundle, loadCards, printCards } from "./multi.ts";
import { kernelFile, kernelSvg, overviewSvg } from "./multi-svg.ts";
import { formatScore, scorecardSvg, showScore, verdictLabel } from "./svg.ts";
import type { RawVisit } from "./types.ts";

const noFail = process.argv.includes("--no-fail");
const args = process.argv.slice(2).filter((arg) => arg !== "--no-fail");
if (args[0] === "--bundle") {
  const rest = args.slice(2);
  const at = rest.indexOf("--expect");
  const expect = at >= 0 ? (rest[at + 1] || "").split(",").filter(Boolean) : [];
  const files = at >= 0 ? [...rest.slice(0, at), ...rest.slice(at + 2)] : rest;
  writeFileSync(args[1], JSON.stringify(bundleCaptures(files, expect), null, 2));
  process.exit(0);
}
const input = args[0];
const out = args[1];
if (!input) {
  console.error("usage: node score.ts <capture.json> [scorecard.svg]");
  console.error("       node score.ts <kernels.json> [scorecards-dir]");
  console.error("       node score.ts --bundle <kernels.json> [--expect a,b,c] <capture.json>...");
  process.exit(2);
}

const data = JSON.parse(readFileSync(input, "utf8"));
if (isBundle(data)) {
  const cards = loadCards(data);
  const allPass = printCards(cards);
  if (out) {
    mkdirSync(out, { recursive: true });
    for (const card of cards) writeFileSync(join(out, kernelFile(card.kernel)), kernelSvg(card, cards));
    writeFileSync(join(out, "all-kernels.svg"), overviewSvg(cards));
  }
  process.exit(noFail || allPass ? 0 : 1);
}
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
