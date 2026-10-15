-- 0005_kb_user_relations：知识库×用户关系（成员 + 收藏单表，与「关系状态」口径对齐）
-- owner 不入表（由 knowledge_bases.owner_id 表达）；star 增减与 knowledge_bases.star_count 同事务
CREATE TABLE kb_user_relations (
  kb_id      BIGINT UNSIGNED NOT NULL,              -- 逻辑引用 knowledge_bases.id
  user_id    BIGINT UNSIGNED NOT NULL,              -- 逻辑引用 users.id
  role       TINYINT         NOT NULL DEFAULT 0,   -- 0 非成员 1 reader 2 editor
  starred    TINYINT         NOT NULL DEFAULT 0,   -- 0 未收藏 1 已收藏
  starred_at DATETIME        NULL,                 -- 收藏时间（「我 star 的库」排序），取消收藏置 NULL
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (kb_id, user_id),
  KEY idx_kb_user_relations_user (user_id)         -- 「我是成员的库」「我 star 的库」
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
