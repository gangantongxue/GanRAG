# todo — 待完成记录

> 用途与使用规则见 [AGENTS.md](../AGENTS.md)「待完成事项管理」。一条记录一行、一个事项一条；完成只改状态列，**禁止删除记录**。
> 状态取值：`⬜待处理` / `🔨进行中` / `✅已完成(YYYY-MM-DD)`。
> **不设序号，按优先级从上到下排列**：后续执行按表格顺序进行；新条目按优先级插入对应位置，已完成条目移至表格末尾。

| 记录 | 登记日期 | 状态 |
|------|---------|------|
| env 配置统一到仓库根 `.env`（服务启动自动加载根 `.env`、删 `service/user/.env`、根 `.env` 补齐 JWT/Redis/邮件占位等全部变量，`.env.example` 与文档同步） | 2026-10-17 | ✅已完成(2026-10-17) |
| 引入 Redis（compose + 配置 + go-redis）+ 邮箱验证码落 Redis（`user:email_code:<scene>:<email>`，TTL 5 分钟、一次性、失败 5 次作废、发送 60s 限频）+ 验证码邮件发送通道（SMTP 落地时定） | 2026-10-15 | ✅已完成(2026-10-17) |
| users 相关代码适配（建表迁移已交付 2026-10-15）：注册/登录改「邮箱+密码」、注册接邮箱验证码、执行 `task db-gen` 并适配 store/handler 到新表结构 | 2026-10-15 | ✅已完成(2026-10-17) |
| Repository 服务实现（阶段二：目录树 5 RPC + 文章 6 RPC + `GetResource` + `ResolveReference`，正文/附件流式读写与相对路径解析） | 2026-10-18 | ⬜待处理 |
| Repository 服务实现（阶段三：分片上传 5 RPC：`InitUpload`/`UploadPart`/`GetUploadStatus`/`CompleteUpload`/`AbortUpload`，断点续传与三层完整性校验） | 2026-10-18 | ⬜待处理 |
| async_tasks 重试 worker + 后台查询接口（按 `status=0` 集中处理，展示 `source_service`/`task_type`/`payload`/`last_error`） | 2026-10-18 | ⬜待处理 |
| 其他服务接入 `pkg/asynctask` 失败上报（AI / vector-store / user 按需） | 2026-10-18 | ⬜待处理 |
| AI 服务实现（建表迁移已交付 2026-10-15：`pkg/db/migrations/ganrag_ai/0001-0005`；消息信封 + 全量步骤存储对齐 UIMessage，见 [database.md](database.md)「ganrag_ai」） | 2026-10-15 | ⬜待处理 |
| 对外接口 / proto 改用 xid——**剩余范围**：`chat_sessions` / `chat_messages` 接口 xid 化与前端联调适配（users / sessions 的 proto、JWT 口径已随「users 相关代码适配」于 2026-10-17 落地，对齐 AGENTS.md「业务主键（XID）规范」） | 2026-10-15 | ⬜待处理 |
| 网关 / AI 服务流式协议对齐 Vercel AI SDK UI message stream（SSE：start / step-start / text-delta / reasoning-delta / tool-* / source / finish），支撑 useChat 原生消费（见 [web/docs/architecture.md](../web/docs/architecture.md)） | 2026-10-15 | ⬜待处理 |
| web 前端实现（P1/P2，现仅 docs；目录结构待定，见 [web/docs/architecture.md](../web/docs/architecture.md)） | 2026-10-15 | ⬜待处理 |
| 忘记密码接口（发码/验码/重置密码，重置成功后吊销该用户全部 sessions，见 [service/user/docs/architecture.md](../service/user/docs/architecture.md)） | 2026-10-15 | ⬜待处理 |
| 登录设备管理接口（设备列表 / 踢设备下线，基于 `sessions.device_id` / `device_info` / `last_active_at`） | 2026-10-15 | ⬜待处理 |
| 文档上传（pdf / docx / html → markitdown 转 md 存 articles.content，不做 OCR）（P1.5，见 [architecture.md](architecture.md)「文档转换」、[product.md](product.md)） | 2026-10-15 | ⬜待处理 |
| 多智能体路由与切换（Decima-small：当前 Agent 概率阈值低于设定值后，在全部 Agent 中重新选择）（P1.5，见 [service/ai/docs/architecture.md](../service/ai/docs/architecture.md)「多智能体架构」） | 2026-10-15 | ⬜待处理 |
| 上下文压缩（约 65% 容量触发；会话级 context + 消息游标；只保留最新隐藏 Agent 提示词并置于摘要前）（P1.5，见 [service/ai/docs/architecture.md](../service/ai/docs/architecture.md)、[data-model.md](data-model.md)「关系状态推导」） | 2026-10-15 | ⬜待处理 |
| 向量化按模型存储与检索路由（按「知识库 × 向量化模型」分集合）（P1.5，见 [service/vector-store/docs/architecture.md](../service/vector-store/docs/architecture.md)「集合与模型约定」） | 2026-10-15 | ⬜待处理 |
| P2 社交接口与界面（star / follow / 搜索 / 个人页面；表已建齐只开接口，见 [product.md](product.md)「分期路线」） | 2026-10-15 | ⬜待处理 |
| P3 多路召回聚合与检索权重接入（权限过滤 → 向量/关键词召回 → 关系/时间/热度重排 → Decima-small 审查，支持调参与消融，见 [product.md](product.md)「检索权重」） | 2026-10-15 | ⬜待处理 |
| 更新 Decima-small 多智能体路由、隐藏 Agent 系统提示词、65% 上下文压缩及向量/关键词双路召回聚合排序设计文档 | 2026-10-16 | ✅已完成(2026-10-16) |
| Repository 服务架构文档补齐（[architecture.md](architecture.md)「各服务架构详情」现为 `-`） | 2026-10-15 | ✅已完成(2026-10-17) |
| **【最高优先级·架构迁移】移除 File Store 服务，直连 Garage（S3）**：删除 `service/file-store/`、`api/proto/file-store/`、`api/gen/file-store/`；Repository 为唯一直接访问 Garage 的服务；单私有桶 + 统一对象 key `o/<object_xid>`；`attachments` 增 `relative_path`（迁移前先 `task db-status`）；前端 Markdown 相对路径 → Gateway `/api/v1/articles/{xid}/resource?path=` → Repository 权限校验 → Presigned URL 302；设计见 [service/repository/docs/architecture.md](../service/repository/docs/architecture.md)「对象存储与文件访问」 | 2026-10-17 | ✅已完成(2026-10-17) |
| 公共业务主键封装（`pkg/eid`：github.com/rs/xid 生成 `<前缀>_<xid>`（3 字母小写前缀按实体类型区分，如 `usr_`/`art_`），库存全串 `xid VARCHAR(32)`（迁移 `0004/0007_xid_prefix`），各服务统一引用、不直接用裸 xid；实体创建接线随「users 相关代码适配」「Repository/AI 服务实现」各自落地） | 2026-10-15 | ✅已完成(2026-10-17) |
| 会话模型代码适配（sessions 表迁移已交付 2026-10-15）：多端登录、(user_id, device_id) upsert、轮换原地更新 `token_hash`、改密/重置删全部会话、惰性过期清理；设备 ID 按方案 A 服务端生成/核对（取值优先级：请求体 > Gateway `did` cookie > 服务端生成 xid；Gateway 设 cookie + 登录响应回填前端 localStorage），详见 [data-model.md](data-model.md)「设备标识（device_id）口径」 | 2026-10-15 | ✅已完成(2026-10-17) |
| Repository 服务协议（proto）定义（`api/proto/repository/v1/repository.proto`，P1 内容闭环 23 RPC：知识库/目录树/文章 CRUD、互链解析、正文与资源 gRPC 流式读取（Garage 不对外暴露）、整段+会话式分片上传（断点续传 `GetUploadStatus`；三层完整性校验：单片 sha256 / 合并前结构校验 / 合并后读回 sha256 对比声明值）；`task proto` 生成，`pkg/eid` 注册上传会话前缀 `upl`） | 2026-10-18 | ✅已完成(2026-10-18) |
| 公共 async_tasks 与服务失败记录（`ganrag_common.async_tasks` + `pkg/asynctask`，业务事务内同事务 outbox，供 Repository 及后续服务 import；含 `task_type`/`payload`/幂等键/状态流转） | 2026-10-18 | ✅已完成(2026-10-18) |
| Repository 服务实现（阶段一：store/handler/gRPC server、知识库 CRUD、Garage 整段上传 `UploadWhole`、权限校验、删除走 async_tasks outbox；接口用 xid，见 `api/proto/repository/v1`、[database.md](database.md)「ganrag_repository」） | 2026-10-15 | ✅已完成(2026-10-18) |
