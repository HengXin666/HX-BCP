"""hxcp 挂载：缓存隔离、失败不伪装成功、浏览器换代要重挂、僵尸不算活。"""

import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "src"))

import hx_browser.hxcp_link as link  # noqa: E402 — 测试要把 src 放到 sys.path 之后才能导入


class _Proc:
    def __init__(self, pid=4242, dead=False, lines=None):
        self.pid = pid
        self.returncode = 0 if dead else None
        self._dead = dead
        self.wait_calls = 0
        self.stdout = ["[hxcp] 已挂载\n"] if lines is None else lines

    def poll(self):
        return self.returncode if self._dead else None

    def wait(self, timeout=None):
        self.wait_calls += 1
        self._dead = True
        self.returncode = -9
        return self.returncode


class HxcpLinkTest(unittest.TestCase):
    def setUp(self):
        self.link = link
        link._RUNNING.clear()
        self._sleep = mock.patch.object(link.time, "sleep", lambda *_a, **_k: None)
        self._sleep.start()
        self._save = mock.patch.object(link, "save_runtime", lambda: None)
        self._save.start()
        self._wait = mock.patch.object(link, "_STOP_WAIT", 0.05)
        self._wait.start()
        self._prep = mock.patch.object(
            link, "prepare_cache_dir", return_value=Path("/tmp/hxcp-test")
        )
        self._prep.start()
        Path("/tmp/hxcp-test").mkdir(parents=True, exist_ok=True)

        def _instant(proc, log_path=None, offset=0):
            code = proc.poll()
            if code is not None:
                raise RuntimeError("hxcp 退出码 %s" % code)

        self._ready = mock.patch.object(link, "_wait_ready", _instant)
        self._ready.start()

    def tearDown(self):
        link._RUNNING.clear()
        self._sleep.stop()
        self._save.stop()
        self._wait.stop()
        self._prep.stop()
        self._ready.stop()

    def test_cache_dir_is_before_run_and_per_env(self):
        link = self.link
        with mock.patch.object(link.shutil, "which", return_value="/usr/bin/hxcp"):
            a = link._hxcp_cmd("http://127.0.0.1:1", Path("/data/hxcp/env_a"))
            b = link._hxcp_cmd("http://127.0.0.1:1", Path("/data/hxcp/env_b"))
        self.assertLess(a.index("--cache-dir"), a.index("run"))
        self.assertLess(b.index("--cache-dir"), b.index("run"))
        self.assertNotEqual(
            a[a.index("--cache-dir") + 1], b[b.index("--cache-dir") + 1]
        )

    def test_uv_fallback_also_puts_cache_dir_before_run(self):
        link = self.link
        with mock.patch.object(link.shutil, "which", return_value=None):
            cmd = link._hxcp_cmd("http://127.0.0.1:9", Path("/data/hxcp/env_a"))
        self.assertEqual(cmd[0], "uv")
        runs = [i for i, part in enumerate(cmd) if part == "run"]
        self.assertLess(cmd.index("--cache-dir"), runs[-1])

    def test_missing_binary_is_failed_not_raise(self):
        link = self.link

        def _no_bin(endpoint, cache_dir):
            raise RuntimeError("找不到 hxcp")

        with mock.patch.object(link, "_hxcp_cmd", _no_bin):
            pid, status = link.start(
                "env_missing", "http://127.0.0.1:9", browser_pid=11
            )
        self.assertEqual(pid, 0)
        self.assertTrue(status.startswith("failed:"))

    def test_new_browser_pid_relaunches(self):
        link = self.link
        old = _Proc(pid=7)
        link._RUNNING["env_r"] = link._Mount(
            7, "http://127.0.0.1:9", old, browser_pid=100
        )
        launched = {}

        def _popen(cmd, **kwargs):
            launched["cmd"] = cmd
            return _Proc(pid=8)

        with (
            mock.patch.object(link, "_signal_group", lambda pid, sig: None),
            mock.patch.object(link.subprocess, "Popen", _popen),
            mock.patch.object(link, "pid_alive", return_value=False),
        ):
            pid, status = link.start("env_r", "http://127.0.0.1:9", browser_pid=200)
        self.assertEqual(status, "running")
        self.assertEqual(pid, 8)
        self.assertIn("--cache-dir", launched["cmd"])
        runs = [i for i, part in enumerate(launched["cmd"]) if part == "run"]
        self.assertLess(launched["cmd"].index("--cache-dir"), runs[-1])
        self.assertTrue(
            launched["cmd"][launched["cmd"].index("--cache-dir") + 1].endswith(
                "hxcp-test"
            )
        )

    def test_same_browser_is_already_running(self):
        link = self.link
        link._RUNNING["env_s"] = link._Mount(
            7, "http://127.0.0.1:9", _Proc(pid=7), browser_pid=100
        )
        with mock.patch.object(
            link.subprocess, "Popen", side_effect=AssertionError("should not relaunch")
        ):
            pid, status = link.start("env_s", "http://127.0.0.1:9", browser_pid=100)
        self.assertEqual((pid, status), (7, "already-running"))

    def test_stop_sends_sigint_before_kill(self):
        link = self.link
        proc = _Proc(pid=7)
        link._RUNNING["env_k"] = link._Mount(
            7, "http://127.0.0.1:9", proc, browser_pid=1
        )
        seen = []

        def _sig(pid, sig):
            seen.append(sig)
            if sig == link.signal.SIGINT:
                proc._dead = True

        with mock.patch.object(link, "_signal_group", _sig):
            status = link.stop("env_k")
        self.assertEqual(status, "stopped")
        self.assertEqual(seen, [link.signal.SIGINT])
        self.assertNotIn(link.signal.SIGKILL, seen)

    def test_status_dead_clears_runtime(self):
        link = self.link
        rt = mock.Mock(pid=1, hxcp_pid=7, hxcp_status="running", cdp_http="")
        link.RUNTIME["env_d"] = rt
        self.addCleanup(link.RUNTIME.pop, "env_d", None)
        link._RUNNING["env_d"] = link._Mount(
            7, "http://127.0.0.1:9", _Proc(pid=7, dead=True), 1
        )
        got = link.status("env_d")
        self.assertEqual(got["status"], "dead")
        self.assertEqual(rt.hxcp_status, "dead")
        self.assertEqual(rt.hxcp_pid, 0)

    def test_zombie_pid_is_not_running(self):
        link = self.link
        link._RUNNING["env_z"] = link._Mount(99, "http://127.0.0.1:9", None, 1)
        with mock.patch.object(link, "pid_alive", return_value=False):
            got = link.status("env_z")
        self.assertEqual(got["status"], "dead")

    def test_not_mounted_is_failed(self):
        link = self.link

        def _popen(cmd, **kwargs):
            return _Proc(pid=3, dead=True, lines=[])

        with mock.patch.object(link.subprocess, "Popen", _popen):
            pid, status = link.start("env_w", "http://127.0.0.1:9", browser_pid=1)
        self.assertEqual(pid, 0)
        self.assertTrue(status.startswith("failed:"))

    def test_symlink_cache_is_rejected_and_unlinked(self):
        link = self.link
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            real = root / "hxcp" / "env_a"
            real.mkdir(parents=True)
            alias = root / "hxcp" / "env_b"
            alias.symlink_to(real, target_is_directory=True)
            settings = mock.Mock(data_dir=str(root))
            with mock.patch.object(
                link.settings_manager, "load", return_value=settings
            ):
                with self.assertRaises(ValueError):
                    link.cache_dir_for("env_b")
                link.drop_cache("env_b")
            self.assertFalse(alias.exists())
            self.assertTrue(real.is_dir())

    def test_reused_pid_is_not_adopted(self):
        link = self.link
        rt = mock.Mock(pid=1, hxcp_pid=42, hxcp_status="running", cdp_http="http://x")
        link.RUNTIME["env_f"] = rt
        self.addCleanup(link.RUNTIME.pop, "env_f", None)
        with mock.patch.object(link, "_is_ours", return_value=False):
            self.assertEqual(link.adopt_from_runtime(), 0)
        self.assertEqual(rt.hxcp_pid, 0)
        self.assertEqual(rt.hxcp_status, "dead")
        self.assertNotIn("env_f", link._RUNNING)

    def test_stop_does_not_signal_reused_pid(self):
        link = self.link
        rt = mock.Mock(pid=1, hxcp_pid=42, hxcp_status="running", cdp_http="http://x")
        link.RUNTIME["env_foreign"] = rt
        self.addCleanup(link.RUNTIME.pop, "env_foreign", None)
        seen = []
        with (
            mock.patch.object(link, "_is_ours", return_value=False),
            mock.patch.object(link, "_signal_group", lambda *a: seen.append(a)),
        ):
            status = link.stop("env_foreign")
        self.assertEqual(status, "already-gone")
        self.assertEqual(seen, [])
        self.assertEqual(rt.hxcp_pid, 0)
        self.assertEqual(rt.hxcp_status, "already-gone")

    def test_adopted_same_browser_does_not_relaunch(self):
        link = self.link
        rt = mock.Mock(
            pid=100, hxcp_pid=42, hxcp_status="running", cdp_http="http://127.0.0.1:9"
        )
        link.RUNTIME["env_ad"] = rt
        self.addCleanup(link.RUNTIME.pop, "env_ad", None)
        with (
            mock.patch.object(link, "_is_ours", return_value=True),
            mock.patch.object(link, "pid_alive", return_value=True),
            mock.patch.object(
                link.subprocess, "Popen", side_effect=AssertionError("relaunch")
            ),
        ):
            self.assertEqual(link.adopt_from_runtime(), 1)
            pid, status = link.start("env_ad", "http://127.0.0.1:9", browser_pid=100)
        self.assertEqual((pid, status), (42, "already-running"))
        self.assertEqual(link._RUNNING["env_ad"].browser_pid, 100)


if __name__ == "__main__":
    unittest.main()
