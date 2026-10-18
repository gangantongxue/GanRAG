// Package cleanup 在数据库事务提交后尽力删除 Garage 对象，并回写公共任务状态。
//
// 任务行由 store 在删除事务内写入 ganrag_common.async_tasks；本包只负责：
// 删除成功 → Resolve；失败 → Fail（保留待处理，后续 worker 可重试）。
package cleanup

import (
	"context"
	"log/slog"

	"github.com/gangantongxue/GanRAG/service/repository/internal/store"
)

// TaskUpdater 公共任务状态回写（*asynctask.Store 实现）
type TaskUpdater interface {
	Resolve(ctx context.Context, xid string) error
	Fail(ctx context.Context, xid, reason string) error
}

// ObjectDeleter 对象删除（*objectstore.Store 实现）
type ObjectDeleter interface {
	Delete(ctx context.Context, key string) error
}

// Runner 对象清理执行器
type Runner struct {
	tasks   TaskUpdater
	objects ObjectDeleter
	log     *slog.Logger
}

// New 创建清理执行器
func New(tasks TaskUpdater, objects ObjectDeleter, log *slog.Logger) *Runner {
	return &Runner{tasks: tasks, objects: objects, log: log}
}

// Run 逐个删除对象；单个失败不影响其余对象
//
// 使用 context.WithoutCancel：数据库事务已提交，客户端断开也应完成尽力清理。
func (r *Runner) Run(ctx context.Context, refs []store.ObjectRef) {
	ctx = context.WithoutCancel(ctx)
	for _, ref := range refs {
		key := "o/" + ref.ObjectXID
		if err := r.objects.Delete(ctx, key); err != nil {
			r.log.Error("删除 Garage 对象失败", "object_xid", ref.ObjectXID, "err", err)
			if ref.TaskXID != "" {
				if taskErr := r.tasks.Fail(ctx, ref.TaskXID, err.Error()); taskErr != nil {
					r.log.Error("回写任务失败状态失败", "task_xid", ref.TaskXID, "err", taskErr)
				}
			}
			continue
		}
		if ref.TaskXID == "" {
			continue
		}
		if err := r.tasks.Resolve(ctx, ref.TaskXID); err != nil {
			r.log.Error("回写任务成功状态失败", "task_xid", ref.TaskXID, "err", err)
		}
	}
}
