-- 0003_user_follows：用户关注关系（社交接口 P2 开放，表结构先建齐）
-- 与 users 同 schema，但沿用跨服务表风格只做逻辑引用（应用层校验），不建物理外键
CREATE TABLE user_follows (
  follower_id BIGINT UNSIGNED NOT NULL,              -- 逻辑引用 users.id
  followee_id BIGINT UNSIGNED NOT NULL,              -- 逻辑引用 users.id
  created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (follower_id, followee_id),
  KEY idx_user_follows_followee (followee_id)        -- 「谁关注了我」
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
