-- 0002_directories：目录树
-- parent_id 0 = 根（不用 NULL，保证唯一约束对根同样生效）；删除目录由应用层递归处理
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
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
