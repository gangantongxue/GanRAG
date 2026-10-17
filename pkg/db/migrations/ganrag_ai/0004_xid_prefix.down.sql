-- 0004_xid_prefix 回滚：剥去类型前缀，列宽改回 CHAR(20)
UPDATE chat_sessions SET xid = SUBSTRING(xid, 5) WHERE LEFT(xid, 4) = 'chs_';
UPDATE chat_messages SET xid = SUBSTRING(xid, 5) WHERE LEFT(xid, 4) = 'msg_';

ALTER TABLE chat_sessions
  MODIFY xid CHAR(20) NOT NULL COMMENT 'XID 业务主键，对外标识';
ALTER TABLE chat_messages
  MODIFY xid CHAR(20) NOT NULL COMMENT 'XID 业务主键，对外标识';
