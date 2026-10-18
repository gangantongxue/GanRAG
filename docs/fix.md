# fix — 缺陷与修复记录

> 用途与使用规则见 [AGENTS.md](../AGENTS.md)「待完成事项管理」。一条记录一行、一个事项一条；完成只改状态列，**禁止删除记录**。
> 状态取值：`⬜待处理` / `🔨进行中` / `✅已完成(YYYY-MM-DD)`。编号 `FIX-NNN` 递增、不复用、不重排。
> 收录范围：缺陷、bug、与规则/文档不符的实现、需要修复性迁移的变更。

| ID | 记录 | 登记日期 | 状态 |
|----|------|---------|------|
| FIX-001 | `refresh_tokens` 含物理外键 `fk_refresh_tokens_user`，违反「禁止物理外键」规则 → 随 2026-10-15 迁移基线重写消除（数据盘已清、旧迁移全删重写，新迁移无 `refresh_tokens`、全部无外键） | 2026-10-15 | ✅已完成(2026-10-15) |
| FIX-002 | `task db-gen` 报错「迁移目录 ganrag_ai 未在 pkg/db/gen 的 schemata 中登记表清单」：2026-10-15 交付 `ganrag_ai`/`ganrag_repository` 迁移后未登记 schemata，db-gen 全程不可用，违反「表结构变更后必须 task db-gen」规则 | 2026-10-17 | ✅已完成(2026-10-17) |
| FIX-003 | 跨服务逻辑引用存的是目标自增 ID（repository `owner_id`/`created_by`、ai `user_id`/`kb_id`），下游服务拿不到 UserService/Repository 的自增 ID，无法解析归属 → 统一口径：同服务引用保留自增 ID 并冗余 `*_xid`，跨服务引用只存目标 xid；对象引用同时冗余 `content_object_xid`/`object_xid`；迁移 user 0005 / repository 0008 / ai 0005（当前全表为空） | 2026-10-18 | ✅已完成(2026-10-18) |
| FIX-004 | Garage 删除失败只写日志，无法集中重试与后台展示 → 新增公共 schema `ganrag_common.async_tasks` 与共享包 `pkg/asynctask`，业务事务内同事务写 outbox，成功/失败回写状态；其他服务直接 import 包使用，不新增独立服务 | 2026-10-18 | ✅已完成(2026-10-18) |
