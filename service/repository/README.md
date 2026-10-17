# Repository 服务

知识库管理服务，同时是**对象存储（Garage，S3 协议）的唯一直接访问方**：文章正文、附件对象的写入/删除、文件资源权限校验与 Presigned URL 签发都在本服务。

架构设计见 [docs/architecture.md](docs/architecture.md)（对象存储与文件访问、Markdown 相对路径解析、权限边界）。

> 当前状态：目录骨架，尚未实现。实现时在 `cmd/server` 下编写 main 包，并在 `Taskfile.yml` 的 `PORT` / `CONTAINER_PORT` 中填写端口。

## Task 命令

本服务的构建与运行由 Task 管理，脚本位于本服务 `scripts/` 目录（各服务独立维护，互不引用）。
实现完成后以下命令即可直接使用：

```bash
task build                          # 编译到 bin/server-linux-amd64（默认 linux/amd64）
task build OS=windows ARCH=amd64    # 编译指定平台
task build-image                    # 构建镜像 ganrag/repository:latest（自动先编译）
task build-image COPY_CONFIG=false  # 构建不含配置的镜像（运行时挂载）
task run                            # 前台启动容器
task run -- --detach                # 后台启动容器
task run -- --detach --mount-config # 用宿主机 configs/config.yaml 覆盖镜像内配置
task stop                           # 停止并删除容器
```

在仓库根目录也可以统一操作：

```bash
task build -- repository
task build-image -- repository
task run -- repository --detach
task stop
```

服务未实现时，build / build-image / run 会提示并跳过，不报错。
