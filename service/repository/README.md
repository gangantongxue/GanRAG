# Repository 服务

知识库管理服务，同时是**对象存储（Garage，S3 协议）的唯一直接访问方**：文章正文、附件对象的读写、文件资源权限校验都在本服务（Garage 不对外暴露）。

架构设计见 [docs/architecture.md](docs/architecture.md)（对象存储与文件访问、Markdown 相对路径解析、权限边界）。

> 当前状态：**阶段一已实现**（知识库 CRUD + `UploadWhole` 整段上传 + 删除走公共 `async_tasks` outbox）。目录树、文章、资源流式读取、分片上传见 [docs/todo.md](../../docs/todo.md)。

## 配置

- `configs/config.yaml`：gRPC 端口 50052、`ganrag_repository` 库、Garage S3、上传上限。
- S3 AK/SK 为空时回退 `GARAGE_DEFAULT_ACCESS_KEY` / `GARAGE_DEFAULT_SECRET_KEY`（与 docker-compose 共用）。
- 启动时自动执行本库迁移，并确保 `ganrag_common.async_tasks` 存在；Garage `HeadBucket` 失败拒绝启动。

## Task 命令

```bash
task build                          # 编译到 bin/server-linux-amd64
task build OS=windows ARCH=amd64    # 编译指定平台
task build-image                    # 构建镜像 ganrag/repository:latest
task build-image COPY_CONFIG=false  # 构建不含配置的镜像（运行时挂载）
task run                            # 前台启动容器
task run -- --detach                # 后台启动容器
task run -- --detach --mount-config # 用宿主机 configs/config.yaml 覆盖镜像内配置
task stop                           # 停止并删除容器
```

在仓库根目录统一操作：

```bash
task build -- repository
task build-image -- repository
task run -- repository --detach
```

## 本地调试

```bash
# 仓库根启动基础设施
task infra
# 服务目录启动（自动读仓库根 .env）
go run ./cmd/server

# 反射 + 调用
grpcurl -plaintext localhost:50052 list
grpcurl -plaintext -d '{"user_xid":"usr_...","name":"demo","visibility":1}' \
  localhost:50052 repository.v1.RepositoryService/CreateKnowledgeBase
```
