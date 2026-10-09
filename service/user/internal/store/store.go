// Package store 负责 User 服务的用户与 refresh token 数据访问
//
// 模型与类型化查询由 `task db-gen` 生成（pkg/db/model/ganrag_user、
// pkg/db/query/ganrag_user），本包只承载业务查询逻辑与错误语义。
package store

import (
	"errors"

	"gorm.io/gorm"

	dbquery "github.com/gangantongxue/GanRAG/pkg/db/query/ganrag_user"
)

// 业务哨兵错误：调用方用 errors.Is 判定并映射为 gRPC 错误码
var (
	ErrUserNotFound    = errors.New("用户不存在")             // 按 ID / 用户名均未命中
	ErrUsernameTaken   = errors.New("用户名已存在")            // 注册时用户名冲突
	ErrRefreshNotFound = errors.New("refresh token 不存在") // 哈希未命中（伪造 token）
	ErrRefreshRevoked  = errors.New("refresh token 已作废") // revoked_at 非空（重用检测信号）
)

// 用户状态常量（对应 users.status 列）
const (
	StatusNormal   int32 = 1 // 正常
	StatusDisabled int32 = 0 // 禁用
)

// Store 数据访问层封装
type Store struct {
	db *gorm.DB       // 底层连接（事务构造用）
	q  *dbquery.Query // 生成的类型化查询入口
}

// New 用 pkg/db.Open 返回的连接构造 Store
//
// 参数：
//   - db: 已完成建库与迁移的 GORM 连接
//
// 返回：
//   - *Store: 数据访问层实例
func New(db *gorm.DB) *Store {
	return &Store{db: db, q: dbquery.Use(db)}
}
