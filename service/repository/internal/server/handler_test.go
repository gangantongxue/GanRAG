package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	repositoryv1 "github.com/gangantongxue/GanRAG/api/gen/repository/v1"
	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_repository"
	"github.com/gangantongxue/GanRAG/pkg/eid"
	"github.com/gangantongxue/GanRAG/service/repository/internal/store"
)

type fakeObjects struct {
	key       string
	data      []byte
	mediaType string
	metadata  map[string]string
	err       error
}

func (f *fakeObjects) Put(_ context.Context, key string, data []byte, contentType string, metadata map[string]string) error {
	f.key, f.data, f.mediaType, f.metadata = key, data, contentType, metadata
	return f.err
}

type fakeCleanup struct {
	called bool
	refs   []store.ObjectRef
}

func (f *fakeCleanup) Run(_ context.Context, refs []store.ObjectRef) {
	f.called = true
	f.refs = refs
}

// newTestHandler 构造 SQLite handler；Delete 用例使用无对象知识库，避免触达公共表
func newTestHandler(t *testing.T) (*Handler, *fakeObjects, *fakeCleanup) {
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
	); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX uk_kb_owner_name ON knowledge_bases (owner_xid, name)").Error; err != nil {
		t.Fatalf("创建知识库唯一索引失败: %v", err)
	}

	objects := &fakeObjects{}
	cleaner := &fakeCleanup{}
	h := NewHandler(store.New(db), objects, cleaner, slog.New(slog.DiscardHandler), 2*1024*1024)
	return h, objects, cleaner
}

// mustCreateKB 通过 handler 创建知识库
func mustCreateKB(t *testing.T, h *Handler, userXID, name string, visibility int32) *repositoryv1.KnowledgeBase {
	t.Helper()
	resp, err := h.CreateKnowledgeBase(t.Context(), &repositoryv1.CreateKnowledgeBaseRequest{
		UserXid: userXID, Name: name, Description: "desc", Visibility: visibility,
	})
	if err != nil {
		t.Fatalf("CreateKnowledgeBase() 错误: %v", err)
	}
	return resp.KnowledgeBase
}

// TestCreateAndGetKnowledgeBase 验证创建、私有/公开读取权限与 can_edit
func TestCreateAndGetKnowledgeBase(t *testing.T) {
	h, _, _ := newTestHandler(t)
	ctx := t.Context()
	owner := eid.New(eid.User)
	other := eid.New(eid.User)

	private := mustCreateKB(t, h, owner, "私有库", 0)
	if private.OwnerXid != owner || !eid.Match(private.Xid, eid.KnowledgeBase) {
		t.Errorf("创建结果 = %+v", private)
	}

	// owner 可读私有库
	resp, err := h.GetKnowledgeBase(ctx, &repositoryv1.GetKnowledgeBaseRequest{UserXid: owner, KbXid: private.Xid})
	if err != nil {
		t.Fatalf("owner GetKnowledgeBase() 错误: %v", err)
	}
	if !resp.CanEdit {
		t.Error("owner can_edit 应为 true")
	}

	// 其他人/匿名读私有库统一 NotFound
	for _, userXID := range []string{other, ""} {
		_, err := h.GetKnowledgeBase(ctx, &repositoryv1.GetKnowledgeBaseRequest{UserXid: userXID, KbXid: private.Xid})
		if status.Code(err) != codes.NotFound {
			t.Errorf("user=%q 读私有库错误 = %v，期望 NotFound", userXID, err)
		}
	}

	// 公开库匿名可读、不可编辑
	public := mustCreateKB(t, h, owner, "公开库", 1)
	resp, err = h.GetKnowledgeBase(ctx, &repositoryv1.GetKnowledgeBaseRequest{KbXid: public.Xid})
	if err != nil {
		t.Fatalf("匿名读公开库错误: %v", err)
	}
	if resp.CanEdit {
		t.Error("匿名读公开库 can_edit 应为 false")
	}

	// 非法 xid
	if _, err := h.GetKnowledgeBase(ctx, &repositoryv1.GetKnowledgeBaseRequest{KbXid: "bad"}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("非法 kb_xid 错误 = %v，期望 InvalidArgument", err)
	}
}

// TestCreateKnowledgeBaseValidation 验证创建入参与重名
func TestCreateKnowledgeBaseValidation(t *testing.T) {
	h, _, _ := newTestHandler(t)
	ctx := t.Context()
	owner := eid.New(eid.User)

	cases := []struct {
		name string
		req  *repositoryv1.CreateKnowledgeBaseRequest
		code codes.Code
	}{
		{"非法用户", &repositoryv1.CreateKnowledgeBaseRequest{UserXid: "bad", Name: "x"}, codes.InvalidArgument},
		{"空名称", &repositoryv1.CreateKnowledgeBaseRequest{UserXid: owner, Name: "  "}, codes.InvalidArgument},
		{"名称过长", &repositoryv1.CreateKnowledgeBaseRequest{UserXid: owner, Name: strings.Repeat("名", 101)}, codes.InvalidArgument},
		{"非法可见性", &repositoryv1.CreateKnowledgeBaseRequest{UserXid: owner, Name: "x", Visibility: 2}, codes.InvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := h.CreateKnowledgeBase(ctx, tc.req); status.Code(err) != tc.code {
				t.Errorf("错误 = %v，期望 %v", err, tc.code)
			}
		})
	}

	mustCreateKB(t, h, owner, "同名", 0)
	_, err := h.CreateKnowledgeBase(ctx, &repositoryv1.CreateKnowledgeBaseRequest{UserXid: owner, Name: "同名"})
	if status.Code(err) != codes.AlreadyExists {
		t.Errorf("重名错误 = %v，期望 AlreadyExists", err)
	}
}

// TestListKnowledgeBases 验证 scope 权限与匿名公开列表
func TestListKnowledgeBases(t *testing.T) {
	h, _, _ := newTestHandler(t)
	ctx := t.Context()
	owner := eid.New(eid.User)
	other := eid.New(eid.User)

	mustCreateKB(t, h, owner, "owner-kb", 0)
	mustCreateKB(t, h, other, "public-kb", 1)

	resp, err := h.ListKnowledgeBases(ctx, &repositoryv1.ListKnowledgeBasesRequest{
		UserXid: owner, Scope: store.ScopeAllVisible, Page: 1, PageSize: 20,
	})
	if err != nil {
		t.Fatalf("ListKnowledgeBases() 错误: %v", err)
	}
	if resp.Total != 2 || len(resp.Items) != 2 {
		t.Errorf("全部可见 total=%d len=%d，期望 2", resp.Total, len(resp.Items))
	}

	resp, err = h.ListKnowledgeBases(ctx, &repositoryv1.ListKnowledgeBasesRequest{Scope: store.ScopePublic})
	if err != nil {
		t.Fatalf("匿名公开列表错误: %v", err)
	}
	if resp.Total != 1 || len(resp.Items) != 1 {
		t.Errorf("匿名公开 total=%d len=%d，期望 1", resp.Total, len(resp.Items))
	}

	if _, err := h.ListKnowledgeBases(ctx, &repositoryv1.ListKnowledgeBasesRequest{Scope: store.ScopeOwned}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("匿名 scope 1 错误 = %v，期望 InvalidArgument", err)
	}
	if _, err := h.ListKnowledgeBases(ctx, &repositoryv1.ListKnowledgeBasesRequest{Scope: 9}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("非法 scope 错误 = %v，期望 InvalidArgument", err)
	}
}

// TestUpdateAndDeleteKnowledgeBase 验证 owner 写权限与删除
func TestUpdateAndDeleteKnowledgeBase(t *testing.T) {
	h, _, cleaner := newTestHandler(t)
	ctx := t.Context()
	owner := eid.New(eid.User)
	other := eid.New(eid.User)

	kb := mustCreateKB(t, h, owner, "old", 0)
	mustCreateKB(t, h, owner, "taken", 0)

	resp, err := h.UpdateKnowledgeBase(ctx, &repositoryv1.UpdateKnowledgeBaseRequest{
		UserXid: owner, KbXid: kb.Xid, Name: "new", Description: "new desc", Visibility: 1,
	})
	if err != nil {
		t.Fatalf("UpdateKnowledgeBase() 错误: %v", err)
	}
	if resp.KnowledgeBase.Name != "new" || resp.KnowledgeBase.Visibility != 1 {
		t.Errorf("更新结果 = %+v", resp.KnowledgeBase)
	}

	// 非 owner 更新 → NotFound
	_, err = h.UpdateKnowledgeBase(ctx, &repositoryv1.UpdateKnowledgeBaseRequest{
		UserXid: other, KbXid: kb.Xid, Name: "hack", Visibility: 0,
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("非 owner 更新错误 = %v，期望 NotFound", err)
	}

	// 重名 → AlreadyExists
	_, err = h.UpdateKnowledgeBase(ctx, &repositoryv1.UpdateKnowledgeBaseRequest{
		UserXid: owner, KbXid: kb.Xid, Name: "taken", Visibility: 1,
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Errorf("重名更新错误 = %v，期望 AlreadyExists", err)
	}

	// 非 owner 删除 → NotFound
	_, err = h.DeleteKnowledgeBase(ctx, &repositoryv1.DeleteKnowledgeBaseRequest{UserXid: other, KbXid: kb.Xid})
	if status.Code(err) != codes.NotFound {
		t.Errorf("非 owner 删除错误 = %v，期望 NotFound", err)
	}

	// owner 删除成功，cleanup 收到空 refs
	if _, err := h.DeleteKnowledgeBase(ctx, &repositoryv1.DeleteKnowledgeBaseRequest{UserXid: owner, KbXid: kb.Xid}); err != nil {
		t.Fatalf("DeleteKnowledgeBase() 错误: %v", err)
	}
	if !cleaner.called || len(cleaner.refs) != 0 {
		t.Errorf("cleanup called=%v refs=%v，期望调用且为空", cleaner.called, cleaner.refs)
	}
}

// TestUploadWhole 验证整段上传的 sha256、大小与元数据
func TestUploadWhole(t *testing.T) {
	h, objects, _ := newTestHandler(t)
	ctx := t.Context()
	owner := eid.New(eid.User)
	data := []byte("hello repository")
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])

	resp, err := h.UploadWhole(ctx, &repositoryv1.UploadWholeRequest{
		UserXid: owner, ContentType: "text/markdown; charset=utf-8", OriginalName: "测试.md",
		Data: data, Sha256: sha,
	})
	if err != nil {
		t.Fatalf("UploadWhole() 错误: %v", err)
	}
	result := resp.Result
	if !eid.Match(result.ObjectXid, eid.Object) || result.Size != uint64(len(data)) || result.Sha256 != sha {
		t.Errorf("上传结果 = %+v", result)
	}
	if objects.key != "o/"+result.ObjectXid || objects.mediaType != "text/markdown" {
		t.Errorf("对象 key/content_type = %q/%q", objects.key, objects.mediaType)
	}
	name, err := base64.RawURLEncoding.DecodeString(objects.metadata["original-name-b64"])
	if err != nil || string(name) != "测试.md" {
		t.Errorf("original-name-b64 解码 = %q, err=%v", name, err)
	}
	if objects.metadata["uploader-xid"] != owner || objects.metadata["sha256"] != sha {
		t.Errorf("metadata = %v", objects.metadata)
	}
}

// TestUploadWholeValidation 验证 sha256、大小与参数错误
func TestUploadWholeValidation(t *testing.T) {
	h, objects, _ := newTestHandler(t)
	ctx := t.Context()
	owner := eid.New(eid.User)

	data := []byte("hello")
	sum := sha256.Sum256(data)
	goodSHA := hex.EncodeToString(sum[:])

	cases := []struct {
		name string
		req  *repositoryv1.UploadWholeRequest
		code codes.Code
	}{
		{"非法用户", &repositoryv1.UploadWholeRequest{UserXid: "bad", Data: data, Sha256: goodSHA}, codes.InvalidArgument},
		{"sha 长度非法", &repositoryv1.UploadWholeRequest{UserXid: owner, Data: data, Sha256: "abc"}, codes.InvalidArgument},
		{"sha 不匹配", &repositoryv1.UploadWholeRequest{UserXid: owner, Data: data, Sha256: strings.Repeat("0", 64)}, codes.DataLoss},
		{"content_type 非法", &repositoryv1.UploadWholeRequest{UserXid: owner, ContentType: "bad\ncontent", Data: data, Sha256: goodSHA}, codes.InvalidArgument},
		{"文件名过长", &repositoryv1.UploadWholeRequest{UserXid: owner, OriginalName: strings.Repeat("名", 256), Data: data, Sha256: goodSHA}, codes.InvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := h.UploadWhole(ctx, tc.req); status.Code(err) != tc.code {
				t.Errorf("错误 = %v，期望 %v", err, tc.code)
			}
		})
	}

	// 超过 2MB
	big := make([]byte, 2*1024*1024+1)
	bigSum := sha256.Sum256(big)
	_, err := h.UploadWhole(ctx, &repositoryv1.UploadWholeRequest{
		UserXid: owner, Data: big, Sha256: hex.EncodeToString(bigSum[:]),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("超限错误 = %v，期望 InvalidArgument", err)
	}

	// S3 写入失败 → Internal
	objects.err = errors.New("s3 down")
	_, err = h.UploadWhole(ctx, &repositoryv1.UploadWholeRequest{
		UserXid: owner, Data: data, Sha256: goodSHA,
	})
	if status.Code(err) != codes.Internal {
		t.Errorf("S3 写入失败错误 = %v，期望 Internal", err)
	}
}
