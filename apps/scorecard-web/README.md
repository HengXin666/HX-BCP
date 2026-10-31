# scorecard-web

指纹评分卡的静态页面, 每个 PR 的每次测评一个深路径 `/ci/case/<pr>/<no>/`.

## 构建

```bash
pnpm install
SCORECARD_PR=18 SCORECARD_NO=1 pnpm build
```

两个变量缺一个或不是从 1 起的整数, 构建直接失败, 不会产出前缀错误的白屏页面.

## 数据

页面运行时读评分卡分支上的 JSON, 默认在 `/ci/case/<pr>/<no>/` 往上四级的 `data/`, 可用 `VITE_DATA_ROOT` 覆盖.

| 文件 | 内容 |
|---|---|
| `data/pr-<pr>/index.json` | `{ pr, runs: [{ no, recordedAt, commit }] }`, runs 从新到旧 |
| `data/pr-<pr>/<no>.json` | 每内核 kernel, label, ua, capturedAt, score, verdict, reason, 以及每站 id, name, verdict, score, note |
| `data/pr-<pr>/<no>/*.svg` | 总览图 `all-kernels.svg` 与每内核图 `kernel-<kernel>.svg` |

生成:

```bash
pnpm record --bundle kernels.json --pr 18 --data <评分卡分支>/data --svg scorecards --commit <sha> --raw-base https://raw.githubusercontent.com/<owner>/<repo>/fingerprint-scorecard
```

`--no` 省略时取 index 里最大的 no 加 1. 不带 excerpt, 三内核一次约 5 KB.

## 本地自检

- `pnpm typecheck`
- `pnpm check:diff` 用 `fixtures/data/pr-18/` 的两次测评断言变好, 变差, 没变. fixture 是构造的数据, 不是实测.
