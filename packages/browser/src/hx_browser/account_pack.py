"""账号包导出/导入 + Firefox profile 直挂 + cookies.sqlite 解析（对应 PRD F2）。"""

from __future__ import annotations

import base64
import hashlib
import json
import logging
import shutil
import sqlite3
import time
from pathlib import Path

from . import cdp
from .models import Account, Environment, Fingerprint, ProxyConfig

log = logging.getLogger("hx_browser.account_pack")

# 平台 → 域名白名单（Cookie 按域名归属导出）
PLATFORM_DOMAINS: dict[str, list[str]] = {
    "google": ["google.com", "youtube.com", "googleusercontent.com", "googleapis.com", "gstatic.com", "google.com.hk", "google.com.tw", "google.co.jp", "google.com.mx"],
    "youtube": ["youtube.com", "google.com", "googleusercontent.com", "googlevideo.com"],
    "github": ["github.com", "githubusercontent.com", "githubassets.com", "github.io"],
    "cloudflare": ["cloudflare.com", "dash.cloudflare.com", "workers.dev"],
    "freebuff": ["freebuff.com"],
    "facebook": ["facebook.com", "fbcdn.net", "instagram.com"],
    "tiktok": ["tiktok.com", "byteoversea.com"],
    "x": ["x.com", "twitter.com"],
    "microsoft": ["microsoft.com", "live.com", "outlook.com", "msn.com"],
    "amazon": ["amazon.com", "amazonaws.com"],
    "shopify": ["shopify.com", "myshopify.com"],
}

KEY_COOKIES = {
    "google": ["SID", "SSID", "APISID", "SAPISID", "HSID", "NID", "__Secure-1PSID", "__Secure-3PSID", "SIDCC", "__Host-GAPS"],
    "youtube": ["SID", "SSID", "__Secure-1PSID", "__Secure-3PSID"],
    "github": ["user_session", "__Host-user_session_same_site", "_gh_sess", "logged_in", "dotcom_user", "_octo"],
    "cloudflare": ["__cf_logged_in", "CF_VERIFIED_DEVICE", "cf_clearance", "curr-account"],
    "freebuff": ["__Secure-next-auth.session-token", "gr_session"],
    "facebook": ["c_user", "xs", "datr", "sb"],
    "tiktok": ["sessionid", "sid_tt"],
    "x": ["auth_token", "ct0"],
    "microsoft": ["ESTSAUTH", "ESTSAUTHPERSISTENT", "MSPOK"],
}

# 强会话 cookie: 只有命中这些才算"真登录" (NID/OTZ/CONSENT 等只是追踪痕迹)
STRONG_KEY_COOKIES: dict[str, list[str]] = {
    "google": ["SID", "__Secure-1PSID", "__Secure-3PSID", "SAPISID", "__Host-GAPS", "LSID"],
    "youtube": ["SID", "__Secure-1PSID", "__Secure-3PSID"],
    "github": ["user_session", "__Host-user_session_same_site", "_gh_sess"],
    "cloudflare": ["__cf_logged_in"],
    "freebuff": ["__Secure-next-auth.session-token", "gr_session"],
    "facebook": ["c_user", "xs"],
    "tiktok": ["sessionid"],
    "x": ["auth_token"],
    "microsoft": ["ESTSAUTH", "ESTSAUTHPERSISTENT", "MSPOK"],
}


def _domain_match(domain: str, whitelist: list[str]) -> bool:
    d = (domain or "").lstrip(".")
    for w in whitelist:
        if d == w or d.endswith("." + w):
            return True
    return False


def resolve_platform_domains(platform: str) -> list[str]:
    platform = (platform or "").lower()
    if platform in PLATFORM_DOMAINS:
        return PLATFORM_DOMAINS[platform]
    return PLATFORM_DOMAINS["google"]


def resolve_key_cookies(platform: str) -> list[str]:
    platform = (platform or "").lower()
    return KEY_COOKIES.get(platform, KEY_COOKIES["google"])


def filter_cookies(cookies: list[dict], domains: list[str]) -> list[dict]:
    return [c for c in cookies if _domain_match(c.get("domain", ""), domains)]


def _login_state(cookies: list[dict], key_names: list[str]) -> str:
    if not cookies:
        return "empty"
    names = {c.get("name", "") for c in cookies}
    hit = [k for k in key_names if k in names]
    if not hit:
        return "empty"
    now = time.time()
    expired = any(c.get("expires", 0) and 0 < float(c["expires"]) < now for c in cookies)
    return "expired" if expired else "ok"


# ---------- 导出 ----------
async def export_account_pack(env: Environment, debug_port: int, password: str = "") -> dict:
    """从运行中的环境导出账号包。"""
    cookies = await cdp.get_cookies(debug_port)
    domains = resolve_platform_domains(env.platform)
    filtered = filter_cookies(cookies, domains)
    key_names = resolve_key_cookies(env.platform)
    pack = {
        "version": 1,
        "platform": env.platform or "custom",
        "exported_at": int(time.time() * 1000),
        "fingerprint": env.fingerprint.model_dump(),
        "cookies": filtered,
        "meta": {
            "logged_in": _login_state(filtered, key_names),
            "key_cookies": key_names,
            "total_cookies": len(cookies),
            "pack_cookies": len(filtered),
            "size_bytes": len(json.dumps(filtered, ensure_ascii=False)),
        },
    }
    if password:
        pack = _encrypt_pack(pack, password)
    return pack


def pack_size_bytes(pack: dict) -> int:
    return len(json.dumps(pack, ensure_ascii=False).encode("utf-8"))


# ---------- 导入 ----------
async def import_account_pack(pack: dict, password: str = "", target_env: Environment | None = None) -> Environment:
    """导入账号包：创建（或覆盖）环境并写入 account.cookies + fingerprint。"""
    if pack.get("encrypted"):
        pack = _decrypt_pack(pack, password)
    if pack.get("version") != 1:
        raise ValueError("不支持的账号包版本")
    cookies = pack.get("cookies", [])
    fingerprint_data = pack.get("fingerprint", {})
    if target_env is None:
        env = Environment(name=f"导入-{pack.get('platform','custom')}-{int(time.time()%100000)}")
    else:
        env = target_env
    env.platform = pack.get("platform", env.platform or "custom")
    # 应用指纹（合并）
    if fingerprint_data:
        merged = {**env.fingerprint.model_dump(), **{k: v for k, v in fingerprint_data.items() if v not in (None, "")}}
        env.fingerprint = Fingerprint(**merged)
    # 写入 Cookie 文本（JSON 格式）
    env.account.cookies = json.dumps(cookies, ensure_ascii=False)
    return env


# ---------- Firefox cookies.sqlite 解析（批量注册 userdir 导入用） ----------
def read_firefox_cookies(profile_dir: Path, domain_filter: list[str] | None = None) -> list[dict]:
    """从 Firefox profile 的 cookies.sqlite 读取 Cookie（批量注册 userdir 产物）。"""
    db = profile_dir / "cookies.sqlite"
    if not db.exists():
        return []
    cookies: list[dict] = []
    try:
        conn = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
        cur = conn.cursor()
        cur.execute("PRAGMA table_info(moz_cookies)")
        cols = [row[1] for row in cur.fetchall()]
        sel = ["name", "value", "host", "path", "expiry", "isSecure", "isHttpOnly"]
        sel = [c for c in sel if c in cols]
        cur.execute(f"SELECT {', '.join(sel)} FROM moz_cookies")
        for row in cur.fetchall():
            rec = dict(zip(sel, row))
            host = str(rec.get("host", ""))
            domain = "." + host if not host.startswith(".") else host
            cookies.append(
                {
                    "name": rec.get("name", ""),
                    "value": rec.get("value", ""),
                    "domain": domain,
                    "path": rec.get("path", "/"),
                    "secure": bool(rec.get("isSecure")),
                    "httpOnly": bool(rec.get("isHttpOnly")),
                    "expires": rec.get("expiry") or 0,
                    "sameSite": "",
                }
            )
        conn.close()
    except Exception as e:  # noqa: BLE001
        log.warning("read firefox cookies failed: %s", e)
    if domain_filter:
        cookies = filter_cookies(cookies, domain_filter)
    return cookies


def import_firefox_profile(src_dir: Path, dst_dir: Path, mode: str = "copy") -> int:
    """导入批量注册 Firefox userdir 为环境 profile 目录。

    mode: copy（默认，复制一份）/ move（移动）/ reference（软链，最快）
    """
    if not src_dir.exists():
        raise FileNotFoundError(f"profile 目录不存在: {src_dir}")
    dst_dir.mkdir(parents=True, exist_ok=True)
    if mode == "reference":
        try:
            (dst_dir / "profile.link").write_text(str(src_dir.resolve()), "utf-8")
            return 0
        except Exception as e:  # noqa: BLE001
            log.warning("symlink failed, fallback to copy: %s", e)
    if mode == "move":
        shutil.move(str(src_dir), str(dst_dir / "profile"))
        return 1
    shutil.copytree(str(src_dir), str(dst_dir / "profile"), dirs_exist_ok=True)
    return 1


def effective_profile_dir(env_dir: Path) -> Path:
    """环境目录下实际的 profile 位置（支持 reference 软链）。"""
    link = env_dir / "profile.link"
    if link.exists():
        return Path(link.read_text("utf-8").strip())
    sub = env_dir / "profile"
    if sub.exists():
        return sub
    return env_dir


# ---------- 可选加密（cryptography，AES-GCM via Fernet） ----------
def _derive_key(password: str) -> bytes:
    salt = b"hx-browser-account-pack"
    return base64.urlsafe_b64encode(hashlib.pbkdf2_hmac("sha256", password.encode(), salt, 200_000, dklen=32))


def _encrypt_pack(pack: dict, password: str) -> dict:
    try:
        from cryptography.fernet import Fernet
    except ImportError:
        raise RuntimeError("账号包加密需要安装依赖: uv sync --extra crypto")
    f = Fernet(_derive_key(password))
    token = f.encrypt(json.dumps(pack, ensure_ascii=False).encode("utf-8"))
    return {"encrypted": True, "ciphertext": token.decode("ascii")}


def _decrypt_pack(pack: dict, password: str) -> dict:
    try:
        from cryptography.fernet import Fernet, InvalidToken
    except ImportError:
        raise RuntimeError("账号包解密需要安装依赖: uv sync --extra crypto")
    f = Fernet(_derive_key(password))
    try:
        data = f.decrypt(pack["ciphertext"].encode("ascii"))
    except InvalidToken:
        raise ValueError("口令错误或包已损坏")
    return json.loads(data.decode("utf-8"))


# ---------- 登录态离线扫描 (前端"已登录账号"展示用) ----------
def scan_env_cookies(env_dir: Path) -> list[dict]:
    """离线读取环境 cookie 库 (Chromium Cookies 或 Firefox cookies.sqlite)。

    只读 cookie 名称 + 过期时间 (值加密不可读不影响判定)。
    返回 [{"name","domain","expires"}, ...]
    """
    cookies: list[dict] = []
    # Chromium: <env_dir>/Default/Cookies
    for db in (env_dir / "Default" / "Cookies", env_dir / "Cookies"):
        if not db.exists():
            continue
        try:
            conn = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
            cur = conn.cursor()
            cur.execute("PRAGMA table_info(cookies)")
            cols = [r[1] for r in cur.fetchall()]
            name_col = "name" if "name" in cols else "cookie_name"
            host_col = "host_key" if "host_key" in cols else "domain"
            exp_col = "expires_utc" if "expires_utc" in cols else "expires"
            cur.execute(f"SELECT {name_col}, {host_col}, {exp_col} FROM cookies")
            for n, h, e in cur.fetchall():
                cookies.append({"name": n or "", "domain": h or "", "expires": e or 0})
            conn.close()
        except Exception as e:  # noqa: BLE001
            log.warning("scan chromium cookies failed %s: %s", db, e)
        break
    # Firefox: cookies.sqlite
    fb_db = env_dir / "cookies.sqlite"
    if fb_db.exists():
        try:
            conn = sqlite3.connect(f"file:{fb_db}?mode=ro", uri=True)
            cur = conn.cursor()
            cur.execute("SELECT name, host, expiry FROM moz_cookies")
            for n, h, e in cur.fetchall():
                cookies.append({"name": n or "", "domain": h or "", "expires": e or 0})
            conn.close()
        except Exception as e:  # noqa: BLE001
            log.warning("scan firefox cookies failed %s: %s", fb_db, e)
    return cookies


def detect_login_states(cookies: list[dict]) -> dict[str, dict]:
    """按平台关键 cookie 判定登录态 (离线, 名称+过期时间)。

    返回: {platform: {"state": "ok"|"expired"|"empty", "keys": [命中关键cookie], "total": n}}
    state 判定: 命中关键 cookie 且未过期 = ok; 命中但关键 cookie 已过期 = expired;
    仅普通 cookie (如 google NID) = empty。
    """
    now_utc = int((time.time() + 11644473600) * 1_000_000)  # Chrome epoch (1601-01-01) 微秒
    names = {c.get("name", "") for c in cookies}
    out: dict[str, dict] = {}
    for platform, keys in KEY_COOKIES.items():
        hits = [k for k in keys if k in names]
        if not hits:
            continue
        strong = STRONG_KEY_COOKIES.get(platform, keys)
        strong_hits = [k for k in strong if k in names]
        if not strong_hits:
            # 只有追踪类 cookie (NID/OTZ/CONSENT/_octo 等) → 不算登录
            out[platform] = {"state": "empty", "keys": hits, "total": len(cookies)}
            continue
        # 判定: 强会话 cookie 是否过期
        expired = False
        for c in cookies:
            if c.get("name") in strong_hits and c.get("expires"):
                e = c["expires"]
                # expires_utc (Chrome epoch µs) 或 unix 秒
                e_sec = e / 1_000_000 if e > 10_000_000_000_000 else e
                if 0 < e_sec < time.time():
                    expired = True
                    break
        state = "expired" if expired else "ok"
        out[platform] = {"state": state, "keys": strong_hits, "total": len(cookies)}
    return out


def detect_login_identities(cookies: list[dict]) -> dict[str, str]:
    """从明文 cookie 提取账号身份 (运行时 CDP 导出可用; 离线值加密时为空)。

    返回: {platform: "身份标识"} 如 github -> "barkervictoria0145"
    """
    by_name = {c.get("name", ""): c.get("value", "") for c in cookies}
    out: dict[str, str] = {}
    gh = by_name.get("dotcom_user") or by_name.get("user_login")
    if gh:
        out["github"] = gh
    for platform, keys in (("google", ["__Secure-1PSID", "SID"]), ("cloudflare", ["__cf_logged_in"]),
                           ("freebuff", ["gr_session"]), ("microsoft", ["MSPOK", "ESTSAUTH"])):
        if any(k in by_name for k in keys):
            out.setdefault(platform, "已登录")
    return out
