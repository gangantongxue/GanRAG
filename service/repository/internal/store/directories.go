package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/gangantongxue/GanRAG/pkg/asynctask"
	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_repository"
	"github.com/gangantongxue/GanRAG/pkg/eid"
)

// GetDirectoryByXID 按业务主键查询目录
func (s *Store) GetDirectoryByXID(ctx context.Context, xid string) (*dbmodel.Directory, error) {
	dir, err := s.q.Directory.WithContext(ctx).Where(s.q.Directory.Xid.Eq(xid)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrDirectoryNotFound
	}
	if err != nil {
		return nil, err
	}
	return dir, nil
}

// getDirectoryByID 按自增主键查询目录（服务内部使用）
func (s *Store) getDirectoryByID(ctx context.Context, id int64) (*dbmodel.Directory, error) {
	dir, err := s.q.Directory.WithContext(ctx).Where(s.q.Directory.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrDirectoryNotFound
	}
	if err != nil {
		return nil, err
	}
	return dir, nil
}

// resolveParent 解析父目录：空串 = 根（0）；否则须为同库目录（跨库统一 NotFound 防枚举）
func (s *Store) resolveParent(ctx context.Context, kbID int64, parentXID string) (int64, string, error) {
	if parentXID == "" {
		return 0, "", nil
	}
	parent, err := s.GetDirectoryByXID(ctx, parentXID)
	if err != nil {
		return 0, "", err
	}
	if parent.KbID != kbID {
		return 0, "", ErrDirectoryNotFound
	}
	return parent.ID, parent.Xid, nil
}

// CreateDirectory 创建目录（同一父目录下名称唯一；kb_xid/parent_xid 同事务冗余）
func (s *Store) CreateDirectory(ctx context.Context, kbID int64, kbXID, parentXID, name string) (*dbmodel.Directory, error) {
	parentID, parentXIDNorm, err := s.resolveParent(ctx, kbID, parentXID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	dir := &dbmodel.Directory{
		Xid:       eid.New(eid.Directory),
		KbID:      kbID,
		KbXid:     kbXID,
		ParentID:  parentID,
		ParentXid: parentXIDNorm,
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.q.Directory.WithContext(ctx).Create(dir); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, ErrDirectoryNameTaken
		}
		return nil, err
	}
	return dir, nil
}

// UpdateDirectory 目录改名（全量替换）
func (s *Store) UpdateDirectory(ctx context.Context, id int64, name string) (*dbmodel.Directory, error) {
	info, err := s.q.Directory.WithContext(ctx).
		Where(s.q.Directory.ID.Eq(id)).
		Updates(map[string]any{"name": name, "updated_at": time.Now()})
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, ErrDirectoryNameTaken
		}
		return nil, err
	}
	if info.RowsAffected == 0 {
		return nil, ErrDirectoryNotFound
	}
	return s.getDirectoryByID(ctx, id)
}

// MoveDirectory 移动目录到新父目录：禁止移入自身或子孙目录（防环）
func (s *Store) MoveDirectory(ctx context.Context, dir *dbmodel.Directory, parentXID string) (*dbmodel.Directory, error) {
	parentID, parentXIDNorm, err := s.resolveParent(ctx, dir.KbID, parentXID)
	if err != nil {
		return nil, err
	}
	// 防环：从目标父目录沿父链上溯，遇到自身即为移入自身或子孙
	for pid := parentID; pid != 0; {
		if pid == dir.ID {
			return nil, ErrDirectoryMoveIntoSelf
		}
		parent, err := s.getDirectoryByID(ctx, pid)
		if err != nil {
			return nil, err
		}
		pid = parent.ParentID
	}

	info, err := s.q.Directory.WithContext(ctx).
		Where(s.q.Directory.ID.Eq(dir.ID)).
		Updates(map[string]any{
			"parent_id":  parentID,
			"parent_xid": parentXIDNorm,
			"updated_at": time.Now(),
		})
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, ErrDirectoryNameTaken
		}
		return nil, err
	}
	if info.RowsAffected == 0 {
		return nil, ErrDirectoryNotFound
	}
	return s.getDirectoryByID(ctx, dir.ID)
}

// DeleteDirectory 级联删除子树（目录/文章/附件行），并同事务为正文与附件对象登记公共删除任务
//
// 返回的对象引用供 handler 在事务提交后尽力删除 Garage 对象（同 DeleteKnowledgeBase 口径）。
func (s *Store) DeleteDirectory(ctx context.Context, dir *dbmodel.Directory, requestedByXID string) ([]ObjectRef, error) {
	var refs []ObjectRef
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		dirIDs, err := collectSubtreeDirIDs(ctx, tx, dir)
		if err != nil {
			return err
		}
		articleIDs, objectXIDs, err := collectSubtreeObjects(ctx, tx, dirIDs)
		if err != nil {
			return err
		}

		taskStore := s.newTasks(tx)
		for _, objectXID := range objectXIDs {
			task, err := taskStore.Report(ctx, asynctask.ReportInput{
				TaskType:      asynctask.TypeGarageObjectDelete,
				SourceService: asynctask.SourceRepository,
				DedupeKey:     objectXID,
				Payload: map[string]any{
					"object_xid": objectXID,
					"object_key": "o/" + objectXID,
					"source":     "directory",
					"source_xid": dir.Xid,
				},
				RequestedByXID: requestedByXID,
			})
			if err != nil {
				return err
			}
			refs = append(refs, ObjectRef{ObjectXID: objectXID, TaskXID: task.Xid})
		}

		if len(articleIDs) > 0 {
			if err := tx.Where("article_id IN ?", articleIDs).Delete(&dbmodel.Attachment{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id IN ?", articleIDs).Delete(&dbmodel.Article{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("id IN ?", dirIDs).Delete(&dbmodel.Directory{}).Error; err != nil {
			return err
		}

		// ponytail: 删除后 COUNT 重算 article_count，文章量大后改增量维护
		var count int64
		if err := tx.Model(&dbmodel.Article{}).Where("kb_id = ?", dir.KbID).Count(&count).Error; err != nil {
			return err
		}
		return tx.Model(&dbmodel.KnowledgeBase{}).Where("id = ?", dir.KbID).
			Update("article_count", count).Error
	})
	if err != nil {
		return nil, err
	}
	return refs, nil
}

// GetDirectoryTree 平铺返回知识库全部目录与文章（前端按 parent_xid/dir_xid 自建树）
func (s *Store) GetDirectoryTree(ctx context.Context, kbID int64) ([]*dbmodel.Directory, []*dbmodel.Article, error) {
	dirs, err := s.q.Directory.WithContext(ctx).
		Where(s.q.Directory.KbID.Eq(kbID)).Order(s.q.Directory.ID).Find()
	if err != nil {
		return nil, nil, err
	}
	articles, err := s.q.Article.WithContext(ctx).
		Where(s.q.Article.KbID.Eq(kbID)).Order(s.q.Article.ID).Find()
	if err != nil {
		return nil, nil, err
	}
	return dirs, articles, nil
}

// collectSubtreeDirIDs BFS 收集目录自身与全部子孙目录 ID
func collectSubtreeDirIDs(ctx context.Context, tx *gorm.DB, root *dbmodel.Directory) ([]int64, error) {
	ids := []int64{root.ID}
	for frontier := []int64{root.ID}; len(frontier) > 0; {
		var next []int64
		if err := tx.WithContext(ctx).Model(&dbmodel.Directory{}).
			Where("kb_id = ? AND parent_id IN ?", root.KbID, frontier).
			Pluck("id", &next).Error; err != nil {
			return nil, err
		}
		ids = append(ids, next...)
		frontier = next
	}
	return ids, nil
}

// collectSubtreeObjects 收集子树内文章 ID 与其正文/附件对象 xid（去重）
func collectSubtreeObjects(ctx context.Context, tx *gorm.DB, dirIDs []int64) ([]int64, []string, error) {
	var articleIDs []int64
	if err := tx.WithContext(ctx).Model(&dbmodel.Article{}).
		Where("dir_id IN ?", dirIDs).Pluck("id", &articleIDs).Error; err != nil {
		return nil, nil, err
	}
	if len(articleIDs) == 0 {
		return articleIDs, nil, nil
	}

	var contentXIDs []string
	if err := tx.WithContext(ctx).Model(&dbmodel.Article{}).
		Where("id IN ? AND content_object_xid <> ''", articleIDs).
		Pluck("content_object_xid", &contentXIDs).Error; err != nil {
		return nil, nil, err
	}
	var attachmentXIDs []string
	if err := tx.WithContext(ctx).Model(&dbmodel.Attachment{}).
		Where("article_id IN ? AND object_xid <> ''", articleIDs).
		Pluck("object_xid", &attachmentXIDs).Error; err != nil {
		return nil, nil, err
	}
	objectXIDs, err := dedupeObjectXIDs(contentXIDs, attachmentXIDs)
	if err != nil {
		return nil, nil, err
	}
	return articleIDs, objectXIDs, nil
}
