# Agent Note: 宿主外部扩展混进副本 (Plasma Integration)

Status: implemented, 未在真实浏览器上复验

## Problem

用户贴出一段 agent 死循环记录: 连续 8 次 `cat .../env_1beb192dcb/Default/Extensions/cimiefiiaegbelhefg`, 每次都回 "不存在".

查证 (2026-11-15, 只读工作区):

- 死循环的直接原因: 路径被截断. 真实目录是 `cimiefiiaegbelhefglklhhakcgmhkai` (32 位扩展 id), 而且它是目录, 不是文件. 重复调用同一个错误路径永远不会有进展.
- 真正的问题是这个目录本身: 它是 **KDE Plasma Integration** 扩展 (manifest 作者 Kai Uwe Broulik). 内容脚本 `*://*/*`, all_frames, document_start 注入每个页面; 权限有 nativeMessaging / tabs / history / downloads.
- `Default/Preferences` 里 `extensions.settings.cimiefiiaegbelhefglklhhakcgmhkai`: location=6 (EXTERNAL_PREF_DOWNLOAD), from_webstore=true. 也就是说, 不是用户装的, 而是 Chromium 读到了宿主的外部扩展清单 (plasma-browser-integration 包放在 /usr/share/chromium/extensions/<id>.json 之类的位置), 然后经副本出口去 Web Store 下载安装.
- env_1beb192dcb 与 env_02a6e83ae3 都有这个扩展 (都是 ci-fingerprint-score, isolation=process). 另两个副本没有 Extensions 目录.

后果: 宿主桌面环境泄进每个副本. 页面可观测到注入脚本 (指纹评分被污染); 副本会去 clients2.google.com 拉扩展, 走副本出口; nativeMessaging 能连宿主的 plasma host. 违反通道隔离.

## Decision

`launch_hooks.chromium_args()` 恒加 `--disable-default-apps` (常量 NO_HOST_EXTENSIONS_FLAG).

依据 Chromium `chrome/browser/extensions/external_provider_impl.cc` (main 分支, 2026-11-15 读取) 的 `CreateExternalProviders`: 带 `kDisableDefaultApps` 时, 在创建 DIR_EXTERNAL_EXTENSIONS / DIR_STANDALONE_EXTERNAL_EXTENSIONS / DIR_USER_EXTERNAL_EXTENSIONS 三个外部清单 provider **之前**直接 return. 策略 provider 在它之前创建, 不受影响.

为什么不用别的办法:
- `--disable-extensions-except`: 会把副本里用户从商店装的扩展也全部停掉, 而且 Chrome 品牌 139 起已移除.
- 只在沙箱里用 tmpfs 盖住 /usr/share/chromium/extensions: 现有 4 个副本全是 isolation=process, 盖不到.
- 写 Preferences 的 `extensions.block_external_extensions`: 也能挡, 但它是策略键, 写进用户偏好是否生效没有实测; 命令行开关的代码路径是确定的.

选 launch_hooks 而不是 browser.py: browser.py 已 533 行且有未提交改动; chromium_args 是每次 Chromium 启动唯一必经的参数入口.

## Consequences

- 新副本不会再装宿主的外部扩展.
- 已装的: 没有 provider 再列出它, Chromium 启动时应按 "orphaned external extension" 卸载. 这一条按源码推断, 未实测. 若复验时仍在, 停副本后删掉该 profile 下 `Extensions/cimiefiiaegbelhefglklhhakcgmhkai` 即可.
- Linux 版 Chromium 本来就没有预装 default apps, 没有其它可见变化.
- Firefox 内核不走这里; Firefox 的宿主扩展通道 (/usr/lib/mozilla/extensions) 本次没查.

## 未做

- 没加体检项去报 "副本里存在 location 2/3/6 的扩展". 想要可以在 doctor 里读 Preferences 判定, 很便宜.
- hxoa 的 cat 对不存在路径只回 "不存在". 若想避免 agent 再这样循环, 服务端可以附上同目录下前缀匹配的候选名. 服务端不在本仓.

## Verification

- tests/test_host_extensions.py: chromium_args 恒含该开关.
- 复验步骤: 启动 env_1beb192dcb, 打开 chrome://extensions 看不到 Plasma Integration, profile 下对应目录消失.
