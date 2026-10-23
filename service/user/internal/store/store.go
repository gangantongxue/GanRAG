// Package store 负责 User 服务的用户与登录会话数据访问
//
// 模型与类型化查询由 `task db-gen` 生成（pkg/db/model/ganrag_user、
// pkg/db/query/ganrag_user），本包只承载业务查询逻辑与错误语义。
package store

import (
	"google.golang.org/grpc/codes"
	"gorm.io/gorm"

	dbquery "github.com/gangantongxue/GanRAG/pkg/db/query/ganrag_user"
	"github.com/gangantongxue/GanRAG/pkg/errcode"
)

// 业务哨兵错误：错误自带 gRPC 状态码，handler 统一走 errcode.Status 归一化
// （同一错误在不同 RPC 有契约分歧时由 handler 覆盖，见 server 包上下文错误变量；
// 映射逻辑统一在 pkg/errcode，禁止在服务内另行定义错误码映射）
var (
	ErrUserNotFound    = errcode.New(errcode.CodeUserNotFound, codes.NotFound, "用户不存在")                       // 按 ID / xid / 邮箱均未命中
	ErrEmailTaken      = errcode.New(errcode.CodeUserEmailTaken, codes.AlreadyExists, "邮箱已注册")                // 注册时邮箱冲突
	ErrSessionNotFound = errcode.New(errcode.CodeUserTokenInvalid, codes.Unauthenticated, "refresh token 无效") // 哈希未命中 / 并发轮换（伪造或已失效）
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
