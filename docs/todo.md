# todo — 待完成记录

> 用途与使用规则见 [AGENTS.md](../AGENTS.md)「待完成事项管理」。一条记录一行、一个事项一条；完成只改状态列，**禁止删除记录**。
> 状态取值：`⬜待处理` / `🔨进行中` / `✅已完成(YYYY-MM-DD)`。编号 `TODO-NNN` 递增、不复用、不重排。
> 收录范围：未完成的功能、优化、文档任务。明确不做（YAGNI）的事项不登记，需求明确后再登记。

| ID | 记录 | 登记日期 | 状态 |
|----|------|---------|------|
| TODO-001 | Repository 服务实现（建表迁移已交付 2026-10-15：`pkg/db/migrations/ganrag_repository/0001-0005`；正文/附件走 file-store、互链即 Markdown 链接、接口用 xid，见 [database.md](database.md)「ganrag_repository」） | 2026-10-15 | ⬜待处理 |
| TODO-002 | AI 服务实现（建表迁移已交付 2026-10-15：`pkg/db/migrations/ganrag_ai/0001-0003`；消息信封 + 全量步骤存储对齐 UIMessage，见 [database.md](database.md)「ganrag_ai」） | 2026-10-15 | ⬜待处理 |
| TODO-003 | 多智能体路由与切换（Laya）（P1.5，见 [service/ai/docs/architecture.md](../service/ai/docs/architecture.md)「多智能体架构」） | 2026-10-15 | ⬜待处理 |
| TODO-004 | 上下文压缩（会话级 context + 消息游标，替换式压缩不落消息表）（P1.5，见 [service/ai/docs/architecture.md](../service/ai/docs/architecture.md)、[data-model.md](data-model.md)「关系状态推导」） | 2026-10-15 | ⬜待处理 |
| TODO-005 | 文档上传（pdf / docx / html → markitdown 转 md 存 articles.content，不做 OCR）（P1.5，见 [architecture.md](architecture.md)「文档转换」、[product.md](product.md)） | 2026-10-15 | ⬜待处理 |
| TODO-006 | 向量化按模型存储与检索路由（按「知识库 × 向量化模型」分集合）（P1.5，见 [service/vector-store/docs/architecture.md](../service/vector-store/docs/architecture.md)「集合与模型约定」） | 2026-10-15 | ⬜待处理 |
| TODO-007 | web 前端实现（P1/P2，现仅 docs；目录结构待定，见 [web/docs/architecture.md](../web/docs/architecture.md)） | 2026-10-15 | ⬜待处理 |
| TODO-008 | P2 社交接口与界面（star / follow / 搜索 / 个人页面；表已建齐只开接口，见 [product.md](product.md)「分期路线」） | 2026-10-15 | ⬜待处理 |
| TODO-009 | P3 检索权重接入与调参（关系状态 → 权重映射，AI 服务配置持有，见 [product.md](product.md)「检索权重」） | 2026-10-15 | ⬜待处理 |
| TODO-010 | Repository 服务架构文档补齐（[architecture.md](architecture.md)「各服务架构详情」现为 `-`） | 2026-10-15 | ⬜待处理 |
| TODO-011 | users 相关代码适配（建表迁移已交付 2026-10-15）：注册/登录改「邮箱+密码」、注册接邮箱验证码、执行 `task db-gen` 并适配 store/handler 到新表结构 | 2026-10-15 | ⬜待处理 |
| TODO-012 | 会话模型代码适配（sessions 表迁移已交付 2026-10-15）：多端登录、(user_id, device_id) upsert、轮换原地更新 `token_hash`、改密/重置删全部会话、惰性过期清理 | 2026-10-15 | ⬜待处理 |
| TODO-013 | 引入 Redis（compose + 配置 + go-redis）+ 邮箱验证码落 Redis（`user:email_code:<scene>:<email>`，TTL 5 分钟、一次性、失败 5 次作废、发送 60s 限频）+ 验证码邮件发送通道（SMTP 落地时定） | 2026-10-15 | ⬜待处理 |
| TODO-014 | 忘记密码接口（发码/验码/重置密码，重置成功后吊销该用户全部 sessions，见 [service/user/docs/architecture.md](../service/user/docs/architecture.md)） | 2026-10-15 | ⬜待处理 |
| TODO-015 | 登录设备管理接口（设备列表 / 踢设备下线，基于 `sessions.device_id` / `device_info` / `last_active_at`） | 2026-10-15 | ⬜待处理 |
| TODO-016 | 对外接口 / proto 改用 xid（`users` / `sessions` / `chat_sessions` / `chat_messages` 的 xid 列已随迁移建好 2026-10-15；剩接口、JWT 口径与前端联调适配，对齐 AGENTS.md「业务主键（XID）规范」） | 2026-10-15 | ⬜待处理 |
| TODO-017 | 公共 XID 封装（`pkg/xid`：github.com/rs/xid 生成 + CHAR(20) 存取/校验约定），各服务统一引用 | 2026-10-15 | ⬜待处理 |
| TODO-018 | 网关 / AI 服务流式协议对齐 Vercel AI SDK UI message stream（SSE：start / step-start / text-delta / reasoning-delta / tool-* / source / finish），支撑 useChat 原生消费（见 [web/docs/architecture.md](../web/docs/architecture.md)） | 2026-10-15 | ⬜待处理 |
