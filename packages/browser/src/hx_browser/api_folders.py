"""Folder routes, a caller only writes into its own lane group.

Agent Note: .agents/notes/implemented/feature/2026-11-01-lane-folders.md

GET    /api/v1/folders            {group_key: [names]} the caller may read
POST   /api/v1/folders            {name} create in the caller's own group
PUT    /api/v1/folders/{name}     {name} rename in the caller's own group
DELETE /api/v1/folders/{name}     delete an empty folder in the caller's own group
"""

from __future__ import annotations

import lane
from fastapi import APIRouter
from fastapi.responses import JSONResponse
from pydantic import BaseModel

from . import folders, lane_guard

router = APIRouter(prefix="/api/v1")


class FolderName(BaseModel):
    name: str = ""


def _ok(data=None) -> JSONResponse:
    return JSONResponse({"code": 0, "msg": "success", "data": data})


def _fail(err: folders.FolderError) -> JSONResponse:
    return JSONResponse({"code": -1, "msg": str(err), "data": None}, status_code=err.status)


def _readable(key: str) -> bool:
    d = lane_guard.current()
    if d is None:
        return True
    owner, _, project = key.partition(":")
    return lane.may_operate(d, owner, project, write=False)


@router.get("/folders")
async def list_folders():
    data = {k: v for k, v in folders.snapshot().items() if _readable(k)}
    data.setdefault(lane_guard.own_group(), [])
    return _ok(data)


@router.post("/folders")
async def create_folder(payload: FolderName):
    try:
        return _ok({"name": folders.create(lane_guard.own_group(), payload.name)})
    except folders.FolderError as e:
        return _fail(e)


@router.put("/folders/{name}")
async def rename_folder(name: str, payload: FolderName):
    try:
        return _ok({"name": folders.rename(lane_guard.own_group(), name, payload.name)})
    except folders.FolderError as e:
        return _fail(e)


@router.delete("/folders/{name}")
async def delete_folder(name: str):
    try:
        folders.delete(lane_guard.own_group(), name)
        return _ok()
    except folders.FolderError as e:
        return _fail(e)
