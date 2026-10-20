"""TLS/HTTP2 指纹探针 — 把「代理有没有改变握手身份」变成可回归的读数。

为什么独立于 sites.py 的四个站:
  sites.py 测的是 **JS 侧**指纹 (canvas/WebGL/时区)。本项目 2026-09-17 的战场在
  **传输层** —— 同一份 JS 指纹可以配截然不同的 TLS/h2 身份。两者必须分开测,
  否则「JS 合格」会被误读成「这环境合格」。

判定用的量必须**顺序无关**, 理由是实测 (2026-09-17, fp-audit 内标定 6 次连打):

    ja4_r / ja3n_hash / akamai_hash          → STABLE (6/6 相同)
    ja3_hash / ja4 / ja4_o                    → VARY   (6/6 各不相同)

Chrome 对 ClientHello 扩展顺序做**逐连接随机置换** (并插入 GREASE), 所以
顺序敏感的 JA3/JA4 连浏览器自己两次连接都比不相等 —— 拿它判「两路是否相同」
永远失败, 只会产出假绿/假红。这是本探针只认顺序无关量的唯一原因。

端点选 tls.browserleaks.com/json: 单次请求返回 ja4/ja4_r/ja3/ja3n/akamai 全字段,
比 tls.peet.ws/api/all 更省 (后者 10KB+ 且要多解析一层)。
"""
from __future__ import annotations

import json
import re

from .cdp import Browser

ENDPOINT = "https://tls.browserleaks.com/json"

# 判等用的顺序无关量 (有区分力且跨连接稳定)
STABLE_KEYS = ("ja3n_hash", "ja3n_text", "ja4_r", "akamai_hash", "akamai_text")
# 仅记录、不参与判等 (顺序敏感, 逐连接变)
VOLATILE_KEYS = ("ja3_hash", "ja3_text", "ja4", "ja4_o", "ja4_ro")


def probe_tls(b: Browser, wait_s: int = 15, samples: int = 1) -> dict:
    """在被测环境内连打指纹端点 samples 次, 返回结构化读数 + 原始证据。

    samples > 1 时一并回报**同路自稳性** —— 这是跨路比对的前提:
    先证明同一路自己稳定, 再谈两路是否相同。
    """
    out = {"site": "tls", "url": ENDPOINT, "status": "unknown", "signals": {}, "raw": ""}
    rows: list[dict] = []
    raw_all: list[str] = []
    for _ in range(max(1, samples)):
        try:
            b.goto(ENDPOINT, wait_s=wait_s)
            txt = b.text() or ""
            raw_all.append(txt[:1500])
            try:
                j = json.loads(txt)
            except Exception:
                m = re.search(r"\{.*\}", txt, re.S)
                j = json.loads(m.group(0)) if m else {}
            if j:
                rows.append(j)
        except Exception as e:  # noqa: BLE001
            raw_all.append("ERR " + type(e).__name__ + ": " + str(e)[:200])
    out["raw"] = "\n---\n".join(raw_all)[:4000]
    if not rows:
        return out

    first = rows[0]
    sig: dict = {}
    for k in STABLE_KEYS + VOLATILE_KEYS:
        v = first.get(k)
        if v:
            sig[k] = str(v)
    # 同路自稳性: 每条顺序无关量在 samples 内是否唯一
    stab = {}
    for k in STABLE_KEYS:
        vals = [str(r.get(k, "")) for r in rows]
        stab[k] = len(set(vals)) <= 1
    sig["_samples"] = len(rows)
    sig["_self_stable"] = all(stab.values())
    sig["_self_stable_detail"] = stab
    out["signals"] = sig
    out["status"] = "ok"
    return out
