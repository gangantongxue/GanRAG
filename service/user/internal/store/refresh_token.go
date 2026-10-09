package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_user"
	dbquery "github.com/gangantongxue/GanRAG/pkg/db/query/ganrag_user"
)

// CreateRefreshToken 写入新 refresh token（哈希），并顺带清理该用户已过期记录
//
// 过期清理为惰性策略：登录 / 刷新时执行，无需定时任务。
//
// 参数：
//   - ctx: 上下文
//   - userID: 归属用户 ID
//   - tokenHash: 明文 token 的 SHA-256 hex
//   - expiresAt: 过期时间（当前时间 + refresh_ttl）
//
// 返回：
//   - error: 写入失败返回数据库错误
func (s *Store) CreateRefreshToken(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := dbquery.Use(tx)
		if err := deleteExpired(ctx, q, userID); err != nil {
			return err
		}
		return q.RefreshToken.WithContext(ctx).Create(&dbmodel.RefreshToken{
			UserID:    userID,
			TokenHash: tokenHash,
			ExpiresAt: expiresAt,
		})
	})
}

// GetRefreshToken 按哈希查询 refresh token 记录
//
// 返回包含已作废（RevokedAt 非空）与已过期的行，具体状态由调用方判定——
// 重用已作废 token 是泄露信号，需吊销该用户全部 token。
//
// 参数：
//   - ctx: 上下文
//   - tokenHash: 明文 token 的 SHA-256 hex
//
// 返回：
//   - *dbmodel.RefreshToken: 查询到的记录
//   - error: 未命中返回 ErrRefreshNotFound
func (s *Store) GetRefreshToken(ctx context.Context, tokenHash string) (*dbmodel.RefreshToken, error) {
	tok, err := s.q.RefreshToken.WithContext(ctx).
		Where(s.q.RefreshToken.TokenHash.Eq(tokenHash)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRefreshNotFound
	}
	if err != nil {
		return nil, err
	}
	return tok, nil
}

// RotateRefreshToken 轮换 refresh token（同事务内作废旧记录 + 插入新记录）
//
// 作废采用条件更新（revoked_at IS NULL）而非直接删除：保留已作废行至其自然过期，
// 才能识别「重用已作废 token」的泄露信号；删除后哈希行消失，无法区分伪造与重用。
//
// 参数：
//   - ctx: 上下文
//   - old: 旧 token 记录（取其 ID 与 UserID）
//   - newHash: 新 token 的 SHA-256 hex
//   - newExpiresAt: 新 token 过期时间
//
// 返回：
//   - error: 旧记录已被并发作废时返回 ErrRefreshRevoked（重用信号），其余为数据库错误
func (s *Store) RotateRefreshToken(ctx context.Context, old *dbmodel.RefreshToken, newHash string, newExpiresAt time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := dbquery.Use(tx)

		// 条件作废：仅当仍活跃时生效，影响行数为 0 说明已被并发轮换/吊销
		info, err := q.RefreshToken.WithContext(ctx).
			Where(q.RefreshToken.ID.Eq(old.ID), q.RefreshToken.RevokedAt.IsNull()).
			Update(q.RefreshToken.RevokedAt, time.Now())
		if err != nil {
			return err
		}
		if info.RowsAffected == 0 {
			return ErrRefreshRevoked
		}

		// 顺带清理该用户已过期记录
		if err := deleteExpired(ctx, q, old.UserID); err != nil {
			return err
		}
		return q.RefreshToken.WithContext(ctx).Create(&dbmodel.RefreshToken{
			UserID:    old.UserID,
			TokenHash: newHash,
			ExpiresAt: newExpiresAt,
		})
	})
}

// RevokeAllRefreshTokens 吊销该用户全部 refresh token（直接删除）
//
// 用于重用检测（强制该用户重新登录）与改密场景；删除后旧哈希不可再命中，
// 后续携带只会得到 ErrRefreshNotFound → Unauthenticated。
//
// 参数：
//   - ctx: 上下文
//   - userID: 用户 ID
//
// 返回：
//   - error: 删除失败返回数据库错误
func (s *Store) RevokeAllRefreshTokens(ctx context.Context, userID int64) error {
	_, err := s.q.RefreshToken.WithContext(ctx).
		Where(s.q.RefreshToken.UserID.Eq(userID)).Delete()
	return err
}

// deleteExpired 惰性清理该用户已过期的 refresh 记录（含已作废行）
//
// 参数：
//   - ctx: 上下文
//   - q: 查询入口（事务内传入事务绑定的 Query）
//   - userID: 用户 ID
//
// 返回：
//   - error: 删除失败返回数据库错误
func deleteExpired(ctx context.Context, q *dbquery.Query, userID int64) error {
	_, err := q.RefreshToken.WithContext(ctx).
		Where(
			q.RefreshToken.UserID.Eq(userID),
			q.RefreshToken.ExpiresAt.Lte(time.Now()),
		).Delete()
	return err
}
