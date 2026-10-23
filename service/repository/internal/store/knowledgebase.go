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

// ObjectRef 待清理对象及对应的公共任务
type ObjectRef struct {
	ObjectXID string // 对象业务主键 obj_<xid>
	TaskXID   string // ganrag_common.async_tasks 任务业务主键 tsk_<xid>
}

// CreateKnowledgeBase 创建知识库
func (s *Store) CreateKnowledgeBase(ctx context.Context, ownerXID, name, description string, visibility int32) (*dbmodel.KnowledgeBase, error) {
	now := time.Now()
	kb := &dbmodel.KnowledgeBase{
		Xid:         eid.New(eid.KnowledgeBase),
		OwnerXid:    ownerXID,
		Name:        name,
		Description: description,
		Visibility:  visibility,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.q.KnowledgeBase.WithContext(ctx).Create(kb); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, ErrKnowledgeBaseNameTaken
		}
		return nil, err
	}
	return kb, nil
}

// GetKnowledgeBaseByXID 按业务主键查询知识库
func (s *Store) GetKnowledgeBaseByXID(ctx context.Context, xid string) (*dbmodel.KnowledgeBase, error) {
	kb, err := s.q.KnowledgeBase.WithContext(ctx).Where(s.q.KnowledgeBase.Xid.Eq(xid)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrKnowledgeBaseNotFound
	}
	if err != nil {
		return nil, err
	}
	return kb, nil
}

// GetKnowledgeBaseByID 按自增主键查询知识库（服务内部使用）
func (s *Store) GetKnowledgeBaseByID(ctx context.Context, id int64) (*dbmodel.KnowledgeBase, error) {
	kb, err := s.q.KnowledgeBase.WithContext(ctx).Where(s.q.KnowledgeBase.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrKnowledgeBaseNotFound
	}
	if err != nil {
		return nil, err
	}
	return kb, nil
}

// ListKnowledgeBases 分页查询知识库
//
// scope 对应 proto：1 我拥有的 / 2 我参与的 / 3 公开 / 4 全部可见。
// userXID 为空时仅公开库可见（scope 4 等价于 scope 3）。
func (s *Store) ListKnowledgeBases(ctx context.Context, userXID string, scope, page, pageSize int32) ([]*dbmodel.KnowledgeBase, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	condition, args := knowledgeBaseScopeSQL(scope, userXID)
	countSQL := "SELECT COUNT(*) FROM knowledge_bases kb WHERE " + condition
	var total int64
	if err := s.db.WithContext(ctx).Raw(countSQL, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}

	listSQL := "SELECT kb.* FROM knowledge_bases kb WHERE " + condition +
		" ORDER BY kb.updated_at DESC, kb.id DESC LIMIT ? OFFSET ?"
	listArgs := append(append([]any{}, args...), pageSize, (page-1)*pageSize)
	var rows []*dbmodel.KnowledgeBase
	if err := s.db.WithContext(ctx).Raw(listSQL, listArgs...).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// knowledgeBaseScopeSQL 返回列表过滤条件与参数（仅拼接固定 SQL，用户值走占位符）
func knowledgeBaseScopeSQL(scope int32, userXID string) (string, []any) {
	const memberExists = "EXISTS (SELECT 1 FROM kb_user_relations r " +
		"WHERE r.kb_id = kb.id AND r.user_xid = ? AND r.role > 0)"

	switch scope {
	case ScopeOwned:
		if userXID == "" {
			return "1 = 0", nil
		}
		return "kb.owner_xid = ?", []any{userXID}
	case ScopeParticipated:
		if userXID == "" {
			return "1 = 0", nil
		}
		return memberExists, []any{userXID}
	case ScopePublic:
		return "kb.visibility = 1", nil
	case ScopeAllVisible:
		if userXID == "" {
			return "kb.visibility = 1", nil
		}
		return "(kb.owner_xid = ? OR kb.visibility = 1 OR " + memberExists + ")", []any{userXID, userXID}
	default:
		return "1 = 0", nil
	}
}

// UpdateKnowledgeBase 更新知识库可变字段（全量替换）
func (s *Store) UpdateKnowledgeBase(ctx context.Context, id int64, name, description string, visibility int32) (*dbmodel.KnowledgeBase, error) {
	info, err := s.q.KnowledgeBase.WithContext(ctx).
		Where(s.q.KnowledgeBase.ID.Eq(id)).
		Updates(map[string]any{
			"name":        name,
			"description": description,
			"visibility":  visibility,
			"updated_at":  time.Now(),
		})
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, ErrKnowledgeBaseNameTaken
		}
		return nil, err
	}
	if info.RowsAffected == 0 {
		return nil, ErrKnowledgeBaseNotFound
	}
	return s.GetKnowledgeBaseByID(ctx, id)
}

// DeleteKnowledgeBase 删除知识库并在同一事务内为待清理对象登记公共任务
//
// 返回的对象引用供 handler 在事务提交后尽力删除 Garage 对象：
// 成功调 async_tasks.Resolve，失败调 async_tasks.Fail。
func (s *Store) DeleteKnowledgeBase(ctx context.Context, kbID int64, kbXID, requestedByXID string) ([]ObjectRef, error) {
	var refs []ObjectRef
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		objectXIDs, err := collectObjectXIDs(ctx, tx, kbID)
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
					"source":     "knowledge_base",
					"source_xid": kbXID,
				},
				RequestedByXID: requestedByXID,
			})
			if err != nil {
				return err
			}
			refs = append(refs, ObjectRef{ObjectXID: objectXID, TaskXID: task.Xid})
		}

		if err := tx.Where("kb_id = ?", kbID).Delete(&dbmodel.Attachment{}).Error; err != nil {
			return err
		}
		if err := tx.Where("kb_id = ?", kbID).Delete(&dbmodel.Article{}).Error; err != nil {
			return err
		}
		if err := tx.Where("kb_id = ?", kbID).Delete(&dbmodel.Directory{}).Error; err != nil {
			return err
		}
		if err := tx.Where("kb_id = ?", kbID).Delete(&dbmodel.KbUserRelation{}).Error; err != nil {
			return err
		}
		info := tx.Where("id = ?", kbID).Delete(&dbmodel.KnowledgeBase{})
		if info.Error != nil {
			return info.Error
		}
		if info.RowsAffected == 0 {
			return ErrKnowledgeBaseNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return refs, nil
}

// collectObjectXIDs 收集知识库下正文与附件的对象 xid（去重）
func collectObjectXIDs(ctx context.Context, tx *gorm.DB, kbID int64) ([]string, error) {
	var contentXIDs []string
	if err := tx.WithContext(ctx).Model(&dbmodel.Article{}).
		Where("kb_id = ? AND content_object_xid <> ''", kbID).
		Pluck("content_object_xid", &contentXIDs).Error; err != nil {
		return nil, err
	}
	var attachmentXIDs []string
	if err := tx.WithContext(ctx).Model(&dbmodel.Attachment{}).
		Where("kb_id = ? AND object_xid <> ''", kbID).
		Pluck("object_xid", &attachmentXIDs).Error; err != nil {
		return nil, err
	}
	return dedupeObjectXIDs(contentXIDs, attachmentXIDs)
}

// dedupeObjectXIDs 多组对象 xid 去重并校验（非法 xid 视为数据损坏，报错中止删除）
func dedupeObjectXIDs(xidLists ...[]string) ([]string, error) {
	total := 0
	for _, list := range xidLists {
		total += len(list)
	}
	seen := make(map[string]struct{}, total)
	out := make([]string, 0, total)
	for _, list := range xidLists {
		for _, xid := range list {
			if _, ok := seen[xid]; ok {
				continue
			}
			if !eid.Match(xid, eid.Object) {
				return nil, fmt.Errorf("对象 xid 非法: %s", xid)
			}
			seen[xid] = struct{}{}
			out = append(out, xid)
		}
	}
	return out, nil
}
