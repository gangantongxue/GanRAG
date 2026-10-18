// Package asynctask 提供跨服务共享的异步任务/失败记录存储（ganrag_common.async_tasks）。
//
// 设计要点：
//   - 表在公共 schema ganrag_common，业务服务通过本包访问，不直接写 SQL；
//   - 使用全限定表名 ganrag_common.async_tasks，业务服务可在自身事务内调用 New(tx)，
//     让任务写入与业务行变更处于同一 MySQL 事务（同实例跨 schema 事务）；
//   - task_type 区分任务/失败类型，payload 存类型相关参数，dedupe_key 提供幂等。
package asynctask

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	dbpkg "github.com/gangantongxue/GanRAG/pkg/db"
	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_common"
	"github.com/gangantongxue/GanRAG/pkg/eid"
	"github.com/gangantongxue/GanRAG/pkg/errcode"
)

// DBName 公共任务表所在 schema（pkg/db/migrations/ganrag_common）
const DBName = "ganrag_common"

// tableName 全限定表名：业务服务用自身连接/事务写入时必须带 schema
const tableName = DBName + ".async_tasks"

// 任务状态（async_tasks.status）
const (
	StatusPending  int32 = 0 // 待处理
	StatusResolved int32 = 1 // 已解决
	StatusIgnored  int32 = 2 // 已忽略
	StatusAll      int32 = -1
)

// 首期任务类型与服务短名；后续服务按需追加，无需改表结构
const (
	TypeGarageObjectDelete = "garage_object_delete"

	SourceRepository = "repository"
)

const (
	maxTaskTypeLen      = 64
	maxSourceServiceLen = 32
	maxDedupeKeyLen     = 191
	maxErrorLen         = 1000
)

// ErrTaskNotFound 任务不存在（统一 NotFound）
var ErrTaskNotFound = errcode.New(codes.NotFound, "任务不存在")

// Task 任务行（生成模型别名，避免调用方再 import model 包）
type Task = dbmodel.AsyncTask

// ReportInput 上报任务入参
type ReportInput struct {
	TaskType       string // 任务/失败类型（必填）
	SourceService  string // 上报服务短名（必填）
	DedupeKey      string // 同服务同类型幂等键；空串表示不去重
	Payload        any    // 类型相关参数，序列化为 JSON；nil 存 NULL
	RequestedByXID string // 触发用户 usr_<xid>，可空
}

// Filter 任务列表过滤条件
type Filter struct {
	SourceService string // 空串不过滤
	TaskType      string // 空串不过滤
	Status        int32  // StatusAll 表示不过滤
	Page          int32  // 从 1 开始，<=0 取 1
	PageSize      int32  // 默认 20，上限 100
}

// Store 异步任务存储
//
// db 可以是普通连接，也可以是业务事务（*gorm.DB）；本包始终使用全限定表名，
// 因此业务服务用自身 schema 的连接也能在同一事务内写入公共表。
type Store struct {
	db    *gorm.DB
	table string
}

// New 用指定连接/事务构造 Store（生产默认使用 ganrag_common.async_tasks）
func New(db *gorm.DB) *Store {
	return &Store{db: db, table: tableName}
}

// NewWithTable 用指定表名构造 Store（测试或特殊部署使用；生产用 New）
func NewWithTable(db *gorm.DB, table string) *Store {
	return &Store{db: db, table: table}
}

// Migrate 确保公共 schema 与 async_tasks 表存在（幂等）
//
// 服务启动时可按需调用；根目录 task db-up 也会统一执行同一套迁移。
func Migrate(cfg dbpkg.Config) error {
	cfg.DBName = DBName
	return dbpkg.Up(cfg)
}

// Report 上报任务：同 source_service + task_type + dedupe_key 已存在时复用并重置为待处理
//
// 返回的任务行包含 xid，调用方在操作成功后 Resolve、失败后 Fail。
func (s *Store) Report(ctx context.Context, in ReportInput) (*Task, error) {
	in.TaskType = strings.TrimSpace(in.TaskType)
	in.SourceService = strings.TrimSpace(in.SourceService)
	if in.TaskType == "" || len(in.TaskType) > maxTaskTypeLen {
		return nil, errcode.InvalidArgument("task_type 不能为空且不超过 64 字符")
	}
	if in.SourceService == "" || len(in.SourceService) > maxSourceServiceLen {
		return nil, errcode.InvalidArgument("source_service 不能为空且不超过 32 字符")
	}
	if len(in.DedupeKey) > maxDedupeKeyLen {
		return nil, errcode.InvalidArgument("dedupe_key 不能超过 191 字符")
	}
	if in.RequestedByXID != "" && !eid.Match(in.RequestedByXID, eid.User) {
		return nil, errcode.InvalidArgument("requested_by_xid 不是合法用户 xid")
	}

	var payload *string
	if in.Payload != nil {
		raw, err := json.Marshal(in.Payload)
		if err != nil {
			return nil, errcode.InvalidArgument("payload 无法序列化为 JSON")
		}
		s := string(raw)
		payload = &s
	}

	var dedupeKey *string
	if in.DedupeKey != "" {
		dedupeKey = &in.DedupeKey
	}

	now := time.Now()
	if dedupeKey != nil {
		// 已存在则复用同一行（幂等上报），避免正常路径走到唯一键冲突
		existing, err := s.findByDedupe(ctx, in.SourceService, in.TaskType, in.DedupeKey)
		if err == nil {
			return s.updateExisting(ctx, existing, in, payload, now)
		}
		if !errors.Is(err, ErrTaskNotFound) {
			return nil, err
		}
	}

	row := &Task{
		Xid:            eid.New(eid.Task),
		TaskType:       in.TaskType,
		SourceService:  in.SourceService,
		DedupeKey:      dedupeKey,
		Payload:        payload,
		RequestedByXid: in.RequestedByXID,
		Status:         StatusPending,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := s.db.WithContext(ctx).Table(s.table).Create(row).Error; err != nil {
		// 并发上报同一幂等键：唯一索引冲突后回查并更新
		if dedupeKey != nil && errors.Is(err, gorm.ErrDuplicatedKey) {
			existing, findErr := s.findByDedupe(ctx, in.SourceService, in.TaskType, in.DedupeKey)
			if findErr != nil {
				return nil, findErr
			}
			return s.updateExisting(ctx, existing, in, payload, now)
		}
		return nil, err
	}
	return row, nil
}

// updateExisting 复用已有的幂等任务：重置为待处理并更新 payload/触发人
func (s *Store) updateExisting(ctx context.Context, row *Task, in ReportInput, payload *string, now time.Time) (*Task, error) {
	if err := s.db.WithContext(ctx).Table(s.table).Where("id = ?", row.ID).Updates(map[string]any{
		"payload":          payload,
		"requested_by_xid": in.RequestedByXID,
		"status":           StatusPending,
		"resolved_at":      nil,
		"updated_at":       now,
	}).Error; err != nil {
		return nil, err
	}
	return s.Get(ctx, row.Xid)
}

// findByDedupe 按幂等键查询任务
func (s *Store) findByDedupe(ctx context.Context, sourceService, taskType, dedupeKey string) (*Task, error) {
	var row Task
	err := s.db.WithContext(ctx).Table(s.table).
		Where("source_service = ? AND task_type = ? AND dedupe_key = ?", sourceService, taskType, dedupeKey).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// Resolve 标记任务已解决
func (s *Store) Resolve(ctx context.Context, xid string) error {
	return s.close(ctx, xid, StatusResolved)
}

// Ignore 标记任务已忽略（人工处理）
func (s *Store) Ignore(ctx context.Context, xid string) error {
	return s.close(ctx, xid, StatusIgnored)
}

// close 关闭任务（已解决/已忽略）
func (s *Store) close(ctx context.Context, xid string, status int32) error {
	now := time.Now()
	info := s.db.WithContext(ctx).Table(s.table).
		Where("xid = ?", xid).
		Updates(map[string]any{
			"status":      status,
			"resolved_at": now,
			"updated_at":  now,
		})
	if info.Error != nil {
		return info.Error
	}
	if info.RowsAffected == 0 {
		return ErrTaskNotFound
	}
	return nil
}

// Fail 记录一次失败：attempts+1、刷新 last_error，保持待处理
func (s *Store) Fail(ctx context.Context, xid, reason string) error {
	info := s.db.WithContext(ctx).Table(s.table).
		Where("xid = ?", xid).
		Updates(map[string]any{
			"status":      StatusPending,
			"attempts":    gorm.Expr("attempts + 1"),
			"last_error":  truncateText(reason, maxErrorLen),
			"resolved_at": nil,
			"updated_at":  time.Now(),
		})
	if info.Error != nil {
		return info.Error
	}
	if info.RowsAffected == 0 {
		return ErrTaskNotFound
	}
	return nil
}

// Get 按 xid 查询任务
func (s *Store) Get(ctx context.Context, xid string) (*Task, error) {
	var row Task
	err := s.db.WithContext(ctx).Table(s.table).Where("xid = ?", xid).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// List 分页查询任务（后台/worker 用）
func (s *Store) List(ctx context.Context, f Filter) ([]*Task, int64, error) {
	if f.Status < StatusAll || f.Status > StatusIgnored {
		return nil, 0, errcode.InvalidArgument("status 不合法")
	}
	if f.Page <= 0 {
		f.Page = 1
	}
	if f.PageSize <= 0 {
		f.PageSize = 20
	}
	if f.PageSize > 100 {
		f.PageSize = 100
	}

	q := s.db.WithContext(ctx).Table(s.table)
	if f.SourceService != "" {
		q = q.Where("source_service = ?", f.SourceService)
	}
	if f.TaskType != "" {
		q = q.Where("task_type = ?", f.TaskType)
	}
	if f.Status != StatusAll {
		q = q.Where("status = ?", f.Status)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rows []*Task
	if err := q.Order("updated_at DESC, id DESC").
		Offset(int((f.Page - 1) * f.PageSize)).
		Limit(int(f.PageSize)).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// truncateText 按字符数截断，避免 MySQL VARCHAR 超长报错
func truncateText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
