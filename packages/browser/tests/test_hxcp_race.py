"""同一环境的启停不能交叉，stdout 也不能接 PIPE。"""

import sys
import tempfile
import threading
import time
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "src"))

import hx_browser.hxcp_link as link  # noqa: E402 — 测试要把 src 放到 sys.path 之后才能导入


class _Proc:
    def __init__(self, pid):
        self.pid = pid
        self._dead = False
        self.stdout = None

    def poll(self):
        return 0 if self._dead else None

    def wait(self, timeout=None):
        self._dead = True
        return 0


class HxcpRaceTest(unittest.TestCase):
    def setUp(self):
        link._RUNNING.clear()
        self._save = mock.patch.object(link, "save_runtime", lambda: None)
        self._save.start()
        Path("/tmp/hxcp-test").mkdir(parents=True, exist_ok=True)
        self._prep = mock.patch.object(
            link, "prepare_cache_dir", return_value=Path("/tmp/hxcp-test")
        )
        self._prep.start()
        self.addCleanup(self._save.stop)
        self.addCleanup(self._prep.stop)
        self.addCleanup(link._RUNNING.clear)

    def test_concurrent_start_keeps_one_process(self):
        procs = []

        def _popen(cmd, **kwargs):
            proc = _Proc(70 + len(procs))
            procs.append(proc)
            return proc

        def _slow(proc, log_path=None, offset=0):
            time.sleep(0.08)

        results = []

        def go():
            results.append(link.start("env_cc", "http://127.0.0.1:9", 1))

        with (
            mock.patch.object(link.subprocess, "Popen", _popen),
            mock.patch.object(link, "_wait_ready", _slow),
        ):
            threads = [threading.Thread(target=go) for _ in range(2)]
            for thread in threads:
                thread.start()
            for thread in threads:
                thread.join(3)
        self.assertEqual(len(procs), 1)
        self.assertEqual(
            sorted(status for _pid, status in results), ["already-running", "running"]
        )

    def test_two_envs_are_not_one_lock(self):
        barrier = threading.Barrier(2)

        def _popen(cmd, **kwargs):
            barrier.wait(timeout=2)
            return _Proc(3)

        def go(env_id):
            link.start(env_id, "http://127.0.0.1:9", 1)

        with (
            mock.patch.object(link.subprocess, "Popen", _popen),
            mock.patch.object(link, "_wait_ready", lambda *a, **k: None),
        ):
            threads = [
                threading.Thread(target=go, args=(env_id,))
                for env_id in ("env_a", "env_b")
            ]
            for thread in threads:
                thread.start()
            for thread in threads:
                thread.join(2)
                self.assertFalse(thread.is_alive())

    def test_stop_during_start_reaps_process(self):
        proc = _Proc(81)
        started = threading.Event()
        seen = []

        def _slow(proc, log_path=None, offset=0):
            started.set()
            time.sleep(0.15)

        def _sig(pid, sig):
            seen.append(sig)
            if sig == link.signal.SIGINT:
                proc._dead = True

        def do_stop():
            self.assertTrue(started.wait(2))
            link.stop("env_sw")

        with (
            mock.patch.object(link.subprocess, "Popen", return_value=proc),
            mock.patch.object(link, "_wait_ready", _slow),
            mock.patch.object(link, "_signal_group", _sig),
            mock.patch.object(link, "_STOP_WAIT", 0.05),
        ):
            starter = threading.Thread(
                target=lambda: link.start("env_sw", "http://127.0.0.1:9", 1)
            )
            stopper = threading.Thread(target=do_stop)
            starter.start()
            stopper.start()
            starter.join(3)
            stopper.join(3)
        self.assertNotIn("env_sw", link._RUNNING)
        self.assertIn(link.signal.SIGINT, seen)

    def test_stdout_is_cache_log_not_a_pipe(self):
        captured = {}

        def _popen(cmd, **kwargs):
            captured.update(kwargs)
            return _Proc(5)

        with (
            mock.patch.object(link.subprocess, "Popen", _popen),
            mock.patch.object(link, "_wait_ready", lambda *a, **k: None),
        ):
            _pid, status = link.start("env_log", "http://127.0.0.1:9", 1)
        self.assertEqual(status, "running")
        self.assertTrue(str(captured["stdout"].name).endswith("hxcp.log"))
        self.assertEqual(captured["stderr"], link.subprocess.STDOUT)
        self.assertNotIn("text", captured)

    def test_ready_waits_for_new_log_line(self):
        class _Alive:
            def poll(self):
                return None

        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "hxcp.log"
            path.write_bytes("旧 已挂载\n".encode())
            offset = path.stat().st_size

            def later():
                time.sleep(0.05)
                with path.open("ab") as fh:
                    fh.write("[hxcp] 已挂载\n".encode())

            threading.Thread(target=later, daemon=True).start()
            with mock.patch.object(link, "_READY_WAIT", 2.0):
                link._wait_ready(_Alive(), path, offset)

    def test_old_log_line_is_not_ready(self):
        class _Alive:
            def poll(self):
                return None

        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "hxcp.log"
            path.write_bytes("旧 已挂载\n".encode())
            with mock.patch.object(link, "_READY_WAIT", 0.05):
                with self.assertRaises(RuntimeError):
                    link._wait_ready(_Alive(), path, path.stat().st_size)


if __name__ == "__main__":
    unittest.main()
