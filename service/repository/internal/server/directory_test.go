package server

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	repositoryv1 "github.com/gangantongxue/GanRAG/api/gen/repository/v1"
	"github.com/gangantongxue/GanRAG/pkg/eid"
)

// TestDirectoryHandlers 验证目录 5 RPC 的参数校验、权限与级联清理
func TestDirectoryHandlers(t *testing.T) {
	h, _, cleaner := newTestHandler(t)
	ctx := t.Context()
	owner := eid.New(eid.User)
	kb := mustCreateKB(t, h, owner, "库", 0)

	// 参数校验
	cases := []struct {
		name string
		req  *repositoryv1.CreateDirectoryRequest
		code codes.Code
	}{
		{"非法用户", &repositoryv1.CreateDirectoryRequest{UserXid: "bad", KbXid: kb.Xid, Name: "x"}, codes.InvalidArgument},
		{"非法知识库", &repositoryv1.CreateDirectoryRequest{UserXid: owner, KbXid: "bad", Name: "x"}, codes.InvalidArgument},
		{"空目录名", &repositoryv1.CreateDirectoryRequest{UserXid: owner, KbXid: kb.Xid, Name: "  "}, codes.InvalidArgument},
		{"目录名过长", &repositoryv1.CreateDirectoryRequest{UserXid: owner, KbXid: kb.Xid, Name: strings.Repeat("目", 101)}, codes.InvalidArgument},
		{"非法父目录", &repositoryv1.CreateDirectoryRequest{UserXid: owner, KbXid: kb.Xid, Name: "x", ParentXid: "bad"}, codes.InvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := h.CreateDirectory(ctx, tc.req); status.Code(err) != tc.code {
				t.Errorf("错误 = %v，期望 %v", err, tc.code)
			}
		})
	}

	// 他人写 → NotFound（防枚举）
	if _, err := h.CreateDirectory(ctx, &repositoryv1.CreateDirectoryRequest{
		UserXid: eid.New(eid.User), KbXid: kb.Xid, Name: "x",
	}); status.Code(err) != codes.NotFound {
		t.Errorf("他人创建错误 = %v，期望 NotFound", err)
	}

	// 创建 root + child
	rootResp, err := h.CreateDirectory(ctx, &repositoryv1.CreateDirectoryRequest{UserXid: owner, KbXid: kb.Xid, Name: "根目录"})
	if err != nil {
		t.Fatalf("CreateDirectory(根) 错误: %v", err)
	}
	root := rootResp.Directory
	childResp, err := h.CreateDirectory(ctx, &repositoryv1.CreateDirectoryRequest{
		UserXid: owner, KbXid: kb.Xid, ParentXid: root.Xid, Name: "子目录",
	})
	if err != nil {
		t.Fatalf("CreateDirectory(子) 错误: %v", err)
	}
	child := childResp.Directory

	// 同父重名 → AlreadyExists
	if _, err := h.CreateDirectory(ctx, &repositoryv1.CreateDirectoryRequest{
		UserXid: owner, KbXid: kb.Xid, ParentXid: root.Xid, Name: "子目录",
	}); status.Code(err) != codes.AlreadyExists {
		t.Errorf("重名错误 = %v，期望 AlreadyExists", err)
	}

	// 改名
	upd, err := h.UpdateDirectory(ctx, &repositoryv1.UpdateDirectoryRequest{UserXid: owner, DirXid: child.Xid, Name: "改名"})
	if err != nil || upd.Directory.Name != "改名" {
		t.Fatalf("UpdateDirectory() = %+v, %v", upd, err)
	}
	if _, err := h.UpdateDirectory(ctx, &repositoryv1.UpdateDirectoryRequest{UserXid: owner, DirXid: "bad", Name: "x"}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("非法 dir_xid 错误 = %v，期望 InvalidArgument", err)
	}

	// 移入子孙 → InvalidArgument；移回根成功
	if _, err := h.MoveDirectory(ctx, &repositoryv1.MoveDirectoryRequest{UserXid: owner, DirXid: root.Xid, ParentXid: child.Xid}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("移入子孙错误 = %v，期望 InvalidArgument", err)
	}
	if _, err := h.MoveDirectory(ctx, &repositoryv1.MoveDirectoryRequest{UserXid: owner, DirXid: child.Xid}); err != nil {
		t.Fatalf("MoveDirectory(到根) 错误: %v", err)
	}

	// 目录树：owner 可读；匿名私有库 → NotFound
	tree, err := h.GetDirectoryTree(ctx, &repositoryv1.GetDirectoryTreeRequest{UserXid: owner, KbXid: kb.Xid})
	if err != nil {
		t.Fatalf("GetDirectoryTree() 错误: %v", err)
	}
	if len(tree.Directories) != 2 || len(tree.Articles) != 0 {
		t.Errorf("目录树 = %d 目录 %d 文章，期望 2 目录 0 文章", len(tree.Directories), len(tree.Articles))
	}
	if _, err := h.GetDirectoryTree(ctx, &repositoryv1.GetDirectoryTreeRequest{KbXid: kb.Xid}); status.Code(err) != codes.NotFound {
		t.Errorf("匿名读私有库错误 = %v，期望 NotFound", err)
	}

	// 公开库匿名可读
	pub := mustCreateKB(t, h, owner, "公开库", 1)
	if _, err := h.GetDirectoryTree(ctx, &repositoryv1.GetDirectoryTreeRequest{KbXid: pub.Xid}); err != nil {
		t.Fatalf("匿名读公开库错误: %v", err)
	}

	// 移回 root 下，验证删除 root 级联 child
	if _, err := h.MoveDirectory(ctx, &repositoryv1.MoveDirectoryRequest{UserXid: owner, DirXid: child.Xid, ParentXid: root.Xid}); err != nil {
		t.Fatalf("MoveDirectory(移回) 错误: %v", err)
	}
	// 删除 root 级联 child，事务后触发对象清理
	if _, err := h.DeleteDirectory(ctx, &repositoryv1.DeleteDirectoryRequest{UserXid: owner, DirXid: root.Xid}); err != nil {
		t.Fatalf("DeleteDirectory() 错误: %v", err)
	}
	if !cleaner.called {
		t.Error("删除后应触发对象清理")
	}
	tree, err = h.GetDirectoryTree(ctx, &repositoryv1.GetDirectoryTreeRequest{UserXid: owner, KbXid: kb.Xid})
	if err != nil {
		t.Fatalf("GetDirectoryTree() 错误: %v", err)
	}
	if len(tree.Directories) != 0 {
		t.Errorf("删除后剩余目录 = %d，期望 0", len(tree.Directories))
	}
}
