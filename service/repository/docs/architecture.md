# Repository 服务架构设计

## 概述

Repository 服务是知识库、目录树、文章、附件的业务边界，也是**对象存储（Garage，S3 协议）的唯一直接访问方**。前端不接触 S3 凭证，Gateway 不持有 S3 凭证，二者都通过 Repository 完成文件的权限校验与读取授权。

> **落地状态**：架构迁移已落地（2026-10-17）——File Store 已移除、`attachments.relative_path` 已随迁移 0006 落库、Garage 基础设施就绪（compose 编排、自动建桶与密钥、数据卷持久化）；**接口协议已定（2026-10-18，[api/proto/repository/v1/repository.proto](../../../api/proto/repository/v1/repository.proto)）**：读写均走 gRPC、Garage 不对外暴露（原 Presigned 302 主路径降为可选部署形态）；**Garage 读写与接口实现随「Repository 服务实现」落地**。跟踪见 [docs/todo.md](../../../docs/todo.md)。

## 对象存储与文件访问（P1 核心，架构迁移最高优先级）

### 选型：Garage（S3 协议）

**选型：Garage**（轻量自托管对象存储，通用 S3 协议，Apache-2.0）。

- 通用 S3 协议：Go 侧用 AWS SDK for Go v2 直连，后续迁移 MinIO / AWS S3 / Cloudflare R2 只改配置（endpoint / region / key / bucket），不动业务代码
- 轻量：单二进制 + SQLite/本地元数据，适合单机与小集群
- 移除自建 File Store：删除 `service/file-store/`、`api/proto/file-store/`、`api/gen/file-store/`，不再有自建本地文件 + JSON 索引的对象存储实现

### 服务边界：谁访问 Garage

```text
浏览器
  ↓ GET /api/v1/articles/{article_xid}/resource?path=...
Gateway            验签 JWT、限流、转发 user_id
  ↓ gRPC（server-streaming）
Repository Service 知识库权限校验、相对路径解析、从 Garage 流式读对象
  ↓ meta + data 分块
Gateway            流式回写 HTTP body
  ↓
浏览器             收到文件内容（Garage 全程不对外暴露）
```

| 层 | 职责 | 不做 |
|----|------|------|
| Gateway | HTTP 边界、JWT 验签、限流、资源路由、流式回写、CORS 与缓存头 | 不复制知识库权限规则、不持有 S3 凭证 |
| Repository | 知识库可见性/成员权限判定、Markdown 相对路径解析、对象归属校验、对象读写（gRPC 流式）、上传会话与完整性校验 | 不把 S3 凭证下发给任何人 |
| Garage | 对象持久化、S3 认证、GET/PUT/DELETE 对象 | 不理解用户、知识库、成员关系；不对外暴露端口 |

**权限判定放在 Repository 层**（知识库领域边界），Gateway 只做认证与路由。理由：知识库可见性（owner / member / visibility）规则属于领域逻辑，复制到 Gateway 会导致两处真相；未来拆出 Knowledge Base Service 时由该服务承接，Gateway 契约不变。

**读写均经 gRPC、Garage 不对外暴露**（2026-10-18 定）：浏览器只访问 Gateway 稳定地址，文件内容经 Gateway 流式转发（上传同理走 gRPC 分片）；Markdown 中**不写**任何 Garage 地址。Presigned URL 302 仅作可选部署形态保留（见「可选部署形态」）。

### 桶设计：单桶私有，不是匿名公共桶

- **一个共享桶**（如 `ganrag-objects`），所有知识库的正文、图片、附件混存
- **Bucket ACL 私有**：不允许匿名读。"共享桶"指所有业务共用一个桶，不是公开可读
- 若桶匿名公开，任何拿到对象地址的人可绕过 Gateway，私有知识库即泄露。因此只要系统有私有知识库，Garage 桶就不能开匿名读
- S3 Access Key / Secret Key **只配置在 Repository**，通过环境变量注入，不入库、不入前端、不入 Gateway

### 对象 Key：统一 `o/<obj_xid>`，不再分目录

不再使用图片专属目录、附件目录、按知识库分桶。所有对象统一：

```text
o/<obj_xid>
```

对象类型、原始文件名、所属文章、所属知识库全部由 MySQL 元数据管理：

| 字段 | 含义 |
|------|------|
| `articles.content_key` | 正文 md 对象 key（如 `o/abc123...`） |
| `attachments.file_key` | 附件对象 key（如 `o/def456...`） |
| `attachments.original_name` | 原始文件名（仅展示用，不参与寻址） |
| `attachments.kind` | 1 文章源文件（pdf/docx/html 原文）/ 2 Markdown 引用的文件或图片（仅表示业务生命周期，不再表示存储目录） |

好处：对象 key 与目录结构、文件重命名、知识库迁移解耦；迁移到其他 S3 服务时 key 原样平移。

### Markdown 相对路径：用户写相对路径，前端渲染时解析

用户在 Markdown 中只写相对路径（图片、文件、跨文档引用），不写完整 URL：

```markdown
![项目截图](assets/screenshot.png)

[设计文档](../design/architecture.md)

[下载原始文件](files/spec.pdf)
```

前端渲染时以**当前文章**为基准解析相对路径，再拼接为 Gateway 资源地址。禁止绝对路径、禁止 `..` 逃逸出知识库、禁止用户路径直接当 S3 key。

#### 两类引用的解析

1. **图片 / 普通文件** → Gateway 资源路由：

```text
/api/v1/articles/<article_xid>/resource?path=assets/screenshot.png
```

Repository 按「当前文章 + 规范化 relative_path」查 `attachments` 得到真正的 `file_key`，校验权限后从 Garage 流式读出、经 Gateway 回写。

2. **Markdown 引用其他文章** → 按知识库目录树解析目标文章，跳转文章页面或文章 API 稳定地址（`/api/v1/articles/<xid>`）。断链保留原文、渲染时标记失效。

#### 安全约束

- 路径在 Repository 再次规范化校验（前端解析只用于渲染，不是权限校验）
- 禁止 `..` 逃逸出当前知识库范围
- 无权限或不存在统一返回 404（不暴露资源是否存在，防枚举）

### 可选部署形态：Presigned URL 302（默认不启用）

默认形态是「Repository 读取 → Gateway 流式返回」（Garage 不对外暴露）。若部署环境放开 Garage 对外端口、需要直连吞吐，可启用 Presigned URL 形态（协议后续追加向后兼容），约束如下：

- 有效期 60 秒（初始值，可配置）
- 只允许 GET 指定对象，不允许列举桶、不允许写
- S3 Access Key 永不下发前端
- Markdown 中只存 Gateway 稳定地址或相对路径，不存 Presigned URL（会过期）

### 上传协议（gRPC 整段 + 会话式分片，断点续传 + 三层完整性校验）

写路径同样不出内网（2026-10-18 定，见 `api/proto/repository/v1/repository.proto`）：

- **整段** `UploadWhole`：≤2MB 小文件一次传；超限 `InvalidArgument`「content too large, use chunked upload」，前端自动降级分片
- **会话式分片**：`InitUpload`（声明 `total_size` 与全文件 sha256）→ `UploadPart`（同 `part_number` 幂等覆盖，可重试）→ `CompleteUpload`；中断用 `GetUploadStatus` 查已收分片补传（**断点续传**）；`AbortUpload` 取消
- **三层完整性校验**：① 单片 sha256 收片即验，坏片只重传该片 ② 合并前结构校验（分片 1..N 连续、Σ大小 == `total_size`）③ 合并后从 Garage 读回对象计算 sha256 与 `InitUpload` 声明值比对——端到端保证「Garage 最终字节 == 前端原始文件」，不符 `DataLoss` 作废重传；校验值落 `attachments.sha256`
- 上限（可配）：整段 2MB、单分片 2MB、总大小 100MB、会话 TTL 24h（过期自动清理分片）；前端 sha256 用 Web Crypto（`crypto.subtle.digest`）原生计算
- 上传得到通用 `object_xid`，文章正文（`content_key`）与附件（`file_key`）以引用挂接；上传未引用对象属可容忍孤儿（同保存文章口径），后续清理任务处理

### 数据库变化：`attachments` 增 `relative_path`

用户自定义相对路径需要持久化，才能由「当前文章 + 相对路径」稳定找回对象：

```sql
relative_path VARCHAR(500) NOT NULL,        -- 规范化后的相对路径，如 assets/logo.png
UNIQUE KEY uk_attachments_article_path (article_id, relative_path)
```

- 只存规范化相对路径（`assets/logo.png`），不存 `../` 或绝对路径
- **迁移前必须 `task db-status`** 确认 `ganrag_repository` 当前版本，已应用迁移禁止修改，新增迁移文件成对（up/down）
- 迁移后执行 `task db-gen` 重新生成 model/query

### 写入与删除流程

**保存文章**：

```text
写正文对象到 Garage → 更新 articles.content_key → 删旧对象
（中断最多留孤儿对象，不会出现指向不存在内容的行）
```

**Markdown 引用文件（kind=2）**：保存时按正文链接全量重建 `attachments` 行，孤儿对象即时清理；`kind=1`（源文件）随文章删除清理。

**删除文章/目录**：应用层事务内级联删 `attachments` 行 + Garage 对象（无物理外键，引用完整性由应用层保证）。

## 相关配置（Repository 持有）

| 配置 | 示例 | 说明 |
|------|------|------|
| `objectstore.endpoint` | `http://garage:3900` | S3 endpoint（Garage S3 API 端口 3900） |
| `objectstore.region` | `garage` | Garage 固定 region |
| `objectstore.bucket` | `ganrag-objects` | 共享单桶 |
| `objectstore.access_key_id` | 环境变量注入 | 不入库、不进前端 |
| `objectstore.secret_access_key` | 环境变量注入 | 不入库、不进前端 |
| `objectstore.force_path_style` | `true` | Garage 需 Path Style |
| `objectstore.presign_ttl` | `60s` | Presigned URL 有效期（可选 Presigned 形态用） |

S3 调用统一带 context 超时；写入、删除、Head、List 均有限制；错误映射统一走 `pkg/errcode`（S3 NoSuchKey → NotFound 等）。

## 部署（根 docker-compose.yml）

单节点 Garage 服务（`dxflrs/garage`），named volume 持久化（meta / data，容器重启、重建、`down` 均不丢数据），与 Repository 同 `ganrag` 网络；garage.toml 由 compose `configs` 注入（敏感值经 `.env` 插值，镜像 `FROM scratch` 无 shell，不做容器内渲染）：

| 端口 | 用途 |
|------|------|
| 3900 | S3 API（Repository 访问；dev 映射 127.0.0.1 便于本地调试，浏览器默认不直连 Garage——读写均经 gRPC 流式） |
| 3901 | RPC（仅容器网络内） |
| 3903 | Admin API（绑定 127.0.0.1，运维用） |

不启用 s3_web（3902 网页托管展示模块）：内容只经 S3 API 读取，省一个常驻监听模块。
首次启动 `server --single-node --default-bucket` 按 `GARAGE_DEFAULT_*` 自动创建默认 Access Key 与私有桶。
默认 Access Key / Secret Key / Bucket 由 `.env` 注入，不进仓库。

## 移除 File Store 的范围（✅ 已完成 2026-10-17）

删除：

- `service/file-store/`（cmd、internal、configs、scripts、Dockerfile、Taskfile、docs、bin、logs、data）
- `api/proto/file-store/`
- `api/gen/file-store/`
- File Store 端口 50054、Docker 镜像任务、run/build 脚本

更新：根 `AGENTS.md`、`README.md`、`docs/architecture.md`、`docs/product.md`、`docs/data-model.md`、`docs/database.md`、`docs/todo.md`、`service/gateway/docs/architecture.md`、根 `docker-compose.yml`、`.env.example`。

## 相关待办

按 [docs/todo.md](../../../docs/todo.md) 优先级顺序：

- 「架构迁移（移除 File Store 直连 Garage）」（✅已完成 2026-10-17）
- 「Repository 服务协议（proto）定义」（✅已完成 2026-10-18，[api/proto/repository/v1/repository.proto](../../../api/proto/repository/v1/repository.proto)）
- 「Repository 服务实现」
- 「Repository 服务架构文档补齐」（✅已完成，本文件即补齐内容）
