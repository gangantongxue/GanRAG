-- 0005_reference_xids down：恢复跨服务自增 ID 列并移除 xid 冗余列
-- 注意：跨服务自增 ID 无法从 xid 还原；仅适用于空表/测试环境。
ALTER TABLE chat_message_parts
  DROP COLUMN message_xid;

ALTER TABLE chat_messages
  DROP COLUMN session_xid;

ALTER TABLE chat_sessions
  DROP COLUMN context_msg_xid,
  CHANGE COLUMN kb_xid kb_id BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '0 = 全局，否则绑定单个知识库（逻辑引用 knowledge_bases.id）',
  CHANGE COLUMN user_xid user_id BIGINT UNSIGNED NOT NULL COMMENT '逻辑引用 users.id';
