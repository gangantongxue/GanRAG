package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_user"
	dbquery "github.com/gangantongxue/GanRAG/pkg/db/query/ganrag_user"
	"github.com/gangantongxue/GanRAG/pkg/eid"
)

// CreateUser 创建用户
//
// 先查后插保证唯一性提示友好，唯一索引兜底并发竞态（gorm.ErrDuplicatedKey 映射）。
// 业务主键由 pkg/eid 生成（usr_<xid> 全串入库，对外标识）。
//
// 参数：
//   - ctx: 上下文
//   - email: 邮箱（归一后值，调用方已校验格式）
//   - username: 展示名（可空，≤64 字符，调用方已校验）
//   - passwordHash: bcrypt 密码哈希
//
// 返回：
//   - *dbmodel.User: 创建成功的用户（含自增 ID 与时间戳）
//   - error: 邮箱冲突返回 ErrEmailTaken，其余为数据库错误
func (s *Store) CreateUser(ctx context.Context, email, username, passwordHash string) (*dbmodel.User, error) {
	q := s.q
	_, err := q.User.WithContext(ctx).Where(q.User.Email.Eq(email)).First()
	if err == nil {
		return nil, ErrEmailTaken
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	now := time.Now()
	u := &dbmodel.User{
		Xid:          eid.New(eid.User),
		Email:        email,
		Username:     username,
		PasswordHash: passwordHash,
		Status:       StatusNormal,
		// 显式赋值：列带 default:CURRENT_TIMESTAMP 时 GORM 会跳过该列并不回填零值字段，
		// 导致返回给调用方的时间戳为零值
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := q.User.WithContext(ctx).Create(u); err != nil {
		// 并发下唯一索引冲突兜底（pkg/db.Open 已开启 TranslateError）
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, ErrEmailTaken
		}
		return nil, err
	}
	return u, nil
}

// GetUserByID 按自增 ID 查询用户（服务内部使用，如 sessions.user_id 关联回查）
//
// 参数：
//   - ctx: 上下文
//   - id: 用户自增 ID
//
// 返回：
//   - *dbmodel.User: 查询到的用户
//   - error: 目标不存在返回 ErrUserNotFound
func (s *Store) GetUserByID(ctx context.Context, id int64) (*dbmodel.User, error) {
	u, err := s.q.User.WithContext(ctx).Where(s.q.User.ID.Eq(id)).First()
	return mapUser(u, err)
}

// GetUserByXID 按业务主键查询用户（对外接口入参口径）
//
// 参数：
//   - ctx: 上下文
//   - xid: 用户业务主键 usr_<xid> 全串（调用方已用 eid.Match 校验）
//
// 返回：
//   - *dbmodel.User: 查询到的用户（含密码哈希，仅供登录/改密校验）
//   - error: 目标不存在返回 ErrUserNotFound
func (s *Store) GetUserByXID(ctx context.Context, xid string) (*dbmodel.User, error) {
	u, err := s.q.User.WithContext(ctx).Where(s.q.User.Xid.Eq(xid)).First()
	return mapUser(u, err)
}

// GetUserByEmail 按邮箱查询用户（登录标识，走唯一索引）
//
// 参数：
//   - ctx: 上下文
//   - email: 邮箱（归一后值）
//
// 返回：
//   - *dbmodel.User: 查询到的用户（含密码哈希，仅供登录校验）
//   - error: 目标不存在返回 ErrUserNotFound
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*dbmodel.User, error) {
	u, err := s.q.User.WithContext(ctx).Where(s.q.User.Email.Eq(email)).First()
	return mapUser(u, err)
}

// mapUser 归一化单条用户查询结果：未命中映射为 ErrUserNotFound
//
// 参数：
//   - u: 查询到的用户（未命中时为 nil）
//   - err: 查询错误
//
// 返回：
//   - *dbmodel.User: 查询到的用户
//   - error: 未命中返回 ErrUserNotFound，其余为数据库错误
func mapUser(u *dbmodel.User, err error) (*dbmodel.User, error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// UpdatePassword 更新密码哈希并删除该用户全部 sessions 行
//
// 同一事务内完成：改密成功后全部登录会话失效，已登录会话在 access 过期（≤15 分钟）
// 后需重新登录（改密/找回密码删全部会话，见 data-model.md「sessions 登录会话」）。
//
// 参数：
//   - ctx: 上下文
//   - userID: 用户自增 ID
//   - passwordHash: 新密码的 bcrypt 哈希
//
// 返回：
//   - error: 用户不存在返回 ErrUserNotFound，事务失败返回数据库错误
func (s *Store) UpdatePassword(ctx context.Context, userID int64, passwordHash string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := dbquery.Use(tx)

		info, err := q.User.WithContext(ctx).Where(q.User.ID.Eq(userID)).
			Update(q.User.PasswordHash, passwordHash)
		if err != nil {
			return err
		}
		if info.RowsAffected == 0 {
			return ErrUserNotFound
		}
		// 改密后删除该用户全部登录会话
		return DeleteUserSessions(ctx, q, userID)
	})
}
