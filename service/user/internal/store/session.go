package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_user"
	dbquery "github.com/gangantongxue/GanRAG/pkg/db/query/ganrag_user"
	"github.com/gangantongxue/GanRAG/pkg/eid"
)

// UpsertSession 登录建会话（每设备一行，同设备重登 upsert），并顺带清理该用户已过期会话
//
// (user_id, device_id) 命中已有行则覆盖旧会话（token_hash 换新、revoked_at 清空，
// 同设备重登复活被踢会话），未命中才插入；单语句 upsert 免并发同设备登录撞唯一索引。
// 过期清理为惰性策略：登录 / 刷新时执行，无需定时任务。
// 会话行的业务主键由 pkg/eid 生成（ses_<xid> 全串，设备管理接口对外用；upsert 不覆盖已有 xid）。
//
// 参数：
//   - ctx: 上下文
//   - userID: 归属用户自增 ID
//   - deviceID: 设备码（合法则沿用请求值，否则服务端生成 dev_<xid>；口径见 data-model.md）
//   - deviceInfo: 设备信息（可空，设备列表展示）
//   - tokenHash: refresh token 明文的 SHA-256 hex
//   - expiresAt: 会话过期时间（当前时间 + refresh_ttl）
//
// 返回：
//   - error: 写入失败返回数据库错误
func (s *Store) UpsertSession(ctx context.Context, userID int64, deviceID, deviceInfo, tokenHash string, expiresAt time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := dbquery.Use(tx)
		if err := deleteExpiredSessions(ctx, q, userID); err != nil {
			return err
		}
		now := time.Now()
		return q.Session.WithContext(ctx).
			Clauses(clause.OnConflict{
				// MySQL 落地为 ON DUPLICATE KEY UPDATE（唯一键 uk_sessions_user_device）
				Columns: []clause.Column{{Name: "user_id"}, {Name: "device_id"}},
				DoUpdates: clause.Assignments(map[string]any{
					"device_info":    deviceInfo,
					"token_hash":     tokenHash,
					"expires_at":     expiresAt,
					"revoked_at":     nil,
					"last_active_at": now,
				}),
			}).
			Create(&dbmodel.Session{
				Xid:          eid.New(eid.Session),
				UserID:       userID,
				DeviceID:     deviceID,
				DeviceInfo:   deviceInfo,
				TokenHash:    tokenHash,
				ExpiresAt:    expiresAt,
				LastActiveAt: now,
				CreatedAt:    now,
			})
	})
}

// GetSessionByTokenHash 按哈希查询登录会话
//
// 返回包含已吊销（RevokedAt 非空）与已过期的行，具体状态由调用方判定。
// 不做重用检测：轮换原地更新 token_hash，旧 token 哈希不再命中即按无效拒绝
// （最简语义，见 data-model.md「sessions 登录会话」）。
//
// 参数：
//   - ctx: 上下文
//   - tokenHash: refresh token 明文的 SHA-256 hex
//
// 返回：
//   - *dbmodel.Session: 查询到的会话
//   - error: 未命中返回 ErrSessionNotFound
func (s *Store) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*dbmodel.Session, error) {
	sess, err := s.q.Session.WithContext(ctx).
		Where(s.q.Session.TokenHash.Eq(tokenHash)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// RotateSession 轮换 refresh token：原地更新会话行的 token_hash，不增行
//
// 条件更新（token_hash 仍为旧值）保证并发轮换只生效一次，
// 影响行数为 0 说明已被并发轮换/吊销，按 token 无效拒绝。
//
// 参数：
//   - ctx: 上下文
//   - old: 旧会话行（取 ID、UserID 与旧 TokenHash）
//   - newHash: 新 token 哈希
//   - newExpiresAt: 新过期时间
//
// 返回：
//   - error: 并发轮换/已吊销返回 ErrSessionNotFound（对外按无效拒绝），其余为数据库错误
func (s *Store) RotateSession(ctx context.Context, old *dbmodel.Session, newHash string, newExpiresAt time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := dbquery.Use(tx)

		info, err := q.Session.WithContext(ctx).
			Where(q.Session.ID.Eq(old.ID), q.Session.TokenHash.Eq(old.TokenHash)).
			Updates(map[string]any{
				"token_hash":     newHash,
				"expires_at":     newExpiresAt,
				"last_active_at": time.Now(),
			})
		if err != nil {
			return err
		}
		if info.RowsAffected == 0 {
			return ErrSessionNotFound
		}

		// 顺带清理该用户已过期会话（惰性清理）
		return deleteExpiredSessions(ctx, q, old.UserID)
	})
}

// DeleteUserSessions 删除该用户全部登录会话（改密 / 找回密码场景）
//
// 直接删除行：会话不复用，重新登录创建新行。
//
// 参数：
//   - ctx: 上下文
//   - q: 查询入口（事务内传入事务绑定的 Query）
//   - userID: 用户自增 ID
//
// 返回：
//   - error: 删除失败返回数据库错误
func DeleteUserSessions(ctx context.Context, q *dbquery.Query, userID int64) error {
	_, err := q.Session.WithContext(ctx).Where(q.Session.UserID.Eq(userID)).Delete()
	return err
}

// deleteExpiredSessions 惰性清理该用户已过期的会话行
//
// 参数：
//   - ctx: 上下文
//   - q: 查询入口（事务内传入事务绑定的 Query）
//   - userID: 用户自增 ID
//
// 返回：
//   - error: 删除失败返回数据库错误
func deleteExpiredSessions(ctx context.Context, q *dbquery.Query, userID int64) error {
	if userID == 0 {
		return nil
	}
	_, err := q.Session.WithContext(ctx).
		Where(
			q.Session.UserID.Eq(userID),
			q.Session.ExpiresAt.Lte(time.Now()),
		).Delete()
	return err
}
