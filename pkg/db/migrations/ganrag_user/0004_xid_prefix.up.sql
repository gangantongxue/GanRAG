-- 0004_xid_prefix：业务主键改「<前缀>_<xid>」全串口径（pkg/eid 生成与校验）
-- 列宽 CHAR(20) → VARCHAR(32)；既有行前拼类型前缀（usr_/ses_），保持取值合法
ALTER TABLE users
  MODIFY xid VARCHAR(32) NOT NULL COMMENT '业务主键，形如 usr_<xid>（pkg/eid），对外标识';
ALTER TABLE sessions
  MODIFY xid VARCHAR(32) NOT NULL COMMENT '业务主键，形如 ses_<xid>（pkg/eid），对外标识（设备管理接口用）';

UPDATE users    SET xid = CONCAT('usr_', xid) WHERE LEFT(xid, 4) <> 'usr_';
UPDATE sessions SET xid = CONCAT('ses_', xid) WHERE LEFT(xid, 4) <> 'ses_';
