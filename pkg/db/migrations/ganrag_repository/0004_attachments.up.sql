-- 0004_attachments：附件（源文件 + 内嵌图片）
-- 附件归单篇文章独占（不跨文章共享）：kind=1 源文件固定绑定随文章删除；kind=2 内嵌图片保存时按正文链接重建、孤儿即时清理
-- file-store bucket 约定 kb-<kb_id>；稳定 URL /api/v1/attachments/<xid>
CREATE TABLE attachments (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid           CHAR(20)      NOT NULL,             -- XID 业务主键，对外标识（稳定 URL）
  kb_id         BIGINT UNSIGNED NOT NULL,           -- 逻辑引用 knowledge_bases.id
  article_id    BIGINT UNSIGNED NOT NULL,           -- 所属文章，逻辑引用 articles.id
  kind          TINYINT         NOT NULL,           -- 1 源文件（pdf/docx/html 原文） 2 内嵌图片/附件
  file_key      VARCHAR(500)    NOT NULL,           -- file-store key（bucket: kb-<kb_id>）
  original_name VARCHAR(255)    NOT NULL,           -- 原始文件名（展示用）
  content_type  VARCHAR(100)    NOT NULL,           -- image/png、application/pdf 等
  size          BIGINT UNSIGNED NOT NULL,
  sha256        CHAR(64)        NOT NULL,           -- 完整性校验（不去重，同文件重复上传即多行）
  created_by    BIGINT UNSIGNED NOT NULL,           -- 逻辑引用 users.id
  created_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_attachments_xid (xid),
  KEY idx_attachments_article (article_id, kind),
  KEY idx_attachments_kb (kb_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
