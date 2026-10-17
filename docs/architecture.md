# GanRAG 架构选型文档

## 概述

本文档记录 GanRAG 项目的总体架构选型，主要涉及服务间通信、公共基础设施等全局性技术决策。

- 产品形态、权限模型与分期路线：[docs/product.md](product.md)
- 数据模型与表结构：[docs/data-model.md](data-model.md)

## 服务架构

```
┌─────────────────────────────────────────────────────────────────┐
│                            Client                               │
└─────────────────────────────────────────────────────────────────┘
                                 │
                                 ▼
┌─────────────────────────────────────────────────────────────────┐
│                       Gateway Service                           │
│                    路由、认证、限流、负载均衡                   │
│                    详见 service/gateway/docs/                   │
└─────────────────────────────────────────────────────────────────┘
                                 │
          ┌──────────────────────┼──────────────────────┐
          ▼                      ▼                      ▼
┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│   AI Service    │  │  User Service   │  │    Repository   │
│     (Eino)      │  │                 │  │     Service     │
│    RAG 处理     │  │   用户管理      │  │   知识库管理    │
│   详见 docs     │  │                 │  │                 │
└─────────────────┘  └─────────────────┘  └─────────────────┘
          │                                       │
          │         基础设施服务（gRPC）          │
          └───────────────────┬───────────────────┘
                              ▼
                    ┌─────────────────┐
                    │  Vector Store   │
                    │   向量化存储    │
                    │  详见 docs      │
                    └─────────────────┘

  对象存储（S3 协议，唯一直接访问方 = Repository）：
  前端 → Gateway（验签/路由）→ Repository（权限 + Presigned URL）→ Garage（S3）
```

## 技术选型

### 服务间通信

**选型：gRPC**

- 高性能：基于 HTTP/2，支持多路复用
- 强类型：通过 Protobuf 定义接口，编译时类型检查
- 跨语言：支持多种编程语言
- 生态丰富：支持拦截器、负载均衡、服务发现等

### 数据库

**选型：MySQL**

- 成熟稳定：广泛使用，社区活跃
- 功能完善：支持事务、索引、外键等
- 性能优异：适合读写混合场景

**ORM：GORM**

- 功能完善：关联、事务、钩子等
- 代码生成：配合 Generator 生成模型代码
- 扩展性强：支持自定义数据类型、插件

**迁移工具：golang-migrate**

- SQL 支持：可直接从 SQL 语句导入
- 成对迁移：每个迁移包含 up.sql 和 down.sql
- 版本控制：支持版本号管理和回滚

### 缓存

**选型：Redis**

- 高性能：内存数据库，读写速度快
- 数据结构丰富：支持 String、Hash、List、Set 等
- 持久化：支持 RDB 和 AOF

### 日志

**选型：slog + lumberjack**

Go 标准库结构化日志 + 日志文件轮转：

- slog：Go 1.21+ 标准库，零依赖
- lumberjack：日志文件切割、轮转、压缩

**日志输出：**
- 控制台：便于开发调试
- 文件：便于生产环境日志收集

### 配置管理

**YAML 配置：Viper**

- 结构化配置：支持嵌套结构
- 类型安全：编译时检查
- 环境覆盖：支持环境变量覆盖
- 多格式支持：YAML、JSON、TOML 等

**环境变量：GoDotEnv**

- 标准化：遵循 .env 文件标准
- 简单易用：一行代码加载
- 安全性：敏感信息不入代码库

### 文档转换（PDF / Word / HTML）

> **落地状态：设计定稿，代码未实现**（P1.5）——实现后更新本文「设计落地状态」。

**选型：markitdown**（微软开源，MIT）

- 通用文档转 Markdown：PDF / Word（docx）/ HTML → md，保留标题、列表、表格、链接结构
- 离线转换：内置转换器本地运行，无云依赖（`pip install 'markitdown[pdf,docx]'`，HTML 为核心内置）
- 集成方式：Python CLI 子进程，stdin 喂字节流、stdout 收 md（官方安全口径 `convert_stream`，不拼文件路径、不解析 URI，防路径穿越与 SSRF）
- **不做 OCR**：只提取文本层，文档内嵌图片中的文字不提取、不参与向量化——前端须明示此限制；扫描件（无文字层）转换为空，拒绝上传

## 技术栈

### 前端

| 层级 | 选型 | 说明 |
|------|------|------|
| 框架 | React + TypeScript | UI 组件化、类型安全 |
| AI 聊天 | Vercel AI SDK | 流式聊天、状态管理 |
| Markdown | streamdown | 流式渲染、代码高亮 |
| 构建工具 | Vite | 快速、轻量 |
| 状态管理 | Zustand | 轻量级 |
| UI 组件库 | Shadcn/ui + Aceternity UI | 基础组件 + 炫酷效果 |
| 样式方案 | Tailwind CSS | 原子化 CSS |
| 动画引擎 | Framer Motion | 流畅动画 |
| HTTP 请求 | Axios | 通用请求 |
| 路由 | React Router | 官方路由 |
| 表单 | React Hook Form | 高性能表单 |

### 后端

**Go 版本：1.27**

| 层级 | 选型 | 说明 |
|------|------|------|
| HTTP 框架 | Hertz | Gateway 使用，详见 service/gateway/docs/ |
| AI 框架 | Eino | AI Service 使用，详见 service/ai/docs/ |
| 服务间通信 | gRPC | 内部服务通信 |
| 数据库 | MySQL | 主数据存储 |
| ORM | GORM | 数据库访问 + Generator 代码生成 |
| 迁移工具 | golang-migrate | SQL 导入，up/down 成对 |
| 缓存 | Redis | 高速缓存 |
| 日志 | slog + lumberjack | 标准库结构化日志 + 文件轮转 |
| 配置(YAML) | Viper | 结构化配置 |
| 配置(.env) | GoDotEnv | 环境变量 |
| 向量存储 | chromem-go | 纯 Go 内嵌向量库，详见 service/vector-store/docs/ |
| 对象存储 | Garage（S3 协议） | 通用 S3，直连可迁移 MinIO/AWS S3/R2；Repository 是唯一直接访问方，详见 [service/repository/docs/architecture.md](../../service/repository/docs/architecture.md)「对象存储与文件访问」 |
| 文档转换 | markitdown (Python CLI) | PDF / Word / HTML → md，上传时转换，详见 docs/product.md |

## 各服务架构详情

| 服务 | 架构文档 |
|------|----------|
| Gateway | [service/gateway/docs/architecture.md](../../service/gateway/docs/architecture.md) |
| AI Service | [service/ai/docs/architecture.md](../../service/ai/docs/architecture.md) |
| User Service | [service/user/docs/architecture.md](../../service/user/docs/architecture.md) |
| Repository Service | [service/repository/docs/architecture.md](../../service/repository/docs/architecture.md) |
| Vector Store | [service/vector-store/docs/architecture.md](../../service/vector-store/docs/architecture.md) |

## 设计落地状态

> 已定稿但尚未实现的设计汇总。**实现完成时更新状态、新增设计文档必须登记于此**，防止文档与代码脱节。
> 状态图例：📝 设计定稿未实现 / 🔨 实现中 / ✅ 已实现。
> **执行跟踪以 [docs/fix.md](fix.md) / [docs/todo.md](todo.md) 为准**（对应编号见「详见」列）；本表只负责设计文档状态，不再另立待办清单。

| 设计项 | 状态 | 阶段 | 详见 |
|--------|------|------|------|
| **移除 File Store，直连 Garage（S3）单桶 + Presigned URL 文件访问** | 🔨 实现中（架构迁移已落地 2026-10-17：File Store 已移除、0006 已迁移、Garage 基础设施就绪；Presigned 读取与资源接口随「Repository 服务实现」落地） | P1 架构迁移 | [service/repository/docs/architecture.md](../../service/repository/docs/architecture.md)（「架构迁移（移除 File Store 直连 Garage）」） |
| Markdown 相对路径解析与 `attachments.relative_path` | 🔨 实现中（`relative_path` 已随 0006 落库 2026-10-17；解析逻辑随 Repository / 前端实现） | P1 架构迁移 | [service/repository/docs/architecture.md](../../service/repository/docs/architecture.md)、[data-model.md](data-model.md)（「架构迁移（移除 File Store 直连 Garage）」） |
| 多智能体路由与切换（Decima-small） | 📝 设计定稿，代码未实现 | P1.5 | [service/ai/docs/architecture.md](../../service/ai/docs/architecture.md)（「多智能体路由与切换」） |
| 上下文压缩（会话级 context + 消息游标 + 隐藏 Agent 提示词） | 📝 设计定稿，代码未实现 | P1.5 | [service/ai/docs/architecture.md](../../service/ai/docs/architecture.md)、[data-model.md](data-model.md)（「上下文压缩」） |
| 多路召回聚合与 Decima-small 相关性审查 | 📝 设计定稿，代码未实现 | P3 | [service/ai/docs/architecture.md](../../service/ai/docs/architecture.md)（「多路召回聚合与检索权重接入」） |
| 文档上传（pdf / docx / html → markitdown） | 📝 设计定稿，代码未实现 | P1.5 | 本文「文档转换」、[product.md](product.md)（「文档上传」） |
| 向量化按模型存储与检索路由 | 📝 设计定稿，代码未实现 | P1.5 | [service/vector-store/docs/architecture.md](../../service/vector-store/docs/architecture.md)（「向量化按模型存储与检索路由」） |
| chat_sessions.agent / context / context_msg_id 字段 | 🗄 表结构已设计，随 P1 建表落地 | P1 | [data-model.md](data-model.md)（「AI 服务实现」） |

## 决策记录

| 日期 | 决策 | 原因 |
|------|------|------|
| 2026-09-12 | 采用 gRPC 做服务间通信 | 高性能、强类型、跨语言 |
| 2026-09-12 | 采用 MySQL 作为主数据库 | 成熟稳定、功能完善 |
| 2026-09-12 | 采用 Redis 作为缓存 | 高性能、数据结构丰富 |
| 2026-09-12 | 采用 Viper 读取 YAML 配置 | 结构化、类型安全 |
| 2026-09-12 | 采用 GoDotEnv 读取 .env | 标准化、安全 |
| 2026-09-12 | 采用 slog + lumberjack 作为日志方案 | 标准库、文件轮转 |
| 2026-09-12 | 采用 GORM 作为 ORM | 功能完善、代码生成 |
| 2026-09-12 | 采用 golang-migrate 做数据库迁移 | SQL 导入、up/down 成对 |
| 2026-09-12 | 自建 Vector Store 服务 | Go 内嵌向量库 + gRPC |
| 2026-09-12 | 自建 File Store 服务 | 本地文件模拟 + gRPC |
| 2026-10-09 | 定稿 File Store 接口：一元 RPC 整体传输、同 key 覆盖语义、JSON 元数据、端口 50054 | 文件以 <10MB 小文件为主，流式属过度设计；upsert 语义与 vector-store 一致；详见 service/file-store/docs/architecture.md |
| 2026-10-09 | User 服务采用双 token 认证：user 签发（access JWT 15m + refresh 不透明串 7 天）、gateway 验签校验 | 签发与校验分离；access 短期 + refresh 轮换与吊销，无需 Redis 会话黑名单；详见 service/user/docs/architecture.md |
| 2026-10-09 | MySQL 由仓库根 docker-compose.yml 统一编排，Task 新增 infra 任务 | MySQL 为 user/repository 等服务共享基础设施，不属于单一服务；Redis 暂不引入 |
| 2026-10-09 | User 服务第一期功能范围：注册、登录、刷新、查自己、修改密码 | 最小可用闭环；OAuth、找回密码、管理接口等待需求明确（YAGNI） |
| 2026-10-09 | 端口规划：gateway 8080、user 50051、repository 50052、ai 50055 | 承接 vector-store 50053、file-store 50054 的顺序分配，见 service/user/docs/architecture.md |
| 2026-10-09 | 产品形态定稿：GitHub 式三栏界面；左栏只展示「我的 + 成员的」，star/关注列表在个人页面，他人的库靠搜索发现 | 左栏保持高频工作集不被低频内容淹没；关系状态仍全部存储供检索权重使用；详见 docs/product.md |
| 2026-10-09 | 知识库权限三级：owner / editor / reader，公开库陌生人只读（由 visibility 推导、不建成员行） | 覆盖「私有库加成员、公开库提升权限」两种诉求；陌生人权限推导省去大量冗余行 |
| 2026-10-09 | md 图片与互链保存时解析为附件/文章 ID 的稳定 URL，私有库图片经 gateway 鉴权访问 | 文章移动/重命名不打断引用；鉴权保证私有库图片不泄露；详见 docs/data-model.md |
| 2026-10-09 | 聊天会话绑定检索范围（单库或全局，kb_id=0 表示全局）；关系状态（own/member/star/follow）作为检索权重信号 | 检索范围随会话固化保证历史会话可复现；权重映射由 AI 服务配置持有，P3 调参 |
| 2026-10-09 | 表结构 P1 一次设计到位，接口/界面按 P1 内容聊天、P2 社交、P3 权重三期交付 | 避免后期破坏性迁移；关系状态从第一天起即为完整事实；详见 docs/data-model.md |
| 2026-10-11 | Gateway 第一期落地：Hertz 路由 + access token 本地验签 + 手写 HTTP→gRPC 转发（先接 user 5 个 RPC）+ 双维度令牌桶限流（auth 1/5、default 10/20，超限 429 统一错误体） | 手写 handler 少依赖、错误映射可控；限流按「登录用户 / 匿名 IP」分桶防爆破；熔断与限流灰度策略等需求出现再加（YAGNI）；详见 service/gateway/docs/architecture.md |
| 2026-10-16 | AI 服务智能体路由与召回审查选型 Decima-small（Apache-2.0，本地 pip 部署） | 结构化输出 choice / noul 及概率；路由和候选审查均由 Go 通过 HTTP 调用 `python -m decima.serve`，不在 Go 内嵌模型运行时；详见 service/ai/docs/architecture.md |
| 2026-10-16 | Agent 切换采用「当前概率阈值 + 全体 Agent 重新选择」 | 当前 Agent 概率达到阈值则保持；低于阈值才取全体最高概率者，最高者仍为当前 Agent 时不切换，避免仅因 top 结果抖动而切换 |
| 2026-10-16 | OpenAI 提示词采用固定通用 System + User 类型隐藏 Agent 提示词 | Agent 提示词用特殊标签注入；切换时追加到上下文末尾；压缩时只保留最新段并移到摘要前；`agent-system` 不进入用户展示和上下文搜索 |
| 2026-10-16 | 多路召回采用权限前置、向量/关键词合并、关系/时间/热度重排和 Decima-small 审查 | 权限不可被排序信号覆盖；保留各信号支持解释、调参和消融，避免静态拍脑袋排序 |
| 2026-10-15 | 多智能体能力列为 P1.5 独立阶段，表字段 P1 一次设计到位 | P1 先单智能体闭环；chat_sessions.agent / chat_messages.agent 随 P1 建表落地，避免破坏性迁移；详见 docs/data-model.md |
| 2026-10-15 | 文章支持 pdf / docx / html 上传：markitdown 转 md 存入 articles.content，原文附件留档，内容只读 | 向量化 / 溯源引用 / 前端渲染统一走 content 口径，重建索引免重跑转换、不受版本漂移影响；不做 OCR（内嵌图片文字不进检索，前端明示）；详见 docs/product.md |
| 2026-10-15 | 模型上下文采用「会话级 context + 消息游标」替换式压缩，不落消息表 | 消息表保持纯对话，模型上下文结构性不进用户页面；多次压缩直接替换无堆积；原文不删、search_context 仍可溯；详见 docs/data-model.md |
| 2026-10-15 | 向量化结果按「知识库 × 向量化模型」分集合存储（`kb-<kb_id>-<model_id>`），检索按模型路由 | 不同模型维度不同、同集合维度必须一致，只能分集合；切换模型全量重建新集合、旧集合保留期可回滚；详见 service/vector-store/docs/architecture.md |
| 2026-10-15 | 文档新增「设计落地状态」聚合表 + 各设计章节就近状态标注 | 已定稿未实现的设计集中登记，防止文档与代码状态脱节；实现时必须同步更新 |
| 2026-10-15 | 数据库禁止物理外键，一律逻辑引用；建立 docs/fix.md / docs/todo.md 待完成登记制度 | 外键耦合迁移、违背服务独立；待完成事项统一登记跟踪，防止文档与代码脱节；详见根 AGENTS.md「数据库设计规范」「待完成事项管理」 |
| 2026-10-15 | 登录标识改邮箱（email 唯一，username 降为可重复展示名、删 nickname）；refresh_tokens 改 sessions 会话表（每设备一行，多端登录 + 设备管理）；引入 Redis 存邮箱验证码（注册/找回密码） | 登录/找回密码统一走邮箱；会话按设备建模支撑设备管理；验证码的过期与一次性语义由 Redis 天然承担不建表；详见 docs/data-model.md、service/user/docs/architecture.md |
| 2026-10-15 | repository 数据库重设计（8 表→5 表）：正文与转换结果存 file-store（`articles.content_key`）、MySQL 不存内容；文章互链不建表即 Markdown 链接；对外实体加 XID 业务主键；附件挂 article_id 吸收绑定表；kb_members+kb_stars 合并 kb_user_relations | 内容是大对象不占 MySQL 行、重建索引免重跑转换；关联即正文本身不需关系表；自增 id 不出服务防遍历；表少口径清；覆盖 2026-10-09「前端渲染统一走 content」口径（改为 pdf/docx/html 展示源文件、content 仅供向量化/AI）；详见 docs/data-model.md「ganrag_repository」、根 AGENTS.md「业务主键（XID）规范」 |
| 2026-10-15 | 聊天记录按「消息信封 + 全量步骤」存储（chat_messages + chat_message_parts），步骤 type/meta 对齐 Vercel AI SDK `UIMessage` part 模型、payload 原样存；消息加 xid、步骤不加；聊天交易型短文本入 MySQL | 每步（思考/工具调用/来源）全量存储支撑任意后加展示、扩展零 DDL；历史无损重组喂 useChat(initialMessages)，工具/思考/来源原生展示；RAG 来源存 article_xid 点击跳文章；详见 docs/data-model.md「ganrag_ai」 |
| 2026-10-17 | **移除 File Store，直连 Garage（S3）**：删除 service/file-store 与 api/ 下 FileStore proto/gen；Repository 为唯一直接访问 Garage 的服务，Gateway 只验签/路由，前端只认 Gateway 稳定地址 | 通用 S3 协议后续可迁 MinIO/AWS S3/R2 只改配置；S3 凭证收敛一处；移除自建本地文件 + JSON 索引维护；详见 service/repository/docs/architecture.md |
| 2026-10-17 | 对象存储采用「单共享桶私有 + 统一对象 key `o/<object_xid>`」，不按知识库分桶、不设图片专属目录 | 「共享桶」≠ 匿名公开桶，桶 ACL 私有防绕过 Gateway；统一 key 与目录/重命名/知识库迁移解耦，迁移 S3 时 key 原样平移 |
| 2026-10-17 | 文件访问走「Gateway 稳定路由 → Repository 权限校验 → Presigned URL 302」；Markdown 内只写相对路径，前端渲染时以当前文章为基准解析并拼 Gateway 地址 | 权限判定收敛 Repository（知识库领域边界），Gateway 不复制规则；相对路径由 Repository 再规范化校验防越权；Markdown 不存 Presigned URL（会过期）；详见 service/repository/docs/architecture.md |
| 2026-10-17 | `attachments` 增 `relative_path`（article_id + relative_path 唯一），支撑「当前文章 + 相对路径」稳定找回对象；迁移前先 `task db-status`，已应用迁移禁改，新增 up/down 成对 | 用户自定义 Markdown 相对引用需持久化才能渲染期解析；见 docs/data-model.md「ganrag_repository」 |
| 2026-10-17 | Garage 不启用 s3_web 网页托管展示模块（无 3902 端口）；数据卷 named volume 持久化，garage.toml 经 compose configs 注入、密钥走 .env，首启 `--single-node --default-bucket` 自动建桶与密钥 | 内容只经 S3 API 读取，桶私有无网站托管需求，省一个常驻监听模块；重启/重建容器不丢数据，秘密不入库 |
