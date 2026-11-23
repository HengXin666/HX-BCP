#!/usr/bin/env node
/*
 * Consumer contract gate.
 *
 * docs/CONSUMER_INTEGRATION_CONTRACT.md is only a promise; this script is what
 * makes it enforceable. It compares the frozen vocabulary in three places that
 * must always agree:
 *
 *   1. docs/CONSUMER_INTEGRATION_CONTRACT.md   (what consumers read)
 *   2. internal/listener/consumer.go           (what the server renders)
 *   3. .agents/skills/hx-consumer-api/SKILL.md (what external agents get)
 *
 * A token that moves in one place and not the others is a broken contract, not
 * documentation drift. Run it before pushing:
 *
 *   node scripts/verify-consumer-contract.ts
 *
 * Exit 0 = the three sides agree. Exit 1 = at least one token drifted; each
 * failure names the sides that disagree.
 */
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const SOURCES = {
  contract: "docs/CONSUMER_INTEGRATION_CONTRACT.md",
  renderer: "internal/listener/consumer.go",
  skill: ".agents/skills/hx-consumer-api/SKILL.md",
};

const read = (relative) => readFileSync(resolve(root, relative), "utf8");

/** Extract a Go []string literal by variable name, e.g. ConsumerFields. */
function goStringSlice(source, name) {
  const match = new RegExp("var\\s+" + name + "\\s*=\\s*\\[\\]string\\{([\\s\\S]*?)\\}").exec(source);
  if (!match) return null;
  return [...match[1].matchAll(/"([^"]+)"/g)].map((entry) => entry[1]);
}

function report(label, failures) {
  if (failures.length === 0) {
    console.log("ok   " + label);
    return 0;
  }
  console.log("FAIL " + label);
  for (const failure of failures) console.log("       " + failure);
  return 1;
}

const contract = read(SOURCES.contract);
const renderer = read(SOURCES.renderer);
const skill = read(SOURCES.skill);

let failures = 0;

// --- 1. The endpoint itself ------------------------------------------------
const endpoint = /const ConsumerNodesPath = "([^"]+)"/.exec(renderer);
if (!endpoint) {
  failures += report("endpoint constant", ["internal/listener/consumer.go: ConsumerNodesPath is not a string literal"]);
} else {
  const path = endpoint[1];
  const missing = [];
  if (!contract.includes(path)) missing.push(SOURCES.contract);
  if (!skill.includes(path)) missing.push(SOURCES.skill);
  // The public token-addressed listing must not live under /api/: that is the
  // session-authenticated administrator namespace, and a public resource there
  // collided with the administrator node list at the same pattern — which
  // net/http's ServeMux reports as a duplicate registration panic at startup.
  // Route-level tests of either service alone cannot see that.
  if (path.startsWith("/api/")) {
    missing.push("path is under /api/, which is session-authenticated administrator space; " +
      "use the token-addressed root namespace beside /sub/");
  }
  failures += report("endpoint " + path, missing.map((file) => file + " does not mention the endpoint"));
}

// --- 2. Field / protocol / transport / format / status vocabularies ---------
const vocabularies = [
  { name: "fields", go: "ConsumerFields", docMarker: "| Field |", skillMarker: "| Field |", required: true },
  { name: "protocols", go: "ConsumerProtocols", required: true },
  { name: "transports", go: "ConsumerTransports", required: true },
  { name: "subscription formats", go: "ConsumerSubscriptionFormats", required: true },
];

for (const vocabulary of vocabularies) {
  const source = goStringSlice(renderer, vocabulary.go);
  if (!source) {
    failures += report(vocabulary.name, [
      SOURCES.renderer + ": " + vocabulary.go + " is missing or not a []string literal",
    ]);
    continue;
  }
  const missing = [];
  for (const token of source) {
    if (!contract.includes(token)) missing.push("contract lacks " + JSON.stringify(token));
    if (!skill.includes(token)) missing.push("skill lacks " + JSON.stringify(token));
  }
  failures += report("vocabulary " + vocabulary.name + " (" + source.length + " values)", missing);
}

// --- 3. Status codes -------------------------------------------------------
const statuses = /var ConsumerStatusCodes = \[\]int\{([^}]*)\}/.exec(renderer.replace(/\s+/g, " "));
if (!statuses) {
  failures += report("status codes", [SOURCES.renderer + ": ConsumerStatusCodes is missing"]);
} else {
  const codes = [...statuses[1].matchAll(/\d+/g)].map((entry) => entry[0]);
  const missing = [];
  for (const code of codes) {
    if (!new RegExp("\\b" + code + "\\b").test(contract)) missing.push("contract lacks " + code);
    if (!new RegExp("\\b" + code + "\\b").test(skill)) missing.push("skill lacks " + code);
  }
  failures += report("status codes (" + codes.join(", ") + ")", missing);
}

// --- 4. The skill must point at the contract, not restate it freely --------
failures += report("skill back-reference", skill.includes(SOURCES.contract)
  ? []
  : [SOURCES.skill + " must reference " + SOURCES.contract]);

if (failures > 0) {
  console.log("");
  console.log("The consumer contract drifted. Update the lagging side(s) and re-run.");
  process.exit(1);
}
console.log("");
console.log("consumer contract: contract doc, renderer, and skill agree");
