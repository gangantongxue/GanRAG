-- 0008_reference_xids：逻辑引用统一带 xid（口径见根 AGENTS.md）
-- 同服务引用保留自增 ID 并冗余目标 xid；跨服务引用只存目标 xid（下游拿不到自增 ID）。
-- 当前全部表为空，跨服务列替换无历史数据可回填。
ALTER TABLE knowledge_bases
  CHANGE COLUMN owner_id owner_xid VARCHAR(32) NOT NULL COMMENT '库主 usr_<xid> 全串（跨服务逻辑引用）';

ALTER TABLE directories
  ADD COLUMN kb_xid VARCHAR(32) NOT NULL DEFAULT '' COMMENT '知识库 kb_<xid> 全串（同服务冗余）' AFTER kb_id,
  ADD COLUMN parent_xid VARCHAR(32) NOT NULL DEFAULT '' COMMENT '父目录 dir_<xid> 全串；空串=根（同服务冗余）' AFTER parent_id;

ALTER TABLE articles
  ADD COLUMN kb_xid VARCHAR(32) NOT NULL DEFAULT '' COMMENT '知识库 kb_<xid> 全串（同服务冗余）' AFTER kb_id,
  ADD COLUMN dir_xid VARCHAR(32) NOT NULL DEFAULT '' COMMENT '目录 dir_<xid> 全串；空串=根（同服务冗余）' AFTER dir_id,
  ADD COLUMN content_object_xid VARCHAR(32) NOT NULL DEFAULT '' COMMENT '正文对象 obj_<xid>（Garage key: o/<object_xid>）' AFTER content_key,
  CHANGE COLUMN created_by created_by_xid VARCHAR(32) NOT NULL COMMENT '创建者 usr_<xid> 全串（跨服务逻辑引用）';

ALTER TABLE attachments
  ADD COLUMN kb_xid VARCHAR(32) NOT NULL DEFAULT '' COMMENT '知识库 kb_<xid> 全串（同服务冗余）' AFTER kb_id,
  ADD COLUMN article_xid VARCHAR(32) NOT NULL DEFAULT '' COMMENT '文章 art_<xid> 全串（同服务冗余）' AFTER article_id,
  ADD COLUMN object_xid VARCHAR(32) NOT NULL DEFAULT '' COMMENT '对象 obj_<xid>（Garage key: o/<object_xid>）' AFTER file_key,
  CHANGE COLUMN created_by created_by_xid VARCHAR(32) NOT NULL COMMENT '上传者 usr_<xid> 全串（跨服务逻辑引用）';

ALTER TABLE kb_user_relations
  ADD COLUMN kb_xid VARCHAR(32) NOT NULL DEFAULT '' COMMENT '知识库 kb_<xid> 全串（同服务冗余）' AFTER kb_id,
  CHANGE COLUMN user_id user_xid VARCHAR(32) NOT NULL COMMENT '用户 usr_<xid> 全串（跨服务逻辑引用）',
  ADD KEY idx_kb_user_relations_user_xid (user_xid);
