-- 0003_user_follows：用户关注关系（社交接口 P2 开放，表结构先建齐）
-- 逻辑引用 users.id（禁止物理外键）；自关注放行业务允许
CREATE TABLE user_follows (
  follower_id BIGINT UNSIGNED NOT NULL,              -- 逻辑引用 users.id
  followee_id BIGINT UNSIGNED NOT NULL,              -- 逻辑引用 users.id
  created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (follower_id, followee_id),
  KEY idx_user_follows_followee (followee_id)        -- 「谁关注了我」
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
