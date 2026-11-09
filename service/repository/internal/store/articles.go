package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/gangantongxue/GanRAG/pkg/asynctask"
	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_repository"
	"github.com/gangantongxue/GanRAG/pkg/eid"
)

// attachmentKind 附件类型：P1 仅 kind=2（正文内嵌图片/文件）
const attachmentKind int32 = 2

// AttachmentInput 附件写入输入（元信息由 handler 经对象 HEAD 回填）
type AttachmentInput struct {
	RelativePath string // 规范化相对路径（handler 已校验）
	ObjectXID    string // 对象 obj_<xid>
	OriginalName string
	ContentType  string
	Size         int64
	Sha256       string
}

// GetArticleByXID 按业务主键查询文章
func (s *Store) GetArticleByXID(ctx context.Context, xid string) (*dbmodel.Article, error) {
	art, err := s.q.Article.WithContext(ctx).Where(s.q.Article.Xid.Eq(xid)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrArticleNotFound
	}
	if err != nil {
		return nil, err
	}
	return art, nil
}

// ListAttachments 列出文章附件（按相对路径排序，供保存时全量重建的基准）
func (s *Store) ListAttachments(ctx context.Context, articleID int64) ([]*dbmodel.Attachment, error) {
	return s.q.Attachment.WithContext(ctx).
		Where(s.q.Attachment.ArticleID.Eq(articleID)).
		Order(s.q.Attachment.RelativePath).
		Find()
}

// CreateArticle 创建文章：文章行 + 附件行（kind=2）同事务，kb.article_count +1
//
// dirXID 空串 = 根；目录须为同库目录（跨库统一 NotFound 防枚举）。
func (s *Store) CreateArticle(ctx context.Context, kbID int64, kbXID, dirXID string, typ int32, title, contentObjectXID, createdByXID string, attachments []AttachmentInput) (*dbmodel.Article, error) {
	dirID, dirXIDNorm, err := s.resolveParent(ctx, kbID, dirXID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	art := &dbmodel.Article{
		Xid:              eid.New(eid.Article),
		KbID:             kbID,
		KbXid:            kbXID,
		DirID:            dirID,
		DirXid:           dirXIDNorm,
		Type:             typ,
		Title:            title,
		ContentObjectXid: contentObjectXID,
		CreatedByXid:     createdByXID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(art).Error; err != nil {
			return err
		}
		rows := buildAttachmentRows(art, attachments, createdByXID, now)
		if len(rows) > 0 {
			if err := tx.Create(rows).Error; err != nil {
				if errors.Is(err, gorm.ErrDuplicatedKey) {
					return ErrAttachmentPathTaken
				}
				return err
			}
		}
		return tx.Model(&dbmodel.KnowledgeBase{}).Where("id = ?", kbID).
			Update("article_count", gorm.Expr("article_count + 1")).Error
	})
	if err != nil {
		return nil, err
	}
	return art, nil
}

// UpdateArticle 更新文章（title/dir/正文指针全量替换；contentObjectXID 空串 = 保留原正文），
// 附件行全量重建（空 = 清空）。
//
// 返回的对象引用为不再被引用的孤儿对象（被替换的旧正文 + 被移除的附件），
// 同事务登记公共删除任务，供 handler 在事务提交后尽力删除 Garage 对象。
func (s *Store) UpdateArticle(ctx context.Context, art *dbmodel.Article, dirXID, title, contentObjectXID string, attachments []AttachmentInput) (*dbmodel.Article, []ObjectRef, error) {
	dirID, dirXIDNorm, err := s.resolveParent(ctx, art.KbID, dirXID)
	if err != nil {
		return nil, nil, err
	}

	// 正文指针：空串 = 保留原正文；非空 = 换正文，旧对象成孤儿
	finalContent := art.ContentObjectXid
	if contentObjectXID != "" {
		finalContent = contentObjectXID
	}

	var refs []ObjectRef
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 孤儿 = 旧引用集合 − 新引用集合（正文 + 附件对象，去重）
		oldXIDs := []string{art.ContentObjectXid}
		var oldAttXIDs []string
		if err := tx.Model(&dbmodel.Attachment{}).
			Where("article_id = ? AND object_xid <> ''", art.ID).
			Pluck("object_xid", &oldAttXIDs).Error; err != nil {
			return err
		}
		oldXIDs = append(oldXIDs, oldAttXIDs...)
		newXIDs := []string{finalContent}
		for _, att := range attachments {
			newXIDs = append(newXIDs, att.ObjectXID)
		}
		orphans, err := diffObjectXIDs(oldXIDs, newXIDs)
		if err != nil {
			return err
		}

		if err := tx.Model(&dbmodel.Article{}).Where("id = ?", art.ID).Updates(map[string]any{
			"dir_id":             dirID,
			"dir_xid":            dirXIDNorm,
			"title":              title,
			"content_object_xid": finalContent,
			"updated_at":         time.Now(),
		}).Error; err != nil {
			return err
		}
		if err := tx.Where("article_id = ?", art.ID).Delete(&dbmodel.Attachment{}).Error; err != nil {
			return err
		}
		rows := buildAttachmentRows(art, attachments, art.CreatedByXid, time.Now())
		if len(rows) > 0 {
			if err := tx.Create(rows).Error; err != nil {
				if errors.Is(err, gorm.ErrDuplicatedKey) {
					return ErrAttachmentPathTaken
				}
				return err
			}
		}

		taskStore := s.newTasks(tx)
		for _, objectXID := range orphans {
			task, err := taskStore.Report(ctx, asynctask.ReportInput{
				TaskType:      asynctask.TypeGarageObjectDelete,
				SourceService: asynctask.SourceRepository,
				DedupeKey:     objectXID,
				Payload: map[string]any{
					"object_xid": objectXID,
					"object_key": "o/" + objectXID,
					"source":     "article",
					"source_xid": art.Xid,
				},
				RequestedByXID: art.CreatedByXid,
			})
			if err != nil {
				return err
			}
			refs = append(refs, ObjectRef{ObjectXID: objectXID, TaskXID: task.Xid})
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	updated, err := s.GetArticleByXID(ctx, art.Xid)
	if err != nil {
		return nil, nil, err
	}
	return updated, refs, nil
}

// DeleteArticle 删除文章：级联附件行，正文与附件对象登记公共删除任务，
// kb.article_count -1。返回的对象引用供 handler 在事务提交后尽力删除 Garage 对象。
func (s *Store) DeleteArticle(ctx context.Context, art *dbmodel.Article, requestedByXID string) ([]ObjectRef, error) {
	var refs []ObjectRef
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var objectXIDs []string
		if art.ContentObjectXid != "" {
			objectXIDs = append(objectXIDs, art.ContentObjectXid)
		}
		var attXIDs []string
		if err := tx.Model(&dbmodel.Attachment{}).
			Where("article_id = ? AND object_xid <> ''", art.ID).
			Pluck("object_xid", &attXIDs).Error; err != nil {
			return err
		}
		objectXIDs = append(objectXIDs, attXIDs...)
		deduped, err := dedupeObjectXIDs(objectXIDs)
		if err != nil {
			return err
		}

		taskStore := s.newTasks(tx)
		for _, objectXID := range deduped {
			task, err := taskStore.Report(ctx, asynctask.ReportInput{
				TaskType:      asynctask.TypeGarageObjectDelete,
				SourceService: asynctask.SourceRepository,
				DedupeKey:     objectXID,
				Payload: map[string]any{
					"object_xid": objectXID,
					"object_key": "o/" + objectXID,
					"source":     "article",
					"source_xid": art.Xid,
				},
				RequestedByXID: requestedByXID,
			})
			if err != nil {
				return err
			}
			refs = append(refs, ObjectRef{ObjectXID: objectXID, TaskXID: task.Xid})
		}

		if err := tx.Where("article_id = ?", art.ID).Delete(&dbmodel.Attachment{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", art.ID).Delete(&dbmodel.Article{}).Error; err != nil {
			return err
		}
		return tx.Model(&dbmodel.KnowledgeBase{}).Where("id = ?", art.KbID).
			Update("article_count", gorm.Expr("article_count - 1")).Error
	})
	if err != nil {
		return nil, err
	}
	return refs, nil
}

// buildAttachmentRows 附件输入转库行（kind=2，冗余 kb/article 引用）
func buildAttachmentRows(art *dbmodel.Article, attachments []AttachmentInput, createdByXID string, now time.Time) []*dbmodel.Attachment {
	rows := make([]*dbmodel.Attachment, 0, len(attachments))
	for _, att := range attachments {
		rows = append(rows, &dbmodel.Attachment{
			Xid:          eid.New(eid.Attachment),
			KbID:         art.KbID,
			KbXid:        art.KbXid,
			ArticleID:    art.ID,
			ArticleXid:   art.Xid,
			Kind:         attachmentKind,
			FileKey:      "o/" + att.ObjectXID,
			ObjectXid:    att.ObjectXID,
			RelativePath: att.RelativePath,
			OriginalName: att.OriginalName,
			ContentType:  att.ContentType,
			Size:         att.Size,
			Sha256:       att.Sha256,
			CreatedByXid: createdByXID,
			CreatedAt:    now,
		})
	}
	return rows
}

// diffObjectXIDs 计算旧引用中不再被新引用的孤儿对象（去重校验后返回）
func diffObjectXIDs(oldXIDs, newXIDs []string) ([]string, error) {
	keep := make(map[string]struct{}, len(newXIDs))
	for _, xid := range newXIDs {
		if xid == "" {
			continue
		}
		if !eid.Match(xid, eid.Object) {
			return nil, fmt.Errorf("对象 xid 非法: %s", xid)
		}
		keep[xid] = struct{}{}
	}
	seen := make(map[string]struct{}, len(oldXIDs))
	out := make([]string, 0, len(oldXIDs))
	for _, xid := range oldXIDs {
		if xid == "" {
			continue
		}
		if _, ok := keep[xid]; ok {
			continue
		}
		if _, ok := seen[xid]; ok {
			continue
		}
		if !eid.Match(xid, eid.Object) {
			return nil, fmt.Errorf("对象 xid 非法: %s", xid)
		}
		seen[xid] = struct{}{}
		out = append(out, xid)
	}
	return out, nil
}
