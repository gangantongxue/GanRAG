-- 0003_chat_message_parts：消息步骤（全量细节层，对齐 Vercel AI SDK UIMessage part 模型）
-- type 取值即 UIMessage part 类型（text / reasoning / tool / source-document / source-url / file / ...），扩展展示零 DDL
-- content 为可检索文本抽取；meta 存 SDK part payload 原样，消息可无损重组为 UIMessage[]
CREATE TABLE chat_message_parts (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  message_id BIGINT UNSIGNED NOT NULL,             -- 逻辑引用 chat_messages.id
  step       INT UNSIGNED    NOT NULL DEFAULT 0,   -- 步骤号（对应 UIMessage step-start 边界，0 起）
  seq        INT UNSIGNED    NOT NULL,             -- 消息内全局顺序（0 起）
  type       VARCHAR(32)     NOT NULL,             -- UIMessage part 类型
  content    LONGTEXT        NULL,                 -- 可检索文本（text / reasoning / tool output 全文）
  meta       JSON            NULL,                 -- SDK part payload 原样存储
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_message_parts_seq (message_id, seq)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
