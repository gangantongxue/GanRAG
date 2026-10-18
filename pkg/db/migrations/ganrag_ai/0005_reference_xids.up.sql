-- 0005_reference_xids：逻辑引用统一带 xid（口径见根 AGENTS.md）
-- 跨服务引用（users / knowledge_bases）只存 xid；同服务引用保留自增 ID 并冗余 xid。
-- 当前表为空，无历史数据回填。
ALTER TABLE chat_sessions
  CHANGE COLUMN user_id user_xid VARCHAR(32) NOT NULL COMMENT '用户 usr_<xid> 全串（跨服务逻辑引用）',
  CHANGE COLUMN kb_id kb_xid VARCHAR(32) NOT NULL DEFAULT '' COMMENT '知识库 kb_<xid> 全串；空串=全局（跨服务逻辑引用）',
  ADD COLUMN context_msg_xid VARCHAR(32) NOT NULL DEFAULT '' COMMENT '上下文游标消息 msg_<xid> 全串（同服务冗余）' AFTER context_msg_id;

ALTER TABLE chat_messages
  ADD COLUMN session_xid VARCHAR(32) NOT NULL DEFAULT '' COMMENT '会话 chs_<xid> 全串（同服务冗余）' AFTER session_id;

ALTER TABLE chat_message_parts
  ADD COLUMN message_xid VARCHAR(32) NOT NULL DEFAULT '' COMMENT '消息 msg_<xid> 全串（同服务冗余）' AFTER message_id;
