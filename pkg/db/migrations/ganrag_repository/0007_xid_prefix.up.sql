-- 0007_xid_prefix：业务主键改「<前缀>_<xid>」全串口径（pkg/eid 生成与校验）
-- 列宽 CHAR(20) → VARCHAR(32)；既有行前拼类型前缀（kb_/dir_/art_/att_），保持取值合法
ALTER TABLE knowledge_bases
  MODIFY xid VARCHAR(32) NOT NULL COMMENT '业务主键，形如 kb_<xid>（pkg/eid），对外标识';
ALTER TABLE directories
  MODIFY xid VARCHAR(32) NOT NULL COMMENT '业务主键，形如 dir_<xid>（pkg/eid），对外标识';
ALTER TABLE articles
  MODIFY xid VARCHAR(32) NOT NULL COMMENT '业务主键，形如 art_<xid>（pkg/eid），对外标识';
ALTER TABLE attachments
  MODIFY xid VARCHAR(32) NOT NULL COMMENT '业务主键，形如 att_<xid>（pkg/eid），对外标识（稳定 URL）';

UPDATE knowledge_bases SET xid = CONCAT('kb_',  xid) WHERE LEFT(xid, 3) <> 'kb_';
UPDATE directories     SET xid = CONCAT('dir_', xid) WHERE LEFT(xid, 4) <> 'dir_';
UPDATE articles        SET xid = CONCAT('art_', xid) WHERE LEFT(xid, 4) <> 'art_';
UPDATE attachments     SET xid = CONCAT('att_', xid) WHERE LEFT(xid, 4) <> 'att_';
