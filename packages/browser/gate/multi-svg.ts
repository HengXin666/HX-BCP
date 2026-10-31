import type { CellState, KernelCard } from "./multi.ts";
import { diffAt, diffCounts, stateAt } from "./multi.ts";
import { SITES } from "./sites.ts";
import { clip, FAIL, INK, LINE, MUTED, PAPER, PASS, row, showScore, text, verdictLabel, WARN } from "./svg.ts";

export const UNMEASURED = "#c9c2b6";
export const RECORD = "#4d6b8a";

const CELL: Record<CellState, { fill: string; word: string; ink: string }> = {
  pass: { fill: PASS, word: "过", ink: PAPER },
  fail: { fill: FAIL, word: "不过", ink: PAPER },
  unmeasured: { fill: UNMEASURED, word: "未测", ink: INK },
  record: { fill: RECORD, word: "记录", ink: PAPER },
};

/** File name of one kernel card inside the scorecards directory */
export function kernelFile(kernel: string): string {
  return `kernel-${kernel.replace(/[^a-zA-Z0-9_-]/g, "_")}.svg`;
}

/** One card for one kernel, the same site rows as the single card plus a column saying where it differs from the others */
export function kernelSvg(card: KernelCard, cards: KernelCard[]): string {
  const width = 1120;
  const rowH = 36;
  const top = card.reason ? 228 : 204;
  const height = top + card.report.results.length * rowH + 72;
  const report = card.report;
  const verdict = verdictLabel(report.verdict);
  const verdictColor = report.verdict === "pass" ? PASS : report.verdict === "incomplete" ? WARN : FAIL;
  const { wins, losses } = diffCounts(card, cards);
  const ua = card.ua || "未取到 UA";
  const rows = report.results
    .map((result, index) => {
      const y = top + index * rowH;
      const note = diffAt(card, cards, result.siteId);
      const color = note.startsWith("它过") ? PASS : FAIL;
      // 整个内核未测时原因已在上方写一次, 行内不再逐行重复同一句
      const shown = card.reason ? { ...result, headline: "" } : result;
      return row(shown, y) + (note ? text(900, y + 12, clip(note, 26), 12, color) : "");
    })
    .join("");
  return [
    svgOpen(width, height),
    `<title>${esc(`HX Gate 内核 ${card.kernel}, ${showScore(report.score)} ${verdict}, UA ${ua}, 采集时间 ${card.capturedAt || "未记录"}`)}</title>`,
    frame(width, height),
    text(32, 48, `HX GATE, 内核 ${card.kernel}`, 15, INK),
    text(width - 32, 48, clip(card.label, 48), 13, MUTED, "end"),
    text(32, 100, showScore(report.score), 48, INK),
    text(176, 88, verdict, 22, verdictColor),
    text(176, 112, `${report.scored.length} 站有结论, ${report.failed.length} 站失败, ${report.missed.length} 站未测, 对比其余 ${cards.length - 1} 个内核, 独过 ${wins} 站, 独挂 ${losses} 站`, 13, MUTED),
    text(32, 144, `UA ${clip(ua, 150)}`, 11, card.ua ? INK : WARN),
    text(32, 164, `采集时间 ${card.capturedAt || "未记录"}, 采集器 ${card.collector || "未记录"}${card.uaNote ? ", " + card.uaNote : ""}`, 11, card.uaNote ? WARN : MUTED),
    card.reason ? text(32, 188, `整个内核未测, 原因 ${card.reason}`, 13, WARN) : "",
    text(900, top - 14, "相对其余内核", 11, MUTED),
    rows,
    text(32, height - 36, "分是站点自己写出的结论, 抽不到的站记未测, 不进平均, 也不拿别的内核的数顶替", 12, MUTED),
    `</svg>`,
  ].join("");
}

/** Overview card, one row per kernel, one cell per checker site colored by pass, fail, unmeasured or record */
export function overviewSvg(cards: KernelCard[]): string {
  const cellW = 84;
  const cellH = 26;
  const rowH = 58;
  const left = 176;
  const gridW = SITES.length * cellW;
  const width = left + gridW + 200;
  const top = 132;
  const height = top + cards.length * rowH + 96;
  const times = [...new Set(cards.map((card) => card.capturedAt).filter(Boolean))].sort();
  const span = times.length === 0 ? "未记录" : times.length === 1 ? times[0] : `${times[0]} 到 ${times[times.length - 1]}`;
  const header = SITES.map((site, index) => text(left + index * cellW + cellW / 2, top - 12, clip(site.name, 11), 10, MUTED, "middle")).join("");
  const rows = cards.map((card, index) => overviewRow(card, top + index * rowH, left, cellW, cellH)).join("");
  return [
    svgOpen(width, height),
    `<title>${esc(`HX Gate 全部内核 ${cards.length} 个, ${cards.map((card) => card.kernel).join(" ")}, 采集时间 ${span}`)}</title>`,
    frame(width, height),
    text(32, 48, `HX GATE, 全部内核 ${cards.length} 个, 按检测站逐格`, 15, INK),
    text(32, 72, `采集时间 ${span}, 每行下方是该内核自己的实测 UA`, 11, MUTED),
    header,
    rows,
    legend(32, height - 52),
    text(32, height - 24, "未测是灰格, 不是失败, 也不进平均, 原因写在该内核单图里", 11, MUTED),
    `</svg>`,
  ].join("");
}

/** One kernel row of the overview, its cells, its score and its own UA and capture time */
function overviewRow(card: KernelCard, y: number, left: number, cellW: number, cellH: number): string {
  const cells = SITES.map((site, index) => {
    const state = stateAt(card, site.id);
    const look = CELL[state];
    const x = left + index * cellW;
    const dash = state === "unmeasured" ? ` stroke="${MUTED}" stroke-dasharray="3 2"` : ` stroke="${PAPER}"`;
    return [
      `<rect x="${x + 2}" y="${y}" width="${cellW - 4}" height="${cellH}" fill="${look.fill}"${dash} data-site="${site.id}" data-state="${state}"/>`,
      text(x + cellW / 2, y + 17, look.word, 11, look.ink, "middle"),
    ].join("");
  }).join("");
  const report = card.report;
  const color = report.verdict === "pass" ? PASS : report.verdict === "incomplete" ? WARN : FAIL;
  const right = left + SITES.length * cellW + 16;
  const info = `UA ${card.ua || "未取到"}, 时间 ${card.capturedAt || "未记录"}${card.reason ? ", 未测原因 " + card.reason : ""}`;
  return [
    `<g data-kernel="${esc(card.kernel)}">`,
    text(32, y + 12, card.kernel, 14, INK),
    text(32, y + 26, clip(card.label, 20), 10, MUTED),
    cells,
    text(right, y + 18, `${showScore(report.score)} ${verdictLabel(report.verdict)}`, 13, color),
    text(left + 2, y + cellH + 16, clip(info, 160), 9, card.ua ? MUTED : WARN),
    `<line x1="32" y1="${y + cellH + 24}" x2="${right + 160}" y2="${y + cellH + 24}" stroke="${LINE}"/>`,
    `</g>`,
  ].join("");
}

/** Color key of the overview cells */
function legend(x: number, y: number): string {
  return (Object.keys(CELL) as CellState[]).map((state, index) => {
    const look = CELL[state];
    const cx = x + index * 120;
    return `<rect x="${cx}" y="${y - 12}" width="16" height="16" fill="${look.fill}"/>` + text(cx + 22, y + 1, `${look.word} ${state}`, 11, INK);
  }).join("");
}

function svgOpen(width: number, height: number): string {
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}" role="img">`;
}

function frame(width: number, height: number): string {
  return `<rect width="${width}" height="${height}" fill="#f3efe6"/><rect x="16" y="16" width="${width - 32}" height="${height - 32}" fill="${PAPER}" stroke="${INK}"/>`;
}

function esc(value: string): string {
  return value.replaceAll("&", "&" + "amp;").replaceAll("<", "&" + "lt;").replaceAll(">", "&" + "gt;").replaceAll('"', "&" + "quot;");
}
