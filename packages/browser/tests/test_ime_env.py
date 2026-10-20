#!/usr/bin/env python3
"""窗口里的输入法环境 + 全局关窗开关的回归测试 (2026-10-17)。

## 为什么必须有

「窗口开出来了」与「窗口能用」是两件事, 而**只看前者永远不会红**: 进程活着、窗口
映射了、CDP 连得上, 只是用户在那个窗口里一个字都打不出来。

2026-10-17 实测 (隔离 Xvfb :99 + kwin_x11 + 私有 fcitx5, 打 "nihao" + 空格, 经 CDP
读回输入框的值):

    不带输入法变量 -> `"nihao "`   (fcitx5 侧 state=0: 窗口**从未**连上输入法)
    带上          -> `"你好"`      (fcitx5 侧 state=2, 当前输入法 pinyin)

根因不在窗口本身, 而在**环境**: 面板用 `nsenter + setpriv` 拉起 hx-browser 服务,
服务自己的 environ 只有 `HOME`/`PATH` (实测 `/proc/<pid>/environ`), 而浏览器进程
继承的是**服务**那份 —— 于是 `XMODIFIERS` 缺失, XIM 客户端根本不去连 fcitx5。

全局 `headless_default` 则是一条**死字段**的历史: 它只在 models.Settings 与
api.SettingsUpdate 里出现, 没有任何读取点, 用户打开它环境照样开可见窗口。

导入一律放模块级 (PY-04 门禁)。
"""

import asyncio
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "src"))

import hxbr_testenv  # noqa: E402
from hx_browser import api, browser, config, desktop, xenv  # noqa: E402
from hx_browser.models import Environment, Settings  # noqa: E402


def _env(headless: bool) -> Environment:
    """造一个只关心显示方案的环境对象 (不碰 store, 不碰磁盘)。"""
    env = Environment(name="t")
    env.id = "env_test_ime"
    env.fingerprint.headless = headless
    return env


class ImeSessionEnvTest(unittest.TestCase):
    """开在用户桌面上的窗口必须带上输入法环境 —— 否则用户在那个窗口里打不出中文。

    2026-10-17 实测 (隔离 Xvfb :99 + kwin_x11 + 私有 fcitx5, 打 "nihao" + 空格,
    经 CDP 读回输入框的值):
      不带这些变量 -> `"nihao "`, fcitx5 侧 state=0 (窗口**从未**连上输入法);
      带上        -> `"你好"`,   fcitx5 侧 state=2 且当前输入法 pinyin。
    根因: 面板用 `nsenter + setpriv` 拉起本服务, 服务自己的 environ 只有 HOME/PATH,
    而窗口继承的是**服务**那份 -> XIM 缺 XMODIFIERS 就根本不连服务器。
    """

    SESSION = {
        "XMODIFIERS": "@im=fcitx",
        "GTK_IM_MODULE": "fcitx",
        "DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus",
        "XDG_RUNTIME_DIR": "/run/user/1000",
        "PATH": "/usr/bin:/bin",  # 白名单外: 不许混进来
    }

    def test_session_env_is_a_whitelist(self):
        got = xenv.session_env(self.SESSION)
        self.assertEqual(got["XMODIFIERS"], "@im=fcitx")
        self.assertNotIn("PATH", got, "只挑白名单, 不整份转发桌面进程的环境")

    def test_desktop_branch_carries_ime_env(self):
        det = {
            "ok": True,
            "display": ":1",
            "xauth": "/tmp/xa",
            "source": "Xwayland#1",
            "environ": dict(self.SESSION),
        }
        with mock.patch.object(desktop, "detect_user_desktop", return_value=det):
            plan = desktop.resolve_display(_env(headless=False))
        self.assertEqual(plan.env.get("XMODIFIERS"), "@im=fcitx")
        self.assertEqual(plan.env.get("GTK_IM_MODULE"), "fcitx")
        self.assertEqual(
            plan.env.get("DBUS_SESSION_BUS_ADDRESS"), "unix:path=/run/user/1000/bus"
        )
        self.assertNotIn("PATH", plan.env)

    def test_desktop_probe_returns_the_whole_environ(self):
        """① 那条路径必须把桌面进程的 environ 带回来 —— 输入法变量只在这里取。"""
        env = dict(self.SESSION, DISPLAY=":1")
        with (
            mock.patch.object(
                desktop.shutil, "which", return_value="/usr/bin/xdpyinfo"
            ),
            mock.patch.object(desktop.xenv, "pids_of", return_value=[4242]),
            mock.patch.object(desktop.xenv, "proc_environ", return_value=env),
            mock.patch.object(desktop.xenv, "display_ok", return_value=True),
        ):
            det = desktop.detect_user_desktop()
        self.assertTrue(det["ok"])
        self.assertEqual(det["environ"]["XMODIFIERS"], "@im=fcitx")

    def test_ime_env_present_even_when_display_came_from_xauth_scan(self):
        """display 由 ③ 扫出来时没有 environ, 但「能不能打字」与 display 怎么探到的无关。"""
        det = {"ok": True, "display": ":1", "xauth": "/tmp/xa", "source": "xauth-scan"}
        with (
            mock.patch.object(desktop, "detect_user_desktop", return_value=det),
            mock.patch.object(
                desktop,
                "_desktop_session_environ",
                return_value={"XMODIFIERS": "@im=fcitx"},
            ),
        ):
            plan = desktop.resolve_display(_env(headless=False))
        self.assertEqual(plan.env.get("XMODIFIERS"), "@im=fcitx")

    def test_headless_branch_gets_no_session_env(self):
        """屏幕外的窗口没人打字: 不注入, 不放大注入面。"""
        self.assertEqual(desktop.resolve_display(_env(headless=True)).env, {})


class GlobalHeadlessSwitchTest(unittest.TestCase):
    """全局 `headless_default` = 一键关窗。2026-10-17 之前它是个**死字段**:
    只在 models.Settings 与 api.SettingsUpdate 里出现, 没有任何读取点 ——
    用户在设置里打开它, 环境照样在桌面上开窗口 (用户 2026-10-17 报的正是这个)。
    """

    def test_global_switch_forces_headless_plan(self):
        plan = desktop.resolve_display(_env(headless=False), headless_default=True)
        self.assertTrue(plan.headless)
        self.assertEqual(plan.source, "headless")
        self.assertEqual(plan.display, "", "headless 计划不该带 display")
        self.assertIn("headless_default", plan.reason, "原因要说明是哪个开关关的窗")

    def test_per_env_switch_still_reported_when_global_off(self):
        plan = desktop.resolve_display(_env(headless=True), headless_default=False)
        self.assertTrue(plan.headless)
        self.assertIn("fingerprint.headless", plan.reason)

    def test_desktop_plan_is_not_headless(self):
        det = {"ok": True, "display": ":1", "xauth": "/tmp/xa", "source": "Xwayland#1"}
        with mock.patch.object(desktop, "detect_user_desktop", return_value=det):
            plan = desktop.resolve_display(_env(headless=False), headless_default=False)
        self.assertFalse(plan.headless)
        self.assertTrue(plan.visible)

    def test_launch_args_follow_the_plan(self):
        """启动参数必须与 plan 同源: plan 说 headless 就**不能**钉 --ozone-platform=x11。

        两处各判一次就会出现「plan=headless, 参数却钉 x11」—— 那是一个连不上 X 的
        Chromium (服务环境里没有 DISPLAY, 进程起完即死, 见 desktop.py 模块头)。
        """
        mgr = browser.BrowserManager(
            Settings(data_dir="/tmp/hxbr-plan"), Path("/tmp/hxbr-plan")
        )
        env = _env(False)
        plans = {
            "headless": desktop.DisplayPlan(
                headless=True, source="headless", reason="桩"
            ),
            "desktop": desktop.DisplayPlan(
                display=":1", visible=True, source="desktop"
            ),
        }
        got = {}
        for name, plan in plans.items():
            with (
                mock.patch.object(
                    browser.kernels, "resolve_executable", return_value="/bin/true"
                ),
                mock.patch.object(
                    browser.launch_hooks, "chromium_args", return_value=[]
                ),
                mock.patch.object(
                    browser.launch_hooks, "write_chromium", return_value=[]
                ),
                mock.patch.object(
                    browser, "build_fingerprint_extension", return_value="/tmp/ext"
                ),
                mock.patch.object(
                    browser.BrowserManager, "_spawn", return_value=(1, "/bin/true")
                ) as sp,
            ):
                asyncio.run(
                    mgr._launch_chromium(env, 43111, Path("/tmp/hxbr-plan"), plan)
                )
            got[name] = sp.call_args[0][1]
        self.assertIn("--headless=new", got["headless"])
        self.assertNotIn("--ozone-platform=x11", got["headless"])
        self.assertIn("--ozone-platform=x11", got["desktop"])
        self.assertNotIn("--headless=new", got["desktop"])


class SettingsRefreshTest(unittest.TestCase):
    """`PUT /settings` 之后**下一次启动**就要用新值。

    改动前: `api.manager()` 把 `BrowserManager.settings` 缓存在**启动时**那份快照上,
    PUT 只写盘 -> 用户改完设置要重启服务才生效。表现与「死字段」一样 (设了没用),
    只是原因更深一层: 字段读到了, 读的却是旧值。
    """

    def test_manager_picks_up_saved_settings(self):
        with tempfile.TemporaryDirectory() as d:
            hxbr_testenv.isolate(Path(d))
            api.manager()  # 先建缓存 (模拟服务已启动)
            s = config.settings_manager.load()
            s.headless_default = True
            config.settings_manager.save(s)
            self.assertTrue(api.manager().settings.headless_default)


if __name__ == "__main__":
    unittest.main()
