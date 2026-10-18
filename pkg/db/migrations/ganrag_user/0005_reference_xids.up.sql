-- 0005_reference_xids：同服务逻辑引用冗余目标 xid（口径见根 AGENTS.md）
-- 保留自增 ID 作为关联/索引键，同时冗余 xid 供对外展示与后续跨服务使用；
-- 当前表为空，无需历史数据回填。
ALTER TABLE sessions
  ADD COLUMN user_xid VARCHAR(32) NOT NULL DEFAULT '' COMMENT '用户 usr_<xid> 全串（同服务冗余）' AFTER user_id;

ALTER TABLE user_follows
  ADD COLUMN follower_xid VARCHAR(32) NOT NULL DEFAULT '' COMMENT '关注者 usr_<xid> 全串（同服务冗余）' AFTER follower_id,
  ADD COLUMN followee_xid VARCHAR(32) NOT NULL DEFAULT '' COMMENT '被关注者 usr_<xid> 全串（同服务冗余）' AFTER followee_id;
