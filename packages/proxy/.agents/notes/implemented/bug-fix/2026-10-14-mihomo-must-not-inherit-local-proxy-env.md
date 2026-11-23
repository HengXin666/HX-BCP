# Agent Note: 受管 Mihomo 不继承本机的代理环境变量

Status: implemented

## Problem

用户原话:

> 「本地的 Mihomo 服务不应该再被本地的其他代理影响出口」

受管数据面进程用 `command.Env = m.commandEnvironment()` 启动, 而那个函数只做了一件事:
替换 `GOMAXPROCS`, 其余 `os.Environ()` 原样透传。Mihomo 是 Go 程序, 它拉取
proxy-providers / rule-sets / geo 文件用的 HTTP transport **读 `HTTP_PROXY` / `HTTPS_PROXY` /
`ALL_PROXY`**(Go 的 `http.ProxyFromEnvironment`)。于是父进程环境里任何一个本机代理
(桌面客户端的「系统代理」、`run.sh` 里 export 的一行、systemd 的 `EnvironmentFile`、运维
shell 的 `export all_proxy=...`)都会静默改写数据面自己的出站路径 —— 而数据面恰恰是最不该
被「某个调用方的环境」决定出口的组件。

同一台机器上还实测确认了第二层干涉: 本机开着 Clash TUN 时, `ip rule` 会把**所有本机发起的
TCP** 送进 TUN 表(实测 `SO_BINDTODEVICE wlo1` 与绑定源地址 `192.168.5.188` 都超时, 而
未绑定连接 0ms 成功)。那一层**不是应用能在无 root 情况下绕过的**: 只能由宿主侧
`ip rule add uidrange <hx-pxy-uid> lookup main priority 8999` 之类的规则排除。本次不假装
应用层能解决它。

## Decision

`commandEnvironment` 在启动 Mihomo 时**剥掉** `http_proxy / https_proxy / all_proxy /
ftp_proxy / no_proxy`(大小写不敏感)。选择「unset」而不是「强制 no_proxy=*」: 未设置的变量
不会被继承来的 `NO_PROXY` 再打开, 子进程的环境与父 shell 导出过什么完全无关。

被 TUN 捕获的那一层不在本次代码范围, 在 Consequences 里给出可执行的宿主侧处置与它的验证方法,
不把它写成「已修」。

## Alternatives considered

- **什么都不做, 让 Mihomo 跟着环境走。** 最强理由: 有些部署**故意**让控制面经本机代理出去拉
  订阅(被墙的源), 透传环境正好满足那类部署, 而且零风险。否决: 这条路径同时把「数据面出口」
  交给了一个数据面看不见的变量; 需要代理拉订阅的部署可以显式配在订阅源上, 而不是靠传环境。
- **强制 `no_proxy=*` 并保留其它变量。** 最强理由: 一行, 且语义直白。否决: 它只堵 Go 的一个
  读取点, 环境里仍然躺着 `ALL_PROXY`; 别的子进程、别的语言、以后新增的 HTTP 客户端还得再堵
  一次。unset 是唯一不依赖「谁读哪个变量」的形态。
- **连控制面自己的 HTTP 客户端也一起不吃代理。** 最强理由: 判据一致, 控制面也不该被环境改写。
  否决(本轮): 控制面的订阅抓取是**用户可配置行为**的一部分(是否经代理拉源是运维的决定),
  而数据面的出口不是。两者的正确默认不同, 不该用同一条规则覆盖。
- **在应用层用 `SO_BINDTODEVICE` 强行绕开本机 TUN。** 最强理由: 看起来是「真正的修复」,
  而且 egress interface 机制已经在了。否决: 本机实测证明它**不可行**——绑定物理网卡后连
  `1.1.1.1:443` 超时(未绑定 0ms 成功), 绑定源地址同样超时; 硬绑会让数据面彻底不可用,
  比被别的代理转发更糟。这也是 wireless 分支当初必须退让的同一个物理原因。

## Consequences

- 环境里 export 过代理时, 受管 Mihomo 的 provider/rule-set/geo 抓取不再经过它;
  数据面的出站不再由父进程环境决定。
- `mihomo -t` 校验子进程也走同一个 `commandEnvironment`, 所以校验与运行环境一致。
- **仍然存在的干涉**: 本机 Clash TUN 的 `ip rule` 会把数据面出站送进 TUN。无 root 的应用
  绕不过。宿主侧处置(需要 root, 本次未执行):

  ```text
  ip rule add uidrange <hx-pxy 服务用户 uid> lookup main priority 8999
  ```

  验证方式: 在 `hx-pxy` 用户下 `curl -sS -o /dev/null -w '%{http_code}' https://1.1.1.1/`
  (加规则后应仍能直连, 且 `ss -tnp` 里该进程的连接不出现在 TUN 设备上)。
- 没有新增依赖、没有新增 goroutine, 只是启动时少传几个环境变量。

## Testing

internal/dataplane/mihomo/manager_test.go 的
`TestCommandEnvironmentStripsLocalProxyVariables`: 同时注入大小写两套
`http_proxy/https_proxy/all_proxy/no_proxy/ftp_proxy`, 断言子环境里一个都不剩,
而不相关变量与 `GOMAXPROCS` 仍然保留。
