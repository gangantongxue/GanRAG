-- 0004_xid_prefix：业务主键改「<前缀>_<xid>」全串口径（pkg/eid 生成与校验）
-- 列宽 CHAR(20) → VARCHAR(32)；既有行前拼类型前缀（chs_/msg_），保持取值合法
-- xid 兼 useChat chatId / UIMessage.id，前缀不影响前端透传
ALTER TABLE chat_sessions
  MODIFY xid VARCHAR(32) NOT NULL COMMENT '业务主键，形如 chs_<xid>（pkg/eid），对外标识';
ALTER TABLE chat_messages
  MODIFY xid VARCHAR(32) NOT NULL COMMENT '业务主键，形如 msg_<xid>（pkg/eid），对外标识';

UPDATE chat_sessions SET xid = CONCAT('chs_', xid) WHERE LEFT(xid, 4) <> 'chs_';
UPDATE chat_messages SET xid = CONCAT('msg_', xid) WHERE LEFT(xid, 4) <> 'msg_';
