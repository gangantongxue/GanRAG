# GanRAG

GanRAG 是一个知识库系统：用户可以上传自己的知识库文章，系统将文章进行向量化存储；内置的 AI 聊天会检索用户上传的文章内容，并据此回答用户的问题。

RAG（检索增强生成）是实现「基于用户自己的知识回答问题」的技术手段，系统的产品定位是知识库（知识上传 → 向量化存储 → AI 问答），而非一个通用 RAG 框架。

技术实现上，系统采用基于 Go 的多服务架构，服务间通过 gRPC 通信，前端为 React + TypeScript。

- 总体架构与技术选型：[docs/architecture.md](docs/architecture.md)

## 目录结构

```
├── api/                  # Protobuf 定义（api/proto）与生成代码（api/gen）
├── docs/                 # 项目级架构文档
├── pkg/                  # 跨服务共享包（config、logger 等）
├── scripts/              # 跨服务公共脚本（仅 Proto 生成）
├── service/              # 各微服务（每个服务含 Taskfile、Dockerfile、独立 scripts/）
│   ├── gateway/          # API 网关（Hertz）
│   ├── ai/               # AI / RAG 服务（Eino）
│   ├── user/             # 用户服务
│   ├── repository/       # 知识库服务
│   ├── vector-store/     # 向量存储服务（chromem-go + gRPC）
│   └── file-store/       # 对象存储服务
├── Taskfile.yml          # 根 Taskfile：统一编排
└── web/                  # 前端
```

## 前置条件

- Go 1.27+
- [Task](https://taskfile.dev/)：`go install github.com/go-task/task/v3/cmd/task@latest`
- Docker（服务以容器方式运行）
- 仅重新生成 Proto 时需要 protoc 与插件（见下文）

## Task 使用

所有构建与运行均由 Task 管理：根目录 Taskfile 负责统一编排，各服务目录下的 Taskfile 负责本服务实现；构建/镜像/运行脚本位于各服务自己的 `scripts/` 目录，服务之间互不引用、可独立定制。

### 根目录命令

| 命令 | 说明 |
|------|------|
| `task proto` | 生成所有服务的 Proto Go 代码（api/gen） |
| `task build` | 顺序编译所有已实现服务（未实现的服务提示并跳过） |
| `task build -- vector-store` | 只编译指定服务 |
| `task build-image` / `task build-image -- vector-store` | 构建镜像（自动先编译） |
| `task run` | 后台启动所有已实现服务 |
| `task run -- vector-store` | 前台启动指定服务（Ctrl+C 停止） |
| `task run -- vector-store --detach` | 后台启动指定服务 |
| `task stop` | 停止并删除所有服务容器 |
| `task --list` | 查看全部任务 |

服务名即 `service/` 下的目录名（gateway、ai、user、repository、vector-store、file-store）。

### 服务目录命令（以 vector-store 为例）

```bash
cd service/vector-store
task build                          # 编译到 bin/server-linux-amd64（默认 linux/amd64）
task build OS=windows ARCH=amd64    # 编译指定平台
task build-image                    # 构建镜像 ganrag/vector-store:latest
task build-image COPY_CONFIG=false  # 构建不含配置的镜像（运行时必须挂载配置）
task run                            # 前台启动容器
task run -- --detach                # 后台启动容器
task run -- --detach --mount-config # 后台启动，并用宿主机 configs/config.yaml 覆盖镜像内配置
task stop                           # 停止并删除容器
```

### 服务目录与脚本约定

```
service/<name>/
├── Taskfile.yml       # 服务任务：build / build-image / run / stop
├── Dockerfile         # alpine 镜像，容器内非 root 用户运行
├── scripts/           # 本服务构建与运行脚本（各服务独立）
│   ├── build.sh       # 交叉编译二进制到 bin/server-<os>-<arch>
│   ├── build-image.sh # 构建 Docker 镜像
│   └── run.sh         # 启动容器（数据/日志映射到宿主机）
├── configs/           # 配置（可复制进镜像，也可运行时挂载）
├── data/、logs/       # 运行数据与日志（已 gitignore，容器运行时映射）
└── cmd/、internal/    # 服务代码
```

跨服务公共脚本只有 `scripts/gen-proto.sh`（Proto 生成）。

### Docker 约定

- 镜像名 `ganrag/<服务名>:<tag>`，容器名 `ganrag-<服务名>`
- 构建镜像前会强制删除同名旧镜像，避免旧镜像变成悬空镜像（`<none>:<none>`）
- 容器内以普通用户运行（UID/GID 与宿主当前用户一致），`data/`、`logs/` 映射到服务目录下，避免 root 属主问题
- 服务加入 `ganrag` 自定义网络，服务间可用容器名互访
- 配置默认复制进镜像；构建时 `COPY_CONFIG=false` 或运行时 `--mount-config` 可改为挂载宿主机配置

## Proto 代码生成

```bash
task proto    # 等价于 ./scripts/gen-proto.sh，扫描 api/proto/**/*.proto 生成到 api/gen
```

依赖（需全局安装）：

```bash
sudo apt-get install -y protobuf-compiler   # 或下载 protoc 官方 release
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
```

生成代码需提交入库。

## 开发

```bash
go test ./...     # 全部测试
go vet ./...
gofmt -l .
```

各服务详细说明见 `service/<name>/README.md`。
