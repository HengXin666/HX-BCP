import type { SiteReport, SiteResult, Verdict } from "./types.ts";
import { GATE_MIN } from "./types.ts";

const INK = "#1b1814";
const MUTED = "#6e675e";
const LINE = "#e2dcd0";
const PAPER = "#fffdf8";
const PASS = "#1e6b45";
const WARN = "#8d5a12";
const FAIL = "#9d2c2c";

export function scorecardSvg(report: SiteReport): string {
  const width = 880;
  const rowH = 36;
  const height = 168 + report.results.length * rowH + 72;
  const verdict = verdictLabel(report.verdict);
  const verdictColor = report.verdict === "pass" ? PASS : report.verdict === "incomplete" ? WARN : FAIL;
  const rows = report.results
    .map((result, index) => row(result, 156 + index * rowH))
    .join("");
  return [
    `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" role="img">`,
    `<title>HX Gate ${showScore(report.score)} ${verdict}</title>`,
    `<rect width="${width}" height="${height}" fill="#f3efe6"/>`,
    `<rect x="16" y="16" width="${width - 32}" height="${height - 32}" fill="${PAPER}" stroke="${INK}"/>`,
    text(32, 48, "HX GATE", 13, MUTED),
    text(848, 48, "按检测站实测", 13, MUTED, "end"),
    text(32, 100, showScore(report.score), 48, INK),
    text(176, 88, verdict, 22, verdictColor),
    text(176, 112, `${report.scored.length} 站有结论 · ${report.failed.length} 站失败 · ${report.missed.length} 站未测`, 13, MUTED),
    rows,
    text(32, height - 36, "分是站点自己写出的结论。抽不到的站不进平均，门禁也不放行。", 12, MUTED),
    `</svg>`,
  ].join("");
}

function row(result: SiteResult, y: number): string {
  const color = result.verdict === "pass" ? PASS : result.kind === "miss" || result.kind === "record" ? WARN : FAIL;
  const ratio = result.score === null ? 0 : Math.max(0, Math.min(100, result.score)) / 100;
  const value = result.score === null ? (result.kind === "record" ? "记录" : "未测") : formatScore(result.score);
  return [
    text(32, y + 14, result.name, 13, INK),
    `<rect x="196" y="${y}" width="420" height="14" fill="${PAPER}" stroke="${LINE}"/>`,
    result.score === null
      ? `<rect x="196" y="${y}" width="420" height="14" fill="none" stroke="${WARN}" stroke-dasharray="4 3"/>`
      : `<rect x="196" y="${y}" width="${(420 * ratio).toFixed(1)}" height="14" fill="${color}"/>`,
    text(630, y + 12, value, 12, color),
    text(688, y + 12, clip(result.headline, 22), 12, MUTED),
  ].join("");
}

function text(x: number, y: number, value: string, size: number, fill: string, anchor: "start" | "end" = "start"): string {
  const align = anchor === "start" ? "" : ` text-anchor="${anchor}"`;
  return `<text x="${x}" y="${y}" fill="${fill}" font-size="${size}" font-family="IBM Plex Sans, Noto Sans SC, Source Han Sans SC, sans-serif"${align}>${escapeXml(value)}</text>`;
}

function escapeXml(value: string): string {
  return value.replaceAll("&", "&" + "amp;").replaceAll("<", "&" + "lt;").replaceAll(">", "&" + "gt;").replaceAll('"', "&" + "quot;");
}

function clip(value: string, max: number): string {
  return value.length > max ? `${value.slice(0, max - 1)}…` : value;
}

export function formatScore(score: number): string {
  return (Math.round(score * 10) / 10).toFixed(1);
}

export function showScore(score: number | null): string {
  return score === null ? "未测" : formatScore(score);
}

export function verdictLabel(verdict: Verdict): string {
  if (verdict === "pass") return "通过";
  if (verdict === "incomplete") return "未完成";
  return "阻断";
}

export { GATE_MIN };
