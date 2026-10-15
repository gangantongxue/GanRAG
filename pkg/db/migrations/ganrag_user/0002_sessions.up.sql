-- 0002_sessions：登录会话（每设备一行，支持多端登录；替代 refresh_tokens）
-- 逻辑引用 users.id（禁止物理外键）；轮换原地更新 token_hash，旧 token 复用按无效拒绝
CREATE TABLE sessions (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid            CHAR(20)      NOT NULL,             -- XID 业务主键，对外标识（设备管理接口用）
  user_id        BIGINT UNSIGNED NOT NULL,           -- 逻辑引用 users.id
  device_id      VARCHAR(64)  NOT NULL,              -- 设备码（客户端生成持久 UUID）
  device_info    VARCHAR(255) NOT NULL DEFAULT '',   -- 设备信息（UA/平台摘要，设备列表展示）
  token_hash     CHAR(64)     NOT NULL,              -- 当前 refresh token 的 SHA-256，轮换原地更新
  expires_at     DATETIME     NOT NULL,              -- 会话过期（refresh TTL 固定期限）
  revoked_at     DATETIME     NULL,                  -- NULL=在线；非 NULL=登出/被踢
  last_active_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,  -- 刷新时顺带更新
  created_at     DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_sessions_xid (xid),
  UNIQUE KEY uk_sessions_token_hash (token_hash),
  UNIQUE KEY uk_sessions_user_device (user_id, device_id),  -- 每设备一行，同设备重登覆盖
  KEY idx_sessions_user_id (user_id)                -- 「我的设备列表」/ 吊销全部
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
