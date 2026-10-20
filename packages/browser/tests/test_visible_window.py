#!/usr/bin/env python3
"""可见窗口与多实例并发的回归测试 (2026-10-13)。

为什么必须有: 本次改动的**全部价值**在「窗口对用户可见吗」这一个断言上, 而它极易
退化成"看起来成功了"。三种退化都不会报错:

    ① 判据从「xdpyinfo 连通」退化成「socket 存在」-> 窗口开在没权限的 display 上;
    ② visible 直接取"探到桌面"的**意图值**, 不做 xdotool 真断言 -> 进程活着就报可见;
    ③ 在 Xvfb 上枚举到窗口就当 visible=true -> 用户看不见却报可见 (本任务明令禁止)。

这三条各有一个测试钉住。全部用**桩/假值**跑, 不依赖本机真有桌面 ——
依赖宿主环境的测试在 CI 上会静默跳过, 等于没有。

导入一律放模块级 (PY-04 门禁): 函数体内 import 会让"这个测试依赖什么"变得要读全文才知道。
"""
import sys
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "src"))

from hx_browser import desktop, ports, window, xenv  # noqa: E402
from hx_browser.envview import env_dict  # noqa: E402
from hx_browser.models import Environment, RuntimeInfo  # noqa: E402


def _env(headless: bool) -> Environment:
    """造一个只关心显示方案的环境对象 (不碰 store, 不碰磁盘)。"""
    env = Environment(name="t")
    env.id = "env_test_visible"
    env.fingerprint.headless = headless
    return env


class WindowPositionTest(unittest.TestCase):
    """窗口位置必须按 serial_no 错开, 否则多实例窗口完全重叠。"""

    def test_staggered_by_serial(self):
        self.assertEqual(window.window_position(1), (80, 60))
        self.assertEqual(window.window_position(2), (120, 60))
        self.assertEqual(window.window_position(3), (160, 60))

    def test_wraps_so_late_serials_stay_on_screen(self):
        """第 49 个环境不回绕就会开到 x=2000 (屏幕外) —— 那又变回"用户看不见"。"""
        x, _ = window.window_position(window.WINDOW_COLS + 1)
        self.assertEqual(x, window.WINDOW_ORIGIN_X)

    def test_zero_serial_does_not_crash(self):
        self.assertEqual(window.window_position(0), (80, 60))


class DisplayCriterionTest(unittest.TestCase):
    """判据必须是**连通性**, 不是 socket 存在 (gh-pool 2026-10-02 实测反例)。"""

    def test_unreachable_display_is_rejected(self):
        # :99999 连 socket 都没有 -> 必然不通。证明判据不是"有 socket 就算通"。
        self.assertFalse(xenv.display_ok(":99999", ""))

    def test_missing_xdpyinfo_is_not_treated_as_ok(self):
        """缺 xdpyinfo 时判**不通** —— 这是与 gh-pool 的有意分歧。

        gh-pool 退化成"有 socket 且 xauth 可读"的弱判据; 弱判据会让
        "窗口可见"变成猜测, 而本模块存在的全部理由就是不猜。
        """
        with mock.patch.object(xenv.shutil, "which", return_value=None):
            self.assertFalse(xenv.display_ok(":1", "/tmp/x"))


class ResolveDisplayTest(unittest.TestCase):
    """headless 与回落两条分支的**明说**义务。"""

    def test_headless_reports_invisible_with_reason(self):
        plan = desktop.resolve_display(_env(headless=True))
        self.assertFalse(plan.visible)
        self.assertEqual(plan.display, "")
        self.assertTrue(plan.reason, "visible=false 时 reason 必须非空, 否则用户无从判断")
        self.assertEqual(plan.source, "headless")

    def test_no_desktop_falls_back_to_xvfb_and_says_so(self):
        """强制探不到桌面 -> 回落 Xvfb, visible=false 且 reason 明说看不见。"""
        det = {"ok": False, "err": "桩: 探不到桌面", "tried": []}
        with mock.patch.object(desktop, "detect_user_desktop", return_value=det):
            with mock.patch.object(desktop.xvfb, "start", return_value=(":77", mock.Mock())):
                plan = desktop.resolve_display(_env(headless=False))
        self.assertFalse(plan.visible, "回落 Xvfb 时**绝不能**报 visible=true")
        self.assertEqual(plan.display, ":77")
        self.assertIn("看不见", plan.reason)
        self.assertIn("桩: 探不到桌面", plan.reason, "原因要透传, 否则排查时只剩一句'看不见'")
        self.assertIn("WAYLAND_DISPLAY", plan.unset, "不 pop 它 Chromium 会走 Wayland 后端")

    def test_xvfb_start_failure_is_reported(self):
        """Xvfb 也起不来时必须明说, 不能给一个空 display 就当成功。"""
        det = {"ok": False, "err": "桩: 无桌面", "tried": []}
        with mock.patch.object(desktop, "detect_user_desktop", return_value=det):
            with mock.patch.object(desktop.xvfb, "start", return_value=("", None)):
                plan = desktop.resolve_display(_env(headless=False))
        self.assertFalse(plan.visible)
        self.assertEqual(plan.source, "xvfb-failed")
        self.assertTrue(plan.reason)

    def test_desktop_found_asks_for_x11_backend(self):
        det = {"ok": True, "display": ":1", "xauth": "/tmp/xa", "source": "Xwayland#1"}
        with mock.patch.object(desktop, "detect_user_desktop", return_value=det):
            plan = desktop.resolve_display(_env(headless=False))
        self.assertTrue(plan.visible)
        self.assertEqual(plan.display, ":1")
        self.assertEqual(plan.env.get("XAUTHORITY"), "/tmp/xa")
        self.assertIn("WAYLAND_DISPLAY", plan.unset)

    def test_source_is_a_kind_not_a_detail(self):
        """source 必须是**类别**, 细节另存 —— 2026-10-13 真机实测踩到的假阴性。

        当时的 bug: source 被填成 "Xwayland#2760" (细节), 而断言判的是
        `source == "desktop"` -> 恒为 false, 于是在**真桌面**上也报 visible=false。
        窗口标题已经拿到了 (证明窗口真的映射了), 却因为判据比错了东西而报不可见。
        """
        det = {"ok": True, "display": ":1", "xauth": "/tmp/xa", "source": "Xwayland#2760"}
        with mock.patch.object(desktop, "detect_user_desktop", return_value=det):
            plan = desktop.resolve_display(_env(headless=False))
        self.assertEqual(plan.source, "desktop", "source 必须是类别, 断言比的就是它")
        self.assertEqual(plan.source_detail, "Xwayland#2760", "细节要留着, 排查时要看")


class WindowAssertionTest(unittest.TestCase):
    """visible 的**唯一真值来源**是 xdotool 断言 —— 不能拿意图冒充。"""

    def test_no_display_cannot_be_asserted(self):
        r = window.find_window(1234, "")
        self.assertFalse(r["ok"])
        self.assertIn("无法断言", r["err"])

    def test_no_pid_cannot_be_asserted(self):
        r = window.find_window(0, ":1")
        self.assertFalse(r["ok"])
        self.assertIn("pid", r["err"])

    def test_titled_window_is_visible(self):
        found = {"ok": True, "windows": [{"id": "1", "title": "T", "geom": ""}]}
        with mock.patch.object(window, "find_window", return_value=found):
            vis, title, why, wid = window.window_verdict(1, ":1", timeout=0)
        self.assertTrue(vis)
        self.assertEqual(title, "T")
        self.assertEqual(why, "")
        self.assertEqual(wid, "1", "window_id 要带出来, 上层据此截图/操作")

    def test_untitled_window_reports_reason_but_keeps_id(self):
        """窗口在、标题还没设上: 如实报不可见 + 说明, 但**保留 window_id**。

        2026-10-13 实测: Chromium 先建一个空标题窗口, 页面标题稍后才设上。
        抓早了会得到空串, 而 T4 面板靠标题显示"这个窗口是什么"。
        故: 标题等待期超时后要说清"标题未就绪", 不能静默给空串。
        """
        found = {"ok": False, "windows": [{"id": "1", "title": "", "geom": ""}], "err": ""}
        with mock.patch.object(window, "find_window", return_value=found):
            vis, title, why, wid = window.window_verdict(1, ":1", timeout=0, title_wait=0)
        self.assertFalse(vis)
        self.assertEqual(title, "")
        self.assertIn("标题未在", why, "必须说明是标题没就绪, 而不是笼统的'无窗口'")
        self.assertEqual(wid, "1", "窗口确实存在, id 不能丢")

    def test_timeout_reports_not_visible_with_reason(self):
        found = {"ok": False, "windows": [], "err": "桩: 无窗口"}
        with mock.patch.object(window, "find_window", return_value=found):
            vis, title, why, wid = window.window_verdict(1, ":1", timeout=0)
        self.assertFalse(vis)
        self.assertEqual(title, "")
        self.assertIn("桩: 无窗口", why)
        self.assertEqual(wid, "")


class PortAllocationTest(unittest.TestCase):
    """并发 start 时端口必须互不相同 —— 光靠探测有窗口期。"""

    def test_concurrent_allocations_differ(self):
        got = [ports.next_port(43900, 0) for _ in range(5)]
        try:
            self.assertEqual(len(set(got)), 5, "并发分配出现重复端口 -> 后起的 chrome 会启动失败")
        finally:
            for p in got:
                ports.release_port(p)

    def test_release_returns_port_to_pool(self):
        first = ports.next_port(43950, 0)
        ports.release_port(first)
        try:
            self.assertEqual(ports.next_port(43950, 0), first)
        finally:
            ports.clear_reserved()


class EnvViewContractTest(unittest.TestCase):
    """T2/T4 消费的字段名与语义 —— 契约测试, 改名必须让这里红。"""

    TOP_KEYS = ("visible", "display", "window_title", "visible_reason",
                "display_source", "display_source_detail", "window_id", "window_pos")

    def test_visible_fields_present_when_not_running(self):
        d = env_dict(Environment(name="t"))
        for key in self.TOP_KEYS:
            self.assertIn(key, d, "契约字段缺失: " + key)
        self.assertFalse(d["visible"])
        self.assertEqual(d["display"], "")
        self.assertTrue(d["visible_reason"], "未运行时也要说明为什么看不见")

    def test_display_source_exposed_at_top_level(self):
        """display_source 是"窗口开在真实桌面还是 Xvfb"的**唯一可判读信号**。

        2026-10-13 Lead 指出它只在 runtime 里、顶层缺失 —— 上层要提示用户
        "你看不见"就得解 runtime, 而环境未运行时 runtime 是 null。
        """
        d = env_dict(Environment(name="t"))
        self.assertIn("display_source", d)
        self.assertEqual(d["display_source"], "")

    def test_runtime_carries_display_contract(self):
        dump = RuntimeInfo().model_dump()
        for key in ("visible", "display", "xauth", "window_title", "window_id",
                    "window_pos", "display_source", "display_source_detail", "reason"):
            self.assertIn(key, dump, "RuntimeInfo 契约字段缺失: " + key)


class RuntimePersistenceTest(unittest.TestCase):
    """窗口断言的结果**必须落盘** —— 2026-10-13 实测缺口。

    真现场: 落盘发生在 start() 里, 那时 visible 还是 plan 的**意图值**(True)。
    断言把它改成真值后若不重写, runtime.json 会留一个假的 visible=True ——
    服务一重启接管, 假值就复活, 且没有任何断言会再纠正它。
    实测证据: 第二个环境在 runtime.json 里 window_title 为空、与 API 返回不一致。
    """

    def test_assert_safe_persists_verdict(self):
        import ast
        src = (ROOT / "src" / "hx_browser" / "browser.py").read_text("utf-8")
        tree = ast.parse(src)
        fn = next(
            n for n in ast.walk(tree)
            if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef))
            and n.name == "_assert_window_safe"
        )
        self.assertIn("_save_runtime", ast.dump(fn),
                      "断言结果没落盘 -> runtime.json 会留着 plan 的意图值 (假 visible)")


class WindowPlacementTest(unittest.TestCase):
    """多实例窗口必须**真的**错开 —— --window-position 在 KWin 下传了不生效。"""

    def test_move_window_reports_actual_position(self):
        """判据取**实测坐标**, 不是请求值 —— '传了参数' != '生效了'。

        实测 (2026-10-13, KDE/KWin): 三个环境请求不同坐标, 实际落点全是合成器
        的智能摆放结果。故 move_window 必须回读 getwindowgeometry 再比对。
        """
        with mock.patch.object(window, "_xdo", return_value=""):
            self.assertFalse(window.move_window("1", 100, 200, ":1"),
                             "读不回坐标时必须报失败, 不能乐观返回 True")

    def test_geometry_parses_negative_coords(self):
        geom = "Window id: 1\n  Position: -12,340 (screen: 0)"
        with mock.patch.object(window, "_xdo", return_value=geom):
            self.assertEqual(window.window_geometry("1", ":1"), (-12, 340))

    def test_geometry_returns_none_on_garbage(self):
        with mock.patch.object(window, "_xdo", return_value="no such window"):
            self.assertIsNone(window.window_geometry("1", ":1"))


if __name__ == "__main__":
    unittest.main()
