"""Kernels whose process HX-Browser does not spawn as a Chromium or Firefox binary: start and stop dispatch.

Agent Note: .agents/notes/implemented/feature/2026-11-22-webkit-local-kernel.md

    safarier   macOS app driven by its CLI, local or over ssh (safarier.py)
    webkit     Playwright WebKit build on this host through a runner process (webkit_local.py)
browser.py calls only start and stop here, so adding another engine of this kind does not touch it.
"""

from __future__ import annotations

from pathlib import Path

from . import safarier, webkit_local

IDS = (safarier.KERNEL_ID, webkit_local.KERNEL_ID)


def handles(kernel_id: str) -> bool:
    return (kernel_id or "").lower() in IDS


async def start(env, settings, profile_dir: Path, plan):
    """RuntimeInfo of the started env; the kernel id on the env picks the engine."""
    if (env.fingerprint.kernel or "").lower() == webkit_local.KERNEL_ID:
        return await webkit_local.start_runtime(env, settings, profile_dir, plan)
    return await safarier.start_runtime(env, settings)


async def stop(settings, info) -> bool:
    if info.kernel == webkit_local.KERNEL_ID:
        return webkit_local.stop(info.pid)
    return await safarier.stop(settings, info.remote_pid or info.pid)


def probe(settings, kernel_id: str):
    """Awaitable probe result for the kernels page."""
    if kernel_id == webkit_local.KERNEL_ID:
        async def done():
            return webkit_local.probe(settings)
        return done()
    return safarier.probe(settings)
