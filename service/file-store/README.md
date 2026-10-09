# File Store 服务

自建对象存储服务，使用本地文件夹模拟对象存储，对外暴露 gRPC 接口，负责：

- 文件上传（≤10MB，同 key 覆盖）
- 文件下载（含 sha256 校验和）
- 文件删除
- 文件元数据管理（每 bucket 一个 JSON 索引）
- 目录管理（按 bucket 列出文件）

文件按 bucket（命名空间）+ key（对象路径）两级定位。技术选型与接口设计详见 [docs/architecture.md](docs/architecture.md)。

## 目录结构

```
service/file-store/
├── cmd/server/          # 服务入口（配置加载、日志初始化、优雅退出）
├── configs/config.yaml  # 默认配置
├── internal/
│   ├── config/          # 配置结构、校验与类型转换
│   ├── store/           # 对象存储核心（校验、读写、元数据索引）
│   └── server/          # gRPC handler 与错误码映射、服务生命周期
└── docs/architecture.md # 架构选型与接口设计文档
```

Proto 定义在仓库根目录 `api/proto/file-store/v1/`，生成代码在 `api/gen/file-store/v1/`。

## 快速开始

### 前置条件

- Go 1.27+
- 仅重新生成 Proto 时需要：`protoc` 与插件（见「开发」章节）

### 启动服务

```bash
# 在 service/file-store 目录下启动（默认读取 configs/config.yaml）
go run ./cmd/server

# 从仓库根目录启动
go run ./service/file-store/cmd/server -config service/file-store/configs/config.yaml

# 启动参数
#   -config  YAML 配置文件路径（默认 configs/config.yaml）
#   -env     .env 文件路径（默认 .env，文件不存在时自动忽略）
```

默认监听 `0.0.0.0:50054`。服务已注册健康检查与服务反射，可用 grpcurl 验证：

```bash
grpcurl -plaintext localhost:50054 list
grpcurl -plaintext -d '{}' localhost:50054 grpc.health.v1.Health/Check
```

## 配置

`configs/config.yaml`：

```yaml
server:
  host: "0.0.0.0"   # gRPC 监听地址
  port: 50054       # gRPC 监听端口

storage:
  path: "./data/file-store" # 数据根目录（bucket 与元数据索引存放于其下，不存在时自动创建）

log:
  output: "both"        # console | file | both
  level: "info"         # debug | info | warn | error
  format: "text"        # json | text
  directory: "./logs"   # 文件输出目录（output 为 file/both 时必填）
```

所有配置项均可用 `GANRAG_` 前缀的环境变量覆盖（嵌套用下划线），例如：

```bash
GANRAG_SERVER_PORT=50054 GANRAG_STORAGE_PATH=/data/file-store go run ./cmd/server
```

配置由仓库公共包 `pkg/config` 读取（内部使用 viper：YAML 文件 + 环境变量覆盖）。

## gRPC 接口

| RPC | 说明 |
|-----|------|
| `Upload` | 上传文件（≤10MB），同 bucket+key 覆盖旧文件并更新元数据；bucket 首次上传时隐式创建 |
| `Download` | 下载文件内容与元数据（含 sha256，便于调用方校验），文件不存在返回 `NotFound` |
| `Delete` | 删除文件及其元数据，文件不存在返回 `NotFound` |
| `GetMetadata` | 查询单个文件的元数据，文件不存在返回 `NotFound` |
| `List` | 列出 bucket 下所有文件的元数据（按 key 升序），bucket 不存在返回空列表 |

### grpcurl 示例

未安装 grpcurl 时：`go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest`

```bash
# 上传（data 字段为 base64 编码的文件内容，"hello" -> aGVsbG8=）
grpcurl -plaintext -d '{
  "bucket": "attachments",
  "key": "article-123/img.png",
  "data": "aGVsbG8=",
  "content_type": "text/plain"
}' localhost:50054 filestore.v1.FileStore/Upload

# 下载（返回 data 与 metadata）
grpcurl -plaintext -d '{"bucket":"attachments","key":"article-123/img.png"}' \
  localhost:50054 filestore.v1.FileStore/Download

# 查询元数据
grpcurl -plaintext -d '{"bucket":"attachments","key":"article-123/img.png"}' \
  localhost:50054 filestore.v1.FileStore/GetMetadata

# 列出 bucket 下所有文件（bucket 不存在返回空列表）
grpcurl -plaintext -d '{"bucket":"attachments"}' \
  localhost:50054 filestore.v1.FileStore/List

# 删除文件
grpcurl -plaintext -d '{"bucket":"attachments","key":"article-123/img.png"}' \
  localhost:50054 filestore.v1.FileStore/Delete
```

### Go 客户端示例

```go
import (
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    filestorev1 "github.com/gangantongxue/GanRAG/api/gen/file-store/v1"
)

// gRPC 默认消息接收上限为 4MB，下载 >4MB 文件需将客户端上限调至 16MB（与服务端一致）
conn, err := grpc.NewClient("file-store:50054",
    grpc.WithTransportCredentials(insecure.NewCredentials()),
    grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(16<<20)),
)
if err != nil {
    return err
}
defer conn.Close()

client := filestorev1.NewFileStoreClient(conn)
up, err := client.Upload(ctx, &filestorev1.UploadRequest{
    Bucket:      "attachments",
    Key:         "article-123/img.png",
    Data:        pngBytes,          // []byte，≤10MB
    ContentType: "image/png",       // 可选，缺省 application/octet-stream
})
if err != nil {
    return err
}
_ = up.GetMetadata().GetSha256() // 十六进制小写，可用于校验下载内容
```

## 存储结构

数据根目录由 `storage.path` 指定（默认 `./data/file-store`，已加入 `.gitignore`）：

```
data/file-store/                # storage.path
├── meta/
│   ├── attachments.json        # bucket attachments 的元数据索引
│   └── documents.json          # bucket documents 的元数据索引
├── attachments/                # bucket 目录（首次上传时自动创建）
│   └── article-123/
│       └── img.png             # 对象内容文件（key 允许 "/" 分层）
└── documents/
    └── report.pdf
```

### 元数据索引

每个 bucket 一个索引文件 `meta/<bucket>.json`：

```json
{
  "files": {
    "article-123/img.png": {
      "size": 20480,
      "content_type": "image/png",
      "sha256": "e3b0c44298fc1c149afbf4c8996fb924...",
      "uploaded_at_unix": 1759977600
    }
  }
}
```

| 字段 | 说明 |
|------|------|
| `size` | 文件字节数 |
| `content_type` | MIME 类型（上传未指定时为 `application/octet-stream`） |
| `sha256` | 内容校验和，十六进制小写 |
| `uploaded_at_unix` | 最后上传时间（Unix 秒） |

### 读写行为

- **上传**：内容文件与元数据索引均先写同目录临时文件、再 `os.Rename` 原子替换，进程中断不会留下半截文件或损坏索引；索引持久化失败时回滚内存索引，保持与磁盘一致。
- **启动加载**：扫描 `meta/*.json` 在内存中重建全部元数据；索引文件损坏（JSON 解析失败）时启动报错，避免带着不一致的索引提供服务。
- **下载/查询**：直接读内存索引，仅下载时读取内容文件。
- **删除**：先删内容文件（已不存在时忽略，保证可重试），再移除索引项并原子持久化。
- **key 规范化**：入库前经 `filepath.Clean` 规范化（如 `sub/../a.txt` -> `a.txt`），再二次校验路径仍在 bucket 根内。

## 使用约束

| 规则 | 说明 |
|------|------|
| bucket | 必填，仅允许字母数字与 `-` `_`；首次上传时隐式创建，无独立 CreateBucket 接口 |
| key | 必填、非空；禁止 `..` 路径穿越、禁止以 `/` 开头、禁止包含 `\`；允许 `/` 分层 |
| 文件大小 | `data` 必填非空，1 字节 ~ 10MB（`MaxFileSize = 10 << 20`） |
| 重复上传 | 同 bucket+key 覆盖旧文件并更新元数据（upsert 语义，与 vector-store 的 Write 一致） |
| 消息大小 | 服务端/客户端 `MaxRecvMsgSize` 均为 16MB；超过的服务端由 gRPC 框架直接拒绝 |
| 传输安全 | 当前 gRPC 为明文传输、未启用 TLS，仅建议在内网/开发环境使用 |

### 错误码

| 状态码 | 触发条件 |
|--------|----------|
| `NotFound` | `Download` / `Delete` / `GetMetadata` 目标不存在 |
| `InvalidArgument` | bucket、key 校验失败，或 data 为空 / 超过 10MB |
| `ResourceExhausted` | 消息体超过服务端 16MB 接收上限（gRPC 框架层拒绝） |
| `Internal` | 其他服务内部错误（已记日志，不向调用方暴露细节） |

## Task 与 Docker

项目使用 [Task](https://taskfile.dev/) 统一管理构建与运行，服务以 Docker 容器方式启动（非 root 用户）。
根目录 Taskfile 负责统一编排；本服务的构建/镜像/运行脚本位于本服务 `scripts/` 目录（各服务独立维护，互不引用）。

### 前置条件

- Task：`go install github.com/go-task/task/v3/cmd/task@latest`
- Docker

### 根目录命令（仓库根目录执行）

| 命令 | 说明 |
|------|------|
| `task proto` | 生成所有服务的 Proto Go 代码 |
| `task build` | 编译所有已实现服务（尚未实现的服务提示并跳过） |
| `task build -- file-store` | 只编译指定服务 |
| `task build-image` / `task build-image -- file-store` | 构建镜像（会自动先编译） |
| `task run` | 后台启动所有已实现服务 |
| `task run -- file-store` | 前台启动指定服务（Ctrl+C 停止） |
| `task run -- file-store --detach` | 后台启动指定服务 |
| `task stop` | 停止并删除所有服务容器 |

服务名即 `service/` 下的目录名（gateway、ai、user、repository、vector-store、file-store）。

### 服务目录命令（在 service/file-store 下执行）

```bash
task build                            # 编译到 bin/server-linux-amd64（默认 linux/amd64）
task build OS=windows ARCH=amd64      # 编译指定平台
task build-image                      # 构建镜像 ganrag/file-store:latest
task build-image COPY_CONFIG=false    # 构建不含配置的镜像（运行时必须挂载配置）
task run                              # 前台启动容器
task run -- --detach                  # 后台启动容器
task run -- --detach --mount-config   # 后台启动，并用宿主机 configs/config.yaml 覆盖镜像内配置
task stop                             # 停止并删除容器
```

### Docker 镜像与容器约定

- 基础镜像 `alpine`，容器内以普通用户 `app` 运行；构建时 UID/GID 默认取当前宿主用户，保证挂载目录可写（运行脚本同时传入 `--user $(id -u):$(id -g)`）
- 二进制：本服务 `scripts/build.sh` 的交叉编译产物（`CGO_ENABLED=0`）复制为镜像内 `/app/server`
- 配置：默认由本服务 `scripts/build-image.sh` 复制进镜像；`COPY_CONFIG=false` 时镜像不含配置，需运行时 `--mount-config` 挂载
- 目录映射（属主为宿主用户，已加入 `.gitignore`）：
  - `service/file-store/data` → `/app/data`（对象数据，对应 `storage.path: ./data/file-store`）
  - `service/file-store/logs` → `/app/logs`（日志文件）
- 网络：容器加入 `ganrag` 自定义网络，服务间可按容器名 `ganrag-<服务名>` 互访
- 端口映射：`--port <宿主端口>:<容器端口>`，本服务 Taskfile 默认 50054:50054
- 查看日志：`docker logs -f ganrag-file-store`

### scripts 说明

本服务脚本（`service/file-store/scripts/`，各服务独立维护）：

| 脚本 | 用途 |
|------|------|
| `scripts/build.sh [--os --arch]` | 编译本服务二进制到 `bin/server-<os>-<arch>` |
| `scripts/build-image.sh [--tag --os --arch --copy-config]` | 构建本服务 Docker 镜像（构建前删除同名旧镜像，避免悬空镜像） |
| `scripts/run.sh [--tag --port --container-port --detach --mount-config]` | 启动本服务容器（默认容器端口 50054） |

跨服务公共脚本（仓库根目录）：

| 脚本 | 用途 |
|------|------|
| `scripts/gen-proto.sh` | 扫描 `api/proto/**/*.proto` 生成 Go 代码（`task proto`） |

## 开发

### 重新生成 Proto

修改 `api/proto/file-store/v1/file_store.proto` 后，在仓库根目录执行：

```bash
task proto    # 等价于 ./scripts/gen-proto.sh，会扫描生成所有服务的 proto
```

依赖（需全局安装）：

```bash
sudo apt-get install -y protobuf-compiler   # 或下载 protoc 官方 release
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
```

生成代码（`api/gen/file-store/v1/*.pb.go`）需提交入库。

### 测试与构建

```bash
# 在仓库根目录执行
go test ./service/file-store/...          # store 单测 + bufconn 端到端测试，不依赖外部环境
go test -race ./service/file-store/...    # 竞态检测
go vet ./service/file-store/...
task build -- file-store                  # 编译二进制（等价 go build ./service/file-store/cmd/server）
task build-image -- file-store            # 构建 Docker 镜像
```

测试覆盖：

- **store 单测**：bucket/key 校验规则（含路径穿越防护）、上传/覆盖/元数据生成、10MB 大小边界、下载/删除/幂等删除、列表排序与空 bucket、索引重启加载与损坏索引报错、并发上传（配合 `-race`）
- **gRPC 端到端（bufconn）**：上传 -> 元数据 -> 下载（sha256 比对）-> 覆盖 -> 列表 -> 删除全流程、错误码映射（`NotFound` / `InvalidArgument`）、10MB 文件收发、超过 16MB 消息的 `ResourceExhausted`
