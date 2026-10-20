import { spawnSync } from "node:child_process";
import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const dir = process.argv[2];
const out = process.argv[3] || "comparison.md";
if (!dir) {
  console.error("usage: node report.mjs <captures-dir> [comparison.md]");
  process.exit(2);
}

const here = dirname(fileURLToPath(import.meta.url));
const files = readdirSync(dir).filter((name) => name.endsWith(".json")).sort();
const columns = [];
for (const name of files) {
  const raw = JSON.parse(readFileSync(join(dir, name), "utf8"));
  if (raw.available === false) {
    const word = raw.status === "unmeasured" ? "未测" : "未安装";
    columns.push({ label: raw.browser || name, missing: raw.error || word, word, rows: [] });
    continue;
  }
  const run = spawnSync(
    process.execPath,
    ["--experimental-strip-types", join(here, "score.ts"), join(dir, name), "--no-fail"],
    { encoding: "utf8" },
  );
  const line = `${run.stdout || ""}${run.stderr || ""}`.split("\n").find((item) => item.startsWith("RESULT "));
  if (!line) {
    columns.push({ label: raw.browser || name, missing: (run.stderr || "打分失败").slice(0, 200), word: "未测", rows: [] });
    continue;
  }
  const result = JSON.parse(line.slice("RESULT ".length));
  columns.push({ label: raw.browser || name, missing: "", score: result.score, verdict: result.verdict, rows: result.rows });
}

const VERDICT = { pass: "通过", block: "阻断", incomplete: "未完成", fail: "失败" };
const sites = columns.find((column) => column.rows.length)?.rows.map((row) => row.name) ?? [];
const header = ["站", ...columns.map((column) => column.label)];
const lines = [
  `# 检测站对比`,
  ``,
  `对照是本机 Google Chrome 和 Firefox（火狐），有头跑在工作流的虚拟桌面（Xvfb）上，无头不加窗口。`,
  `hx 行是指纹浏览器的每个内核，同样分有头和无头。没有浏览器写未安装，接不上写未测，都不是 0 分。`,
  ``,
  `| ${header.join(" | ")} |`,
  `| ${header.map(() => "---").join(" | ")} |`,
];
for (const site of sites) {
  const cells = [site];
  for (const column of columns) {
    if (column.missing) {
      cells.push(column.word || "未测");
      continue;
    }
    const row = column.rows.find((item) => item.name === site);
    const mark = row && row.verdict === "fail" ? "失败 " : "";
    cells.push(row ? `${mark}${row.score ?? "—"} ${row.headline}` : "—");
  }
  lines.push(`| ${cells.join(" | ")} |`);
}
lines.push("");
for (const column of columns) {
  const tail = column.missing ? `${column.word || "未测"}：${column.missing}` : `${column.score ?? "未测"} ${VERDICT[column.verdict] || column.verdict}`;
  lines.push(`- ${column.label}：${tail}`);
}
writeFileSync(out, lines.join("\n"));
console.log(lines.join("\n"));
