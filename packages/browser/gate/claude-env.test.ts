import { test } from "node:test";
import assert from "node:assert/strict";
import { scoreClaudeEnv, type ClaudeEnv } from "./claude-env.ts";

const clean: ClaudeEnv = {
  timeZone: "America/Los_Angeles",
  intlLocale: "en-US",
  tzOffset: 420,
  languages: ["en-US", "en"],
  ua: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/141.0.0.0 Safari/537.36",
  platform: "Win32",
  brands: ["Google Chrome"],
  cnFonts: [],
  vendorFonts: [],
  webglRenderer: "ANGLE (NVIDIA GeForce RTX 3060)",
  webrtcIps: [],
};

test("clean US profile scores 0", () => {
  const r = scoreClaudeEnv(clean);
  assert.equal(r.score, 0);
  assert.equal(r.exitCode, 0);
});

test("NodeCloak README sample (UTC, en-US, software WebGL) stays low", () => {
  const r = scoreClaudeEnv({ ...clean, timeZone: "UTC", tzOffset: 0, webglRenderer: "ANGLE (Google, Vulkan 1.3.0 (SwiftShader Device))" });
  assert.equal(r.score, 1);
  assert.equal(r.level, "low");
});

test("untouched Chinese desktop is high risk and blocks", () => {
  const r = scoreClaudeEnv({ ...clean, timeZone: "Asia/Shanghai", intlLocale: "zh-CN", tzOffset: -480, languages: ["zh-CN", "zh"], cnFonts: ["Microsoft YaHei"], vendorFonts: ["MiSans"], webrtcIps: ["192.168.1.5"] });
  assert.equal(r.score, 24 + 18 + 14 + 10 + 10 + 6 + 6);
  assert.equal(r.exitCode, 2);
});

test("traditional Chinese language counts half", () => {
  const r = scoreClaudeEnv({ ...clean, languages: ["zh-TW"] });
  assert.equal(r.score, 9);
  assert.equal(r.level, "low");
});

test("Hong Kong zone is not mainland, but UTC+8 still counts", () => {
  const r = scoreClaudeEnv({ ...clean, timeZone: "Asia/Hong_Kong", tzOffset: -480 });
  assert.equal(r.score, 6);
});

test("WeChat webview is flagged", () => {
  const r = scoreClaudeEnv({ ...clean, ua: clean.ua + " MicroMessenger/8.0" });
  assert.ok(r.hits.some((h) => h.id === "cnBrowser"));
});
