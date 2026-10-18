package cleanup

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/gangantongxue/GanRAG/service/repository/internal/store"
)

type fakeTasks struct {
	resolved   []string
	failed     map[string]string
	failErr    error
	resolveErr error
}

func (f *fakeTasks) Resolve(_ context.Context, xid string) error {
	f.resolved = append(f.resolved, xid)
	return f.resolveErr
}

func (f *fakeTasks) Fail(_ context.Context, xid, reason string) error {
	if f.failed == nil {
		f.failed = make(map[string]string)
	}
	f.failed[xid] = reason
	return f.failErr
}

type fakeObjects struct {
	errs    map[string]error
	deleted []string
}

func (f *fakeObjects) Delete(_ context.Context, key string) error {
	f.deleted = append(f.deleted, key)
	return f.errs[key]
}

// TestRunSuccess 验证删除成功 → Resolve
func TestRunSuccess(t *testing.T) {
	tasks := &fakeTasks{}
	objects := &fakeObjects{}
	r := New(tasks, objects, slog.New(slog.DiscardHandler))

	r.Run(t.Context(), []store.ObjectRef{{ObjectXID: "obj_1", TaskXID: "tsk_1"}})

	if len(objects.deleted) != 1 || objects.deleted[0] != "o/obj_1" {
		t.Errorf("删除 key = %v，期望 o/obj_1", objects.deleted)
	}
	if len(tasks.resolved) != 1 || tasks.resolved[0] != "tsk_1" {
		t.Errorf("Resolve = %v，期望 tsk_1", tasks.resolved)
	}
	if len(tasks.failed) != 0 {
		t.Errorf("不应有失败任务: %v", tasks.failed)
	}
}

// TestRunFailure 验证删除失败 → Fail 且不影响后续对象
func TestRunFailure(t *testing.T) {
	tasks := &fakeTasks{}
	objects := &fakeObjects{errs: map[string]error{"o/obj_1": errors.New("boom")}}
	r := New(tasks, objects, slog.New(slog.DiscardHandler))

	r.Run(t.Context(), []store.ObjectRef{
		{ObjectXID: "obj_1", TaskXID: "tsk_1"},
		{ObjectXID: "obj_2", TaskXID: "tsk_2"},
	})

	if len(objects.deleted) != 2 {
		t.Errorf("删除次数 = %d，期望 2（单对象失败不阻断后续）", len(objects.deleted))
	}
	if !strings.Contains(tasks.failed["tsk_1"], "boom") {
		t.Errorf("失败原因 = %q，期望包含 boom", tasks.failed["tsk_1"])
	}
	if len(tasks.resolved) != 1 || tasks.resolved[0] != "tsk_2" {
		t.Errorf("Resolve = %v，期望 tsk_2", tasks.resolved)
	}
}
