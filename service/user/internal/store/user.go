package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_user"
	dbquery "github.com/gangantongxue/GanRAG/pkg/db/query/ganrag_user"
)

// CreateUser 创建用户
//
// 先查后插保证唯一性提示友好，唯一索引兜底并发竞态（gorm.ErrDuplicatedKey 映射）。
//
// 参数：
//   - ctx: 上下文
//   - username: 用户名（3-32 字符，调用方已校验格式）
//   - passwordHash: bcrypt 密码哈希
//   - nickname: 昵称（首期默认等于用户名）
//
// 返回：
//   - *dbmodel.User: 创建成功的用户（含自增 ID 与时间戳）
//   - error: 用户名冲突返回 ErrUsernameTaken，其余为数据库错误
func (s *Store) CreateUser(ctx context.Context, username, passwordHash, nickname string) (*dbmodel.User, error) {
	q := s.q
	_, err := q.User.WithContext(ctx).Where(q.User.Username.Eq(username)).First()
	if err == nil {
		return nil, ErrUsernameTaken
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	now := time.Now()
	u := &dbmodel.User{
		Username:     username,
		PasswordHash: passwordHash,
		Nickname:     nickname,
		Status:       StatusNormal,
		// 显式赋值：列带 default:CURRENT_TIMESTAMP 时 GORM 会跳过该列并不回填零值字段，
		// 导致返回给调用方的时间戳为零值
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := q.User.WithContext(ctx).Create(u); err != nil {
		// 并发下唯一索引冲突兜底（pkg/db.Open 已开启 TranslateError）
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, ErrUsernameTaken
		}
		return nil, err
	}
	return u, nil
}

// GetUserByID 按 ID 查询用户
//
// 参数：
//   - ctx: 上下文
//   - id: 用户 ID
//
// 返回：
//   - *dbmodel.User: 查询到的用户
//   - error: 目标不存在返回 ErrUserNotFound
func (s *Store) GetUserByID(ctx context.Context, id int64) (*dbmodel.User, error) {
	u, err := s.q.User.WithContext(ctx).Where(s.q.User.ID.Eq(id)).First()
	return mapUser(u, err)
}

// GetUserByUsername 按用户名查询用户
//
// 参数：
//   - ctx: 上下文
//   - username: 用户名（走唯一索引，单条级查询）
//
// 返回：
//   - *dbmodel.User: 查询到的用户（含密码哈希，仅供登录校验）
//   - error: 目标不存在返回 ErrUserNotFound
func (s *Store) GetUserByUsername(ctx context.Context, username string) (*dbmodel.User, error) {
	u, err := s.q.User.WithContext(ctx).Where(s.q.User.Username.Eq(username)).First()
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

// UpdatePassword 更新密码哈希并吊销该用户全部 refresh token
//
// 同一事务内完成：改密成功后旧 refresh 全部失效，已登录会话在 access 过期（≤15 分钟）后需重新登录。
//
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
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
		// 改密后吊销该用户全部 refresh token
		_, err = q.RefreshToken.WithContext(ctx).Where(q.RefreshToken.UserID.Eq(userID)).Delete()
		return err
	})
}
