-- 0002_refresh_tokens：refresh token（只存哈希）
-- revoked_at 为轮换/吊销标记：NULL 表示活跃，非 NULL 表示已作废；
-- 已作废行保留至其自然过期，用于「重用已作废 token → 吊销该用户全部」的重用检测。
CREATE TABLE refresh_tokens (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id    BIGINT UNSIGNED NOT NULL,
  token_hash CHAR(64)        NOT NULL,  -- SHA-256 hex（明文 refresh 的哈希）
  expires_at DATETIME        NOT NULL,
  revoked_at DATETIME        NULL,      -- 轮换/吊销时间；NULL = 活跃
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_refresh_token_hash (token_hash),
  KEY idx_refresh_tokens_user_id (user_id),
  CONSTRAINT fk_refresh_tokens_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
