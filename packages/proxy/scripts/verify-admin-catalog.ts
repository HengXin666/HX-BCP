#!/usr/bin/env node
/*
 * Admin capability catalog gate.
 *
 * The capability catalog (internal/api/capabilities*.go) is the contract that
 * lets an agent drive the management API without reading Go source. That only
 * holds if three sides agree:
 *
 *   1. internal/api/capabilities_catalog.go  (what the server publishes)
 *   2. .agents/skills/hx-proxy-admin/SKILL.md (what an agent reads)
 *   3. .agents/skills/hx-proxy-admin/scripts/hx-catalog.py (what an agent runs)
 *
 * The Go test TestCapabilityCatalog* already locks the catalog against the
 * validators whose vocabulary it republishes, so enum values cannot go stale.
 * What that test cannot see is the agent-facing side: a skill that documents an
 * endpoint the server does not serve, or a script that calls one. This gate
 * covers exactly that seam.
 *
 * Run before pushing:
 *
 *   node scripts/verify-admin-catalog.ts
 *
 * Exit 0 = the three sides agree. Exit 1 = a token drifted.
 */
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const SOURCES = {
  catalog: "internal/api/capabilities_catalog.go",
  types: "internal/api/capabilities.go",
  quickstart: "internal/api/quickstart.go",
  skill: ".agents/skills/hx-proxy-admin/SKILL.md",
  script: ".agents/skills/hx-proxy-admin/scripts/hx-catalog.py",
};

const read = (relative) => readFileSync(resolve(root, relative), "utf8");

function report(label, failures) {
  if (failures.length === 0) {
    console.log("ok   " + label);
    return 0;
  }
  console.log("FAIL " + label);
  for (const failure of failures) console.log("       " + failure);
  return 1;
}

/** Every quoted value following the given Go field name, in order. */
function goFieldValues(source, field) {
  const pattern = new RegExp("\\b" + field + ":\\s*\"([^\"]+)\"", "g");
  return [...source.matchAll(pattern)].map((match) => match[1]);
}

/** A Go const string literal. */
function goConst(source, name) {
  const match = new RegExp("const\\s+" + name + "\\s*=\\s*\"([^\"]+)\"").exec(source);
  return match ? match[1] : null;
}

const catalog = read(SOURCES.catalog);
const types = read(SOURCES.types);
const quickstartHandler = read(SOURCES.quickstart);
const skill = read(SOURCES.skill);
const script = read(SOURCES.script);
const agentFacing = skill + "\n" + script;

let failures = 0;

// --- 1. Endpoint ids and paths ---------------------------------------------
// Two paths are written as Go constants (they are also registered from those
// constants), so the gate resolves them the same way the compiler would.
const constantPaths = new Map([
  ["CapabilityCatalogPath", goConst(types, "CapabilityCatalogPath")],
  ["QuickstartPath", goConst(quickstartHandler, "QuickstartPath")],
]);
const paths = new Set(goFieldValues(catalog, "Path"));
for (const value of constantPaths.values()) {
  if (value) paths.add(value);
}
const ids = new Set(goFieldValues(catalog, "ID"));

failures += report("catalog publishes endpoints", paths.size > 0 ? [] : [
  SOURCES.catalog + ": no Path values found; did the endpoint table move?",
]);

// A skill that names an admin path the catalog does not publish is teaching an
// agent to call something that may not exist.
const adminPath = /\/api\/v1\/[A-Za-z0-9\-\/]*/g;
const namedPaths = new Set([...agentFacing.matchAll(adminPath)].map((match) => match[0].replace(/\/$/, "")));
const unknownPaths = [];
for (const path of namedPaths) {
  if (paths.has(path)) continue;
  // A parameterised path ("/subscriptions/<id>/refresh") is referenced by its
  // published form; a bare collection prefix is how a human would name it.
  const covers = [...paths].some((published) => published.startsWith(path + "/") || published === path);
  if (!covers) unknownPaths.push(path + " is not a catalog path");
}
failures += report("skill paths resolve to catalog entries", unknownPaths);

// --- 2. Enum references in the catalog must exist ---------------------------
// Every Enum: "name" must be an enum the catalog actually publishes, or the
// skill cannot look the value up.
// Enum blocks are the literal entries of the enums slice; a Name field inside
// an endpoint describes that endpoint, not an enum.
const enumsSlice = /enums := \[\]CapabilityEnum\{([\s\S]*?)\n\t\}/.exec(types);
const declaredEnums = new Set(
  enumsSlice ? [...enumsSlice[1].matchAll(/Name:\s*\"([a-z0-9_]+)\"/g)].map((match) => match[1]) : [],
);
const enumReferences = [...catalog.matchAll(/Enum:\s*\"([^\"]+)\"/g)].map((match) => match[1]);
const danglingEnums = [];
for (const reference of enumReferences) {
  if (!declaredEnums.has(reference)) danglingEnums.push("Enum " + reference + " is referenced but not declared in " + SOURCES.types);
}
failures += report("catalog enum references resolve (" + enumReferences.length + " uses)", danglingEnums);
if (declaredEnums.size === 0) {
  failures += report("catalog declares enums", [SOURCES.types + ": no enum entries found; the check above is vacuous"]);
}

// --- 3. The two constants the agent-facing side hard-codes ------------------
const catalogPath = goConst(types, "CapabilityCatalogPath");
if (!catalogPath) {
  failures += report("CapabilityCatalogPath", [SOURCES.types + ": CapabilityCatalogPath is not a const string"]);
} else {
  const missing = [];
  if (!paths.has(catalogPath)) missing.push("the catalog does not publish its own path " + catalogPath);
  if (!skill.includes(catalogPath)) missing.push(SOURCES.skill + " does not mention " + catalogPath);
  failures += report("catalog path " + catalogPath, missing);
}

const quickstartPath = constantPaths.get("QuickstartPath");
// The path is a constant reference, so match the constant and resolve it.
const quickstartEntry = /ID:\s*\"quickstart\.create\",[\s\S]{0,300}?Path:\s*([A-Za-z_][A-Za-z0-9_]*|\"[^\"]+\")/.exec(catalog);
let quickstartFromCatalog = "";
if (quickstartEntry) {
  const reference = quickstartEntry[1];
  quickstartFromCatalog = reference.startsWith('"')
    ? reference.slice(1, -1)
    : (constantPaths.get(reference) ?? "");
}
if (!quickstartFromCatalog) {
  failures += report("quickstart endpoint", ["the catalog does not document an endpoint with id quickstart.create"]);
} else {
  const missing = [];
  if (quickstartPath && quickstartPath !== quickstartFromCatalog) {
    missing.push("QuickstartPath " + quickstartPath + " != catalog path " + quickstartFromCatalog);
  }
  if (!agentFacing.includes(quickstartFromCatalog)) {
    missing.push("neither the skill nor the script mentions " + quickstartFromCatalog);
  }
  if (!skill.includes("quickstart.create")) {
    missing.push(SOURCES.skill + " does not name the quickstart.create endpoint id");
  }
  failures += report("quickstart path " + quickstartFromCatalog, missing);
}

// --- 4. The script must not carry its own endpoint table --------------------
// A second hard-coded path list is exactly how the two sides drift apart; the
// script is supposed to learn paths from the catalog at runtime.
const scriptPaths = new Set(
  [...script.matchAll(/"(\/api\/v1\/[A-Za-z0-9\-\/]*)"/g)].map((match) => match[1]),
);
const hardCoded = [...scriptPaths].filter((path) => path !== catalogPath);
failures += report("script reads paths from the catalog", hardCoded.map((path) =>
  "script hard-codes " + path + "; it should come from the catalog response"));

// --- 5. The agent side must state the product boundary ----------------------
// A skill that omits these invites an agent to promise capabilities the control
// plane does not have.
const boundaries = [
  { marker: "not_supported", why: "the skill must point at the catalog's not_supported section" },
  { marker: "upstream_proxy_group_id", why: "the skill must state that chaining is residential-only" },
];
failures += report("product boundary is stated", boundaries
  .filter((entry) => !skill.includes(entry.marker) && !agentFacing.includes(entry.marker))
  .map((entry) => entry.why));

if (failures > 0) {
  console.log("");
  console.log("The admin catalog drifted. Update the lagging side(s) and re-run.");
  process.exit(1);
}
console.log("");
console.log("admin catalog: published catalog, skill, and script agree");
