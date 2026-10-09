# GanRAG 数据模型文档

## 概述

本文档定义全部业务表结构，是 P1 实现的**一次性设计**（表结构一次到位，接口与界面分期交付，避免破坏性迁移）。功能与术语见 [product.md](product.md)；账号域细节见 [service/user/docs/architecture.md](../service/user/docs/architecture.md)。

## 服务归属

MySQL 单实例（根目录 compose 编排），**每个服务一个 schema、一套独立迁移**，遵循「服务独立、互不引用」的项目约定；SQL 迁移文件集中存放在 `pkg/db/migrations/<库名>/`（由 `pkg/db` 统一同步与执行，修改规范见根 AGENTS.md「SQL 迁移修改规范」）：

| Schema | 拥有者 | 表 | 迁移位置 |
|--------|--------|----|----------|
| `ganrag_user` | User 服务 | `users`、`refresh_tokens`、`user_follows` | `pkg/db/migrations/ganrag_user/` |
| `ganrag_repository` | Repository 服务 | `knowledge_bases`、`directories`、`articles`、`attachments`、`article_attachments`、`article_refs`、`kb_members`、`kb_stars` | `pkg/db/migrations/ganrag_repository/` |
| `ganrag_ai` | AI 服务 | `chat_sessions`、`chat_messages` | `pkg/db/migrations/ganrag_ai/` |

**跨服务引用规则**：跨 schema 的关联（如 `kb_members.user_id` → `users.id`）**只做逻辑引用，不建物理外键**——外键会把两个服务的迁移耦合成必须按序执行，违背服务独立。引用完整性由应用层保证；跨服务查询走 gRPC，不做跨 schema JOIN。

## ER 总览

```
ganrag_user                          ganrag_repository
┌────────────┐                       ┌─────────────────────┐
│   users    │◄──┐                   │  knowledge_bases    │
├────────────┤   │                   ├─────────────────────┤
│ id         │   │ owner_id          │ id / owner_id       │
│ username   │   ├───┐               │ name / description  │
│ password_hash     │               │ visibility          │
│ ...        │   │   │               │ article/star_count  │
└─────┬──────┘   │   │               └──┬───────┬───────┬──┘
      │          │   └──────────────────┘       │       │
      │ refresh_tokens（见 user 文档）           │       │
      │          │                      ┌───────┘       │
┌─────┴──────┐   │   ┌──────────────┐   │   ┌───────────┴─┐
│user_follows│   │   │ kb_members   │   │   │  kb_stars   │
├────────────┤   │   ├──────────────┤   │   ├─────────────┤
│ follower_id│   │   │ kb_id        │   │   │ kb_id       │
│ followee_id│   │   │ user_id (逻辑)│   │   │ user_id(逻辑)│
└────────────┘   │   │ role         │   │   └─────────────┘
                 │   └──────────────┘   │
                 │                      │
                 │   ┌──────────────┐   │   ┌────────────────┐
                 │   │ directories  │◄──┼───┤ articles       │
                 │   │ id/kb_id     │   │   ├────────────────┤
                 │   │ parent_id    │   │   │ kb_id/dir_id   │
                 │   │ name         │   │   │ type/title     │
                 │   └──────────────┘   │   │ content(LONG)  │
                 │                      │   │ created_by     │
                 │                      │   └──┬────────┬────┘
                 │                      │      │        │
                 │                      │  ┌───┴──────┐ │ ┌────────────────┐
                 │                      │  │article_  │ │ │ article_refs   │
                 │                      │  │attachmts │ │ ├────────────────┤
                 │                      │  └───┬──────┘ │ │ from/to_article│
                 │                      │      │        │ └────────────────┘
                 │                      │  ┌───┴────────┴─┐
                 │                      │  │ attachments  │──▶ file-store
                 │                      │  │ kb_id/file_key│   bucket kb-<id>
                 │                      │  └──────────────┘
ganrag_ai
┌─────────────────┐   ┌──────────────────┐
│ chat_sessions   │   │ chat_messages    │
├─────────────────┤   ├──────────────────┤
│ user_id         │──▶│ session_id       │
│ kb_id (0=全局)   │   │ role / content   │
│ title           │   │ sources (预留)    │
└─────────────────┘   └──────────────────┘
```

## 关键设计决策

| 决策点 | 结论 | 原因 |
|--------|------|------|
| 文章内容存储 | 正文存 MySQL `LONGTEXT`，不进 file-store | 内容与元数据同库事务保证一致性；file-store 面向附件（图片），非文档存储 |
| 引用方式 | 图片与互链一律**按 ID 存稳定 URL**，不存路径 | 文章移动 / 重命名 / 目录调整都不会打断引用 |
| 改写时机 | md 保存时解析相对路径 → 绑定附件/目标文章 → 改写为稳定 URL | 渲染端零解析成本；解析失败的链接保留原文，渲染时标记失效 |
| 根节点表示 | `parent_id` / `dir_id` / `kb_id`（会话）用 **0 表示根**，不用 NULL | MySQL 唯一索引对 NULL 不去重，0 值让 `UNIQUE(kb_id, parent_id, name)` 对根目录同样生效 |
| 标题约束 | 文章标题**不设唯一** | 引用靠 ID 不靠标题；同目录同名仅是编辑体验问题，由应用层提示 |
| 计数冗余 | `knowledge_bases` 冗余 `article_count`、`star_count` | 列表页高频展示，避免每行 COUNT；同事务内增减 |
| 附件清理 | `article_attachments` 记录文章↔附件绑定 | 文章删除 / 编辑移除图片时，删除「不再被任何文章引用」的孤儿附件（含 file-store 实体） |
| 成员表不含 owner | owner 由 `knowledge_bases.owner_id` 表示，不入 `kb_members` | 避免两处真相；「我的库」= owner_id 查询，语义清晰 |
| 权重不入库 | 关系状态存库，权重映射由 AI 服务配置持有 | 权重是排序策略、需频繁调参，不是业务事实；存库会把调优变成改数据 |
| 跨 schema 引用 | 逻辑引用，无物理外键 | 迁移独立、可并行开发（见「服务归属」） |
| 会话范围 | `chat_sessions.kb_id = 0` 表示全局 | 与根节点约定一致 |

## 表结构 DDL

统一约定：`BIGINT UNSIGNED` 自增主键；`created_at` / `updated_at` 自动维护；`ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`。

### ganrag_repository（Repository 服务）

#### knowledge_bases 知识库

```sql
CREATE TABLE knowledge_bases (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  owner_id      BIGINT UNSIGNED NOT NULL,           -- 逻辑引用 users.id（跨服务，无外键）
  name          VARCHAR(100)  NOT NULL,
  description   VARCHAR(500)  NOT NULL DEFAULT '',
  visibility    TINYINT       NOT NULL DEFAULT 0,   -- 0 私有 1 公开
  article_count INT UNSIGNED  NOT NULL DEFAULT 0,   -- 冗余计数
  star_count    INT UNSIGNED  NOT NULL DEFAULT 0,   -- 冗余计数
  created_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_kb_owner_name (owner_id, name),     -- 同一 owner 下名称唯一
  KEY idx_kb_visibility (visibility),
  KEY idx_kb_owner (owner_id)
);
```

#### directories 目录树

```sql
CREATE TABLE directories (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  kb_id      BIGINT UNSIGNED NOT NULL,
  parent_id  BIGINT UNSIGNED NOT NULL DEFAULT 0,    -- 0 = 根
  name       VARCHAR(100)    NOT NULL,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_dir_parent_name (kb_id, parent_id, name),  -- 同一父目录下名称唯一（含根）
  KEY idx_dir_kb_parent (kb_id, parent_id)
);
```

- 删除目录 = 递归删除子目录与其中文章（应用层事务内处理，级联清理附件）

#### articles 文章

```sql
CREATE TABLE articles (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  kb_id      BIGINT UNSIGNED NOT NULL,
  dir_id     BIGINT UNSIGNED NOT NULL DEFAULT 0,    -- 0 = 根
  type       TINYINT         NOT NULL,              -- 1 md 2 txt
  title      VARCHAR(200)    NOT NULL,
  content    LONGTEXT        NOT NULL,              -- md 原文（引用已改写为稳定 URL）
  created_by BIGINT UNSIGNED NOT NULL,              -- 逻辑引用 users.id
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_articles_kb_dir (kb_id, dir_id),
  KEY idx_articles_kb_updated (kb_id, updated_at)
);
```

#### attachments 图片附件

```sql
CREATE TABLE attachments (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  kb_id         BIGINT UNSIGNED NOT NULL,
  file_key      VARCHAR(500)    NOT NULL,           -- file-store 内 key（bucket 外）
  original_name VARCHAR(255)    NOT NULL,           -- 原始文件名（展示用）
  content_type  VARCHAR(100)    NOT NULL,           -- image/png 等
  size          BIGINT UNSIGNED NOT NULL,
  sha256        CHAR(64)        NOT NULL,
  created_by    BIGINT UNSIGNED NOT NULL,
  created_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_attachments_kb (kb_id)
);
```

- **file-store bucket 约定：`kb-<kb_id>`**，key 形如 `<uuid>/<原始文件名>`；Repository 服务是 file-store 的唯一调用方，gateway 只经 Repository 鉴权后取流
- 稳定 URL 形如 `/api/v1/attachments/<id>`，经 gateway 鉴权（公开库可见、私有库校验成员）后转发

#### article_attachments 文章-附件绑定

```sql
CREATE TABLE article_attachments (
  article_id    BIGINT UNSIGNED NOT NULL,
  attachment_id BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (article_id, attachment_id),
  KEY idx_article_attachments_att (attachment_id)   -- 反查：附件被哪些文章引用
);
```

- 每次保存文章时全量重建该文章的绑定行；随后删除本 kb 内「零引用」附件（表 + file-store）

#### article_refs 文章互链

```sql
CREATE TABLE article_refs (
  from_article_id BIGINT UNSIGNED NOT NULL,
  to_article_id   BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (from_article_id, to_article_id),
  KEY idx_article_refs_to (to_article_id)           -- 反查：谁链向了本文
);
```

- 保存时从 md 解析链接并全量重建；用途：删除文章时提示被引用情况、未来「反向链接」与 RAG 上下文扩展

#### kb_members 成员

```sql
CREATE TABLE kb_members (
  kb_id      BIGINT UNSIGNED NOT NULL,
  user_id    BIGINT UNSIGNED NOT NULL,              -- 逻辑引用 users.id
  role       TINYINT         NOT NULL,              -- 1 reader 2 editor（owner 不入表）
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (kb_id, user_id),
  KEY idx_kb_members_user (user_id)                 -- 「我是成员的库」查询
);
```

#### kb_stars 收藏

```sql
CREATE TABLE kb_stars (
  kb_id      BIGINT UNSIGNED NOT NULL,
  user_id    BIGINT UNSIGNED NOT NULL,              -- 逻辑引用 users.id
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (kb_id, user_id),
  KEY idx_kb_stars_user (user_id)                   -- 「我 star 的库」查询
);
```

- star / 取消 star 与 `knowledge_bases.star_count` 增减在同一事务

### ganrag_user（User 服务，增量）

`users`、`refresh_tokens` 已定稿（见 user 服务架构文档 0001/0002 迁移），本设计新增：

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

- 与 users 同 schema，故此表**可以**建物理外键；为与跨服务表风格一致，仍只做逻辑引用（应用层校验）

### ganrag_ai（AI 服务）

#### chat_sessions 聊天会话

```sql
CREATE TABLE chat_sessions (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id         BIGINT UNSIGNED NOT NULL,          -- 逻辑引用 users.id
  kb_id           BIGINT UNSIGNED NOT NULL DEFAULT 0,-- 0 = 全局，否则绑定单个知识库
  title           VARCHAR(200)    NOT NULL DEFAULT '', -- 首条消息自动生成，可改
  created_at      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  last_message_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP, -- 右栏排序键
  PRIMARY KEY (id),
  KEY idx_sessions_user_active (user_id, last_message_at)  -- 我的会话按活跃倒序
);
```

#### chat_messages 消息

```sql
CREATE TABLE chat_messages (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  session_id BIGINT UNSIGNED NOT NULL,
  role       TINYINT         NOT NULL,               -- 1 user 2 assistant
  content    LONGTEXT        NOT NULL,
  sources    JSON            NULL,                   -- 预留：回答引用的来源（文章/分块），P2+ 接入
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_chat_messages_session (session_id, id)     -- 会话内按序加载
);
```

## 关系状态推导

关系状态**不是一张表**，而是查询时从多个表推导的字段（供左栏分组、个人页面、检索权重使用）：

```text
relation(kb, me) =
  OWN                若 kb.owner_id = me
  MEMBER             若 kb_members 存在 (kb, me)
  STARRED            若 kb_stars 存在 (kb, me)
  FOLLOWED_AUTHOR    若 user_follows 存在 (me, kb.owner_id)
  OTHER              以上皆无
```

- 可同时命中多个信号；**可见性**（能否看到）与**权重**（排多前）是两件事：可见性由 owner/member/visibility 三者决定，权重取最高命中信号（见 product.md 检索权重表）
- 全局检索候选集 SQL 语义：`owner_id = me OR 存在成员行 OR visibility = 1`，**此过滤必须先于任何加权执行**

## 检索实现约定（AI 服务落地时遵循）

1. 向量库按知识库隔离：vector-store 集合 `kb-<kb_id>`（文章分块入库），全局检索时对可见集合逐个查询后归并
2. 文章保存/删除时，Repository 服务向 AI 服务同步增量（或 AI 订阅），由 AI 负责向量的写入与清理
3. 重排序阶段读取关系状态四个信号 → 按权重配置调整排名（P1 只做候选集过滤，P3 接入权重）

## 分期与迁移对照

| 阶段 | 迁移 | 说明 |
|------|------|------|
| P1 | user: 0001-0003（含 0003_user_follows，社交接口 P2 开放）；repository: 0001-0008（八张表）；ai: 0001-0002 | 全部表一次建齐，社交接口 P2 再开 |
| P2 | 无新表 | star / follow / 搜索 / 个人页面接口与界面 |
| P3 | 无新表 | 权重配置接入与调参 |

> 表结构先于接口建齐，是为了让关系状态从第一天起就是完整事实；P2 只是把已有数据的读写口打开，不做 DDL 变更。
