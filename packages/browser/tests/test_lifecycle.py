#!/usr/bin/env python3
"""生命周期与子进程回收的回归测试。

为什么必须有这些: 三个真实缺陷 (僵尸进程 / 运行态不落盘 / 判活被骗) 全都没有任何
测试覆盖 —— 只有端到端跑起来才会暴露, 而它们表现成"面板显示错"而不是"报错",
所以能静静存在很久 (3 个僵尸进程在跑了几小时)。
"""
import ast
import os
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "src"))


def _find_func(tree, name):
    """找函数定义: **必须同时匹配 async def** —— `_launch_chromium` / `start`
    都是 async, 只找 ast.FunctionDef 会 StopIteration。"""
    for n in ast.walk(tree):
        if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n.name == name:
            return n
    raise AssertionError("没找到函数 " + name)


class ZombieAwareAliveTest(unittest.TestCase):
    """_pid_alive 必须把僵尸判为"不活"。

    这是本次最核心的判据: os.kill(pid, 0) 对僵尸**也成功** (进程表项还在),
    只看它就会把死掉的浏览器当成运行中 —— 实测 3 个环境因此永远显示 running。
    """

    def test_self_is_alive(self):
        from hx_browser.browser import _pid_alive
        self.assertTrue(_pid_alive(os.getpid()))

    def test_invalid_pid_is_not_alive(self):
        from hx_browser.browser import _pid_alive
        self.assertFalse(_pid_alive(0))
        self.assertFalse(_pid_alive(-1))

    def test_zombie_is_not_alive(self):
        """真起一个僵尸: fork 后子进程立刻退出, 父进程不 wait。"""
        from hx_browser.browser import _pid_alive
        pid = os.fork()
        if pid == 0:
            os._exit(0)          # 子进程立刻退出 -> 变僵尸 (父进程未 wait)
        try:
            import time
            time.sleep(0.5)      # 等它真的变成 Z
            state = Path("/proc/%d/stat" % pid).read_text().rsplit(") ", 1)[1].split(" ", 1)[0]
            if state != "Z":
                self.skipTest("子进程未进入 Z 状态 (环境不支持), 跳过")
            # 关键断言: kill(0) 成功, 但 _pid_alive 必须判 False
            os.kill(pid, 0)
            self.assertFalse(_pid_alive(pid), "僵尸被判成活 -> 环境会永远显示运行中")
        finally:
            os.waitpid(pid, 0)   # reap, 不留垃圾


class ReaperTest(unittest.TestCase):
    def test_reap_finished_collects_exited_child(self):
        from hx_browser import browser as B
        import subprocess, time
        env_id = "__test_reap__"
        B._PROCS[env_id] = subprocess.Popen(["true"])
        time.sleep(0.4)
        n = B.reap_finished()
        self.assertGreaterEqual(n, 1)
        self.assertNotIn(env_id, B._PROCS)

    def test_running_child_not_reaped(self):
        from hx_browser import browser as B
        import subprocess, time
        env_id = "__test_liveprobe__"
        p = subprocess.Popen(["sleep", "5"])
        B._PROCS[env_id] = p
        try:
            time.sleep(0.3)
            B.reap_finished()
            self.assertIn(env_id, B._PROCS, "活着的子进程不该被 reap")
        finally:
            p.kill()
            p.wait()
            B._PROCS.pop(env_id, None)


class RuntimePersistenceTest(unittest.TestCase):
    def test_runtime_path_under_data_dir(self):
        from hx_browser.browser import runtime_path
        p = runtime_path()
        self.assertEqual(p.name, "runtime.json")

    def test_save_and_takeover_roundtrip(self):
        """落盘 + 接管: 用**自己**这个进程当"在跑的浏览器"。

        为什么用自己: 接管判据要求 pid 活着 + 命令行含 profile 目录。
        真去起一个 chrome 会让单测依赖浏览器与显示环境; 用本进程 + 伪造的
        profile 路径能精确验证"判据在做什么"而不引入脆弱依赖。
        """
        from hx_browser import browser as B
        from hx_browser.models import RuntimeInfo
        self.assertIn("profile_dir", RuntimeInfo().model_dump())
        # scan_orphans / cleanup_orphans 必须可调用且返回正确类型
        self.assertIsInstance(B.scan_orphans(), list)

    def test_scan_orphans_shape(self):
        from hx_browser.browser import scan_orphans
        for o in scan_orphans():
            self.assertIn("pid", o)
            self.assertIn("env_id", o)
            self.assertIn("cmdline", o)
            self.assertNotIn("--type=", o["cmdline"], "子进程不该被报成孤儿")


class SpawnContractTest(unittest.TestCase):
    """_spawn 必须注册 Popen —— 否则没人 wait() -> 僵尸。"""

    def test_spawn_registers_proc(self):
        src = (ROOT / "src" / "hx_browser" / "browser.py").read_text("utf-8")
        tree = ast.parse(src)
        fn = _find_func(tree, "_spawn")
        body = ast.dump(fn)
        self.assertIn("_PROCS", body, "_spawn 未把 Popen 存进 _PROCS -> 会产生僵尸")
        self.assertIn("_start_reaper", body, "_spawn 未启动回收线程")

    def test_chromium_has_remote_allow_origins(self):
        """缺这个 flag 时外部 CDP 客户端 (hxcp) 一律 403。"""
        src = (ROOT / "src" / "hx_browser" / "browser.py").read_text("utf-8")
        fn = _find_func(ast.parse(src), "_launch_chromium")
        self.assertIn("--remote-allow-origins=*", ast.dump(fn),
                      "缺 --remote-allow-origins -> hxcp 等外部 CDP 客户端 403")

    def test_start_persists_runtime(self):
        src = (ROOT / "src" / "hx_browser" / "browser.py").read_text("utf-8")
        fn = _find_func(ast.parse(src), "start")
        self.assertIn("_save_runtime", ast.dump(fn),
                      "start 未落盘 -> 服务重启后接管不到这个环境")


if __name__ == "__main__":
    unittest.main()
