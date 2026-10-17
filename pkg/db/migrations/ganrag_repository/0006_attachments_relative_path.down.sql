-- 0006_attachments_relative_path 回滚：删除 relative_path 及其唯一索引
ALTER TABLE attachments
  DROP INDEX uk_attachments_article_path,
  DROP COLUMN relative_path;
