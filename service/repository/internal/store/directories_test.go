package store

import (
	"errors"
	"testing"

	dbcommon "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_common"
	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_repository"
	"github.com/gangantongxue/GanRAG/pkg/eid"
)

// TestDirectoryCRUDAndMove 验证目录创建/改名/移动、重名、防环与跨库 parent
func TestDirectoryCRUDAndMove(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := t.Context()
	owner := eid.New(eid.User)
	kb := mustCreateKB(t, s, owner, "库", 0)
	other := mustCreateKB(t, s, owner, "别的库", 0)

	root, err := s.CreateDirectory(ctx, kb.ID, kb.Xid, "", "根目录")
	if err != nil {
		t.Fatalf("CreateDirectory(根) 错误: %v", err)
	}
	if root.ParentID != 0 || root.ParentXid != "" || !eid.Match(root.Xid, eid.Directory) {
		t.Errorf("根目录 = %+v", root)
	}

	child, err := s.CreateDirectory(ctx, kb.ID, kb.Xid, root.Xid, "子目录")
	if err != nil {
		t.Fatalf("CreateDirectory(子) 错误: %v", err)
	}
	if child.ParentID != root.ID || child.ParentXid != root.Xid {
		t.Errorf("子目录 = %+v", child)
	}

	// 同父目录重名
	if _, err := s.CreateDirectory(ctx, kb.ID, kb.Xid, root.Xid, "子目录"); !errors.Is(err, ErrDirectoryNameTaken) {
		t.Errorf("同父重名错误 = %v，期望 ErrDirectoryNameTaken", err)
	}
	// 跨库 parent 统一 NotFound（防枚举）
	otherDir, err := s.CreateDirectory(ctx, other.ID, other.Xid, "", "他库目录")
	if err != nil {
		t.Fatalf("CreateDirectory(他库) 错误: %v", err)
	}
	if _, err := s.CreateDirectory(ctx, kb.ID, kb.Xid, otherDir.Xid, "x"); !errors.Is(err, ErrDirectoryNotFound) {
		t.Errorf("跨库 parent 错误 = %v，期望 ErrDirectoryNotFound", err)
	}

	// 改名：正常 + 改成同父已有名
	renamed, err := s.UpdateDirectory(ctx, child.ID, "改名")
	if err != nil || renamed.Name != "改名" {
		t.Fatalf("UpdateDirectory() = %+v, %v", renamed, err)
	}
	top, err := s.CreateDirectory(ctx, kb.ID, kb.Xid, "", "顶层")
	if err != nil {
		t.Fatalf("CreateDirectory(顶层) 错误: %v", err)
	}
	if _, err := s.UpdateDirectory(ctx, top.ID, "根目录"); !errors.Is(err, ErrDirectoryNameTaken) {
		t.Errorf("改名重名错误 = %v，期望 ErrDirectoryNameTaken", err)
	}

	// 防环：移入自身 / 子孙
	grand, err := s.CreateDirectory(ctx, kb.ID, kb.Xid, child.Xid, "孙目录")
	if err != nil {
		t.Fatalf("CreateDirectory(孙) 错误: %v", err)
	}
	if _, err := s.MoveDirectory(ctx, root, root.Xid); !errors.Is(err, ErrDirectoryMoveIntoSelf) {
		t.Errorf("移入自身错误 = %v，期望 ErrDirectoryMoveIntoSelf", err)
	}
	if _, err := s.MoveDirectory(ctx, root, grand.Xid); !errors.Is(err, ErrDirectoryMoveIntoSelf) {
		t.Errorf("移入子孙错误 = %v，期望 ErrDirectoryMoveIntoSelf", err)
	}
	// 跨库目标 parent
	if _, err := s.MoveDirectory(ctx, root, otherDir.Xid); !errors.Is(err, ErrDirectoryNotFound) {
		t.Errorf("跨库移动错误 = %v，期望 ErrDirectoryNotFound", err)
	}

	// 移动到根 + 平铺树
	moved, err := s.MoveDirectory(ctx, child, "")
	if err != nil || moved.ParentID != 0 || moved.ParentXid != "" {
		t.Fatalf("MoveDirectory(到根) = %+v, %v", moved, err)
	}
	dirs, articles, err := s.GetDirectoryTree(ctx, kb.ID)
	if err != nil {
		t.Fatalf("GetDirectoryTree() 错误: %v", err)
	}
	if len(dirs) != 4 || len(articles) != 0 {
		t.Errorf("目录树 = %d 目录 %d 文章，期望 4 目录 0 文章", len(dirs), len(articles))
	}

	if _, err := s.GetDirectoryByXID(ctx, eid.New(eid.Directory)); !errors.Is(err, ErrDirectoryNotFound) {
		t.Errorf("不存在目录错误 = %v，期望 ErrDirectoryNotFound", err)
	}
}

// TestDeleteDirectoryCascade 验证子树级联删除、对象清理任务登记与 article_count 重算
func TestDeleteDirectoryCascade(t *testing.T) {
	s, db := newTestStore(t)
	ctx := t.Context()
	owner := eid.New(eid.User)
	kb := mustCreateKB(t, s, owner, "库", 0)
	a, err := s.CreateDirectory(ctx, kb.ID, kb.Xid, "", "a")
	if err != nil {
		t.Fatalf("CreateDirectory(a) 错误: %v", err)
	}
	b, err := s.CreateDirectory(ctx, kb.ID, kb.Xid, a.Xid, "b")
	if err != nil {
		t.Fatalf("CreateDirectory(b) 错误: %v", err)
	}

	contentA, attachA, outside := eid.New(eid.Object), eid.New(eid.Object), eid.New(eid.Object)
	artIn := &dbmodel.Article{
		Xid: eid.New(eid.Article), KbID: kb.ID, KbXid: kb.Xid, DirID: b.ID, DirXid: b.Xid,
		Type: 1, Title: "子树文章", ContentKey: "o/" + contentA,
		ContentObjectXid: contentA, CreatedByXid: owner,
	}
	if err := db.Create(artIn).Error; err != nil {
		t.Fatalf("插入子树文章失败: %v", err)
	}
	if err := db.Create(&dbmodel.Attachment{
		Xid: eid.New(eid.Attachment), KbID: kb.ID, KbXid: kb.Xid,
		ArticleID: artIn.ID, ArticleXid: artIn.Xid, Kind: 2,
		FileKey: "o/" + attachA, ObjectXid: attachA,
		RelativePath: "assets/a.png", OriginalName: "a.png", ContentType: "image/png",
		Size: 1, Sha256: "x", CreatedByXid: owner,
	}).Error; err != nil {
		t.Fatalf("插入附件失败: %v", err)
	}
	artOut := &dbmodel.Article{
		Xid: eid.New(eid.Article), KbID: kb.ID, KbXid: kb.Xid, DirID: 0, DirXid: "",
		Type: 1, Title: "根文章", ContentKey: "o/" + outside,
		ContentObjectXid: outside, CreatedByXid: owner,
	}
	if err := db.Create(artOut).Error; err != nil {
		t.Fatalf("插入根文章失败: %v", err)
	}
	if err := db.Model(&dbmodel.KnowledgeBase{}).Where("id = ?", kb.ID).
		Update("article_count", 2).Error; err != nil {
		t.Fatalf("设置 article_count 失败: %v", err)
	}

	refs, err := s.DeleteDirectory(ctx, a, owner)
	if err != nil {
		t.Fatalf("DeleteDirectory() 错误: %v", err)
	}
	// 子树内正文 + 附件两个对象
	got := map[string]bool{}
	for _, r := range refs {
		got[r.ObjectXID] = true
	}
	if len(refs) != 2 || !got[contentA] || !got[attachA] {
		t.Fatalf("对象引用 = %+v，期望正文与附件各一", refs)
	}

	// outbox 同事务登记
	var tasks []dbcommon.AsyncTask
	if err := db.Find(&tasks).Error; err != nil {
		t.Fatalf("查询任务失败: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("任务数 = %d，期望 2", len(tasks))
	}

	// 子树删净、子树外保留
	var dirCount, inCount, attCount, outCount int64
	db.Model(&dbmodel.Directory{}).Where("kb_id = ?", kb.ID).Count(&dirCount)
	db.Model(&dbmodel.Article{}).Where("id = ?", artIn.ID).Count(&inCount)
	db.Model(&dbmodel.Attachment{}).Where("article_id = ?", artIn.ID).Count(&attCount)
	db.Model(&dbmodel.Article{}).Where("id = ?", artOut.ID).Count(&outCount)
	if dirCount != 0 || inCount != 0 || attCount != 0 || outCount != 1 {
		t.Errorf("删除后残留: 目录 %d 子树文章 %d 附件 %d 根文章 %d", dirCount, inCount, attCount, outCount)
	}

	// article_count 重算为子树外剩余数
	gotKB, err := s.GetKnowledgeBaseByID(ctx, kb.ID)
	if err != nil {
		t.Fatalf("GetKnowledgeBaseByID() 错误: %v", err)
	}
	if gotKB.ArticleCount != 1 {
		t.Errorf("article_count = %d，期望 1", gotKB.ArticleCount)
	}
}
