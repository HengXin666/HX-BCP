// noise.js: 同 seed 稳定 / 不同 seed 隔离 / 关闭无变化 / 不动 alpha 与 WebGL 区间外 (对齐 NodeCloak 的 vm realm 测试)
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";

const source = readFileSync(new URL("../src/hx_browser/noise.js", import.meta.url), "utf8");

function stubs() {
  class TextMetrics { get width() { return 123.5; } }
  class CanvasRenderingContext2D {
    getImageData(x, y, w, h) {
      const data = new Uint8ClampedArray(w * h * 4);
      for (let i = 0; i < data.length; i += 4) data.set([24, 40, 60, i % 8 ? 255 : 0], i);
      return { data, width: w, height: h };
    }
    measureText() { return new TextMetrics(); }
  }
  class WebGLRenderingContext {
    readPixels(x, y, w, h, format, type, out, offset = 0) {
      for (let i = offset; i < offset + w * h * 4; i += 4) out.set([64, 64, 64, 255], i);
    }
  }
  class AudioBuffer {
    constructor() { this.data = new Float32Array(512).fill(0.125); }
    getChannelData() { return this.data; }
  }
  Object.assign(globalThis, { CanvasRenderingContext2D, WebGLRenderingContext, AudioBuffer });
}

function realm(seed, on = true) {
  const ctx = vm.createContext({});
  vm.runInContext("(" + stubs.toString() + ")()", ctx);
  const fp = { noise_seed: seed, canvas_noise: on, webgl_noise: on, audio_noise: on };
  vm.runInContext("(" + source + ")(" + JSON.stringify(fp) + ", globalThis)", ctx);
  return (code) => vm.runInContext(code, ctx);
}

const A = 11111;
const B = 22222;
const pixels = (run) => Array.from(run("new CanvasRenderingContext2D().getImageData(0, 0, 64, 64).data"));
const width = (run) => run("new CanvasRenderingContext2D().measureText('hello').width");
const gl = (run, type = 5121) => Array.from(run("(() => { const out = new Uint8Array(4 + 32 * 32 * 4); new WebGLRenderingContext().readPixels(0, 0, 32, 32, 6408, " + type + ", out, 4); return out; })()"));
const audio = (run) => Array.from(run("(() => { const b = new AudioBuffer(); b.getChannelData(0); return b.getChannelData(0); })()"));

test("canvas: same seed stable, seeds isolated, alpha and transparent pixels untouched", () => {
  const a = realm(A), b = realm(B), plain = realm(A, false);
  const pa = pixels(a), pp = pixels(plain);
  assert.deepEqual(pa, pixels(a));
  assert.notDeepEqual(pa, pp);
  assert.notDeepEqual(pa, pixels(b));
  for (let i = 0; i < pa.length; i += 4) {
    assert.equal(pa[i + 3], pp[i + 3]);
    if (pp[i + 3] === 0) assert.deepEqual(pa.slice(i, i + 3), pp.slice(i, i + 3));
  }
});

test("measureText: stable per seed, tiny shift, off is exact", () => {
  const a = realm(A), b = realm(B), plain = realm(A, false);
  assert.equal(width(plain), 123.5);
  assert.equal(width(a), width(a));
  assert.notEqual(width(a), 123.5);
  assert.notEqual(width(a), width(b));
  assert.ok(Math.abs(width(a) - 123.5) < 1e-3);
});

test("measureText: equal raw width stays equal across font strings (font probes keep working)", () => {
  const a = realm(A);
  const at = (font) => a("(() => { const c = new CanvasRenderingContext2D(); c.font = " + JSON.stringify(font) + "; return c.measureText('mmm').width; })()");
  assert.equal(at('72px "Menlo",monospace'), at("72px monospace"));
});

test("webgl: only the requested range of UNSIGNED_BYTE RGBA reads changes", () => {
  const a = realm(A), plain = realm(A, false);
  const ga = gl(a);
  assert.deepEqual(ga.slice(0, 4), [0, 0, 0, 0]);
  assert.deepEqual(ga, gl(a));
  assert.notDeepEqual(ga, gl(plain));
  assert.deepEqual(gl(a, 5126), gl(plain, 5126));
});

test("audio: applied once per buffer, stable and seed-dependent", () => {
  const a = realm(A), b = realm(B), plain = realm(A, false);
  assert.deepEqual(audio(a), audio(a));
  assert.notDeepEqual(audio(a), audio(plain));
  assert.notDeepEqual(audio(a), audio(b));
  assert.ok(audio(plain).every((v) => v === 0.125));
});
