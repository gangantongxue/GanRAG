-- 0001_users：用户账号（邮箱登录标识 + XID 业务主键）
CREATE TABLE users (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid           CHAR(20)      NOT NULL,             -- XID 业务主键，对外标识（github.com/rs/xid）
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
