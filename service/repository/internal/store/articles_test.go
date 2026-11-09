package store

import (
	"errors"
	"testing"

	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_repository"
	"github.com/gangantongxue/GanRAG/pkg/eid"
)

// mustCreateArticle 创建文章（含附件），失败即中止
func mustCreateArticle(t *testing.T, s *Store, kbID int64, kbXID, dirXID, title, contentXID string, atts []AttachmentInput) *dbmodel.Article {
	t.Helper()
	art, err := s.CreateArticle(t.Context(), kbID, kbXID, dirXID, 1, title, contentXID, eid.New(eid.User), atts)
	if err != nil {
		t.Fatalf("CreateArticle() 错误: %v", err)
	}
	return art
}

// countTasks 统计公共删除任务数（按对象 xid 幂等键）
func countTasks(t *testing.T, s *Store, objectXIDs ...string) int64 {
	t.Helper()
	var n int64
	if err := s.db.Table("async_tasks").Where("dedupe_key IN ?", objectXIDs).Count(&n).Error; err != nil {
		t.Fatalf("统计任务失败: %v", err)
	}
	return n
}

// TestArticleCRUD 验证创建（含附件）、查询、附件清单与计数
func TestArticleCRUD(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := t.Context()
	kb := mustCreateKB(t, s, eid.New(eid.User), "库", 0)

	content := eid.New(eid.Object)
	art := mustCreateArticle(t, s, kb.ID, kb.Xid, "", "标题", content, []AttachmentInput{
		{RelativePath: "assets/b.png", ObjectXID: eid.New(eid.Object), ContentType: "image/png", Size: 10, Sha256: "b"},
		{RelativePath: "assets/a.png", ObjectXID: eid.New(eid.Object), ContentType: "image/png", Size: 20, Sha256: "a"},
	})
	if !eid.Match(art.Xid, eid.Article) || art.ContentObjectXid != content {
		t.Errorf("创建结果 = %+v", art)
	}
	if art.CreatedByXid == "" {
		t.Errorf("created_by_xid 为空")
	}

	got, err := s.GetArticleByXID(ctx, art.Xid)
	if err != nil {
		t.Fatalf("GetArticleByXID() 错误: %v", err)
	}
	if got.Title != "标题" || got.DirXid != "" || got.DirID != 0 {
		t.Errorf("查询结果 = %+v", got)
	}

	atts, err := s.ListAttachments(ctx, art.ID)
	if err != nil {
		t.Fatalf("ListAttachments() 错误: %v", err)
	}
	if len(atts) != 2 || atts[0].RelativePath != "assets/a.png" || atts[1].RelativePath != "assets/b.png" {
		t.Errorf("附件清单 = %+v", atts)
	}
	if atts[0].Kind != 2 || atts[0].FileKey != "o/"+atts[0].ObjectXid || atts[0].ArticleXid != art.Xid {
		t.Errorf("附件行 = %+v", atts[0])
	}

	var kbRow dbmodel.KnowledgeBase
	if err := s.db.First(&kbRow, kb.ID).Error; err != nil {
		t.Fatalf("查询知识库失败: %v", err)
	}
	if kbRow.ArticleCount != 1 {
		t.Errorf("article_count = %d，期望 1", kbRow.ArticleCount)
	}

	if _, err := s.GetArticleByXID(ctx, eid.New(eid.Article)); !errors.Is(err, ErrArticleNotFound) {
		t.Errorf("不存在文章错误 = %v，期望 ErrArticleNotFound", err)
	}
}

// TestCreateArticleDirScope 验证跨库目录与同路径冲突
func TestCreateArticleDirScope(t *testing.T) {
	s, _ := newTestStore(t)
	owner := eid.New(eid.User)
	kb := mustCreateKB(t, s, owner, "库", 0)
	otherKB := mustCreateKB(t, s, eid.New(eid.User), "其他库", 0)
	foreignDir, err := s.CreateDirectory(t.Context(), otherKB.ID, otherKB.Xid, "", "外库目录")
	if err != nil {
		t.Fatalf("CreateDirectory() 错误: %v", err)
	}

	if _, err := s.CreateArticle(t.Context(), kb.ID, kb.Xid, foreignDir.Xid, 1, "标题", "", owner, nil); !errors.Is(err, ErrDirectoryNotFound) {
		t.Errorf("跨库目录错误 = %v，期望 ErrDirectoryNotFound", err)
	}

	dup := []AttachmentInput{
		{RelativePath: "a.png", ObjectXID: eid.New(eid.Object)},
		{RelativePath: "a.png", ObjectXID: eid.New(eid.Object)},
	}
	if _, err := s.CreateArticle(t.Context(), kb.ID, kb.Xid, "", 1, "标题", "", owner, dup); !errors.Is(err, ErrAttachmentPathTaken) {
		t.Errorf("同路径附件错误 = %v，期望 ErrAttachmentPathTaken", err)
	}
}

// TestUpdateArticleRebuild 验证全量替换、附件重建与孤儿对象任务登记
func TestUpdateArticleRebuild(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := t.Context()
	kb := mustCreateKB(t, s, eid.New(eid.User), "库", 0)
	dir, err := s.CreateDirectory(ctx, kb.ID, kb.Xid, "", "目录")
	if err != nil {
		t.Fatalf("CreateDirectory() 错误: %v", err)
	}

	oldContent := eid.New(eid.Object)
	keepAtt := eid.New(eid.Object)
	dropAtt := eid.New(eid.Object)
	newContent := eid.New(eid.Object)
	newAtt := eid.New(eid.Object)
	art := mustCreateArticle(t, s, kb.ID, kb.Xid, "", "旧标题", oldContent, []AttachmentInput{
		{RelativePath: "a.png", ObjectXID: dropAtt},
		{RelativePath: "b.png", ObjectXID: keepAtt},
	})

	updated, refs, err := s.UpdateArticle(ctx, art, dir.Xid, "新标题", newContent, []AttachmentInput{
		{RelativePath: "b.png", ObjectXID: keepAtt},
		{RelativePath: "c.png", ObjectXID: newAtt},
	})
	if err != nil {
		t.Fatalf("UpdateArticle() 错误: %v", err)
	}
	if updated.Title != "新标题" || updated.DirXid != dir.Xid || updated.ContentObjectXid != newContent {
		t.Errorf("更新结果 = %+v", updated)
	}

	// 孤儿 = 被换掉的旧正文 + 被移除的附件；保留的 b.png 不清理
	got := map[string]bool{}
	for _, r := range refs {
		got[r.ObjectXID] = true
	}
	if len(refs) != 2 || !got[oldContent] || !got[dropAtt] || got[keepAtt] || got[newContent] {
		t.Errorf("孤儿对象 = %+v，期望 {%s %s}", refs, oldContent, dropAtt)
	}
	if n := countTasks(t, s, oldContent, dropAtt, keepAtt, newContent); n != 2 {
		t.Errorf("公共删除任务数 = %d，期望 2", n)
	}

	atts, err := s.ListAttachments(ctx, art.ID)
	if err != nil {
		t.Fatalf("ListAttachments() 错误: %v", err)
	}
	if len(atts) != 2 || atts[0].RelativePath != "b.png" || atts[1].RelativePath != "c.png" {
		t.Errorf("重建后附件 = %+v", atts)
	}

	// content_object_xid 空串 = 保留原正文（attachments 全量重建，同清单重传 = 不变）
	updated2, refs2, err := s.UpdateArticle(ctx, updated, "", "再改", "", []AttachmentInput{
		{RelativePath: "b.png", ObjectXID: keepAtt},
		{RelativePath: "c.png", ObjectXID: newAtt},
	})
	if err != nil {
		t.Fatalf("UpdateArticle(保留正文) 错误: %v", err)
	}
	if updated2.ContentObjectXid != newContent {
		t.Errorf("正文指针 = %s，期望保留 %s", updated2.ContentObjectXid, newContent)
	}
	if len(refs2) != 0 {
		t.Errorf("保留正文时孤儿 = %+v，期望空", refs2)
	}

	var kbRow dbmodel.KnowledgeBase
	if err := s.db.First(&kbRow, kb.ID).Error; err != nil {
		t.Fatalf("查询知识库失败: %v", err)
	}
	if kbRow.ArticleCount != 1 {
		t.Errorf("article_count = %d，期望 1", kbRow.ArticleCount)
	}
}

// TestDeleteArticle 验证级联删除、对象任务登记与计数
func TestDeleteArticle(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := t.Context()
	kb := mustCreateKB(t, s, eid.New(eid.User), "库", 0)
	content := eid.New(eid.Object)
	attObj := eid.New(eid.Object)
	art := mustCreateArticle(t, s, kb.ID, kb.Xid, "", "标题", content, []AttachmentInput{
		{RelativePath: "a.png", ObjectXID: attObj},
	})

	refs, err := s.DeleteArticle(ctx, art, eid.New(eid.User))
	if err != nil {
		t.Fatalf("DeleteArticle() 错误: %v", err)
	}
	if len(refs) != 2 {
		t.Errorf("清理对象 = %+v，期望正文 + 附件各 1", refs)
	}
	if n := countTasks(t, s, content, attObj); n != 2 {
		t.Errorf("公共删除任务数 = %d，期望 2", n)
	}

	if _, err := s.GetArticleByXID(ctx, art.Xid); !errors.Is(err, ErrArticleNotFound) {
		t.Errorf("删除后查询错误 = %v，期望 ErrArticleNotFound", err)
	}
	var attCount int64
	if err := s.db.Model(&dbmodel.Attachment{}).Where("article_id = ?", art.ID).Count(&attCount).Error; err != nil {
		t.Fatalf("统计附件失败: %v", err)
	}
	if attCount != 0 {
		t.Errorf("残留附件行 = %d，期望 0", attCount)
	}

	var kbRow dbmodel.KnowledgeBase
	if err := s.db.First(&kbRow, kb.ID).Error; err != nil {
		t.Fatalf("查询知识库失败: %v", err)
	}
	if kbRow.ArticleCount != 0 {
		t.Errorf("article_count = %d，期望 0", kbRow.ArticleCount)
	}

	// 空正文文章删除不登记任务
	empty := mustCreateArticle(t, s, kb.ID, kb.Xid, "", "空文章", "", nil)
	if refs, err := s.DeleteArticle(ctx, empty, eid.New(eid.User)); err != nil || len(refs) != 0 {
		t.Errorf("空文章删除 = (%+v, %v)，期望无对象任务", refs, err)
	}
}
