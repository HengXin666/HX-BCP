# 指纹评分

流水线的评分只认**环境自检结果**, 不认主观判断。

## 输出

```json
{
  "verdict": "green",
  "checked": 7,
  "mismatched": [],
  "evidence": [{ "field": "timezone", "expected": "Asia/Singapore", "actual": "Asia/Singapore" }]
}
```

| verdict | 含义 | 流水线行为 |
|---|---|---|
| green | 全部字段与派生值一致 | 通过 |
| yellow | 存在非身份字段差异 (如屏幕、字重) | 通过但记录, 需人复核 |
| red | 存在身份字段不一致 (UA / 平台 / 时区 / 核心数 / 语言) | 失败, 阻断 |

## 检查项

| 字段 | 来源 | 判定 |
|---|---|---|
| userAgent | 环境派生 | 与 seed 派生值逐字一致 |
| platform | 环境派生 | 一致 |
| timezone | 环境派生 | 一致, 且与出口地理不冲突 |
| hardwareConcurrency | 环境派生 | 一致 |
| languages | 环境派生 | 顺序一致 |
| screen | 环境派生 | 一致 (yellow: 容差内) |
| webgl vendor/renderer | 环境派生 | 一致 (yellow: 同族) |

## 复算

```sh
cd packages/fingerprint-audit
uv run audit.py --launch headless --json
```

退出码: 0 = green, 1 = yellow, 2 = red。流水线直接用它当闸门, 不再另写判定。
