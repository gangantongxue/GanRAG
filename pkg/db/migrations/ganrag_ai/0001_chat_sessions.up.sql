-- 0001_chat_sessions：聊天会话
-- kb_id 0=全局（随会话固化检索范围）；context + context_msg_id 会话级压缩上下文与游标（替换式压缩，原始消息不删）
-- xid 兼 useChat chatId（前端 Vercel AI SDK）
CREATE TABLE chat_sessions (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid             CHAR(20)      NOT NULL,             -- XID 业务主键，对外标识
  user_id         BIGINT UNSIGNED NOT NULL,            -- 逻辑引用 users.id
  kb_id           BIGINT UNSIGNED NOT NULL DEFAULT 0,  -- 0 = 全局，否则绑定单个知识库（逻辑引用 knowledge_bases.id）
  agent           VARCHAR(32)     NOT NULL DEFAULT '', -- 当前智能体 id（粘性状态，P1.5 多智能体路由用）
  context         LONGTEXT        NULL,                -- 压缩后的模型上下文；NULL = 未压缩
  context_msg_id  BIGINT UNSIGNED NOT NULL DEFAULT 0,  -- 拼接游标：消息 id >= 此值接在 context 后进 prompt
  title           VARCHAR(200)    NOT NULL DEFAULT '', -- 首条消息自动生成，可改
  created_at      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at      DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  last_message_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP, -- 右栏排序键
  PRIMARY KEY (id),
  UNIQUE KEY uk_chat_sessions_xid (xid),
  KEY idx_sessions_user_active (user_id, last_message_at)  -- 「我的会话」按活跃倒序
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
