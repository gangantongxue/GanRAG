package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/gangantongxue/GanRAG/pkg/asynctask"
	dbcommon "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_common"
	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_repository"
	"github.com/gangantongxue/GanRAG/pkg/eid"
)

// newTestStore 用 SQLite 临时库构造 Store；async_tasks 使用本地表名（测试无 ganrag_common schema）
func newTestStore(t *testing.T) (*Store, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{
		TranslateError: true,
		Logger:         gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("打开 SQLite 失败: %v", err)
	}
	if err := db.AutoMigrate(
		&dbmodel.KnowledgeBase{},
		&dbmodel.Directory{},
		&dbmodel.Article{},
		&dbmodel.Attachment{},
		&dbmodel.KbUserRelation{},
		&dbcommon.AsyncTask{},
	); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	// AutoMigrate 推不出组合唯一索引，按迁移补齐
	if err := db.Exec("CREATE UNIQUE INDEX uk_kb_owner_name ON knowledge_bases (owner_xid, name)").Error; err != nil {
		t.Fatalf("创建知识库唯一索引失败: %v", err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX uk_async_tasks_dedupe ON async_tasks (source_service, task_type, dedupe_key)").Error; err != nil {
		t.Fatalf("创建任务唯一索引失败: %v", err)
	}

	s := New(db)
	s.newTasks = func(tx *gorm.DB) *asynctask.Store {
		return asynctask.NewWithTable(tx, "async_tasks")
	}
	return s, db
}

// mustCreateKB 创建测试知识库
func mustCreateKB(t *testing.T, s *Store, ownerXID, name string, visibility int32) *dbmodel.KnowledgeBase {
	t.Helper()
	kb, err := s.CreateKnowledgeBase(t.Context(), ownerXID, name, "", visibility)
	if err != nil {
		t.Fatalf("CreateKnowledgeBase(%s) 错误: %v", name, err)
	}
	return kb
}

// TestKnowledgeBaseCRUD 验证创建、查询、更新与同 owner 重名
func TestKnowledgeBaseCRUD(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := t.Context()
	owner := eid.New(eid.User)

	kb := mustCreateKB(t, s, owner, "我的库", 0)
	if !eid.Match(kb.Xid, eid.KnowledgeBase) {
		t.Errorf("Xid = %q，期望知识库 xid", kb.Xid)
	}
	if _, err := s.CreateKnowledgeBase(ctx, owner, "我的库", "", 0); !errors.Is(err, ErrKnowledgeBaseNameTaken) {
		t.Errorf("同 owner 重名错误 = %v，期望 ErrKnowledgeBaseNameTaken", err)
	}
	// 不同 owner 可同名
	mustCreateKB(t, s, eid.New(eid.User), "我的库", 0)

	got, err := s.GetKnowledgeBaseByXID(ctx, kb.Xid)
	if err != nil {
		t.Fatalf("GetKnowledgeBaseByXID() 错误: %v", err)
	}
	if got.OwnerXid != owner || got.Name != "我的库" {
		t.Errorf("查询结果 = %+v", got)
	}

	updated, err := s.UpdateKnowledgeBase(ctx, kb.ID, "改名", "描述", 1)
	if err != nil {
		t.Fatalf("UpdateKnowledgeBase() 错误: %v", err)
	}
	if updated.Name != "改名" || updated.Description != "描述" || updated.Visibility != 1 {
		t.Errorf("更新结果 = %+v", updated)
	}

	if _, err := s.GetKnowledgeBaseByXID(ctx, eid.New(eid.KnowledgeBase)); !errors.Is(err, ErrKnowledgeBaseNotFound) {
		t.Errorf("不存在知识库错误 = %v，期望 ErrKnowledgeBaseNotFound", err)
	}
}

// TestListKnowledgeBasesScopes 验证 4 种列表 scope
func TestListKnowledgeBasesScopes(t *testing.T) {
	s, db := newTestStore(t)
	ctx := t.Context()
	me := eid.New(eid.User)
	other := eid.New(eid.User)

	owned := mustCreateKB(t, s, me, "owned", 0)
	public := mustCreateKB(t, s, other, "public", 1)
	participated := mustCreateKB(t, s, other, "participated", 0)
	if err := db.Create(&dbmodel.KbUserRelation{
		KbID: participated.ID, KbXid: participated.Xid, UserXid: me, Role: 1, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("插入成员关系失败: %v", err)
	}

	cases := []struct {
		name  string
		scope int32
		want  int64
	}{
		{"owned", ScopeOwned, 1},
		{"participated", ScopeParticipated, 1},
		{"public", ScopePublic, 1},
		{"all", ScopeAllVisible, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, total, err := s.ListKnowledgeBases(ctx, me, tc.scope, 1, 20)
			if err != nil {
				t.Fatalf("ListKnowledgeBases() 错误: %v", err)
			}
			if total != tc.want || int64(len(rows)) != tc.want {
				t.Errorf("total=%d len=%d，期望 %d", total, len(rows), tc.want)
			}
		})
	}

	// 匿名只看到公开库
	_, total, err := s.ListKnowledgeBases(ctx, "", ScopePublic, 1, 20)
	if err != nil {
		t.Fatalf("匿名 ListKnowledgeBases() 错误: %v", err)
	}
	if total != 1 {
		t.Errorf("匿名公开库 total = %d，期望 1", total)
	}
	_ = owned
	_ = public
}

// TestDeleteKnowledgeBaseOutbox 验证级联删除与同事务任务登记
func TestDeleteKnowledgeBaseOutbox(t *testing.T) {
	s, db := newTestStore(t)
	ctx := t.Context()
	owner := eid.New(eid.User)
	kb := mustCreateKB(t, s, owner, "to-delete", 0)
	contentObject := eid.New(eid.Object)
	attachmentObject := eid.New(eid.Object)

	if err := db.Create(&dbmodel.Article{
		Xid: eid.New(eid.Article), KbID: kb.ID, KbXid: kb.Xid, DirID: 0, DirXid: "",
		Type: 1, Title: "文章", ContentKey: "o/" + contentObject,
		ContentObjectXid: contentObject, CreatedByXid: owner,
	}).Error; err != nil {
		t.Fatalf("插入文章失败: %v", err)
	}
	if err := db.Create(&dbmodel.Attachment{
		Xid: eid.New(eid.Attachment), KbID: kb.ID, KbXid: kb.Xid,
		ArticleID: 1, ArticleXid: eid.New(eid.Article), Kind: 2,
		FileKey: "o/" + attachmentObject, ObjectXid: attachmentObject,
		RelativePath: "assets/a.png", OriginalName: "a.png", ContentType: "image/png",
		Size: 1, Sha256: "x", CreatedByXid: owner,
	}).Error; err != nil {
		t.Fatalf("插入附件失败: %v", err)
	}
	if err := db.Create(&dbmodel.Directory{
		Xid: eid.New(eid.Directory), KbID: kb.ID, KbXid: kb.Xid, ParentID: 0, ParentXid: "", Name: "d",
	}).Error; err != nil {
		t.Fatalf("插入目录失败: %v", err)
	}
	if err := db.Create(&dbmodel.KbUserRelation{
		KbID: kb.ID, KbXid: kb.Xid, UserXid: owner, Role: 1, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("插入关系失败: %v", err)
	}

	refs, err := s.DeleteKnowledgeBase(ctx, kb.ID, kb.Xid, owner)
	if err != nil {
		t.Fatalf("DeleteKnowledgeBase() 错误: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("对象引用数 = %d，期望 2", len(refs))
	}

	var tasks []dbcommon.AsyncTask
	if err := db.Find(&tasks).Error; err != nil {
		t.Fatalf("查询任务失败: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("任务数 = %d，期望 2", len(tasks))
	}
	for _, task := range tasks {
		if task.Status != asynctask.StatusPending || task.TaskType != asynctask.TypeGarageObjectDelete {
			t.Errorf("任务字段异常: %+v", task)
		}
	}
	if _, err := s.GetKnowledgeBaseByXID(ctx, kb.Xid); !errors.Is(err, ErrKnowledgeBaseNotFound) {
		t.Errorf("删除后查询错误 = %v，期望 ErrKnowledgeBaseNotFound", err)
	}
}
