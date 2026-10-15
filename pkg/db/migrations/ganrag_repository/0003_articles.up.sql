-- 0003_articles：文章
-- MySQL 不存内容：content_key 为正文 md 的 file-store key（bucket: kb-<kb_id>）
-- md/txt 的 content_key 内为正文原文（展示+向量化）；pdf/docx/html 内为 markitdown 转换结果（仅向量化与 AI 检索，展示走源文件附件）
-- 文章互链不建表：关联即 Markdown 链接（稳定 URL 指向 xid）
CREATE TABLE articles (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid         CHAR(20)      NOT NULL,               -- XID 业务主键，对外标识
  kb_id       BIGINT UNSIGNED NOT NULL,             -- 逻辑引用 knowledge_bases.id
  dir_id      BIGINT UNSIGNED NOT NULL DEFAULT 0,   -- 0 = 根，逻辑引用 directories.id
  type        TINYINT         NOT NULL,             -- 1 md 2 txt 3 pdf 4 docx 5 html（原始格式）
  title       VARCHAR(200)    NOT NULL,             -- 不设唯一（引用靠 xid 不靠标题）
  content_key VARCHAR(500)    NOT NULL,             -- 正文 md 的 file-store key
  created_by  BIGINT UNSIGNED NOT NULL,             -- 逻辑引用 users.id
  created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_articles_xid (xid),
  KEY idx_articles_kb_dir (kb_id, dir_id),
  KEY idx_articles_kb_updated (kb_id, updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
