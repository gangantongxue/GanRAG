-- 0006_attachments_relative_path：attachments 增 relative_path（架构迁移：移除 File Store 直连 Garage）
-- 「当前文章 + 规范化相对路径」唯一，支撑 Markdown 渲染期按相对路径稳定找回对象；
-- 只存规范化相对路径（如 assets/logo.png），禁绝对路径、禁 `..` 越界，由应用层写入前规范化校验
ALTER TABLE attachments
  ADD COLUMN relative_path VARCHAR(500) NOT NULL AFTER file_key,
  ADD UNIQUE KEY uk_attachments_article_path (article_id, relative_path);
