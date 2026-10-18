-- 0005_reference_xids down：移除同服务引用 xid 冗余列
ALTER TABLE user_follows
  DROP COLUMN followee_xid,
  DROP COLUMN follower_xid;

ALTER TABLE sessions
  DROP COLUMN user_xid;
