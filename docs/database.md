# GanRAG 数据库设计文档

## 概述

本文档是四库 **12 张表**的**结构参考**（完整 SQL + 字段 + 索引 + 口径），是编写迁移与代码的直接依据。设计决策与推导见 [data-model.md](data-model.md)；规范（外键 / XID / Redis Key / 内容存储口径）见根 [AGENTS.md](../AGENTS.md)；实施跟踪见 [fix.md](fix.md) / [todo.md](todo.md)。

| Schema | 拥有者 | 表 | 迁移位置 |
|--------|--------|----|----------|
| `ganrag_user` | User 服务 | users、sessions、user_follows | `pkg/db/migrations/ganrag_user/`（0001-0005） |
| `ganrag_repository` | Repository 服务 | knowledge_bases、directories、articles、attachments、kb_user_relations | `pkg/db/migrations/ganrag_repository/`（0001-0008） |
| `ganrag_ai` | AI 服务 | chat_sessions、chat_messages、chat_message_parts | `pkg/db/migrations/ganrag_ai/`（0001-0005） |
| `ganrag_common` | 共享基础设施（经 `pkg/asynctask`） | async_tasks | `pkg/db/migrations/ganrag_common/`（0001） |

## 通用约定

- 主键：`BIGINT UNSIGNED NOT NULL AUTO_INCREMENT`；时间戳 `DATETIME`，`created_at` / `updated_at` 自动维护
- 表尾：`ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`
- **禁止物理外键**：表间关联一律逻辑引用，完整性与级联清理由应用层负责
- **业务主键**：对外展示实体带 `xid VARCHAR(32)`（`pkg/eid` 生成 `<前缀>_<xid>` 全串，前缀按实体类型：`usr_`/`ses_`/`kb_`/`dir_`/`art_`/`att_`/`chs_`/`msg_`/`tsk_`）+ UNIQUE；对外接口/URL/前端只用 xid 全串，自增 id 不出服务
- **逻辑引用**：同服务引用存自增 id 并冗余目标 `*_xid`；跨服务引用只存目标 xid；对象引用保留 key 并冗余 `object_xid`
- **共享 schema**：`ganrag_common` 只存运维/失败任务数据，所有服务通过 `pkg/asynctask` 访问，禁止业务代码直写 SQL
- **内容存储口径**：文章/文档大对象不入 MySQL（存 Garage 对象存储，只存 key，统一 `o/<obj_xid>`）；聊天消息、思考、工具结果、模型上下文等交易型短文本入 MySQL（LONGTEXT / JSON）

---

# ganrag_user（User 服务）

## users 用户账号

```sql
CREATE TABLE users (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid           VARCHAR(32)      NOT NULL,             -- 业务主键 <前缀>_<xid>（pkg/eid），对外标识
  email         VARCHAR(254) NOT NULL,              -- 登录标识（登录/找回密码）
  username      VARCHAR(64)  NOT NULL DEFAULT '',   -- 展示名，可重复、可不填
  password_hash VARCHAR(100) NOT NULL,              -- bcrypt 结果
  status        TINYINT      NOT NULL DEFAULT 1,    -- 1 正常 0 禁用
  created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_users_xid (xid),
  UNIQUE KEY uk_users_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT UNSIGNED | 自增主键，仅内部与逻辑引用使用 |
| xid | VARCHAR(32) | 对外业务主键（<前缀>_<xid>，pkg/eid） |
| email | VARCHAR(254) | 登录标识，唯一；注册/找回密码走邮箱验证码 |
| username | VARCHAR(64) | 展示名，可重复、可不填，无唯一约束 |
| password_hash | VARCHAR(100) | bcrypt（实际 60 字符，留余量） |
| status | TINYINT | 1 正常 / 0 禁用（禁用后登录与 refresh 拒绝） |
| created_at / updated_at | DATETIME | 自动维护 |

| 索引 | 列 | 用途 |
|------|----|------|
| PRIMARY | id | 主键（内部） |
| uk_users_xid | (xid) | 对外标识唯一 |
| uk_users_email | (email) | 登录点查、注册查重 |

## sessions 登录会话

```sql
CREATE TABLE sessions (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid            VARCHAR(32)      NOT NULL,             -- 业务主键 <前缀>_<xid>（pkg/eid），对外标识（设备管理接口用）
  user_id        BIGINT UNSIGNED NOT NULL,           -- 逻辑引用 users.id（同服务）
  user_xid       VARCHAR(32)      NOT NULL DEFAULT '', -- 用户 usr_<xid> 全串（同服务冗余）
  device_id      VARCHAR(64)  NOT NULL,              -- 设备码（服务端生成/核对，见 data-model.md「设备标识（device_id）口径」）
  device_info    VARCHAR(255) NOT NULL DEFAULT '',   -- 设备信息（UA/平台摘要，设备列表展示）
  token_hash     CHAR(64)     NOT NULL,              -- 当前 refresh token 的 SHA-256，轮换原地更新
  expires_at     DATETIME     NOT NULL,              -- 会话过期（refresh TTL 固定期限）
  revoked_at     DATETIME     NULL,                  -- NULL=在线；非 NULL=登出/被踢
  last_active_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  created_at     DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_sessions_xid (xid),
  UNIQUE KEY uk_sessions_token_hash (token_hash),
  UNIQUE KEY uk_sessions_user_device (user_id, device_id),
  KEY idx_sessions_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT UNSIGNED | 自增主键，仅内部使用 |
| xid | VARCHAR(32) | 对外业务主键（<前缀>_<xid>，pkg/eid），设备管理接口使用 |
| user_id | BIGINT UNSIGNED | 逻辑引用 users.id（同服务） |
| user_xid | VARCHAR(32) | 用户 usr_<xid> 全串（同服务冗余，随会话写入） |
| device_id | VARCHAR(64) | 设备码，服务端生成/核对（口径见 [data-model.md](data-model.md)「设备标识（device_id）口径」） |
| device_info | VARCHAR(255) | 设备信息（UA/平台摘要），列表展示 |
| token_hash | CHAR(64) | 当前 refresh token 的 SHA-256 hex；轮换**原地更新**，不做重用检测 |
| expires_at | DATETIME | 过期时间；惰性清理（登录/刷新时顺带删该用户过期行） |
| revoked_at | DATETIME NULL | NULL=在线；非 NULL=登出/被踢（行保留供列表展示） |
| last_active_at | DATETIME | 刷新时顺带更新，设备列表排序/展示 |

| 索引 | 列 | 用途 |
|------|----|------|
| PRIMARY | id | 主键（内部） |
| uk_sessions_xid | (xid) | 对外标识唯一 |
| uk_sessions_token_hash | (token_hash) | 刷新点查 |
| uk_sessions_user_device | (user_id, device_id) | 每设备一行，同设备重登 upsert |
| idx_sessions_user_id | (user_id) | 「我的设备列表」/ 吊销全部 |

语义：多端登录 = 多行；改密/找回密码 = 删该用户全部行；「踢设备」= 置 `revoked_at`。设备 ID 为服务端生成/核对（取值优先级：请求体 > Gateway `did` cookie > 服务端生成 xid，口径见 [data-model.md](data-model.md)「设备标识（device_id）口径」）。

## user_follows 用户关注

```sql
CREATE TABLE user_follows (
  follower_id BIGINT UNSIGNED NOT NULL,              -- 逻辑引用 users.id（同服务）
  follower_xid VARCHAR(32)    NOT NULL DEFAULT '',   -- 关注者 usr_<xid> 全串（同服务冗余）
  followee_id BIGINT UNSIGNED NOT NULL,              -- 逻辑引用 users.id（同服务）
  followee_xid VARCHAR(32)    NOT NULL DEFAULT '',   -- 被关注者 usr_<xid> 全串（同服务冗余）
  created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (follower_id, followee_id),
  KEY idx_user_follows_followee (followee_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

| 字段 | 类型 | 说明 |
|------|------|------|
| follower_id / followee_id | BIGINT UNSIGNED | 联合主键；逻辑引用 users.id；自关注放行 |
| follower_xid / followee_xid | VARCHAR(32) | 关注者 / 被关注者 usr_<xid> 全串（同服务冗余） |
| created_at | DATETIME | 自动维护 |

| 索引 | 列 | 用途 |
|------|----|------|
| PRIMARY | (follower_id, followee_id) | 关注唯一，兼「我关注了谁」 |
| idx_user_follows_followee | (followee_id) | 「谁关注了我」 |

## 邮箱验证码（Redis，不建表）

| 约定 | 内容 |
|------|------|
| Key | `user:email_code:<scene>:<email>`（scene：`register` / `reset_password`） |
| Value | 验证码 SHA-256 哈希 |
| TTL | 5 分钟（过期由 Redis 自然淘汰） |
| 一次性 | 验证成功即 DEL |
| 失败计数 | 辅助键 `user:email_code_tries:<scene>:<email>`（INCR），TTL 5 分钟；校验失败 5 次作废验证码（DEL 码与计数，防爆破） |
| 发送限频 | 同邮箱 60s 一次：`user:email_code_limit:<email>`（不分场景），TTL 60s |

---

# ganrag_repository（Repository 服务）

## knowledge_bases 知识库

```sql
CREATE TABLE knowledge_bases (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid           VARCHAR(32)      NOT NULL,
  owner_xid     VARCHAR(32)      NOT NULL,           -- 库主 usr_<xid> 全串（跨服务逻辑引用）
  name          VARCHAR(100)  NOT NULL,
  description   VARCHAR(500)  NOT NULL DEFAULT '',
  visibility    TINYINT       NOT NULL DEFAULT 0,   -- 0 私有 1 公开
  article_count INT UNSIGNED  NOT NULL DEFAULT 0,
  star_count    INT UNSIGNED  NOT NULL DEFAULT 0,
  created_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_kb_xid (xid),
  UNIQUE KEY uk_kb_owner_name (owner_xid, name),
  KEY idx_kb_visibility_updated (visibility, updated_at),
  KEY idx_kb_owner (owner_xid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT UNSIGNED | 自增主键，仅内部与逻辑引用使用 |
| xid | VARCHAR(32) | 对外业务主键（<前缀>_<xid>，pkg/eid） |
| owner_xid | VARCHAR(32) | 库主 usr_<xid> 全串（跨服务逻辑引用，只存 xid） |
| name | VARCHAR(100) | 库名，同一 owner 下唯一 |
| description | VARCHAR(500) | 描述，默认空串 |
| visibility | TINYINT | 0 私有 / 1 公开；公开库陌生人只读（推导权限，不建关系行） |
| article_count / star_count | INT UNSIGNED | 冗余计数，同事务增减 |
| created_at / updated_at | DATETIME | 自动维护 |

| 索引 | 列 | 用途 |
|------|----|------|
| PRIMARY | id | 主键（内部） |
| uk_kb_xid | (xid) | 对外标识唯一 |
| uk_kb_owner_name | (owner_xid, name) | 同 owner 库名唯一 |
| idx_kb_visibility_updated | (visibility, updated_at) | 发现页/搜索排序 |
| idx_kb_owner | (owner_xid) | 「我的库」 |

## directories 目录树

```sql
CREATE TABLE directories (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid        VARCHAR(32)      NOT NULL,
  kb_id      BIGINT UNSIGNED NOT NULL,              -- 逻辑引用 knowledge_bases.id（同服务）
  kb_xid     VARCHAR(32)     NOT NULL DEFAULT '',   -- 知识库 kb_<xid> 全串（同服务冗余）
  parent_id  BIGINT UNSIGNED NOT NULL DEFAULT 0,    -- 0 = 根，逻辑引用 directories.id（同服务）
  parent_xid VARCHAR(32)     NOT NULL DEFAULT '',   -- 父目录 dir_<xid> 全串；空串=根（同服务冗余）
  name       VARCHAR(100)    NOT NULL,
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_dir_xid (xid),
  UNIQUE KEY uk_dir_parent_name (kb_id, parent_id, name),
  KEY idx_dir_kb_parent (kb_id, parent_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT UNSIGNED | 自增主键，仅内部与逻辑引用使用 |
| xid | VARCHAR(32) | 对外业务主键（<前缀>_<xid>，pkg/eid），前端目录树使用 |
| kb_id | BIGINT UNSIGNED | 所属知识库，逻辑引用 knowledge_bases.id（同服务） |
| kb_xid | VARCHAR(32) | 知识库 kb_<xid> 全串（同服务冗余） |
| parent_id | BIGINT UNSIGNED | 父目录，**0 表示根**（不用 NULL，保证唯一约束对根生效） |
| parent_xid | VARCHAR(32) | 父目录 dir_<xid> 全串；空串=根（同服务冗余） |
| name | VARCHAR(100) | 目录名，同一父目录下唯一（含根） |
| created_at / updated_at | DATETIME | 自动维护 |

| 索引 | 列 | 用途 |
|------|----|------|
| PRIMARY | id | 主键（内部） |
| uk_dir_xid | (xid) | 对外标识唯一 |
| uk_dir_parent_name | (kb_id, parent_id, name) | 同父目录名称唯一 |
| idx_dir_kb_parent | (kb_id, parent_id) | 浏览子目录 |

删除规则：应用层事务内递归删除子目录与其中文章，级联清理正文文件与附件。

## articles 文章

```sql
CREATE TABLE articles (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid         VARCHAR(32)      NOT NULL,
  kb_id       BIGINT UNSIGNED NOT NULL,             -- 逻辑引用 knowledge_bases.id（同服务）
  kb_xid      VARCHAR(32)     NOT NULL DEFAULT '',  -- 知识库 kb_<xid> 全串（同服务冗余）
  dir_id      BIGINT UNSIGNED NOT NULL DEFAULT 0,   -- 0 = 根，逻辑引用 directories.id（同服务）
  dir_xid     VARCHAR(32)     NOT NULL DEFAULT '',  -- 目录 dir_<xid> 全串；空串=根（同服务冗余）
  type        TINYINT         NOT NULL,             -- 1 md 2 txt 3 pdf 4 docx 5 html（原始格式）
  title       VARCHAR(200)    NOT NULL,
  content_key VARCHAR(500)    NOT NULL,             -- 正文 md 对象 key（o/<obj_xid>，MySQL 不存内容）
  content_object_xid VARCHAR(32) NOT NULL DEFAULT '', -- 正文对象 obj_<xid>（对象引用）
  created_by_xid VARCHAR(32)  NOT NULL,             -- 创建者 usr_<xid> 全串（跨服务逻辑引用）
  created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_articles_xid (xid),
  KEY idx_articles_kb_dir (kb_id, dir_id),
  KEY idx_articles_kb_updated (kb_id, updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT UNSIGNED | 自增主键，仅内部与逻辑引用使用 |
| xid | VARCHAR(32) | 对外业务主键（<前缀>_<xid>，pkg/eid） |
| kb_id | BIGINT UNSIGNED | 所属知识库，逻辑引用 knowledge_bases.id（同服务） |
| kb_xid | VARCHAR(32) | 知识库 kb_<xid> 全串（同服务冗余） |
| dir_id | BIGINT UNSIGNED | 所属目录，0 表示根（同服务） |
| dir_xid | VARCHAR(32) | 目录 dir_<xid> 全串；空串=根（同服务冗余） |
| type | TINYINT | 原始格式：1 md / 2 txt / 3 pdf / 4 docx / 5 html |
| title | VARCHAR(200) | 标题，不设唯一（引用靠 xid 不靠标题） |
| content_key | VARCHAR(500) | 正文 md 对象 key（统一 `o/<obj_xid>`） |
| content_object_xid | VARCHAR(32) | 正文对象 obj_<xid>（对象引用，不解析 key） |
| created_by_xid | VARCHAR(32) | 创建者 usr_<xid> 全串（跨服务逻辑引用） |
| created_at / updated_at | DATETIME | 自动维护 |

| 索引 | 列 | 用途 |
|------|----|------|
| PRIMARY | id | 主键（内部） |
| uk_articles_xid | (xid) | 对外标识唯一 |
| idx_articles_kb_dir | (kb_id, dir_id) | 目录下文章列表 |
| idx_articles_kb_updated | (kb_id, updated_at) | 按更新时间排序 |

**展示与检索口径**：

| type | 用户展示 | content_key 存的内容 | 向量化 |
|------|---------|---------------------|--------|
| md / txt | 正文（按 content_key 取流渲染） | 正文 md 原文 | ✔ |
| pdf / docx / html | **源文件附件**（attachments kind=1，原始排版） | markitdown 转换结果 md | ✔ |

**内容读写**：保存 = 先写 Garage 对象 → 再更新 `content_key` → 删旧对象；中断最多留孤儿对象，不会出现指向不存在内容的行；向量化/重建索引/换模型按 `content_key` 读 md，不重跑 markitdown。

**文章互链（不建表）**：关联即 Markdown 链接，用户只写**相对路径**（`[标题](../design/arch.md)`、`![](assets/img.png)`）；前端渲染时以当前文章为基准解析：图片/文件拼 Gateway 资源地址 `/api/v1/articles/<xid>/resource?path=...`，文章跳 `/api/v1/articles/<xid>`；断链保留原文、渲染时标记失效。

## attachments 附件

```sql
CREATE TABLE attachments (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid           VARCHAR(32)      NOT NULL,
  kb_id         BIGINT UNSIGNED NOT NULL,           -- 逻辑引用 knowledge_bases.id（同服务）
  kb_xid        VARCHAR(32)     NOT NULL DEFAULT '',-- 知识库 kb_<xid> 全串（同服务冗余）
  article_id    BIGINT UNSIGNED NOT NULL,           -- 所属文章，逻辑引用 articles.id（同服务）
  article_xid   VARCHAR(32)     NOT NULL DEFAULT '',-- 文章 art_<xid> 全串（同服务冗余）
  kind          TINYINT         NOT NULL,           -- 1 源文件 2 内嵌图片/附件
  file_key      VARCHAR(500)    NOT NULL,           -- 对象 key（o/<obj_xid>，统一单桶）
  object_xid    VARCHAR(32)     NOT NULL DEFAULT '',-- 对象 obj_<xid>（对象引用，不解析 key）
  relative_path VARCHAR(500)    NOT NULL,           -- 规范化相对路径（如 assets/logo.png）
  original_name VARCHAR(255)    NOT NULL,
  content_type  VARCHAR(100)    NOT NULL,
  size          BIGINT UNSIGNED NOT NULL,
  sha256        CHAR(64)        NOT NULL,           -- 完整性校验（不去重）
  created_by_xid VARCHAR(32)    NOT NULL,           -- 上传者 usr_<xid> 全串（跨服务逻辑引用）
  created_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_attachments_xid (xid),
  UNIQUE KEY uk_attachments_article_path (article_id, relative_path),
  KEY idx_attachments_article (article_id, kind),
  KEY idx_attachments_kb (kb_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT UNSIGNED | 自增主键，仅内部使用 |
| xid | VARCHAR(32) | 对外业务主键（<前缀>_<xid>，pkg/eid），稳定 URL `/api/v1/attachments/<xid>` |
| kb_id | BIGINT UNSIGNED | 所属知识库，逻辑引用 knowledge_bases.id（同服务） |
| kb_xid | VARCHAR(32) | 知识库 kb_<xid> 全串（同服务冗余） |
| article_id | BIGINT UNSIGNED | 所属文章（源文件 1:1，内嵌图片 1:N） |
| article_xid | VARCHAR(32) | 文章 art_<xid> 全串（同服务冗余） |
| kind | TINYINT | 1 源文件：上传时建立、固定绑定、随文章删除；2 内嵌图片/附件：保存文章时按正文链接全量重建、孤儿即时清理 |
| file_key | VARCHAR(500) | Garage 对象 key，统一 `o/<obj_xid>` |
| object_xid | VARCHAR(32) | 对象 obj_<xid>（对象引用，不解析 key） |
| relative_path | VARCHAR(500) | 规范化 Markdown 相对路径，(article_id, relative_path) 唯一 |
| original_name | VARCHAR(255) | 原始文件名，展示用 |
| content_type | VARCHAR(100) | MIME 类型 |
| size | BIGINT UNSIGNED | 字节数 |
| sha256 | CHAR(64) | 完整性校验值 |
| created_by_xid | VARCHAR(32) | 上传者 usr_<xid> 全串（跨服务逻辑引用） |
| created_at | DATETIME | 自动维护（行不可变） |

| 索引 | 列 | 用途 |
|------|----|------|
| PRIMARY | id | 主键（内部） |
| uk_attachments_xid | (xid) | 对外标识唯一 |
| uk_attachments_article_path | (article_id, relative_path) | 「当前文章 + 相对路径」稳定找回对象（渲染期解析） |
| idx_attachments_article | (article_id, kind) | 文章附件清理/列举 |
| idx_attachments_kb | (kb_id) | 库内清理 |

约定：附件归单篇文章独占，不跨文章共享（文章复制需复制附件行与对象）；Repository 是 Garage 唯一直接访问方，Gateway 验签路由后由 Repository 校验权限并流式返回（Garage 不对外暴露）；`relative_path` 只存规范化相对路径（禁绝对路径、禁 `..` 越权）。

## kb_user_relations 知识库×用户关系

```sql
CREATE TABLE kb_user_relations (
  kb_id      BIGINT UNSIGNED NOT NULL,              -- 逻辑引用 knowledge_bases.id（同服务）
  kb_xid     VARCHAR(32)     NOT NULL DEFAULT '',   -- 知识库 kb_<xid> 全串（同服务冗余）
  user_xid   VARCHAR(32)     NOT NULL,              -- 用户 usr_<xid> 全串（跨服务逻辑引用）
  role       TINYINT         NOT NULL DEFAULT 0,   -- 0 非成员 1 reader 2 editor
  starred    TINYINT         NOT NULL DEFAULT 0,   -- 0 未收藏 1 已收藏
  starred_at DATETIME        NULL,                 -- 收藏时间，取消收藏置 NULL
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (kb_id, user_xid),
  KEY idx_kb_user_relations_user_xid (user_xid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

| 字段 | 类型 | 说明 |
|------|------|------|
| kb_id | BIGINT UNSIGNED | 联合主键；逻辑引用 knowledge_bases.id（同服务） |
| kb_xid | VARCHAR(32) | 知识库 kb_<xid> 全串（同服务冗余） |
| user_xid | VARCHAR(32) | 联合主键；用户 usr_<xid> 全串（跨服务逻辑引用） |
| role | TINYINT | 0 非成员 / 1 reader / 2 editor；owner 不入表（由 knowledge_bases.owner_xid 表达） |
| starred | TINYINT | 0 未收藏 / 1 已收藏 |
| starred_at | DATETIME NULL | 收藏时间（「我 star 的库」排序） |
| created_at | DATETIME | 首次产生关系的时间 |

| 索引 | 列 | 用途 |
|------|----|------|
| PRIMARY | (kb_id, user_xid) | 关系唯一（一行承载成员+收藏两个状态） |
| idx_kb_user_relations_user_xid | (user_xid) | 「我是成员的库」「我 star 的库」 |

规则：star/取消 star 与 `knowledge_bases.star_count` 增减同一事务；关系状态（own / member / star，加 ganrag_user 的 follow）构成检索权重信号。

---

# ganrag_ai（AI 服务）

## chat_sessions 聊天会话

```sql
CREATE TABLE chat_sessions (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid             VARCHAR(32)      NOT NULL,
  user_xid        VARCHAR(32)      NOT NULL,           -- 用户 usr_<xid> 全串（跨服务逻辑引用）
  kb_xid          VARCHAR(32)     NOT NULL DEFAULT '', -- 知识库 kb_<xid> 全串；空串=全局（跨服务逻辑引用）
  agent           VARCHAR(32)     NOT NULL DEFAULT '',
  context         LONGTEXT        NULL,                -- AI 内部压缩上下文：最新 Agent 提示词 + 摘要，不直接展示
  context_msg_id  BIGINT UNSIGNED NOT NULL DEFAULT 0,  -- 压缩游标（同服务）
  context_msg_xid VARCHAR(32)     NOT NULL DEFAULT '', -- 游标消息 msg_<xid> 全串（同服务冗余）
  title           VARCHAR(200)    NOT NULL DEFAULT '',
  created_at      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  last_message_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_chat_sessions_xid (xid),
  KEY idx_sessions_user_active (user_xid, last_message_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT UNSIGNED | 自增主键，仅内部与逻辑引用使用 |
| xid | VARCHAR(32) | 对外业务主键（<前缀>_<xid>，pkg/eid），兼 useChat chatId |
| user_xid | VARCHAR(32) | 用户 usr_<xid> 全串（跨服务逻辑引用） |
| kb_xid | VARCHAR(32) | 检索范围：空串全局 / 否则单知识库，随会话固化（跨服务逻辑引用） |
| agent | VARCHAR(32) | 当前智能体 id（粘性状态，P1.5 多智能体路由用） |
| context | LONGTEXT NULL | AI 内部压缩上下文；包含最新 Agent 提示词和摘要，NULL=未压缩；不直接返回前端 |
| context_msg_id | BIGINT UNSIGNED | 压缩游标：prompt = context + id ≥ 此值的消息 content；压缩替换 context 并推进游标，原始消息不删 |
| context_msg_xid | VARCHAR(32) | 游标消息 msg_<xid> 全串（同服务冗余） |
| title | VARCHAR(200) | 首条消息自动生成，可改 |
| created_at / updated_at / last_message_at | DATETIME | last_message_at 为右栏排序键 |

| 索引 | 列 | 用途 |
|------|----|------|
| PRIMARY | id | 主键（内部） |
| uk_chat_sessions_xid | (xid) | 对外标识唯一 |
| idx_sessions_user_active | (user_xid, last_message_at) | 「我的会话」按活跃倒序 |

## chat_messages 消息（信封层）

```sql
CREATE TABLE chat_messages (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid        VARCHAR(32)      NOT NULL,
  session_id BIGINT UNSIGNED NOT NULL,             -- 逻辑引用 chat_sessions.id（同服务）
  session_xid VARCHAR(32)    NOT NULL DEFAULT '',  -- 会话 chs_<xid> 全串（同服务冗余）
  role       TINYINT         NOT NULL,             -- 1 user 2 assistant
  agent      VARCHAR(32)     NOT NULL DEFAULT '',
  content    LONGTEXT        NOT NULL,             -- 最终输出（assistant）或用户正文
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_chat_messages_xid (xid),
  KEY idx_chat_messages_session (session_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT UNSIGNED | 自增主键，兼消息时序（压缩游标基于 id） |
| xid | VARCHAR(32) | 对外业务主键（<前缀>_<xid>，pkg/eid），兼 UIMessage.id |
| session_id | BIGINT UNSIGNED | 所属会话，逻辑引用 chat_sessions.id（同服务） |
| session_xid | VARCHAR(32) | 会话 chs_<xid> 全串（同服务冗余） |
| role | TINYINT | 1 user / 2 assistant |
| agent | VARCHAR(32) | 产生该消息的智能体 id，user 消息为空 |
| content | LONGTEXT | **最终输出层**（思考/工具细节在 parts） |
| created_at | DATETIME | 自动维护 |

| 索引 | 列 | 用途 |
|------|----|------|
| PRIMARY | id | 主键（兼时序） |
| uk_chat_messages_xid | (xid) | 对外标识唯一 |
| idx_chat_messages_session | (session_id, id) | 会话内按序加载、会话内检索 |

## chat_message_parts 消息步骤（全量细节层）

```sql
CREATE TABLE chat_message_parts (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  message_id BIGINT UNSIGNED NOT NULL,             -- 逻辑引用 chat_messages.id（同服务）
  message_xid VARCHAR(32)    NOT NULL DEFAULT '',  -- 消息 msg_<xid> 全串（同服务冗余）
  step       INT UNSIGNED    NOT NULL DEFAULT 0,   -- 步骤号（UIMessage step-start 边界）
  seq        INT UNSIGNED    NOT NULL,             -- 消息内全局顺序（0 起）
  type       VARCHAR(32)     NOT NULL,             -- UIMessage part 类型
  content    LONGTEXT        NULL,                 -- 可检索文本抽取
  meta       JSON            NULL,                 -- SDK part payload 原样存储
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_message_parts_seq (message_id, seq)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

| 字段 | 类型 | 说明 |
|------|------|------|
| id | BIGINT UNSIGNED | 自增主键，仅内部使用（不单独对外寻址） |
| message_id | BIGINT UNSIGNED | 所属消息，逻辑引用 chat_messages.id（同服务） |
| message_xid | VARCHAR(32) | 消息 msg_<xid> 全串（同服务冗余） |
| step | INT UNSIGNED | 步骤号（0 起），对应 UIMessage step-start 边界 |
| seq | INT UNSIGNED | 消息内全局顺序，(message_id, seq) 唯一 |
| type | VARCHAR(32) | 对齐 Vercel AI SDK `UIMessage` part 类型（见下表） |
| content | LONGTEXT NULL | 可检索文本（text / reasoning / tool output 全文） |
| meta | JSON NULL | SDK part payload 原样存储 |
| created_at | DATETIME | 自动维护 |

| 索引 | 列 | 用途 |
|------|----|------|
| PRIMARY | id | 主键（内部） |
| uk_message_parts_seq | (message_id, seq) | 按序还原一条消息的全部步骤 |

**type 约定（= UIMessage part 类型，扩展展示零 DDL）**：

| type | content 存 | meta 存 | useChat 展示 |
|------|-----------|---------|---------|
| `text` | 正文段文本 | — | streamdown 渲染 |
| `reasoning` | 思考全文 | `state` 等 | 思考过程折叠区 |
| `tool` | `output` 全文（可检索） | `toolCallId` / `toolName` / `input` / `state` / `output` / `errorText` 原样 | 工具卡片（输入/输出/错误/加载态） |
| `source-document` | 标题 / 检索片段 | `article_xid` / `title` / `snippet` / `score` | 来源列表，点击跳 `/articles/<article_xid>` |
| `source-url` | 标题 | `url` 等 | 外链来源 |
| `file` | — | `mediaType` / `url`（附件 xid 稳定 URL） | 图片/文件气泡 |
| `agent-system` | Agent 专属系统提示词 | `agent_id` / 注入时间 | 内部使用，禁止返回前端 |
| （新增展示需求） | 按需 | 按需 | 自定义 part，零 DDL 变更 |

**存储与读取约定**：

- 全量存储：一次 assistant 回答的每一步（reasoning、每次 tool 调用与结果、每个正文段、来源）都落 part 行
- payload 原样：消息可无损重组为 `UIMessage[]` 喂 `useChat(initialMessages)`
- prompt 拼装：固定 System Prompt + `context` + 游标后消息的 `content`；压缩后的 `context` 只保留最新 Agent 提示词和摘要；多智能体如需工具轨迹从 parts 按 type 过滤
- 检索：`search_context` 覆盖 `chat_messages.content` 与可检索 parts 的 `content`，明确排除 `agent-system`
- 安全边界：`agent-system` 只由服务端 Agent 注册表生成，不进入 `useChat(initialMessages)` 或任何用户可见 API 响应；序列化必须按展示类型白名单处理
- 存储方式：`agent-system` 作为对应用户消息的隐藏 part 保存，不创建独立的隐藏 `chat_messages` 行；现有 `type VARCHAR(32)` 已能容纳该类型，因此本次不新增表、不新增字段、不新增迁移
- 删除：删会话 = 应用层级联删 messages + parts
- 聊天记录整体入 MySQL（含工具结果全文）：交易型短文本，不适用「文章内容不入 MySQL」规则

---

# ganrag_common（共享基础设施）

> 唯一共享 schema：只存运维/失败任务类数据，业务服务统一通过 `pkg/asynctask` 访问。
> 业务服务在自身事务内用全限定表名 `ganrag_common.async_tasks` 写入，保证任务与业务行同事务。

## async_tasks 异步任务 / 失败记录

```sql
CREATE TABLE async_tasks (
  id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid               VARCHAR(32)   NOT NULL,             -- tsk_<xid>，后台展示
  task_type         VARCHAR(64)   NOT NULL,             -- 任务/失败类型，如 garage_object_delete
  source_service    VARCHAR(32)   NOT NULL,             -- 上报服务短名，如 repository / ai
  dedupe_key        VARCHAR(191)  NULL,                 -- 同服务同类型幂等键；NULL 表示不去重
  payload           JSON          NULL,                 -- 类型相关参数（对象 key、来源实体 xid 等）
  requested_by_xid  VARCHAR(32)   NOT NULL DEFAULT '',  -- 触发用户 usr_<xid>
  status            TINYINT       NOT NULL DEFAULT 0,   -- 0 待处理 1 已解决 2 已忽略
  attempts          INT UNSIGNED  NOT NULL DEFAULT 0,   -- 失败次数，Fail 时 +1
  last_error        VARCHAR(1000) NOT NULL DEFAULT '',  -- 最近一次失败原因
  resolved_at       DATETIME      NULL,                 -- 解决/忽略时间
  created_at        DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at        DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_async_tasks_xid (xid),
  UNIQUE KEY uk_async_tasks_dedupe (source_service, task_type, dedupe_key),
  KEY idx_async_tasks_type_status (source_service, task_type, status, updated_at),
  KEY idx_async_tasks_status_updated (status, updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

| 字段 | 类型 | 说明 |
|------|------|------|
| xid | VARCHAR(32) | 任务业务主键 `tsk_<xid>`（pkg/eid），后台展示 |
| task_type | VARCHAR(64) | 任务/失败类型；扩展只加常量，不改表 |
| source_service | VARCHAR(32) | 上报服务短名，唯一键组成部分 |
| dedupe_key | VARCHAR(191) NULL | 同服务同类型幂等键（如 `object_xid`）；NULL 不去重 |
| payload | JSON NULL | 类型相关参数，结构由 task_type 约定 |
| requested_by_xid | VARCHAR(32) | 触发用户 usr_<xid>，系统任务空串 |
| status | TINYINT | 0 待处理 / 1 已解决 / 2 已忽略 |
| attempts / last_error | INT UNSIGNED / VARCHAR(1000) | 失败累计与最近错误 |
| resolved_at | DATETIME NULL | 解决/忽略时间 |

| 索引 | 列 | 用途 |
|------|----|------|
| PRIMARY | id | 主键（内部） |
| uk_async_tasks_xid | (xid) | 任务对外标识唯一 |
| uk_async_tasks_dedupe | (source_service, task_type, dedupe_key) | 幂等上报（NULL 不参与去重） |
| idx_async_tasks_type_status | (source_service, task_type, status, updated_at) | 后台按服务/类型筛选 |
| idx_async_tasks_status_updated | (status, updated_at) | worker/后台待处理队列 |

首期任务类型：

- `garage_object_delete`（`source_service=repository`）：Garage 对象删除；payload 含 `object_xid` / `object_key` / `source` / `source_xid`。

---

## 参考

- 设计决策与推导：[data-model.md](data-model.md)
- 规范（外键 / XID / Redis Key / 内容口径）：根 [AGENTS.md](../AGENTS.md)
- 实施跟踪：[fix.md](fix.md)、[todo.md](todo.md)
- User 服务流程与接口：[service/user/docs/architecture.md](../service/user/docs/architecture.md)
