# pkg/asynctask

跨服务共享的异步任务 / 失败记录存储，表位于公共 schema `ganrag_common.async_tasks`。

## 约定

- 业务服务**不要**直接写 SQL，统一 import 本包。
- `Report` 使用全限定表名 `ganrag_common.async_tasks`，可在业务事务内调用（同 MySQL 实例跨 schema 事务）。
- `task_type` 区分任务类型（首期 `garage_object_delete`），`source_service` 区分上报服务，`dedupe_key` 提供幂等。
- `payload` 存类型相关参数；`requested_by_xid` 存触发用户。
- 状态：0 待处理 / 1 已解决 / 2 已忽略。

## 用法

```go
import "github.com/gangantongxue/GanRAG/pkg/asynctask"

// 业务事务内写任务（与业务行同事务）
task, err := asynctask.New(tx).Report(ctx, asynctask.ReportInput{
    TaskType:       asynctask.TypeGarageObjectDelete,
    SourceService:  asynctask.SourceRepository,
    DedupeKey:      objectXID,
    Payload:        map[string]any{"object_xid": objectXID, "object_key": "o/" + objectXID},
    RequestedByXID: userXID,
})

// 事务提交后
_ = asynctask.New(db).Resolve(ctx, task.Xid)
_ = asynctask.New(db).Fail(ctx, task.Xid, err.Error())
```

服务启动时可调用 `asynctask.Migrate(cfg.Database)` 确保公共表存在（`task db-up` 也会统一迁移）。

## 测试

SQLite 单测使用 `asynctask.NewWithTable(db, "async_tasks")` 去掉 schema 限定；生产使用 `New`。
