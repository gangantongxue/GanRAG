-- 回滚 0001_users：先删除 refresh_tokens（0002.down）再删本表
DROP TABLE users;
