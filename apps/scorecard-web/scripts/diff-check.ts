import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { compareCases } from "../src/lib/diff.ts";
import type { CaseRecord } from "../src/lib/record.ts";

/** 读 fixtures/data/pr-18/<no>.json */
function fixture(no: number): CaseRecord {
	return JSON.parse(readFileSync(new URL(`../fixtures/data/pr-18/${no}.json`, import.meta.url), "utf8")) as CaseRecord;
}

/** 打印一次对比, 每内核一行 */
function show(title: string, diff: ReturnType<typeof compareCases>): void {
	console.log(`== ${title} first=${diff.first} previousNo=${diff.previousNo} better=${diff.better} worse=${diff.worse} same=${diff.same} unmeasuredDelta=${diff.unmeasuredDelta}`);
	for (const row of diff.kernels) {
		const flips = row.flips.map((flip) => `${flip.id} ${flip.from}->${flip.to} ${flip.direction}`).join(", ");
		console.log(`   ${row.kernel} ${row.trend} ${row.before} -> ${row.after} delta=${row.delta} unmeasured ${row.unmeasuredBefore}->${row.unmeasuredAfter} flips=[${flips}]`);
	}
}

const previous = fixture(1);
const current = fixture(2);

const diff = compareCases(current, previous);
show("第 2 次 对比 第 1 次", diff);
const by = (kernel: string) => diff.kernels.find((row) => row.kernel === kernel);
assert.equal(by("chromium")?.trend, "better");
assert.equal(by("chromium")?.delta, 4.5);
assert.deepEqual(by("chromium")?.flips.map((flip) => [flip.id, flip.direction]), [["sannysoft", "better"]]);
assert.equal(by("firefox")?.trend, "worse");
assert.deepEqual(by("firefox")?.flips.map((flip) => [flip.id, flip.direction]), [["creepjs", "worse"]]);
assert.equal(by("safari")?.trend, "same");
assert.equal(by("safari")?.delta, null);
assert.equal(by("safari")?.unmeasuredAfter, 9);
assert.equal(diff.unmeasuredDelta, 0);

const first = compareCases(previous, null);
show("第 1 次 无上一次", first);
assert.equal(first.first, true);
assert.ok(first.kernels.every((row) => row.trend === "first"));

const dropped: CaseRecord = { ...current, no: 3, kernels: current.kernels.filter((kernel) => kernel.kernel !== "firefox") };
const swapped = compareCases(dropped, current);
show("第 3 次 少了 firefox", swapped);
assert.equal(swapped.kernels.find((row) => row.kernel === "firefox")?.trend, "gone");
assert.equal(swapped.same, 2);

console.log("diff-check ok");
