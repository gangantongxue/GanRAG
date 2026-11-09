package server

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	repositoryv1 "github.com/gangantongxue/GanRAG/api/gen/repository/v1"
	"github.com/gangantongxue/GanRAG/pkg/eid"
)

// mustUpload 通过 UploadWhole 上传对象并返回 object_xid
func mustUpload(t *testing.T, h *Handler, userXID, name string, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	resp, err := h.UploadWhole(t.Context(), &repositoryv1.UploadWholeRequest{
		UserXid: userXID, Data: data, ContentType: "image/png", OriginalName: name,
		Sha256: hex.EncodeToString(sum[:]),
	})
	if err != nil {
		t.Fatalf("UploadWhole() 错误: %v", err)
	}
	return resp.Result.ObjectXid
}

// TestNormalizeRelativePath 验证相对路径规范化与逃逸拦截
func TestNormalizeRelativePath(t *testing.T) {
	ok := map[string]string{
		"assets/logo.png": "assets/logo.png",
		"./a.png":         "a.png",
		`assets\a.png`:    "assets/a.png",
		"a/./b.png":       "a/b.png",
	}
	for in, want := range ok {
		got, err := normalizeRelativePath(in)
		if err != nil || got != want {
			t.Errorf("normalizeRelativePath(%q) = (%q, %v)，期望 %q", in, got, err, want)
		}
	}
	bad := []string{"", "..", "a/../b.png", "/etc/passwd", "a/..", "../x", "./", "   "}
	for _, in := range bad {
		if got, err := normalizeRelativePath(in); err == nil {
			t.Errorf("normalizeRelativePath(%q) = %q，期望拒绝", in, got)
		}
	}
}

// TestArticleParamValidation 验证入参校验全部走 InvalidArgument
func TestArticleParamValidation(t *testing.T) {
	h, _, _ := newTestHandler(t)
	ctx := t.Context()
	owner := eid.New(eid.User)
	kb := mustCreateKB(t, h, owner, "库", 1)

	cases := []struct {
		name string
		req  *repositoryv1.CreateArticleRequest
	}{
		{"非法 user_xid", &repositoryv1.CreateArticleRequest{UserXid: "bad", KbXid: kb.Xid, Type: 1, Title: "t"}},
		{"非法 kb_xid", &repositoryv1.CreateArticleRequest{UserXid: owner, KbXid: "bad", Type: 1, Title: "t"}},
		{"非法 dir_xid", &repositoryv1.CreateArticleRequest{UserXid: owner, KbXid: kb.Xid, DirXid: "bad", Type: 1, Title: "t"}},
		{"非法 type", &repositoryv1.CreateArticleRequest{UserXid: owner, KbXid: kb.Xid, Type: 9, Title: "t"}},
		{"空标题", &repositoryv1.CreateArticleRequest{UserXid: owner, KbXid: kb.Xid, Type: 1, Title: "  "}},
		{"非法正文对象", &repositoryv1.CreateArticleRequest{UserXid: owner, KbXid: kb.Xid, Type: 1, Title: "t", ContentObjectXid: "bad"}},
		{"附件路径逃逸", &repositoryv1.CreateArticleRequest{UserXid: owner, KbXid: kb.Xid, Type: 1, Title: "t",
			Attachments: []*repositoryv1.AttachmentRef{{RelativePath: "../x.png", ObjectXid: eid.New(eid.Object)}}}},
		{"附件对象非法", &repositoryv1.CreateArticleRequest{UserXid: owner, KbXid: kb.Xid, Type: 1, Title: "t",
			Attachments: []*repositoryv1.AttachmentRef{{RelativePath: "a.png", ObjectXid: "bad"}}}},
		{"附件路径重复", &repositoryv1.CreateArticleRequest{UserXid: owner, KbXid: kb.Xid, Type: 1, Title: "t",
			Attachments: []*repositoryv1.AttachmentRef{
				{RelativePath: "./a.png", ObjectXid: eid.New(eid.Object)},
				{RelativePath: "a.png", ObjectXid: eid.New(eid.Object)},
			}}},
		{"附件对象不存在", &repositoryv1.CreateArticleRequest{UserXid: owner, KbXid: kb.Xid, Type: 1, Title: "t",
			Attachments: []*repositoryv1.AttachmentRef{{RelativePath: "a.png", ObjectXid: eid.New(eid.Object)}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.CreateArticle(ctx, tc.req)
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("错误 = %v，期望 InvalidArgument", err)
			}
		})
	}
}

// TestArticleLifecycle 验证元信息 CRUD 主流程与权限（无对象文章）
func TestArticleLifecycle(t *testing.T) {
	h, _, cleaner := newTestHandler(t)
	ctx := t.Context()
	owner := eid.New(eid.User)
	other := eid.New(eid.User)
	private := mustCreateKB(t, h, owner, "私有库", 0)
	dirResp, err := h.CreateDirectory(ctx, &repositoryv1.CreateDirectoryRequest{UserXid: owner, KbXid: private.Xid, Name: "目录"})
	if err != nil {
		t.Fatalf("CreateDirectory() 错误: %v", err)
	}

	created, err := h.CreateArticle(ctx, &repositoryv1.CreateArticleRequest{
		UserXid: owner, KbXid: private.Xid, DirXid: dirResp.Directory.Xid, Type: 1, Title: "标题",
	})
	if err != nil {
		t.Fatalf("CreateArticle() 错误: %v", err)
	}
	artXID := created.Article.Xid
	if created.Article.DirXid != dirResp.Directory.Xid || created.Article.Title != "标题" {
		t.Errorf("创建结果 = %+v", created.Article)
	}

	// owner 可读可编辑；陌生人私有库统一 NotFound
	got, err := h.GetArticle(ctx, &repositoryv1.GetArticleRequest{UserXid: owner, ArticleXid: artXID})
	if err != nil {
		t.Fatalf("owner GetArticle() 错误: %v", err)
	}
	if !got.CanEdit || len(got.Attachments) != 0 {
		t.Errorf("GetArticle = %+v", got)
	}
	if _, err := h.GetArticle(ctx, &repositoryv1.GetArticleRequest{UserXid: other, ArticleXid: artXID}); status.Code(err) != codes.NotFound {
		t.Errorf("陌生人读私有库错误 = %v，期望 NotFound", err)
	}
	if _, err := h.GetArticle(ctx, &repositoryv1.GetArticleRequest{ArticleXid: artXID}); status.Code(err) != codes.NotFound {
		t.Errorf("匿名读私有库错误 = %v，期望 NotFound", err)
	}

	// 更新：改名 + 移到根（dir_xid 全量替换）
	updated, err := h.UpdateArticle(ctx, &repositoryv1.UpdateArticleRequest{
		UserXid: owner, ArticleXid: artXID, Title: "新标题",
	})
	if err != nil {
		t.Fatalf("UpdateArticle() 错误: %v", err)
	}
	if updated.Article.Title != "新标题" || updated.Article.DirXid != "" {
		t.Errorf("更新结果 = %+v", updated.Article)
	}

	// 陌生人与匿名写统一 NotFound
	if _, err := h.UpdateArticle(ctx, &repositoryv1.UpdateArticleRequest{UserXid: other, ArticleXid: artXID, Title: "x"}); status.Code(err) != codes.NotFound {
		t.Errorf("陌生人更新错误 = %v，期望 NotFound", err)
	}
	if _, err := h.DeleteArticle(ctx, &repositoryv1.DeleteArticleRequest{UserXid: other, ArticleXid: artXID}); status.Code(err) != codes.NotFound {
		t.Errorf("陌生人删除错误 = %v，期望 NotFound", err)
	}

	if _, err := h.DeleteArticle(ctx, &repositoryv1.DeleteArticleRequest{UserXid: owner, ArticleXid: artXID}); err != nil {
		t.Fatalf("DeleteArticle() 错误: %v", err)
	}
	if !cleaner.called {
		t.Errorf("删除后未触发对象清理")
	}
	if _, err := h.GetArticle(ctx, &repositoryv1.GetArticleRequest{UserXid: owner, ArticleXid: artXID}); status.Code(err) != codes.NotFound {
		t.Errorf("删除后查询错误 = %v，期望 NotFound", err)
	}
}

// TestArticleWithAttachments 验证附件元数据经对象 HEAD 回填（创建 + 查询）
func TestArticleWithAttachments(t *testing.T) {
	h, _, _ := newTestHandler(t)
	ctx := t.Context()
	owner := eid.New(eid.User)
	kb := mustCreateKB(t, h, owner, "库", 1)

	content := mustUpload(t, h, owner, "正文.md", []byte("# hello"))
	pic := mustUpload(t, h, owner, "logo.png", []byte("pngdata"))
	doc := mustUpload(t, h, owner, "手册.txt", []byte("manual"))

	created, err := h.CreateArticle(ctx, &repositoryv1.CreateArticleRequest{
		UserXid: owner, KbXid: kb.Xid, Type: 1, Title: "标题", ContentObjectXid: content,
		Attachments: []*repositoryv1.AttachmentRef{
			{RelativePath: "assets/logo.png", ObjectXid: pic},
			{RelativePath: "docs/手册.txt", ObjectXid: doc},
		},
	})
	if err != nil {
		t.Fatalf("CreateArticle() 错误: %v", err)
	}

	got, err := h.GetArticle(ctx, &repositoryv1.GetArticleRequest{ArticleXid: created.Article.Xid})
	if err != nil {
		t.Fatalf("GetArticle() 错误: %v", err)
	}
	if len(got.Attachments) != 2 {
		t.Fatalf("附件清单 = %+v", got.Attachments)
	}
	att := got.Attachments[0]
	if att.RelativePath != "assets/logo.png" || att.ObjectXid != pic {
		t.Errorf("附件 = %+v", att)
	}
	if att.OriginalName != "logo.png" || att.ContentType != "image/png" || att.Size != uint64(len("pngdata")) {
		t.Errorf("附件元数据 = %+v，期望从对象 HEAD 回填", att)
	}
}
