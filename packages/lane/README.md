# lane

人机双通道准入判定, 判据唯一实现。

令牌只落本机文件 (权限 0600), 命令不回显令牌本身:

    uv run bcp-lane issue   # 生成新令牌, 旧令牌随即失效
    uv run bcp-lane check   # 查看本机是否已有令牌, 没有时退出码为 1

