import assert from "node:assert/strict";
import test from "node:test";
import { scoreVisits } from "./engine.ts";
import { interpret } from "./interpret.ts";
import type { RawVisit } from "./types.ts";

function visit(siteId: string, excerpt: string, extra: Partial<RawVisit> = {}): RawVisit {
  return {
    siteId,
    name: siteId,
    url: "https://example.test/",
    capturedAt: "2026-10-18T00:00:00.000Z",
    browser: "test",
    ok: true,
    status: 200,
    excerpt,
    passed: null,
    failed: null,
    ...extra,
  };
}

test("Iphey Unreliable is not read as Reliable, and MX is the score", () => {
  const result = interpret(visit("iphey", "Your Digital Identity Looks Unreliable 30 MX SCORE"));
  assert.equal(result.headline, "Unreliable");
  assert.equal(result.score, 30);
  assert.equal(result.verdict, "fail");
});

test("CreepJS uses the site's headless percent", () => {
  const result = interpret(
    visit("creepjs", "FP ID: abc 94% like headless: xx 100% headless: yy 0% stealth: zz"),
  );
  assert.equal(result.score, 0);
  assert.equal(result.verdict, "fail");
  assert.match(result.headline, /100% headless/);
});

test("BrowserScan can be 85 and still fail when it says bot", () => {
  const result = interpret(
    visit("browserscan", "Chrome Headless Bot Detection: Yes Browser fingerprint authenticity: 85%"),
  );
  assert.equal(result.score, 85);
  assert.equal(result.verdict, "fail");
});

test("a blog sentence about inconsistency is not a Pixelscan result", () => {
  const result = interpret(
    visit("pixelscan", "Verify your browser fingerprint. Your Browser Fingerprint is inconsistent How to Fix it? Best Anti-Detect Browsers"),
  );
  assert.equal(result.kind, "miss");
  assert.equal(result.score, null);
});

test("canvas signature is recorded and kept out of the average", () => {
  const report = scoreVisits([
    visit("browserleaks-canvas", "Signature D64418DFFDD77D1444AE9BD3549AEFEE Uniqueness 99.95%"),
    visit("browserleaks-webrtc", "No Leak Public IP Address 34.85.213.44"),
  ]);
  assert.equal(report.results[0].kind, "record");
  assert.equal(report.score, 100);
  assert.equal(report.verdict, "incomplete");
});

test("any failed site blocks even if another site is clean", () => {
  const report = scoreVisits([
    visit("deviceinfo", "Are you a bot? You are a bot!"),
    visit("browserleaks-webrtc", "No Leak"),
  ]);
  assert.equal(report.verdict, "block");
  assert.equal(report.failed.length, 1);
});

test("CreepJS without like headless is not a pass", () => {
  const result = interpret(visit("creepjs", "20% headless"));
  assert.equal(result.kind, "miss");
  assert.equal(result.score, null);
});

test("a round with no concluded site is not scored zero", () => {
  const report = scoreVisits([visit("pixelscan", "still loading")]);
  assert.equal(report.score, null);
  assert.equal(report.verdict, "incomplete");
});

test("Trustworthy without an MX score is not a pass", () => {
  const result = interpret(visit("iphey", "Trustworthy"));
  assert.equal(result.kind, "miss");
});

test("a bot verdict does not invent a webdriver reason", () => {
  const result = interpret(visit("deviceinfo", "You are a bot"));
  assert.equal(result.verdict, "fail");
  assert.equal(result.detail, "");
});

test("Clearcote coherence score is the site's number", () => {
  const result = interpret(
    visit("clearcote", "Audit complete. Coherence score 99 of 100. 161 of 163 scored checks agree. Coherent"),
  );
  assert.equal(result.score, 99);
  assert.equal(result.verdict, "pass");
  assert.equal(result.headline, "Coherent");
});
