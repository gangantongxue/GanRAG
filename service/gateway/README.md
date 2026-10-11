# Gateway 服务

API 网关入口：请求路由分发、access token 本地验签、双维度令牌桶限流、请求/响应日志（HTTP 框架 Hertz）。
详细说明见 [docs/architecture.md](docs/architecture.md)。

> 当前状态：第一期已实现（Hertz 骨架 + 验签 + 限流 + user 服务 5 个 RPC 的 HTTP 转发）。
> 配置必填项：`jwt.secret`（与 user 服务共享，建议 `GANRAG_JWT_SECRET` 环境变量注入）。

## Task 命令

本服务的构建与运行由 Task 管理，脚本位于本服务 `scripts/` 目录（各服务独立维护，互不引用）。
以下命令即可直接使用：

```bash
task build                          # 编译到 bin/server-linux-amd64（默认 linux/amd64）
task build OS=windows ARCH=amd64    # 编译指定平台
task build-image                    # 构建镜像 ganrag/gateway:latest（自动先编译）
task build-image COPY_CONFIG=false  # 构建不含配置的镜像（运行时挂载）
task run                            # 前台启动容器
task run -- --detach                # 后台启动容器
task run -- --detach --mount-config # 用宿主机 configs/config.yaml 覆盖镜像内配置
task stop                           # 停止并删除容器
```

在仓库根目录也可以统一操作：

```bash
task build -- gateway
task build-image -- gateway
task run -- gateway --detach
task stop
```
