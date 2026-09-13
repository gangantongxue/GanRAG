# Vector Store 服务

Go 内嵌向量存储服务，基于 [chromem-go](https://github.com/philippgille/chromem-go)，对外暴露 gRPC 接口，负责：

- 向量写入（按 ID upsert）
- 相似度检索（精确余弦相似度）
- 集合（索引）管理
- 向量元数据管理与按条件删除

嵌入向量由调用方生成（如 AI Service 负责向量化），本服务不调用外部 embedding 模型。
技术选型与接口设计详见 [docs/architecture.md](docs/architecture.md)。

## 目录结构

```
service/vector-store/
├── cmd/server/          # 服务入口（配置加载、日志初始化、优雅退出）
├── configs/config.yaml  # 默认配置
├── internal/
│   ├── config/          # 配置结构、校验与类型转换
│   ├── store/           # chromem-go 封装（集合管理、写入、检索、删除）
│   └── server/          # gRPC handler 与错误码映射、服务生命周期
└── docs/architecture.md # 架构选型文档
```

Proto 定义在仓库根目录 `api/proto/vector-store/v1/`，生成代码在 `api/gen/vector-store/v1/`。

## 快速开始

### 前置条件

- Go 1.27+
- 仅重新生成 Proto 时需要：`protoc` 与插件（见「开发」章节）

### 启动服务

```bash
# 在 service/vector-store 目录下启动（默认读取 configs/config.yaml）
go run ./cmd/server

# 从仓库根目录启动
go run ./service/vector-store/cmd/server -config service/vector-store/configs/config.yaml

# 启动参数
#   -config  YAML 配置文件路径（默认 configs/config.yaml）
#   -env     .env 文件路径（默认 .env，文件不存在时自动忽略）
```

默认监听 `0.0.0.0:50053`。服务已注册健康检查与服务反射，可用 grpcurl 验证：

```bash
grpcurl -plaintext localhost:50053 list
grpcurl -plaintext -d '{}' localhost:50053 grpc.health.v1.Health/Check
```

## 配置

`configs/config.yaml`：

```yaml
server:
  host: "0.0.0.0"   # gRPC 监听地址
  port: 50053       # gRPC 监听端口

storage:
  mode: "persistent"          # persistent（写入落盘，默认） | memory（内存，开发调试）
  path: "./data/vector-store" # persistent 模式的数据目录
  compress: true              # persistent 模式下是否对落盘文件 gzip 压缩

log:
  output: "both"        # console | file | both
  level: "info"         # debug | info | warn | error
  format: "text"        # json | text
  directory: "./logs"   # 文件输出目录（output 为 file/both 时必填）
```

所有配置项均可用 `GANRAG_` 前缀的环境变量覆盖（嵌套用下划线），例如：

```bash
GANRAG_SERVER_PORT=50054 GANRAG_STORAGE_MODE=memory go run ./cmd/server
```

## gRPC 接口

| RPC | 说明 |
|-----|------|
| `CreateCollection` | 创建集合（重名返回 `AlreadyExists`） |
| `DeleteCollection` | 删除集合及其全部文档 |
| `ListCollections` | 列出所有集合及文档数量 |
| `Write` | 批量写入文档（同 ID 覆盖，unary 批量） |
| `Search` | 传入查询向量，返回相似度最高的 top_k 个文档 |
| `Delete` | 按 ID 或元数据/内容条件删除文档 |
| `GetByID` | 按文档 ID 查询单个文档 |

### grpcurl 示例

未安装 grpcurl 时：`go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest`

```bash
# 创建集合
grpcurl -plaintext -d '{"name":"knowledge"}' \
  localhost:50053 vectorstore.v1.VectorStore/CreateCollection

# 批量写入（embedding 由调用方生成）
grpcurl -plaintext -d '{
  "collection": "knowledge",
  "documents": [
    {"id":"doc-1","embedding":[1,0,0],"content":"苹果","metadata":{"type":"fruit"}},
    {"id":"doc-2","embedding":[0,1,0],"content":"香蕉","metadata":{"type":"fruit"}}
  ],
  "create_if_missing": true
}' localhost:50053 vectorstore.v1.VectorStore/Write

# 检索（top_k 超过文档数时按实际数量返回）
grpcurl -plaintext -d '{"collection":"knowledge","query_embedding":[1,0,0],"top_k":3}' \
  localhost:50053 vectorstore.v1.VectorStore/Search

# 检索 + 元数据过滤（where、where_document 均支持）
grpcurl -plaintext -d '{"collection":"knowledge","query_embedding":[1,0,0],"top_k":3,"where":{"type":"fruit"}}' \
  localhost:50053 vectorstore.v1.VectorStore/Search

# 按 ID 查询
grpcurl -plaintext -d '{"collection":"knowledge","id":"doc-1"}' \
  localhost:50053 vectorstore.v1.VectorStore/GetByID

# 删除文档（ids 非空时忽略 where/where_document）
grpcurl -plaintext -d '{"collection":"knowledge","ids":["doc-1"]}' \
  localhost:50053 vectorstore.v1.VectorStore/Delete

# 列出集合 / 删除集合
grpcurl -plaintext -d '{}' localhost:50053 vectorstore.v1.VectorStore/ListCollections
grpcurl -plaintext -d '{"name":"knowledge"}' \
  localhost:50053 vectorstore.v1.VectorStore/DeleteCollection
```

### Go 客户端示例

```go
import (
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    vectorstorev1 "github.com/gangantongxue/GanRAG/api/gen/vector-store/v1"
)

conn, err := grpc.NewClient("vector-store:50053",
    grpc.WithTransportCredentials(insecure.NewCredentials()))
if err != nil {
    return err
}
defer conn.Close()

client := vectorstorev1.NewVectorStoreClient(conn)
resp, err := client.Search(ctx, &vectorstorev1.SearchRequest{
    Collection:     "knowledge",
    QueryEmbedding: queryEmbedding, // []float32，由调用方生成
    TopK:           5,
})
```

## 存储结构

### 文档记录（Document）结构

一条文档记录由四部分组成，其中 `embedding`、`content`、`metadata` 均由调用方在 `Write` 时提供：

| 字段 | 说明 |
|------|------|
| `id` | 文档唯一 ID，集合内唯一；同 ID 重复写入为覆盖（upsert） |
| `embedding` | 嵌入向量，由调用方生成；同一集合内维度必须一致 |
| `content` | 原始文本片段，检索命中后原样返回。允许为空，但一般建议与向量一起存储 |
| `metadata` | 元数据，调用方自定义的字符串键值对，检索/查询时原样返回 |

`metadata` 没有固定 schema，key 和 value 都是字符串，服务不解释其含义（仅 `Search` 的 `where` 会用它做精确匹配）。
建议把「来源、定位」类信息都放进去，例如文章片段：

```json
{
  "id": "art-123:7",
  "embedding": [0.12, -0.03, "..."],
  "content": "这一段是文章《Go 并发编程》中的第 7 个片段……",
  "metadata": {
    "article_id": "art-123",
    "article_title": "Go 并发编程",
    "chunk_index": "7",
    "chunk_total": "42",
    "source": "wiki/go-concurrency.md"
  }
}
```

拿到检索结果后，可据此还原片段出处：

- 限定单篇文章检索：`where: {"article_id": "art-123"}`
- 拼接原文顺序：检索按相似度返回、不保证片段顺序，客户端按 `chunk_index`（转 int）排序即可
- 取相邻片段：把 ID 设计为确定性格式（如 `文章ID:片段序号`），命中后可直接用 `GetByID` 取前后片段，便于 RAG 拼接上下文

### 持久化模式（persistent）

数据目录由 `storage.path` 指定（默认 `./data/vector-store`，已加入 `.gitignore`）。
chromem-go 不使用数据库文件，而是**每个文档一个文件**：集合对应一个子目录，目录中每个文件是一条独立记录。

以集合 `knowledge`、文档 `doc-1` / `doc-2`、`compress: true` 为例：

```
data/vector-store/            # storage.path
└── e0f89587/                 # 集合目录：sha256("knowledge") 前 4 字节的十六进制
    ├── 00000000.gob.gz       # 集合元数据（固定文件名）
    ├── bb0e4f49.gob.gz       # 文档 doc-1：sha256("doc-1") 前 4 字节的十六进制
    └── 664b4034.gob.gz       # 文档 doc-2：sha256("doc-2") 前 4 字节的十六进制
```

命名与格式规则：

| 项目 | 规则 |
|------|------|
| 集合目录名 | `sha256(集合名)` 前 4 字节的十六进制（8 字符），避免集合名直接作为路径 |
| 集合元数据文件 | 固定为 `00000000.gob`，内容为 gob 编码的 `{Name, Metadata}` |
| 文档文件名 | `sha256(文档 ID)` 前 4 字节的十六进制 + `.gob`，内容为 gob 编码的文档（ID、Embedding、Content、Metadata） |
| 压缩 | `storage.compress: true` 时所有文件名追加 `.gz`（gzip 压缩），否则为纯 gob |
| 权限 | 集合目录创建时使用 `0700` |

启动与写入行为：

- **启动加载**：扫描数据目录，逐个读取集合目录中的元数据文件与文档文件，在内存中重建全部集合与向量数据。磁盘上**不保存检索索引**，索引只存在于内存。
- **写入**：每次写入（含同 ID 覆盖）同步写对应文档文件，写入即落盘；覆盖写入会直接覆盖同 ID 的文件。
- **删除文档**：删除对应文档文件；**删除集合**：删除整个集合目录。
- **检索**：在内存中对候选文档做全量精确余弦相似度计算（当前 chromem-go 未提供 ANN 索引），因此集合规模增大时，启动加载时间和单次检索耗时会随之增长。

### 内存模式（memory）

`storage.mode: memory` 时所有数据仅存在于内存，进程退出即丢失，适合开发调试；接口行为与持久化模式完全一致。

### 维度校验说明

store 层会在内存中记录每个集合的向量维度，用于写入/检索前的维度一致性校验。
该记录不落盘：服务重启后，集合维度会在其**首次写入**时按该批次向量重新建立；重启后若首次查询就传入错误维度，将由底层库报错并映射为 `InvalidArgument`。

## 使用约束

- 写入的每个文档必须提供非空 ID 与非空 `embedding`（服务不会自动生成向量）
- 同一集合内所有向量维度必须一致，同一批次内也必须一致
- `Write` 为 upsert 语义：同 ID 重复写入会覆盖旧文档
- `Search` 的 `top_k` 必须 ≥ 1；查询向量维度需与集合一致
- `Search` 支持元数据精确匹配（`where`）与文档内容过滤（`where_document`，支持 `$contains` / `$not_contains`）；元数据过滤不支持数值范围与 `$and`/`$or` 组合
- `Delete` 必须提供 `ids` 或 `where`/`where_document` 至少一项，防止误删全部文档；`ids` 非空时忽略过滤条件
- 当前 gRPC 为明文传输、未启用 TLS，仅建议在内网/开发环境使用

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
| `task build -- vector-store` | 只编译指定服务 |
| `task build-image` / `task build-image -- vector-store` | 构建镜像（会自动先编译） |
| `task run` | 后台启动所有已实现服务 |
| `task run -- vector-store` | 前台启动指定服务（Ctrl+C 停止） |
| `task run -- vector-store --detach` | 后台启动指定服务 |
| `task stop` | 停止并删除所有服务容器 |

服务名即 `service/` 下的目录名（gateway、ai、user、repository、vector-store、file-store）。

### 服务目录命令（在 service/vector-store 下执行）

```bash
task build                            # 编译到 bin/server-linux-amd64（默认 linux/amd64）
task build OS=windows ARCH=amd64      # 编译指定平台
task build-image                      # 构建镜像 ganrag/vector-store:latest
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
  - `service/vector-store/data` → `/app/data`（向量数据，对应 `storage.path: ./data/vector-store`）
  - `service/vector-store/logs` → `/app/logs`（日志文件）
- 网络：容器加入 `ganrag` 自定义网络，服务间可按容器名 `ganrag-<服务名>` 互访
- 端口映射：`--port <宿主端口>:<容器端口>`，本服务默认 50053
- 查看日志：`docker logs -f ganrag-vector-store`

### scripts 说明

本服务脚本（`service/vector-store/scripts/`，各服务独立维护）：

| 脚本 | 用途 |
|------|------|
| `scripts/build.sh [--os --arch]` | 编译本服务二进制到 `bin/server-<os>-<arch>` |
| `scripts/build-image.sh [--tag --os --arch --copy-config]` | 构建本服务 Docker 镜像 |
| `scripts/run.sh [--tag --port --container-port --detach --mount-config]` | 启动本服务容器 |

跨服务公共脚本（仓库根目录）：

| 脚本 | 用途 |
|------|------|
| `scripts/gen-proto.sh` | 扫描 `api/proto/**/*.proto` 生成 Go 代码（`task proto`） |

尚未实现的服务（缺少 `cmd/server/*.go`）在执行 build/build-image/run 时会提示并跳过，不阻断批量命令。

## 开发

### 重新生成 Proto

修改 `api/proto/vector-store/v1/vector_store.proto` 后，在仓库根目录执行：

```bash
task proto    # 等价于 ./scripts/gen-proto.sh，会扫描生成所有服务的 proto
```

依赖（需全局安装）：

```bash
sudo apt-get install -y protobuf-compiler   # 或下载 protoc 官方 release
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
```

生成代码（`api/gen/vector-store/v1/*.pb.go`）需提交入库。

### 测试与构建

```bash
# 在仓库根目录执行
go test ./service/vector-store/...          # store 单测 + bufconn 端到端测试，不依赖外部环境
go test -race ./service/vector-store/...    # 竞态检测
go vet ./service/vector-store/...
task build -- vector-store                  # 编译二进制（等价 go build ./service/vector-store/cmd/server）
task build-image -- vector-store            # 构建 Docker 镜像
```

测试覆盖：集合生命周期、写入/upsert/入参校验、检索排序与过滤、按条件删除、维度校验、持久化重启加载，以及 gRPC 端到端与错误码映射。
