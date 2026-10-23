// Package store 负责 Repository 服务的数据访问（知识库/目录/文章/附件）。
//
// 模型与类型化查询由 task db-gen 生成（pkg/db/model/ganrag_repository、
// pkg/db/query/ganrag_repository）；本包只承载业务查询、事务与错误语义。
package store

import (
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	"github.com/gangantongxue/GanRAG/pkg/asynctask"
	dbquery "github.com/gangantongxue/GanRAG/pkg/db/query/ganrag_repository"
	"github.com/gangantongxue/GanRAG/pkg/errcode"
)

// 业务哨兵错误：错误自带 gRPC 状态码，handler 统一走 errcode.Status 归一化
var (
	ErrKnowledgeBaseNotFound  = errcode.New(40430, codes.NotFound, "知识库不存在")
	ErrKnowledgeBaseNameTaken = errcode.New(40930, codes.AlreadyExists, "知识库名称已存在")
	ErrDirectoryNotFound      = errcode.New(40431, codes.NotFound, "目录不存在")
	ErrDirectoryNameTaken     = errcode.New(40931, codes.AlreadyExists, "同级目录名称已存在")
	ErrDirectoryMoveIntoSelf  = errcode.New(40030, codes.InvalidArgument, "不能移入自身或子孙目录")
)

// 知识库列表 scope（对齐 api/proto/repository/v1 ListKnowledgeBasesRequest）
const (
	ScopeOwned        int32 = 1 // 我拥有的
	ScopeParticipated int32 = 2 // 我参与的
	ScopePublic       int32 = 3 // 公开
	ScopeAllVisible   int32 = 4 // 我的 + 参与 + 公开
)

// Store 数据访问层封装
type Store struct {
	db       *gorm.DB
	q        *dbquery.Query
	newTasks func(*gorm.DB) *asynctask.Store // 测试可替换为本地表名
}

// New 用 pkg/db.Open 返回的连接构造 Store
func New(db *gorm.DB) *Store {
	return &Store{
		db:       db,
		q:        dbquery.Use(db),
		newTasks: asynctask.New,
	}
}
