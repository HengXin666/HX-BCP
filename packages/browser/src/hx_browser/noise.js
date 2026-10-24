// 按副本 seed 的指纹噪声, 对齐 NodeCloak fingerprint.js 的 canvas / font / webgl / audio 部分。
// 同 seed 每次读到同样的结果, 不同 seed 之间不同; 关闭时一个字节都不改。
// 只改 RGB, 不动 alpha 与全透明像素; WebGL 只改请求的目标区间; 先调原函数, 报错语义不变。
// fingerprint.py 以 (FP, window) 调用; tests/noise.test.mjs 在 node vm 里验证。
function (FP, W) {
  'use strict';
  const SEED = (Number(FP.noise_seed) >>> 0) || 1;
  function mix(n) {
    let x = (SEED ^ Math.imul(n >>> 0, 0x9E3779B1)) >>> 0;
    x ^= x >>> 16; x = Math.imul(x, 0x85EBCA6B) >>> 0;
    x ^= x >>> 13; x = Math.imul(x, 0xC2B2AE35) >>> 0;
    x ^= x >>> 16;
    return x >>> 0;
  }
  function mixStr(s) {
    let h = 0x811C9DC5;
    for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 0x01000193) >>> 0;
    return mix(h);
  }
  // 约 1/16 的非透明像素, 在一个由 seed 决定的 RGB 通道上 ±1
  function nudge(arr, base, salt, len) {
    for (let p = 0; p * 4 < len; p++) {
      const i = base + p * 4;
      if (arr[i + 3] === 0) continue;
      const k = mix(salt + p);
      if ((k & 15) !== 0) continue;
      const c = i + ((k >>> 4) % 3);
      arr[c] = arr[c] >= 255 ? 254 : arr[c] + 1;
    }
  }

  const C2D = W.CanvasRenderingContext2D;
  if (FP.canvas_noise && C2D) {
    const gd = C2D.prototype.getImageData;
    C2D.prototype.getImageData = function () {
      const img = gd.apply(this, arguments);
      try { nudge(img.data, 0, 0x1000, img.data.length); } catch (e) {}
      return img;
    };
    const mt = C2D.prototype.measureText;
    if (mt) {
      C2D.prototype.measureText = function (text) {
        const m = mt.apply(this, arguments);
        try {
          const w = m.width;
          if (typeof w === 'number' && Number.isFinite(w) && w > 0) {
            const k = mixStr(String(text) + '|' + String(this.font));
            const d = ((k % 997) + 1) * 1e-7 * ((k & 1024) ? 1 : -1);
            Object.defineProperty(m, 'width', { value: w + d, configurable: true, enumerable: true });
          }
        } catch (e) {}
        return m;
      };
    }
    const CE = W.HTMLCanvasElement;
    const doc = W.document;
    if (CE && doc) {
      const td = CE.prototype.toDataURL;
      // 导出走一份加了同样噪声的副本, 不再截断 base64 (旧做法会产生坏图, 本身就是异常信号)
      CE.prototype.toDataURL = function () {
        try {
          const w = this.width;
          const h = this.height;
          if (w > 0 && h > 0 && w * h <= 4194304) {
            const c = doc.createElement('canvas');
            c.width = w;
            c.height = h;
            const x = c.getContext('2d');
            x.drawImage(this, 0, 0);
            x.putImageData(x.getImageData(0, 0, w, h), 0, 0);
            return td.apply(c, arguments);
          }
        } catch (e) {}
        return td.apply(this, arguments);
      };
    }
  }

  if (FP.webgl_noise) {
    [W.WebGLRenderingContext, W.WebGL2RenderingContext].forEach(function (GL) {
      if (!GL || !GL.prototype.readPixels) return;
      const rp = GL.prototype.readPixels;
      GL.prototype.readPixels = function (x, y, w, h, format, type, out, offset) {
        const r = rp.apply(this, arguments);
        try {
          if (type === 5121 && format === 6408 && out && out.BYTES_PER_ELEMENT === 1) {
            const start = offset >>> 0;
            const len = Math.min(out.length - start, Math.abs(w * h) * 4);
            if (len > 0) nudge(out, start, 0x2000, len);
          }
        } catch (e) {}
        return r;
      };
    });
  }

  const AB = W.AudioBuffer;
  if (FP.audio_noise && AB) {
    const gcd = AB.prototype.getChannelData;
    const done = new WeakSet();
    AB.prototype.getChannelData = function () {
      const data = gcd.apply(this, arguments);
      try {
        if (data && data.length > 100 && !done.has(data)) {
          done.add(data);
          for (let i = 0; i < data.length; i++) {
            const k = mix(0x3000 + i);
            if ((k & 31) === 0) data[i] += ((k >>> 5) & 1 ? 1 : -1) * 1e-7;
          }
        }
      } catch (e) {}
      return data;
    };
  }
}
