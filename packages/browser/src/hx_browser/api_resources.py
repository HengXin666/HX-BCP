"""Proxies, templates and extensions.

Agent Note: .agents/notes/implemented/architecture/2026-11-01-api-split.md
"""

from __future__ import annotations

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel

from .api_common import ok, store
from .models import Environment, Extension, Fingerprint, Proxy, Template, now_ms
from .proxy import check_proxy

router = APIRouter(prefix="/api/v1")


# ---------- 代理 ----------
class ProxyCreate(BaseModel):
    label: str = ""
    type: str = "http"
    host: str = ""
    port: int = 0
    username: str = ""
    password: str = ""


@router.get("/proxies")
async def list_proxies():
    return ok({"list": [p.model_dump() for p in store().load_proxies()]})


@router.post("/proxies")
async def create_proxy(payload: ProxyCreate):
    p = Proxy(id="px_" + __import__("secrets").token_hex(4), **payload.model_dump())
    proxies = store().load_proxies()
    proxies.insert(0, p)
    store().save_proxies(proxies)
    return ok(p.model_dump())


def _merge(items: list, item_id: str, payload: dict, fields: tuple[str, ...]):
    """Merge the allowed keys of payload into the item with item_id, returns it or None."""
    hit = next((x for x in items if x.id == item_id), None)
    if hit is None:
        return None
    for k in fields:
        if k in payload and payload[k] is not None:
            setattr(hit, k, type(getattr(hit, k))(payload[k]) if not isinstance(getattr(hit, k), (list, dict)) else payload[k])
    return hit


@router.put("/proxies/{proxy_id}")
async def update_proxy(proxy_id: str, payload: dict):
    """Partial update; an empty or missing password keeps the stored one."""
    proxies = store().load_proxies()
    if not payload.get("password"):
        payload = {k: v for k, v in payload.items() if k != "password"}
    p = _merge(proxies, proxy_id, payload, ("label", "type", "host", "port", "username", "password"))
    if p is None:
        raise HTTPException(404, "代理不存在")
    store().save_proxies(proxies)
    return ok(p.model_dump())


@router.delete("/proxies/{proxy_id}")
async def delete_proxy(proxy_id: str):
    proxies = [p for p in store().load_proxies() if p.id != proxy_id]
    store().save_proxies(proxies)
    return ok()


class ProxyCheckReq(BaseModel):
    type: str = "http"
    host: str = ""
    port: int = 0
    username: str = ""
    password: str = ""


@router.post("/proxies/check")
async def proxy_check(payload: ProxyCheckReq):
    geo = await check_proxy(payload.model_dump())
    return ok(geo)


# ---------- 模板 ----------
class TemplateCreate(BaseModel):
    name: str = ""
    description: str = ""
    fingerprint: dict = {}
    proxy_strategy: dict = {}
    extensions: list[str] = []
    launch: dict = {}


@router.get("/templates")
async def list_templates():
    return ok({"list": [t.model_dump() for t in store().load_templates()]})


@router.post("/templates")
async def create_template(payload: TemplateCreate):
    t = Template(id="tpl_" + __import__("secrets").token_hex(4), created_at=now_ms(), **payload.model_dump())
    templates = store().load_templates()
    templates.insert(0, t)
    store().save_templates(templates)
    return ok(t.model_dump())


@router.put("/templates/{tpl_id}")
async def update_template(tpl_id: str, payload: dict):
    templates = store().load_templates()
    t = _merge(templates, tpl_id, payload, ("name", "description", "fingerprint", "proxy_strategy", "extensions", "launch"))
    if t is None:
        raise HTTPException(404, "模板不存在")
    store().save_templates(templates)
    return ok(t.model_dump())


@router.delete("/templates/{tpl_id}")
async def delete_template(tpl_id: str):
    templates = [t for t in store().load_templates() if t.id != tpl_id]
    store().save_templates(templates)
    return ok()


@router.post("/templates/{tpl_id}/apply")
async def apply_template(tpl_id: str, count: int = 1):
    t = next((x for x in store().load_templates() if x.id == tpl_id), None)
    if not t:
        raise HTTPException(404, "模板不存在")
    created = []
    for i in range(max(0, min(count, 50))):
        env = Environment(
            name=f"{t.name}-{i + 1}",
            fingerprint=Fingerprint(**{**Fingerprint().model_dump(), **t.fingerprint}),
            launch=type(Environment().launch)(**{**Environment().launch.model_dump(), **t.launch}),
        )
        env.id = "env_" + __import__("secrets").token_hex(5)
        store().add_env(env)
        created.append(env)
    return ok({"count": len(created)})


# ---------- 扩展 ----------
class ExtensionCreate(BaseModel):
    name: str = ""
    path: str = ""
    categories: list[str] = []
    platforms: list[str] = []
    bind_env_ids: list[str] = []


@router.get("/extensions")
async def list_extensions():
    return ok({"list": [e.model_dump() for e in store().load_extensions()]})


@router.post("/extensions")
async def create_extension(payload: ExtensionCreate):
    e = Extension(id="ext_" + __import__("secrets").token_hex(4), created_at=now_ms(), **payload.model_dump())
    exts = store().load_extensions()
    exts.insert(0, e)
    store().save_extensions(exts)
    return ok(e.model_dump())


@router.put("/extensions/{ext_id}")
async def update_extension(ext_id: str, payload: dict):
    """Partial update of name, path, categories, platforms; bindings change only through /bind."""
    exts = store().load_extensions()
    e = _merge(exts, ext_id, payload, ("name", "path", "categories", "platforms"))
    if e is None:
        raise HTTPException(404, "扩展不存在")
    store().save_extensions(exts)
    return ok(e.model_dump())


@router.delete("/extensions/{ext_id}")
async def delete_extension(ext_id: str):
    exts = [e for e in store().load_extensions() if e.id != ext_id]
    store().save_extensions(exts)
    return ok()


@router.post("/extensions/{ext_id}/bind")
async def bind_extension(ext_id: str, env_ids: list[str]):
    exts = store().load_extensions()
    for e in exts:
        if e.id == ext_id:
            e.bind_env_ids = env_ids
            # 同步到环境 launch.extensions
            for env in store().load_envs():
                if env.id in env_ids and e.path not in env.launch.extensions:
                    env.launch.extensions.append(e.path)
                if env.id not in env_ids and e.path in env.launch.extensions:
                    env.launch.extensions.remove(e.path)
                store().update_env(env)
    store().save_extensions(exts)
    return ok()
