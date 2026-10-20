#!/usr/bin/env python3
"""flows.py 的单元测试 —— 记录器与查询层的行为契约。

为什么单测不能只测 happy path: 这个模块的失败模式全是**静默**的 ——
丢了记录不报错、截断没生效不报错、队列满了不报错。所以边界必须逐条钉住。
"""
import json
import os
import sys
import tempfile
import time
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "src"))

from hxcp import flows  # noqa: E402


class TrimTest(unittest.TestCase):
    def test_long_header_is_truncated_with_marker(self):
        rec = flows.trim_record({"url": "u", "req_headers": {"authorization": "x" * 9000}})
        v = rec["req_headers"]["authorization"]
        self.assertTrue(v.endswith("...[truncated]"))
        self.assertLess(len(v), 9000)

    def test_header_shapes_all_normalized(self):
        # CDP 给 dict, store 存 list[list] —— 两种都得吃得下
        a = flows.trim_record({"req_headers": {"a": "1"}})["req_headers"]
        b = flows.trim_record({"req_headers": [["a", "1"]]})["req_headers"]
        c = flows.trim_record({"req_headers": [{"name": "a", "value": "1"}]})["req_headers"]
        self.assertEqual(a, {"a": "1"})
        self.assertEqual(b, {"a": "1"})
        self.assertEqual(c, {"a": "1"})

    def test_post_data_truncated(self):
        rec = flows.trim_record({"post_data": "z" * 50000})
        self.assertLess(len(rec["post_data"]), 50000)

    def test_does_not_mutate_input(self):
        src = {"req_headers": {"a": "x" * 9000}}
        flows.trim_record(src)
        self.assertEqual(len(src["req_headers"]["a"]), 9000)


class FlowLogTest(unittest.TestCase):
    def setUp(self):
        self.d = tempfile.mkdtemp(prefix="hxcp-flows-")

    def test_disabled_writes_nothing(self):
        fl = flows.FlowLog(self.d, enabled=False)
        fl.write({"kind": "flow", "url": "u", "at": time.time()})
        fl.close()
        self.assertEqual(os.listdir(self.d), [])

    def test_write_then_read_roundtrip(self):
        fl = flows.FlowLog(self.d, enabled=True)
        fl.write({"kind": "flow", "verdict": "skip:sensitive_path", "url": "https://a.test/x",
                  "status": 200, "method": "POST", "req_headers": {"cookie": "s=1"},
                  "resp_headers": {"set-cookie": "s=1"}, "at": time.time()})
        fl.close()
        recs = list(flows.iter_flows(self.d))
        self.assertEqual(len(recs), 1)
        self.assertEqual(recs[0]["verdict"], "skip:sensitive_path")
        self.assertEqual(recs[0]["req_headers"]["cookie"], "s=1")
        self.assertEqual(recs[0]["resp_headers"]["set-cookie"], "s=1")

    def test_queue_full_drops_and_counts(self):
        # 观测面永远不能反压被观测的链路: 满队列必须丢, 且必须计数
        fl = flows.FlowLog(self.d, enabled=True, queue_max=2)
        for i in range(200):
            fl.write({"kind": "flow", "url": "https://a.test/%d" % i, "at": time.time()})
        time.sleep(0.3)
        self.assertGreater(fl.dropped, 0)
        fl.close()

    def test_corrupt_line_does_not_break_query(self):
        fl = flows.FlowLog(self.d, enabled=True)
        fl.write({"kind": "flow", "url": "https://a.test/ok", "at": time.time()})
        fl.close()
        p = [os.path.join(self.d, f) for f in os.listdir(self.d)][0]
        with open(p, "a", encoding="utf-8") as fh:
            fh.write(chr(123) + chr(34) + "broken" + chr(34) + ": ")
        self.assertEqual(len(list(flows.iter_flows(self.d))), 1)
        self.assertEqual(flows.flow_stats(self.d)["unparsed"], 1)

    def test_filters(self):
        fl = flows.FlowLog(self.d, enabled=True)
        now = time.time()
        fl.write({"kind": "flow", "verdict": "skip:set_cookie", "url": "https://api.a.test/v1/login",
                  "status": 200, "at": now})
        fl.write({"kind": "hit", "url": "https://cdn.b.test/a.js", "status": 200, "at": now})
        fl.close()
        self.assertEqual(len(list(flows.iter_flows(self.d, host="api.a.test"))), 1)
        self.assertEqual(len(list(flows.iter_flows(self.d, host="a.test"))), 1,
                         "后缀匹配: a.test 应命中 api.a.test")
        self.assertEqual(len(list(flows.iter_flows(self.d, kind="hit"))), 1)
        self.assertEqual(len(list(flows.iter_flows(self.d, q="set_cookie"))), 1)
        self.assertEqual(len(list(flows.iter_flows(self.d, status=404))), 0)
        self.assertEqual(len(list(flows.iter_flows(self.d, limit=1))), 1)

    def test_stats_counts_only_real_verdicts(self):
        fl = flows.FlowLog(self.d, enabled=True)
        fl.write({"kind": "hit", "url": "https://a.test/x", "at": time.time()})
        fl.close()
        st = flows.flow_stats(self.d)
        self.assertEqual(st["records"], 1)
        self.assertEqual(st["unparsed"], 0)
        self.assertEqual(st["top_verdicts"], [], "无 verdict 的记录不该被计成噪音条目")

    def test_purge_keeps_today(self):
        fl = flows.FlowLog(self.d, enabled=True)
        fl.write({"kind": "flow", "url": "https://a.test/x", "at": time.time()})
        fl.close()
        self.assertEqual(flows.purge(self.d, keep_days=0), 0)
        self.assertEqual(flows.purge(self.d, keep_days=-1), 1)

    def test_newest_first_by_default(self):
        fl = flows.FlowLog(self.d, enabled=True)
        for i in range(3):
            fl.write({"kind": "flow", "url": "https://a.test/%d" % i, "at": time.time()})
        fl.close()
        urls = [r["url"] for r in flows.iter_flows(self.d)]
        self.assertEqual(urls, list(reversed(sorted(urls))),
                         "默认应按文件内顺序倒序 (最新在前)")


if __name__ == "__main__":
    unittest.main()
