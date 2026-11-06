"""Mac 机型常量 —— WebKit 族 (safari 档) 指纹的屏幕 / DPR / 核数取值来源。

数值逐项抄自 safarier Sources/SafarierCore/MacDeviceModel.swift 的 presets (31 个里挑 15 个,
覆盖 MacBook Pro 16 / MacBook Pro 14 / MacBook Air / iMac 24 / Mac Studio / Mac mini 六类)。

    screen_w / screen_h  显示器逻辑分辨率 (点) = 网页 screen.width/height
                         (笔记本 = 内建屏 = 视口; 台式机按常见搭配显示器, 见各工厂函数)
    scale                像素比 (Retina 为 2) = devicePixelRatio
    display_p3 / hdr     color-gamut: p3 / dynamic-range: high

纯数据 + 纯函数, 不探测、不读文件。
"""

from __future__ import annotations

import random
from dataclasses import dataclass

# WebKit 族 UA 的固定段. 平台段与 605.1.15 均为 WebKit 写死的冻结值:
#   WebKit Source/WebCore/platform/mac/UserAgentMac.mm:39
#   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko)"
UA_PLATFORM = "Macintosh; Intel Mac OS X 10_15_7"
WEBKIT_UA_BUILD = "605.1.15"
# Safari 当前正式版: 27.2, 2026-10-26 发布 (webkit.org/blog/18515 "WebKit Features for Safari 27.2";
# Apple Safari Release Notes 索引里 27.0 于 2026-09-14 发布). Safari 升级时改这里.
SAFARI_VERSION = "27.2"
# WebKit Source/WebCore/html/canvas/WebGLRenderingContextBase.cpp: UNMASKED_VENDOR/RENDERER_WEBGL 写死
WEBGL_VENDOR = "Apple Inc."
WEBGL_RENDERER = "Apple GPU"

CAT_MBP16 = 'MacBook Pro 16"'
CAT_MBP14 = 'MacBook Pro 14"'
CAT_AIR = "MacBook Air"
CAT_IMAC = 'iMac 24"'
CAT_STUDIO = "Mac Studio"
CAT_MINI = "Mac mini"
CATEGORIES = (CAT_MBP16, CAT_MBP14, CAT_AIR, CAT_IMAC, CAT_STUDIO, CAT_MINI)


@dataclass(frozen=True)
class MacModel:
    id: str
    name: str
    chip: str
    cpu_cores: int
    gpu_cores: int
    screen_w: int
    screen_h: int
    scale: int
    display_p3: bool
    hdr: bool
    category: str

    @property
    def resolution(self) -> str:
        return f"{self.screen_w}x{self.screen_h}"


# 工厂函数与 Swift 端 pro16 / pro14 / macBookAir / iMac / studio 一一对应
def _pro16(id_: str, chip: str, cpu: int, gpu: int) -> MacModel:
    return MacModel(id_, f'MacBook Pro 16" ({chip})', f"Apple {chip}", cpu, gpu, 1728, 1117, 2, True, True, CAT_MBP16)


def _pro14(id_: str, chip: str, cpu: int, gpu: int) -> MacModel:
    return MacModel(id_, f'MacBook Pro 14" ({chip})', f"Apple {chip}", cpu, gpu, 1512, 982, 2, True, True, CAT_MBP14)


def _air(id_: str, size: str, chip: str, cpu: int, gpu: int, w: int, h: int) -> MacModel:
    return MacModel(id_, f'MacBook Air {size}" ({chip})', f"Apple {chip}", cpu, gpu, w, h, 2, True, False, CAT_AIR)


def _imac(id_: str, chip: str, cpu: int, gpu: int) -> MacModel:
    # iMac 24" 4.5K Retina: 屏幕 2240x1260
    return MacModel(id_, f'iMac 24" ({chip})', f"Apple {chip}", cpu, gpu, 2240, 1260, 2, True, False, CAT_IMAC)


def _studio(id_: str, chip: str, cpu: int, gpu: int) -> MacModel:
    # 搭配 Studio Display 5K: 屏幕 2560x1440, P3, SDR
    return MacModel(id_, f"Mac Studio ({chip} · Studio Display)", f"Apple {chip}", cpu, gpu, 2560, 1440, 2, True, False, CAT_STUDIO)


MODELS: tuple[MacModel, ...] = (
    _pro16("macbook-pro-16-m4-max", "M4 Max", 16, 40),
    _pro16("macbook-pro-16-m3-max", "M3 Max", 16, 40),
    _pro16("macbook-pro-16-m1-pro", "M1 Pro", 10, 16),
    _pro14("macbook-pro-14-m4-pro", "M4 Pro", 12, 16),
    _pro14("macbook-pro-14-m4", "M4", 10, 10),
    _pro14("macbook-pro-14-m3-pro", "M3 Pro", 12, 18),
    _air("macbook-air-15-m3", "15", "M3", 8, 10, 1710, 1107),
    _air("macbook-air-13-m2", "13", "M2", 8, 10, 1470, 956),
    _air("macbook-air-13-m1", "13", "M1", 8, 7, 1440, 900),
    _imac("imac-24-m4", "M4", 10, 10),
    _imac("imac-24-m1", "M1", 8, 8),
    _studio("mac-studio-m4-max", "M4 Max", 16, 40),
    _studio("mac-studio-m2-ultra", "M2 Ultra", 24, 76),
    # Mac mini 搭配 4K 显示器 (1920x1080 @2x): Retina 款 P3, SDR 款 sRGB
    MacModel("mac-mini-m4-pro", "Mac mini (M4 Pro · 4K Retina)", "Apple M4 Pro", 12, 16, 1920, 1080, 2, True, False, CAT_MINI),
    MacModel("mac-mini-m2", "Mac mini (M2 · 4K SDR)", "Apple M2", 8, 10, 1920, 1080, 2, False, False, CAT_MINI),
)
_BY_ID = {m.id: m for m in MODELS}


def by_id(model_id: str) -> MacModel | None:
    """按 id 取机型; 未收录返回 None。"""
    return _BY_ID.get((model_id or "").strip().lower())


def pick(rng: random.Random | None = None) -> MacModel:
    """随机挑一个机型。传入 rng 即可复现 (测试用 random.Random(seed))。"""
    return (rng or random).choice(MODELS)


def web_cpu_cores(cores: int) -> int:
    """网页读到的 navigator.hardwareConcurrency: 真 WebKit 只报 4 或 8 (核数 <8 报 4, 否则 8)。
    出处: safarier MacDeviceModel.swift webVisibleCPUCores (引 WebKit NavigatorBase.cpp)。"""
    return 4 if cores < 8 else 8


def safari_user_agent() -> str:
    """WebKit 族 UA: 含 Version/ 与 Safari/605.1.15, 不含 Chrome/。"""
    return (
        f"Mozilla/5.0 ({UA_PLATFORM}) AppleWebKit/{WEBKIT_UA_BUILD} (KHTML, like Gecko) "
        f"Version/{SAFARI_VERSION} Safari/{WEBKIT_UA_BUILD}"
    )
