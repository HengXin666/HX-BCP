#!/usr/bin/env python3
"""取证档 (ForensicStore) 的行为契约。

为什么这些边界必须逐条钉住: 取证档的失败模式**全是静默的** ——
体没落盘不报错 (只写个 dropped)、轮转删多了不报错、超预算不报错。
"取证"这个词一旦名不副实, 回放出来的是残缺协议而没有任何信号, 所以每条闸都要有实证。
"""
import json
import os
import sys
import tempfile
import time
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "src"))

from hxcp import flows  # noqa: E402


class ForensicStoreTest(unittest.TestCase):
    def setUp(self):
        self.d = tempfile.mkdtemp(prefix="hxcp-fx-")

    def ev(self, **kw):
        return flows.ForensicStore(self.d, enabled=True, **kw)

    # ── 内容寻址与完整回读 ────────────────────────────────
    def test_roundtrip_is_byte_exact(self):
        fs = self.ev()
        data = bytes(range(256)) * 200          # 51200B, 含全部字节值
        info = fs.put(data)
        self.assertEqual(info["size"], len(data))
        self.assertEqual(len(info["sha256"]), 64)
        self.assertEqual(fs.get(info["sha256"]), data, "取回的必须与写入逐字节相同")

    def test_same_body_stored_once(self):
        fs = self.ev()
        a = fs.put(b"same-payload")
        b = fs.put(b"same-payload")
        self.assertEqual(a["sha256"], b["sha256"])
        self.assertEqual(fs.stats()["written"], 1, "重复体只落一份")
        self.assertEqual(fs.stats()["deduped"], 1)

    def test_empty_body_gets_no_file_and_no_reference(self):
        # 空体不该产生空文件, 也不该给出一个指向空文件的引用
        fs = self.ev()
        info = fs.put(b"")
        self.assertEqual(fs._files(), [])
        self.assertEqual(info["sha256"], "", "空体的引用必须为空串 (调用方据此判空)")

    # ── 三道闸 ────────────────────────────────────────────
    def test_oversize_is_dropped_not_written(self):
        fs = self.ev(max_part_bytes=1024)
        info = fs.put(b"x" * 1025)
        self.assertEqual(info["dropped"], "too_large")
        self.assertFalse(fs.has(info["sha256"]))
        self.assertEqual(fs.stats()["too_large"], 1)

    def test_pool_budget_rotates_oldest_first(self):
        old = flows.FORENSIC_GC_INTERVAL_S
        flows.FORENSIC_GC_INTERVAL_S = 0.0   # 测试里不要等节流窗口
        try:
            fs = self.ev(max_bytes=3000, min_free_bytes=0)
            infos = []
            for i in range(10):
                b = ("%02d" % i).encode() + b"z" * 998      # 每条 1000B
                infos.append(fs.put(b))
                time.sleep(0.01)                            # 拉开 mtime, 轮转才是确定的
            total = sum(sz for _, sz, _ in fs._files())
            st = fs.stats()
            self.assertLessEqual(total, 3000, "超预算必须真的删到预算内")
            self.assertGreater(st["evicted"], 0, "必须发生轮转")
            self.assertTrue(fs.has(infos[-1]["sha256"]), "最新的那条必须还在 (从旧到新删)")
            self.assertFalse(fs.has(infos[0]["sha256"]), "最旧的那条必须先被删")
        finally:
            flows.FORENSIC_GC_INTERVAL_S = old

    def test_disk_free_watermark_evicts_even_under_budget(self):
        """池预算没到但盘要满了 —— 第二道闸必须独立生效。
        判据: 把水位设成比当前空闲空间还大, 池就该被清空, 而不是只删到预算内。"""
        old = flows.FORENSIC_GC_INTERVAL_S
        flows.FORENSIC_GC_INTERVAL_S = 0.0
        try:
            free = flows._free_bytes(flows.Path(self.d))
            if free < 0:
                self.skipTest("该平台取不到磁盘余量")
            fs = self.ev(max_bytes=10 ** 12, min_free_bytes=free + 10 ** 9)
            for i in range(3):
                fs.put(("k%d" % i).encode() + b"y" * 500)
                time.sleep(0.01)
            self.assertEqual(len(fs._files()), 0, "低于余量水位必须继续删到池空")
        finally:
            flows.FORENSIC_GC_INTERVAL_S = old

    def test_budget_never_raises_to_caller(self):
        # 写入路径在 CDP 回调链路上: 池坏了也只能吞, 不能把被观测的请求拖死
        fs = self.ev()
        fs.root = flows.Path("/proc/self/cannot-write-here")   # 不可写
        info = fs.put(b"data")
        self.assertEqual(info["dropped"], "write_failed")
        self.assertTrue(fs.stats()["error"])

    # ── 引用是路径片段的唯一来源: 必须校验 ─────────────────
    def test_reference_is_validated_against_traversal(self):
        fs = self.ev()
        fs.put(b"real")
        for bad in ("../secret", "/etc/passwd", "..", "abc", "", "g" * 64,
                    "a" * 63, "A" * 64, "a" * 64 + "/../../x"):
            self.assertIsNone(fs.get(bad), "非法引用必须拒绝: " + repr(bad))

    def test_purge_by_mtime_keeps_fresh(self):
        fs = self.ev()
        old = fs.put(b"old-body")
        new = fs.put(b"new-body")
        p = fs.path_for(old["sha256"])
        os.utime(p, (time.time() - 10 * 86400,) * 2)
        n = fs.purge(keep_days=7)
        self.assertEqual(n, 1)
        self.assertFalse(fs.has(old["sha256"]))
        self.assertTrue(fs.has(new["sha256"]), "新鲜体不能被牵连 (目录名是 hash 前缀, 无日期语义)")

    def test_read_side_handle_resolves_the_same_directory(self):
        """读侧 (API/MCP) 的 open_forensic 必须落在与写侧**同一个目录**。

        这条是本模块最容易烂掉的地方: 引擎写进 forensic_path(cache_dir), API 用同一个函数读;
        任何一侧改成自己拼路径, 症状是"记录里有引用但永远取不到" —— 静默且难查。
        """
        wr = flows.ForensicStore(flows.forensic_path(self.d), enabled=True)
        h = wr.put(b"payload")
        rd = flows.open_forensic(self.d)
        self.assertTrue(rd.enabled)
        self.assertEqual(rd.root, wr.root)
        self.assertEqual(rd.get(h["sha256"]), b"payload")


class FlowLogForensicTest(unittest.TestCase):
    """FlowLog 与池的接线: 摘要留在 flows, 全量在池, 记录里留引用。"""

    def setUp(self):
        self.d = tempfile.mkdtemp(prefix="hxcp-fxlog-")

    def _rec(self, body):
        return {"kind": "flow", "verdict": "skip:no_store", "url": "https://api.test/v1/login",
                "status": 200, "method": "POST", "post_data": body, "at": time.time()}

    def test_forensic_record_keeps_summary_in_flows_and_full_body_in_pool(self):
        """核心契约: **摘要留在 flows, 全量在旁边的池里靠引用取回**。

        这两条必须同时成立 —— 只满足一条就是设计走偏:
          · 若 JSONL 里存了全量, 每条记录被撑大, flow_search 的逐行扫描跟着崩塌;
          · 若池里没有全量, 回放拿不到完整请求体, 取证档等于没做。
        """
        fs = flows.ForensicStore(self.d + "/p", enabled=True)
        fl = flows.FlowLog(self.d + "/f", enabled=True, forensic=fs)
        body = "a" * 50000                      # 远超 MAX_BODY=8192
        fl.write(self._rec(body))
        fl.close()

        recs = list(flows.iter_flows(self.d + "/f"))
        self.assertEqual(len(recs), 1)
        r = recs[0]
        self.assertTrue(r["post_data"].endswith("...[truncated]"), "摘要留在 flows")
        self.assertLess(len(r["post_data"]), 9000, "JSONL 记录不得被完整体撑大")
        ref = r["body_ref"]
        self.assertEqual(ref["size"], len(body))
        self.assertEqual(fs.get(ref["sha256"]).decode(), body, "全量在池里, 按引用逐字节取回")

    def test_without_forensic_behaviour_unchanged(self):
        fl = flows.FlowLog(self.d + "/f2", enabled=True)
        fl.write(self._rec("b" * 50000))
        fl.close()
        r = list(flows.iter_flows(self.d + "/f2"))[0]
        self.assertTrue(r["post_data"].endswith("...[truncated]"), "观测面行为不能变")
        self.assertNotIn("body_ref", r)

    def test_disabled_pool_says_so_in_the_record(self):
        # 开了开关但池没就绪: 记录里必须留痕, 否则看的人会以为拿到了完整体
        fs = flows.ForensicStore(self.d + "/p3", enabled=False)
        fl = flows.FlowLog(self.d + "/f3", enabled=True, forensic=fs)
        fl.write(self._rec("c" * 50000))
        fl.close()
        r = list(flows.iter_flows(self.d + "/f3"))[0]
        self.assertEqual(r["body_ref"]["dropped"], "store_disabled")
        self.assertTrue(r["post_data"].endswith("...[truncated]"),
                        "没有引用时仍要截断 —— 不能让 JSONL 无上限地长")

    def test_oversize_body_reference_marks_dropped_and_falls_back_to_trim(self):
        # 体太大进不了池 -> 引用里写明原因, 且 post_data 退回 8K 摘要 (不能无上限地进 JSONL)
        fs = flows.ForensicStore(self.d + "/p4", enabled=True, max_part_bytes=100)
        fl = flows.FlowLog(self.d + "/f4", enabled=True, forensic=fs)
        fl.write(self._rec("d" * 50000))
        fl.close()
        r = list(flows.iter_flows(self.d + "/f4"))[0]
        self.assertEqual(r["body_ref"]["dropped"], "too_large")
        self.assertTrue(r["post_data"].endswith("...[truncated]"), "没有引用就必须截断")

    def test_stats_surface_the_pool(self):
        fs = flows.ForensicStore(self.d + "/p5", enabled=True)
        fl = flows.FlowLog(self.d + "/f5", enabled=True, forensic=fs)
        fl.write(self._rec("e" * 100))
        fl.close()
        st = fl.stats()
        self.assertIn("forensic", st, "'完整体到底存了没有'必须能从 stats 看到")
        self.assertEqual(st["forensic"]["written"], 1)

    def test_bodyless_request_gets_no_reference(self):
        # GET 没有请求体 -> 不该有条目撑着一个空引用 (那会让"有引用"不再等价于"有完整体")
        fs = flows.ForensicStore(self.d + "/p6", enabled=True)
        fl = flows.FlowLog(self.d + "/f6", enabled=True, forensic=fs)
        rec = self._rec("")
        rec.pop("post_data")
        fl.write(rec)
        fl.close()
        self.assertNotIn("body_ref", list(flows.iter_flows(self.d + "/f6"))[0])


class TrimContractTest(unittest.TestCase):
    def test_trim_record_signature_unchanged(self):
        # 取证档不该改记录形态: 这个签名不变是"观测面行为不变"的可验证形式
        rec = flows.trim_record({"post_data": "z" * 50000})
        self.assertTrue(rec["post_data"].endswith("...[truncated]"))
        self.assertEqual(len(rec["post_data"]), 8192 + len("...[truncated]"))


if __name__ == "__main__":
    unittest.main()
