# GanRAG 数据模型文档

## 概述

本文档定义全部业务表结构与设计决策（一次性设计，表结构一次到位、接口与界面分期交付，避免破坏性迁移）；**结构参考（每表完整 SQL + 字段 + 索引）见 [database.md](database.md)**。功能与术语见 [product.md](product.md)；账号域细节见 [service/user/docs/architecture.md](../service/user/docs/architecture.md)。

## 服务归属

MySQL 单实例（根目录 compose 编排），**每个服务一个 schema、一套独立迁移**，遵循「服务独立、互不引用」的项目约定；SQL 迁移文件集中存放在 `pkg/db/migrations/<库名>/`（由 `pkg/db` 统一同步与执行，修改规范见根 AGENTS.md「SQL 迁移修改规范」）：

| Schema | 拥有者 | 表 | 迁移位置 |
|--------|--------|----|----------|
| `ganrag_user` | User 服务 | `users`、`sessions`、`user_follows` | `pkg/db/migrations/ganrag_user/` |
| `ganrag_repository` | Repository 服务 | `knowledge_bases`、`directories`、`articles`、`attachments`、`kb_user_relations` | `pkg/db/migrations/ganrag_repository/` |
| `ganrag_ai` | AI 服务 | `chat_sessions`、`chat_messages`、`chat_message_parts` | `pkg/db/migrations/ganrag_ai/` |

**引用规则**：表间关联（无论同 schema 还是跨 schema）**一律逻辑引用，禁止物理外键**（见根 AGENTS.md「数据库设计规范」）——外键会把迁移耦合成必须按序执行，违背服务独立。引用完整性与级联删除由应用层保证；跨服务查询走 gRPC，不做跨 schema JOIN。

## ER 总览

```
ganrag_user                          ganrag_repository
┌────────────┐                       ┌─────────────────────┐
│   users    │◄──┐                   │  knowledge_bases    │
├────────────┤   │                   ├─────────────────────┤
│ id         │   │ owner_id          │ id / xid / owner_id │
│ email      │   ├───┐               │ name / visibility │
│ password_hash     │               │ article/star_count  │
│ ...        │   │   │               └──┬───────┬───────┬──┘
└─────┬──────┘   │   │                    │       │       │
      │          │   └──────────────────┘       │       │
      │ sessions（见 user 文档）          ┌───────┘       │
      │          │ ┌──────────────────┐  │   ┌───────────┴─┐
┌─────┴──────┐   │   │ kb_user_       │  │   │ directories │
│user_follows│   │   │ relations      │  │   ├─────────────┤
├────────────┤   │   ├────────────────┤  │   │ id / xid    │
│ follower_id│   │   │ kb_id          │  │   │ kb_id/parent│
│ followee_id│   │   │ user_id (逻辑)  │  │   │ name        │
└────────────┘   │   │ role / starred │  │   └─────────────┘
                 │   └────────────────┘  │
                 │                       │ ┌────────────────┐
                 │                       └▶│ articles       │
                 │                         ├────────────────┤
                 │                         │ id / xid       │
                 │                         │ kb_id / dir_id │
                 │                         │ type / title   │
                 │                         │ content_key ───┼─▶ Garage (S3)
                 │                         │ created_by     │   key: o/<object_xid>
                 │                         └───────┬────────┘
                 │                                 │
                 │                         ┌───────┴──────────┐
                 │                         │ attachments      │
                 │                         ├──────────────────┤
                 │                         │ xid / article_id │
                 │                         │ kind / file_key ─┼─▶ Garage (S3)
                 │                         └──────────────────┘
ganrag_ai
┌─────────────────┐   ┌──────────────────┐   ┌──────────────────┐
│ chat_sessions   │──▶│ chat_messages    │──▶│chat_message_parts│
├─────────────────┤   ├──────────────────┤   ├──────────────────┤
│ user_id / kb_id │   │ role / content   │   │ step / seq / type│
│ agent / context │   │ agent            │   │ content / meta   │
└─────────────────┘   └──────────────────┘   └──────────────────┘
```

## 关键设计决策

| 决策点 | 结论 | 原因 |
|--------|------|------|
| 文章内容存储 | 正文与转换结果一律存 **Garage（S3）**（`articles.content_key`，对象 key 统一 `o/<object_xid>`），MySQL 只存元数据与对象 key | 内容是大对象不占 MySQL 行；重建索引 / 换模型直接读对象存储，不重跑 markitdown |
| 引用方式 | 图片与互链以 **Markdown 链接**形式存在于正文（稳定 URL 指向 xid），**不建关系表** | 关联即正文内容本身；URL 按 xid，文章移动 / 改名不打断引用；断链渲染时标记 |
| 业务主键 | 对外展示实体一律加 `xid CHAR(20)`（XID），对外接口/URL/前端只用 xid；逻辑引用存自增 id | 自增 id 不出服务，防遍历枚举；见根 AGENTS.md「业务主键（XID）规范」 |
| 改写时机 | md 保存时解析相对路径 → 绑定附件/目标文章 → 改写为稳定 URL | 渲染端零解析成本；解析失败的链接保留原文，渲染时标记失效 |
| 根节点表示 | `parent_id` / `dir_id` / `kb_id`（会话）用 **0 表示根**，不用 NULL | MySQL 唯一索引对 NULL 不去重，0 值让 `UNIQUE(kb_id, parent_id, name)` 对根目录同样生效 |
| 标题约束 | 文章标题**不设唯一** | 引用靠 xid 不靠标题；同目录同名仅是编辑体验问题，由应用层提示 |
| 计数冗余 | `knowledge_bases` 冗余 `article_count`、`star_count` | 列表页高频展示，避免每行 COUNT；同事务内增减 |
| 登录标识 | `users.email` 唯一（登录/找回密码），`username` 仅展示名可重复 | 身份语义归邮箱；展示名无唯一约束避免改名冲突 |
| 会话模型 | refresh token 存储改为 `sessions` 表（每设备一行，`user_id`+`device_id` 唯一） | 支持多端登录与设备管理；轮换原地更新 `token_hash`，旧 token 复用按无效拒绝（不做重用检测） |
| 验证码存储 | 邮箱验证码存 Redis（TTL、一次性、发送限频），不建表 | 过期与一次性语义由 Redis 天然承担，见 [service/user/docs/architecture.md](../service/user/docs/architecture.md) |
| Redis Key 规范 | `<服务短名>:<域>:<标识>`（如 `user:email_code:register:<email>`） | 分层命名空间防撞名，服务短名与目录名一致；见根 AGENTS.md「Redis Key 规范」 |
| 附件清理 | 附件直接挂 `article_id`（`kind` 区分源文件 / 内嵌图片），保存时按正文链接重建 kind=2 并清孤儿 | 少一张绑定表；附件单文章独占，清理路径单一 |
| 关系单表 | `kb_user_relations` 单表承载成员（role）与收藏（starred）；owner 由 `knowledge_bases.owner_id` 表示，不入表 | 与「关系状态」口径对齐，少两张表；避免两处真相 |
| 权重不入库 | 关系状态存库，权重映射由 AI 服务配置持有 | 权重是排序策略、需频繁调参，不是业务事实；存库会把调优变成改数据 |
| 物理外键 | 全局禁止，一律逻辑引用（应用层保证完整性与级联删除） | 迁移独立、可并行开发；规则见根 AGENTS.md「数据库设计规范」 |
| 会话范围 | `chat_sessions.kb_id = 0` 表示全局 | 与根节点约定一致 |
| 聊天记录存储 | 消息信封 + 全量步骤两层（`chat_messages` + `chat_message_parts`）；步骤 type/meta 对齐 Vercel AI SDK `UIMessage` part 模型，payload 原样存 | 每步（思考/工具/来源）全量可存可展示，扩展展示零 DDL；历史无损重组为 `useChat(initialMessages)` |
| 内容入 MySQL 口径 | 文章/文档大对象不入 MySQL（存 Garage 对象存储，只存 key）；聊天消息、思考、工具结果、模型上下文等**交易型短文本入 MySQL** | 前者是文档大对象、后者高频按序读写且随事务；见根 AGENTS.md「数据库设计规范」 |

## 表结构 DDL

统一约定：`BIGINT UNSIGNED` 自增主键；`created_at` / `updated_at` 自动维护；`ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`。

### ganrag_repository（Repository 服务）

> **建表迁移已交付**（2026-10-15 迁移基线重写，`pkg/db/migrations/ganrag_repository/0001-0005`）；服务实现见 [todo.md](todo.md) 「Repository 服务实现」。统一约定补充：对外实体带 `xid CHAR(20)` 业务主键（见根 AGENTS.md「业务主键（XID）规范」）；完整结构参考见 [database.md](database.md)。

#### knowledge_bases 知识库

```sql
CREATE TABLE knowledge_bases (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid           CHAR(20)      NOT NULL,             -- XID 业务主键，对外标识
  owner_id      BIGINT UNSIGNED NOT NULL,           -- 逻辑引用 users.id
  name          VARCHAR(100)  NOT NULL,
  description   VARCHAR(500)  NOT NULL DEFAULT '',
  visibility    TINYINT       NOT NULL DEFAULT 0,   -- 0 私有 1 公开
  article_count INT UNSIGNED  NOT NULL DEFAULT 0,   -- 冗余计数
  star_count    INT UNSIGNED  NOT NULL DEFAULT 0,   -- 冗余计数
  created_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_kb_xid (xid),
  UNIQUE KEY uk_kb_owner_name (owner_id, name),     -- 同一 owner 下名称唯一
  KEY idx_kb_visibility_updated (visibility, updated_at),  -- 发现页/搜索排序
  KEY idx_kb_owner (owner_id)
);
```

- 公开库陌生人只读：权限由 `visibility` 推导，陌生人不建关系行

#### directories 目录树

```sql
CREATE TABLE directories (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid        CHAR(20)      NOT NULL,                -- XID 业务主键，对外标识
  kb_id      BIGINT UNSIGNED NOT NULL,              -- 逻辑引用 knowledge_bases.id
  parent_id  BIGINT UNSIGNED NOT NULL DEFAULT 0,    -- 0 = 根，逻辑引用 directories.id
  name       VARCHAR(100)    NOT NULL,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_dir_xid (xid),
  UNIQUE KEY uk_dir_parent_name (kb_id, parent_id, name),  -- 同一父目录下名称唯一（含根）
  KEY idx_dir_kb_parent (kb_id, parent_id)
);
```

- 删除目录 = 递归删除子目录与其中文章（应用层事务内处理，级联清理正文文件与附件）

#### articles 文章

```sql
CREATE TABLE articles (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid         CHAR(20)      NOT NULL,               -- XID 业务主键，对外标识
  kb_id       BIGINT UNSIGNED NOT NULL,             -- 逻辑引用 knowledge_bases.id
  dir_id      BIGINT UNSIGNED NOT NULL DEFAULT 0,   -- 0 = 根，逻辑引用 directories.id
  type        TINYINT         NOT NULL,             -- 1 md 2 txt 3 pdf 4 docx 5 html（原始格式）
  title       VARCHAR(200)    NOT NULL,
  content_key VARCHAR(500)    NOT NULL,             -- 正文 md 对象 key（o/<object_xid>，MySQL 不存内容）
  created_by  BIGINT UNSIGNED NOT NULL,             -- 逻辑引用 users.id
  created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_articles_xid (xid),
  KEY idx_articles_kb_dir (kb_id, dir_id),
  KEY idx_articles_kb_updated (kb_id, updated_at)
);
```

- **展示与检索口径**：md/txt 展示正文（按 `content_key` 取流）；pdf/docx/html 展示**源文件附件**（kind=1，原始排版），`content_key` 内为 markitdown 转换结果，仅向量化与 AI 检索
- **内容读写**：保存 = 先写 Garage 对象 → 再更新 `content_key` → 删旧对象；中断最多留孤儿对象（后续清理），不会出现指向不存在内容的行；向量化 / 重建索引 / 换模型按 `content_key` 读 md，不重跑 markitdown
- **文章互链不建表**：关联即 Markdown 链接，用户只写**相对路径**（如 `[标题](../design/arch.md)`、`![](assets/img.png)`）；前端渲染时以当前文章为基准解析，图片/文件拼 Gateway 资源地址 `/api/v1/articles/<xid>/resource?path=...`、文章跳 `/api/v1/articles/<xid>`；断链保留原文、渲染时标记失效；「删除前提示被引用」「反向链接」由渲染端现算或走向量检索

#### attachments 附件

```sql
CREATE TABLE attachments (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid           CHAR(20)      NOT NULL,             -- XID 业务主键，对外标识（稳定 URL）
  kb_id         BIGINT UNSIGNED NOT NULL,           -- 逻辑引用 knowledge_bases.id
  article_id    BIGINT UNSIGNED NOT NULL,           -- 所属文章，逻辑引用 articles.id
  kind          TINYINT         NOT NULL,           -- 1 源文件（pdf/docx/html 原文，固定绑定） 2 内嵌图片/附件（保存时按正文链接重建）
  file_key      VARCHAR(500)    NOT NULL,           -- 对象 key（o/<object_xid>，统一单桶）
  relative_path VARCHAR(500)    NOT NULL,           -- 规范化相对路径（如 assets/logo.png），供渲染期解析
  original_name VARCHAR(255)    NOT NULL,           -- 原始文件名（展示用）
  content_type  VARCHAR(100)    NOT NULL,           -- image/png 等
  size          BIGINT UNSIGNED NOT NULL,
  sha256        CHAR(64)        NOT NULL,           -- 完整性校验（不去重，同文件重复上传即多行）
  created_by    BIGINT UNSIGNED NOT NULL,           -- 逻辑引用 users.id
  created_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_attachments_xid (xid),
  UNIQUE KEY uk_attachments_article_path (article_id, relative_path),
  KEY idx_attachments_article (article_id, kind),
  KEY idx_attachments_kb (kb_id)
);
```

- **对象存储**：Garage（S3）单共享桶 + 统一对象 key `o/<object_xid>`（不按知识库分桶、不设图片专属目录）；Repository 是唯一直接访问方，Gateway 验签 + 路由后由 Repository 校验权限并签发 Presigned URL（302）
- 稳定 URL 形如 `/api/v1/attachments/<xid>`，经 gateway 鉴权（公开库可见、私有库校验成员）后转发
- 附件归单篇文章独占，不跨文章共享（文章复制需复制附件行与对象）；kind=2 保存时按正文相对路径引用全量重建、孤儿即时清理；kind=1 随文章删除清理；`relative_path` 只存规范化相对路径（禁绝对路径、禁 `..` 越权）

#### kb_user_relations 知识库×用户关系

```sql
CREATE TABLE kb_user_relations (
  kb_id      BIGINT UNSIGNED NOT NULL,              -- 逻辑引用 knowledge_bases.id
  user_id    BIGINT UNSIGNED NOT NULL,              -- 逻辑引用 users.id
  role       TINYINT         NOT NULL DEFAULT 0,   -- 0 非成员 1 reader 2 editor（owner 不入表）
  starred    TINYINT         NOT NULL DEFAULT 0,   -- 0 未收藏 1 已收藏
  starred_at DATETIME        NULL,                 -- 收藏时间（「我 star 的库」排序）
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (kb_id, user_id),
  KEY idx_kb_user_relations_user (user_id)         -- 「我是成员的库」「我 star 的库」
);
```

- star / 取消 star 与 `knowledge_bases.star_count` 增减在同一事务；关系状态（own / member / star，加 ganrag_user 的 follow）构成检索权重信号

### ganrag_user（User 服务）

> **建表迁移已交付**（2026-10-15 迁移基线重写，`pkg/db/migrations/ganrag_user/0001-0003`，含 xid）；代码适配见 [todo.md](todo.md) 「users 相关代码适配」 / 「会话模型代码适配」 / 「对外接口 xid」。接口与流程详见 [service/user/docs/architecture.md](../service/user/docs/architecture.md)；完整结构参考见 [database.md](database.md)。

#### users 用户账号

```sql
CREATE TABLE users (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid           CHAR(20)      NOT NULL,             -- XID 业务主键，对外标识
  email         VARCHAR(254) NOT NULL,             -- 登录标识（登录/找回密码）
  username      VARCHAR(64)  NOT NULL DEFAULT '',  -- 展示名，可重复、可不填
  password_hash VARCHAR(100) NOT NULL,             -- bcrypt 结果
  status        TINYINT      NOT NULL DEFAULT 1,   -- 1 正常 0 禁用
  created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_users_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

#### sessions 登录会话（替代 refresh_tokens）

```sql
CREATE TABLE sessions (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid            CHAR(20)      NOT NULL,             -- XID 业务主键，对外标识（设备管理接口用）
  user_id        BIGINT UNSIGNED NOT NULL,          -- 逻辑引用 users.id
  device_id      VARCHAR(64)  NOT NULL,             -- 设备码（服务端生成/核对，见「设备标识（device_id）口径」）
  device_info    VARCHAR(255) NOT NULL DEFAULT '',  -- 设备信息（UA/平台摘要，设备列表展示）
  token_hash     CHAR(64)     NOT NULL,             -- 当前 refresh token 的 SHA-256，轮换原地更新
  expires_at     DATETIME     NOT NULL,
  revoked_at     DATETIME     NULL,                 -- NULL=在线；非 NULL=登出/被踢
  last_active_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  created_at     DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_sessions_token_hash (token_hash),
  UNIQUE KEY uk_sessions_user_device (user_id, device_id),  -- 每设备一行，同设备重登覆盖
  KEY idx_sessions_user_id (user_id)                -- 「我的设备列表」/ 吊销全部
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

- 一行 = 一个设备的登录会话，多端登录即多行；同设备重复登录 upsert 该行（覆盖旧会话）；`device_id` 取值口径见下节
- 轮换原地更新 `token_hash`，**不做重用检测**：旧 token 复用按无效拒绝（无 `prev_token_hash` / 作废行保留）
- 「踢设备」= 置 `revoked_at`（行保留供展示）；改密 / 找回密码 = 删除该用户全部行
- 过期清理沿用惰性策略：登录 / 刷新时顺带删除该用户 `expires_at` 已过期行

#### 设备标识（device_id）口径（方案 A）

> Web 端不存在硬件级设备唯一标识；`device_id` 的可靠性靠「服务端生成 + 浏览器双份持久（cookie + localStorage）」保证，同一浏览器 profile 内长期稳定。迁移文件 `0002_sessions.up.sql` 内的列注释为历史文本（已应用禁改），口径以本节为准。

| 规则 | 约定 |
|------|------|
| 生成方 | 服务端为权威生成方：登录请求携带合法 `device_id`（1-64 位 `[A-Za-z0-9_-]`）则沿用，否则服务端生成 |
| 取值优先级 | 请求体 `device_id`（前端 localStorage）> Gateway `did` cookie > 服务端生成 |
| 生成器 | `github.com/rs/xid`（20 字符，与 `sessions.xid` 同源，后续统一收进 `pkg/xid`） |
| Gateway cookie | 登录成功后 `Set-Cookie: did=<device_id>; HttpOnly; SameSite=Lax; Path=/; Max-Age=31536000`（生产加 `Secure`），每次登录续期；登录请求无 body `device_id` 时读 cookie 填入 gRPC 请求 |
| 回填同步 | 登录响应返回 `device_id`，前端写 localStorage，下次登录放请求体；两处不一致以请求体为准，cookie 覆盖为同值（收敛到单一值） |
| 失效语义 | cookie 与 localStorage 全清 = 新设备行（接受，无解）；不做疑似同设备归并（YAGNI） |
| device_info | 请求体可选（前端展示名）；缺省 Gateway 取 `User-Agent` 截断 255 字符；仅设备列表展示用 |

#### 邮箱验证码（Redis，不建表）

| 约定 | 内容 |
|------|------|
| Key | `user:email_code:<scene>:<email>`（scene：`register` / `reset_password`） |
| Value | 验证码 SHA-256 哈希 |
| TTL | 5 分钟（过期由 Redis 自然淘汰） |
| 一次性 | 验证成功即 DEL；校验失败 5 次作废该 key（防爆破） |
| 发送限频 | 同邮箱 60s 一次：`user:email_code_limit:<email>`，TTL 60s |

#### user_follows 关注

```sql
CREATE TABLE user_follows (                          -- 0003_user_follows
  follower_id BIGINT UNSIGNED NOT NULL,              -- 逻辑引用 users.id
  followee_id BIGINT UNSIGNED NOT NULL,              -- 逻辑引用 users.id
  created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (follower_id, followee_id),
  KEY idx_user_follows_followee (followee_id)        -- 「谁关注了我」
);
```

- 与 users 同 schema 亦遵守「禁止物理外键」全局规则，只做逻辑引用（应用层校验）

### ganrag_ai（AI 服务）

> **建表迁移已交付**（2026-10-15 迁移基线重写，`pkg/db/migrations/ganrag_ai/0001-0003`）；AI 服务实现见 [todo.md](todo.md) 「AI 服务实现」。聊天记录按「消息信封 + 全量步骤」两层设计；步骤词表与 payload 对齐 Vercel AI SDK（`@ai-sdk/react` useChat）的 `UIMessage` part 模型，历史消息可直接重组喂给 `useChat(initialMessages)`。完整结构参考见 [database.md](database.md)。

#### chat_sessions 聊天会话

```sql
CREATE TABLE chat_sessions (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid             CHAR(20)      NOT NULL,             -- XID 业务主键，对外标识（兼 useChat chatId）
  user_id         BIGINT UNSIGNED NOT NULL,            -- 逻辑引用 users.id
  kb_id           BIGINT UNSIGNED NOT NULL DEFAULT 0,  -- 0 = 全局，否则绑定单个知识库
  agent           VARCHAR(32)     NOT NULL DEFAULT '', -- 当前智能体 id（粘性状态，P1.5 多智能体路由用）
  context         LONGTEXT        NULL,                -- AI 内部压缩上下文（含最新 Agent 提示词与摘要）；NULL = 未压缩
  context_msg_id  BIGINT UNSIGNED NOT NULL DEFAULT 0,  -- 拼接游标：消息 id >= 此值接在 context 后进 prompt；0 = 全量
  title           VARCHAR(200)    NOT NULL DEFAULT '', -- 首条消息自动生成，可改
  created_at      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  last_message_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP, -- 右栏排序键
  PRIMARY KEY (id),
  UNIQUE KEY uk_chat_sessions_xid (xid),
  KEY idx_sessions_user_active (user_id, last_message_at)
);
```

- `kb_id` 随会话固化检索范围（0 全局 / 否则单库），保证历史会话可复现
- `context` + `context_msg_id`：会话级压缩上下文与游标，压缩时**替换** context 并推进游标，原始消息不删

#### chat_messages 消息（信封层）

```sql
CREATE TABLE chat_messages (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid        CHAR(20)      NOT NULL,               -- XID 业务主键，对外标识（兼 UIMessage.id）
  session_id BIGINT UNSIGNED NOT NULL,             -- 逻辑引用 chat_sessions.id
  role       TINYINT         NOT NULL,             -- 1 user 2 assistant
  agent      VARCHAR(32)     NOT NULL DEFAULT '',  -- 产生该消息的智能体 id，user 消息为空
  content    LONGTEXT        NOT NULL,             -- 最终输出（assistant）或用户正文；prompt 拼装与检索用
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_chat_messages_xid (xid),
  KEY idx_chat_messages_session (session_id, id)
);
```

- `content` 为**最终输出层**（不含思考/工具细节，那些在 parts）；prompt 拼装 = 固定 System Prompt + `context` + 游标后消息；`search_context` 检索覆盖可见 `content` 与 parts 的 `content`

#### chat_message_parts 消息步骤（全量细节层，对齐 UIMessage parts）

```sql
CREATE TABLE chat_message_parts (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  message_id BIGINT UNSIGNED NOT NULL,             -- 逻辑引用 chat_messages.id
  step       INT UNSIGNED    NOT NULL DEFAULT 0,   -- 步骤号（对应 UIMessage step-start 边界，0 起）
  seq        INT UNSIGNED    NOT NULL,             -- 消息内全局顺序（0 起）
  type       VARCHAR(32)     NOT NULL,             -- 对齐 UIMessage part 类型（见下表）
  content    LONGTEXT        NULL,                 -- 可检索文本（text / reasoning / tool output 全文）
  meta       JSON            NULL,                 -- SDK part payload 原样存储
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_message_parts_seq (message_id, seq)
);
```

| type（= UIMessage part 类型） | content 存 | meta 存 | useChat 展示 |
|------|-----------|---------|---------|
| `text` | 正文段文本 | — | streamdown 渲染 |
| `reasoning` | 思考全文 | `state` 等 | 思考过程折叠区（SDK 原生状态） |
| `tool` | `output` 全文（可检索） | `toolCallId` / `toolName` / `input` / `state` / `output` / `errorText` 原样 | 工具卡片（输入/输出/错误/加载态） |
| `source-document` | 标题 / 检索片段 | `article_xid` / `title` / `snippet` / `score`（+ SDK `sourceId` / `mediaType`） | 来源列表，点击跳 `/articles/<article_xid>` |
| `source-url` | 标题 | `url` 等 | 外链来源 |
| `file` | — | `mediaType` / `url`（附件 xid 稳定 URL） | 图片/文件气泡 |
| `agent-system` | Agent 专属系统提示词 | `agent_id` / 注入时间 | **内部隐藏，不返回前端** |
| （新增展示需求） | 按需 | 按需 | 自定义 part，**零 DDL 变更** |

- **全量存储**：一次 assistant 回答的每一步（reasoning、每次 tool 调用与结果、每个正文段、来源）都落 part 行，按 `seq` 排序、`step` 分组
- **payload 原样**：`meta` 存 SDK part payload 原文，`content` 为可检索文本抽取；消息可无损重组为 `UIMessage[]`
- **来源跳转**：RAG 引用走 `source-document`，`article_xid`（不存自增 id）供点击跳转；snippet 只存检索片段，不存文章全文
- **Agent 提示词隔离**：`agent-system` 只由服务端 Agent 注册表生成，作为当前用户消息下的隐藏 part 存储，用于重建模型请求；不得进入 `useChat(initialMessages)`、用户消息展示或 `search_context` 结果。API 必须按类型白名单序列化，不能依赖前端隐藏。
- **聊天记录整体入 MySQL**（含工具结果全文）：对话交易型短文本，高频按序读写，不适用「文章内容不入 MySQL」规则
- **删除**：删会话 = 应用层级联删 messages + parts（无外键）

## 关系状态推导

关系状态**不是一张表**，而是查询时从多个表推导的字段（供左栏分组、个人页面、检索权重使用）：

```text
relation(kb, me) =
  OWN                若 kb.owner_id = me
  MEMBER             若 kb_user_relations.role >= 1 (kb, me)
  STARRED            若 kb_user_relations.starred = 1 (kb, me)
  FOLLOWED_AUTHOR    若 user_follows 存在 (me, kb.owner_id)
  OTHER              以上皆无
```

- 可同时命中多个信号；**可见性**（能否看到）与**权重**（排多前）是两件事：可见性由 owner/member/visibility 三者决定，权重取最高命中信号（见 product.md 检索权重表）
- 全局检索候选集 SQL 语义：`owner_id = me OR 存在成员行 OR visibility = 1`，**此过滤必须先于任何加权执行**

## 检索实现约定（AI 服务落地时遵循）

1. 向量库按「知识库 × 向量化模型」隔离：vector-store 集合 `kb-<kb_id>-<model_id>`（文章分块入库），全局检索时对可见集合逐个查询后归并；检索只查**当前模型**后缀的集合——用什么模型，就检索对应的向量化结果
2. 文章保存/删除时，Repository 服务向 AI 服务同步增量（或 AI 订阅），由 AI 负责向量的写入与清理
3. 检索先做权限硬过滤，再分别走向量与关键词召回并合并去重；重排序使用排名归一化、关系、时间、热度，并由 Decima-small 对前 K 个候选做 `noul` 相关性审查（P1 只做候选集过滤，P3 接入权重与审查）
4. 向量输入统一为 `articles.content_key` 指向的 Garage 对象内 md（pdf/docx/html 即转换后的 md）：转换结果一次存储，重建索引 / 换 embedding 模型不重跑 markitdown；文档内嵌图片不提取，不参与分块
5. 模型上下文不进用户消息流：压缩上下文存 `chat_sessions.context`，内部结构包含最新 `agent-system` 提示词和摘要；prompt 拼装 = 固定 System Prompt + context + `chat_messages` 中 `id >= context_msg_id` 的可见消息；预计请求 token 达模型上限的 65% 时触发压缩，压缩时**替换** context 并推进游标，最新 Agent 提示词置于摘要前，原始消息不删，`search_context` 排除 `agent-system`
6. 切换向量化模型：新写入即切新模型集合 → 后台按 `articles.content_key` 读 md **全量重建**新模型集合（不重跑 markitdown）→ 重建完成前旧集合继续服务 → 完成后配置原子切换并下线旧集合（保留期可回滚）；旧向量按 metadata `embedding_model` 批量清理

## 分期与迁移对照

| 阶段 | 迁移 | 说明 |
|------|------|------|
| P1 | user: 0001-0003（含 0003_user_follows，社交接口 P2 开放）；repository: 0001-0005（五张表）；ai: 0001-0003（三张表） | 全部表一次建齐，社交接口 P2 再开 |
| P2 | 无新表 | star / follow / 搜索 / 个人页面接口与界面 |
| P3 | 无新表 | 权重配置接入与调参 |

> 表结构先于接口建齐，是为了让关系状态从第一天起就是完整事实；P2 只是把已有数据的读写口打开，不做 DDL 变更。
