-- 0001_async_tasks：公共异步任务/失败记录（跨服务共享基础设施表）
-- 归属 ganrag_common：不存业务领域数据，所有服务通过 pkg/asynctask 访问；
-- 业务服务在自身事务内用全限定表名 ganrag_common.async_tasks 写入，保证与业务行同事务。
CREATE TABLE async_tasks (
  id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  xid               VARCHAR(32)   NOT NULL,             -- 业务主键 tsk_<xid>（pkg/eid），后台展示
  task_type         VARCHAR(64)   NOT NULL,             -- 任务/失败类型，如 garage_object_delete
  source_service    VARCHAR(32)   NOT NULL,             -- 上报服务短名，如 repository / ai
  dedupe_key        VARCHAR(191)  NULL,                 -- 同服务同类型幂等键；NULL 表示不去重
  payload           JSON          NULL,                 -- 类型相关参数（对象 key、来源实体 xid 等）
  requested_by_xid  VARCHAR(32)   NOT NULL DEFAULT '',  -- 触发用户 usr_<xid>，系统任务为空串
  status            TINYINT       NOT NULL DEFAULT 0,   -- 0 待处理 1 已解决 2 已忽略
  attempts          INT UNSIGNED  NOT NULL DEFAULT 0,   -- 已失败次数，Fail 时 +1
  last_error        VARCHAR(1000) NOT NULL DEFAULT '',  -- 最近一次失败原因
  resolved_at       DATETIME      NULL,                 -- 解决/忽略时间
  created_at        DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at        DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_async_tasks_xid (xid),
  UNIQUE KEY uk_async_tasks_dedupe (source_service, task_type, dedupe_key),
  KEY idx_async_tasks_type_status (source_service, task_type, status, updated_at),
  KEY idx_async_tasks_status_updated (status, updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
