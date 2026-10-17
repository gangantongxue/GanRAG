-- 0004_xid_prefix 回滚：剥去类型前缀，列宽改回 CHAR(20)
UPDATE users    SET xid = SUBSTRING(xid, 5) WHERE LEFT(xid, 4) = 'usr_';
UPDATE sessions SET xid = SUBSTRING(xid, 5) WHERE LEFT(xid, 4) = 'ses_';

ALTER TABLE users
  MODIFY xid CHAR(20) NOT NULL COMMENT 'XID 业务主键，对外标识（github.com/rs/xid）';
ALTER TABLE sessions
  MODIFY xid CHAR(20) NOT NULL COMMENT 'XID 业务主键，对外标识（设备管理接口用）';
