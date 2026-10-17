-- 0007_xid_prefix 回滚：剥去类型前缀，列宽改回 CHAR(20)
UPDATE knowledge_bases SET xid = SUBSTRING(xid, 4) WHERE LEFT(xid, 3) = 'kb_';
UPDATE directories     SET xid = SUBSTRING(xid, 5) WHERE LEFT(xid, 4) = 'dir_';
UPDATE articles        SET xid = SUBSTRING(xid, 5) WHERE LEFT(xid, 4) = 'art_';
UPDATE attachments     SET xid = SUBSTRING(xid, 5) WHERE LEFT(xid, 4) = 'att_';

ALTER TABLE knowledge_bases
  MODIFY xid CHAR(20) NOT NULL COMMENT 'XID 业务主键，对外标识';
ALTER TABLE directories
  MODIFY xid CHAR(20) NOT NULL COMMENT 'XID 业务主键，对外标识';
ALTER TABLE articles
  MODIFY xid CHAR(20) NOT NULL COMMENT 'XID 业务主键，对外标识';
ALTER TABLE attachments
  MODIFY xid CHAR(20) NOT NULL COMMENT 'XID 业务主键，对外标识（稳定 URL）';
