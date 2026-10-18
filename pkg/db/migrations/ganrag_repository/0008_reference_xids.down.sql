-- 0008_reference_xids down：恢复跨服务自增 ID 列并移除 xid 冗余列
-- 注意：跨服务自增 ID 无法从 xid 还原；仅适用于空表/测试环境。
ALTER TABLE kb_user_relations
  DROP KEY idx_kb_user_relations_user_xid,
  CHANGE COLUMN user_xid user_id BIGINT UNSIGNED NOT NULL COMMENT '逻辑引用 users.id',
  DROP COLUMN kb_xid;

ALTER TABLE attachments
  CHANGE COLUMN created_by_xid created_by BIGINT UNSIGNED NOT NULL COMMENT '逻辑引用 users.id',
  DROP COLUMN object_xid,
  DROP COLUMN article_xid,
  DROP COLUMN kb_xid;

ALTER TABLE articles
  CHANGE COLUMN created_by_xid created_by BIGINT UNSIGNED NOT NULL COMMENT '逻辑引用 users.id',
  DROP COLUMN content_object_xid,
  DROP COLUMN dir_xid,
  DROP COLUMN kb_xid;

ALTER TABLE directories
  DROP COLUMN parent_xid,
  DROP COLUMN kb_xid;

ALTER TABLE knowledge_bases
  CHANGE COLUMN owner_xid owner_id BIGINT UNSIGNED NOT NULL COMMENT '逻辑引用 users.id';
