# File Store 服务

对象存储服务：使用本地文件夹模拟对象存储，对外暴露 gRPC 接口（文件上传/下载/删除、元数据与目录管理）。
详细说明见 [docs/architecture.md](docs/architecture.md)。

> 当前状态：目录骨架，尚未实现。实现时在 `cmd/server` 下编写 main 包，并在 `Taskfile.yml` 的 `PORT` / `CONTAINER_PORT` 中填写端口。

## Task 命令

本服务的构建与运行由 Task 管理，脚本位于本服务 `scripts/` 目录（各服务独立维护，互不引用）。
实现完成后以下命令即可直接使用：

```bash
task build                          # 编译到 bin/server-linux-amd64（默认 linux/amd64）
task build OS=windows ARCH=amd64    # 编译指定平台
task build-image                    # 构建镜像 ganrag/file-store:latest（自动先编译）
task build-image COPY_CONFIG=false  # 构建不含配置的镜像（运行时挂载）
task run                            # 前台启动容器
task run -- --detach                # 后台启动容器
task run -- --detach --mount-config # 用宿主机 configs/config.yaml 覆盖镜像内配置
task stop                           # 停止并删除容器
```

在仓库根目录也可以统一操作：

```bash
task build -- file-store
task build-image -- file-store
task run -- file-store --detach
task stop
```

服务未实现时，build / build-image / run 会提示并跳过，不报错。
