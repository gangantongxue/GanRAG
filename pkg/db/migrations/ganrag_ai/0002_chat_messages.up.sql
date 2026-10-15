-- 0002_chat_messages：聊天消息（信封层）
-- content 为最终输出层（思考/工具细节在 chat_message_parts）；prompt 拼装 = context + 游标后消息 content
-- xid 兼 UIMessage.id（前端 Vercel AI SDK useChat）
CREATE TABLE chat_messages (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid        CHAR(20)      NOT NULL,               -- XID 业务主键，对外标识
  session_id BIGINT UNSIGNED NOT NULL,             -- 逻辑引用 chat_sessions.id
  role       TINYINT         NOT NULL,             -- 1 user 2 assistant
  agent      VARCHAR(32)     NOT NULL DEFAULT '',  -- 产生该消息的智能体 id，user 消息为空
  content    LONGTEXT        NOT NULL,             -- 最终输出（assistant）或用户正文
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_chat_messages_xid (xid),
  KEY idx_chat_messages_session (session_id, id)   -- 会话内按序加载、会话内检索
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
