-- 0001_knowledge_bases：知识库
-- owner_id 逻辑引用 users.id（跨服务，禁止物理外键）；公开库陌生人只读由 visibility 推导
CREATE TABLE knowledge_bases (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid           CHAR(20)      NOT NULL,             -- XID 业务主键，对外标识
  owner_id      BIGINT UNSIGNED NOT NULL,           -- 逻辑引用 users.id
  name          VARCHAR(100)  NOT NULL,
  description   VARCHAR(500)  NOT NULL DEFAULT '',
  visibility    TINYINT       NOT NULL DEFAULT 0,   -- 0 私有 1 公开
  article_count INT UNSIGNED  NOT NULL DEFAULT 0,   -- 冗余计数，同事务增减
  star_count    INT UNSIGNED  NOT NULL DEFAULT 0,   -- 冗余计数，同事务增减
  created_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_kb_xid (xid),
  UNIQUE KEY uk_kb_owner_name (owner_id, name),     -- 同一 owner 下名称唯一
  KEY idx_kb_visibility_updated (visibility, updated_at),  -- 发现页/搜索排序
  KEY idx_kb_owner (owner_id)                       -- 「我的库」
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
