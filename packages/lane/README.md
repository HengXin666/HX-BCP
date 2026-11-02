# lane

人机双通道准入判定, 判据唯一实现。

本机回环连接 (socket 对端是 127.0.0.1 / ::1, 且 Host 是本机名) 直接算人类通道, 不需要令牌; 非本机来源仍要令牌或由令牌换来的会话。理由与前置反代的限制见 .agents/notes/implemented/feature/2026-11-01-loopback-human.md。

令牌只落本机文件 (权限 0600), 命令不回显令牌本身:

    uv run bcp-lane issue   # 生成新令牌, 旧令牌随即失效
    uv run bcp-lane check   # 查看本机是否已有令牌, 没有时退出码为 1

