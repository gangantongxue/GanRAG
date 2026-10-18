package asynctask

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_common"
	"github.com/gangantongxue/GanRAG/pkg/eid"
)

// newTestStore 用 SQLite 临时库构造 Store（表名去掉 schema 限定）
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{
		TranslateError: true,
		Logger:         gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("打开 SQLite 失败: %v", err)
	}
	if err := db.AutoMigrate(&dbmodel.AsyncTask{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	// AutoMigrate 推不出组合唯一索引，手动补齐（对齐 ganrag_common 迁移）
	if err := db.Exec("CREATE UNIQUE INDEX uk_async_tasks_dedupe ON async_tasks (source_service, task_type, dedupe_key)").Error; err != nil {
		t.Fatalf("创建幂等唯一索引失败: %v", err)
	}
	return NewWithTable(db, "async_tasks")
}

// TestReportDedupe 验证上报、幂等键复用与 payload 更新
func TestReportDedupe(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	dedupeKey := eid.New(eid.Object)
	first, err := s.Report(ctx, ReportInput{
		TaskType:      TypeGarageObjectDelete,
		SourceService: SourceRepository,
		DedupeKey:     dedupeKey,
		Payload:       map[string]any{"object_key": "o/obj_1"},
	})
	if err != nil {
		t.Fatalf("Report() 错误: %v", err)
	}
	if !eid.Match(first.Xid, eid.Task) || first.Status != StatusPending || first.Attempts != 0 {
		t.Fatalf("首次任务字段异常: %+v", first)
	}
	if first.Payload == nil || !strings.Contains(*first.Payload, "o/obj_1") {
		t.Fatalf("Payload = %v，期望包含 object_key", first.Payload)
	}

	second, err := s.Report(ctx, ReportInput{
		TaskType:      TypeGarageObjectDelete,
		SourceService: SourceRepository,
		DedupeKey:     dedupeKey,
		Payload:       map[string]any{"object_key": "o/obj_2"},
	})
	if err != nil {
		t.Fatalf("幂等 Report() 错误: %v", err)
	}
	if second.Xid != first.Xid {
		t.Errorf("幂等 Xid = %q，期望复用 %q", second.Xid, first.Xid)
	}
	if second.Payload == nil || !strings.Contains(*second.Payload, "o/obj_2") {
		t.Errorf("Payload = %v，期望更新为 o/obj_2", second.Payload)
	}
}

// TestFailResolveIgnore 验证失败累计与关闭状态
func TestFailResolveIgnore(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	task, err := s.Report(ctx, ReportInput{
		TaskType:      TypeGarageObjectDelete,
		SourceService: SourceRepository,
		DedupeKey:     eid.New(eid.Object),
	})
	if err != nil {
		t.Fatalf("Report() 错误: %v", err)
	}

	if err := s.Fail(ctx, task.Xid, "s3 timeout"); err != nil {
		t.Fatalf("Fail() 错误: %v", err)
	}
	if err := s.Fail(ctx, task.Xid, "s3 timeout again"); err != nil {
		t.Fatalf("再次 Fail() 错误: %v", err)
	}
	failed, err := s.Get(ctx, task.Xid)
	if err != nil {
		t.Fatalf("Get() 错误: %v", err)
	}
	if failed.Status != StatusPending || failed.Attempts != 2 || failed.LastError != "s3 timeout again" {
		t.Errorf("失败后字段 = status:%d attempts:%d last_error:%q", failed.Status, failed.Attempts, failed.LastError)
	}

	if err := s.Resolve(ctx, task.Xid); err != nil {
		t.Fatalf("Resolve() 错误: %v", err)
	}
	resolved, err := s.Get(ctx, task.Xid)
	if err != nil {
		t.Fatalf("Get() 错误: %v", err)
	}
	if resolved.Status != StatusResolved || resolved.ResolvedAt == nil {
		t.Errorf("解决后字段 = status:%d resolved_at:%v", resolved.Status, resolved.ResolvedAt)
	}

	if err := s.Ignore(ctx, task.Xid); err != nil {
		t.Fatalf("Ignore() 错误: %v", err)
	}
	ignored, err := s.Get(ctx, task.Xid)
	if err != nil {
		t.Fatalf("Get() 错误: %v", err)
	}
	if ignored.Status != StatusIgnored || ignored.ResolvedAt == nil {
		t.Errorf("忽略后字段 = status:%d resolved_at:%v", ignored.Status, ignored.ResolvedAt)
	}

	if err := s.Resolve(ctx, eid.New(eid.Task)); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("不存在任务 Resolve 错误 = %v，期望 ErrTaskNotFound", err)
	}
}

// TestList 验证过滤与分页
func TestList(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	for i := range 3 {
		task, err := s.Report(ctx, ReportInput{
			TaskType:      TypeGarageObjectDelete,
			SourceService: SourceRepository,
			DedupeKey:     eid.New(eid.Object),
			Payload:       map[string]any{"seq": i},
		})
		if err != nil {
			t.Fatalf("Report() 错误: %v", err)
		}
		if i == 0 {
			if err := s.Fail(ctx, task.Xid, "boom"); err != nil {
				t.Fatalf("Fail() 错误: %v", err)
			}
		}
		if i == 1 {
			if err := s.Resolve(ctx, task.Xid); err != nil {
				t.Fatalf("Resolve() 错误: %v", err)
			}
		}
	}

	rows, total, err := s.List(ctx, Filter{Status: StatusPending, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("List() 错误: %v", err)
	}
	if total != 2 || len(rows) != 2 {
		t.Errorf("待处理任务 total=%d len=%d，期望 2", total, len(rows))
	}

	rows, total, err = s.List(ctx, Filter{Status: StatusResolved, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("List() 错误: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Errorf("已解决任务 total=%d len=%d，期望 1", total, len(rows))
	}

	rows, total, err = s.List(ctx, Filter{Status: StatusAll, Page: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("List() 错误: %v", err)
	}
	if total != 3 || len(rows) != 2 {
		t.Errorf("全部分页 total=%d len=%d，期望 3/2", total, len(rows))
	}
}

// TestValidation 验证非法入参
func TestValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	if _, err := s.Report(ctx, ReportInput{SourceService: SourceRepository}); err == nil {
		t.Error("空 task_type 应报错")
	}
	if _, err := s.Report(ctx, ReportInput{TaskType: TypeGarageObjectDelete}); err == nil {
		t.Error("空 source_service 应报错")
	}
	if _, err := s.Report(ctx, ReportInput{
		TaskType:       TypeGarageObjectDelete,
		SourceService:  SourceRepository,
		RequestedByXID: "bad",
	}); err == nil {
		t.Error("非法 requested_by_xid 应报错")
	}
	if _, _, err := s.List(ctx, Filter{Status: 99}); err == nil {
		t.Error("非法 status 应报错")
	}
}
