package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_user"
)

// newTestStore 用 SQLite 临时文件库构造测试 Store
//
// 表结构由 AutoMigrate 按生成模型创建（MySQL 侧 DDL 走 pkg/db 迁移，
// 此处只验证查询与事务逻辑，保持 go test 无需外部依赖）。
//
// 返回：
//   - *Store: 数据访问层实例
//   - *gorm.DB: 底层连接（用例做直接断言）
func newTestStore(t *testing.T) (*Store, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{
		TranslateError: true,
		// 测试静默 SQL 日志（record not found 等为预期分支）
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("打开 SQLite 失败: %v", err)
	}
	if err := db.AutoMigrate(&dbmodel.User{}, &dbmodel.RefreshToken{}, &dbmodel.UserFollow{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	return New(db), db
}

// mustCreateUser 创建测试用户并返回其 ID
func mustCreateUser(t *testing.T, s *Store, username string) int64 {
	t.Helper()
	u, err := s.CreateUser(context.Background(), username, "hash-"+username, username)
	if err != nil {
		t.Fatalf("CreateUser(%s) 错误: %v", username, err)
	}
	return u.ID
}

// TestCreateUser 验证创建用户与用户名唯一性
func TestCreateUser(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	u, err := s.CreateUser(ctx, "alice", "hash-alice", "alice")
	if err != nil {
		t.Fatalf("CreateUser() 错误: %v", err)
	}
	if u.ID <= 0 {
		t.Errorf("ID = %d，期望自增主键 > 0", u.ID)
	}
	if u.Status != StatusNormal {
		t.Errorf("Status = %d，期望 %d", u.Status, StatusNormal)
	}
	if u.CreatedAt.IsZero() || u.UpdatedAt.IsZero() {
		t.Error("创建/更新时间不应为零值")
	}

	// 重复用户名
	if _, err := s.CreateUser(ctx, "alice", "hash-2", "alice"); !errors.Is(err, ErrUsernameTaken) {
		t.Errorf("重复注册错误 = %v，期望 ErrUsernameTaken", err)
	}
}

// TestGetUser 验证按 ID / 用户名查询与未命中映射
func TestGetUser(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	id := mustCreateUser(t, s, "bob")

	byID, err := s.GetUserByID(ctx, id)
	if err != nil {
		t.Fatalf("GetUserByID() 错误: %v", err)
	}
	if byID.Username != "bob" {
		t.Errorf("Username = %q，期望 bob", byID.Username)
	}

	byName, err := s.GetUserByUsername(ctx, "bob")
	if err != nil {
		t.Fatalf("GetUserByUsername() 错误: %v", err)
	}
	if byName.ID != id {
		t.Errorf("ID = %d，期望 %d", byName.ID, id)
	}

	if _, err := s.GetUserByID(ctx, 99999); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("不存在 ID 错误 = %v，期望 ErrUserNotFound", err)
	}
	if _, err := s.GetUserByUsername(ctx, "ghost"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("不存在用户名错误 = %v，期望 ErrUserNotFound", err)
	}
}

// TestUpdatePassword 验证改密事务：更新哈希 + 吊销全部 refresh token
func TestUpdatePassword(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	id := mustCreateUser(t, s, "carol")

	// 两个活跃 refresh token
	if err := s.CreateRefreshToken(ctx, id, "hash-rt-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateRefreshToken() 错误: %v", err)
	}
	if err := s.CreateRefreshToken(ctx, id, "hash-rt-2", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateRefreshToken() 错误: %v", err)
	}

	if err := s.UpdatePassword(ctx, id, "new-hash"); err != nil {
		t.Fatalf("UpdatePassword() 错误: %v", err)
	}

	u, err := s.GetUserByUsername(ctx, "carol")
	if err != nil {
		t.Fatalf("GetUserByUsername() 错误: %v", err)
	}
	if u.PasswordHash != "new-hash" {
		t.Errorf("PasswordHash = %q，期望 new-hash", u.PasswordHash)
	}
	for _, h := range []string{"hash-rt-1", "hash-rt-2"} {
		if _, err := s.GetRefreshToken(ctx, h); !errors.Is(err, ErrRefreshNotFound) {
			t.Errorf("改密后 token %s 错误 = %v，期望被吊销(ErrRefreshNotFound)", h, err)
		}
	}

	// 用户不存在
	if err := s.UpdatePassword(ctx, 99999, "x"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("不存在用户改密错误 = %v，期望 ErrUserNotFound", err)
	}
}

// TestCreateRefreshToken_CleansExpired 验证惰性清理：写入时删除该用户过期记录
func TestCreateRefreshToken_CleansExpired(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	id := mustCreateUser(t, s, "dave")

	// 直接插入一条已过期记录
	if err := db.Create(&dbmodel.RefreshToken{
		UserID: id, TokenHash: "expired-hash", ExpiresAt: time.Now().Add(-time.Hour),
	}).Error; err != nil {
		t.Fatalf("插入过期记录失败: %v", err)
	}

	if err := s.CreateRefreshToken(ctx, id, "fresh-hash", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateRefreshToken() 错误: %v", err)
	}

	if _, err := s.GetRefreshToken(ctx, "expired-hash"); !errors.Is(err, ErrRefreshNotFound) {
		t.Errorf("过期记录错误 = %v，期望被惰性清理", err)
	}
	if _, err := s.GetRefreshToken(ctx, "fresh-hash"); err != nil {
		t.Errorf("新记录应存在: %v", err)
	}
}

// TestRotateRefreshToken 验证轮换：作废旧记录 + 插入新记录 + 二次轮换检出
func TestRotateRefreshToken(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	id := mustCreateUser(t, s, "erin")

	if err := s.CreateRefreshToken(ctx, id, "old-hash", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateRefreshToken() 错误: %v", err)
	}
	old, err := s.GetRefreshToken(ctx, "old-hash")
	if err != nil {
		t.Fatalf("GetRefreshToken() 错误: %v", err)
	}
	if old.RevokedAt != nil {
		t.Fatal("新签发的 token 不应已作废")
	}

	// 轮换：旧的标记作废（保留行供重用检测），新的插入
	if err := s.RotateRefreshToken(ctx, old, "new-hash", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("RotateRefreshToken() 错误: %v", err)
	}
	revoked, err := s.GetRefreshToken(ctx, "old-hash")
	if err != nil {
		t.Fatalf("轮换后旧记录应仍可查（重用检测依赖）: %v", err)
	}
	if revoked.RevokedAt == nil {
		t.Error("旧记录 RevokedAt 应非空")
	}
	if _, err := s.GetRefreshToken(ctx, "new-hash"); err != nil {
		t.Errorf("新记录应存在: %v", err)
	}

	// 再次轮换同一旧记录：检出重用
	if err := s.RotateRefreshToken(ctx, old, "another-hash", time.Now().Add(time.Hour)); !errors.Is(err, ErrRefreshRevoked) {
		t.Errorf("二次轮换错误 = %v，期望 ErrRefreshRevoked", err)
	}
}

// TestRevokeAllRefreshTokens 验证吊销该用户全部 token（重用检测 / 改密场景）
func TestRevokeAllRefreshTokens(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	id := mustCreateUser(t, s, "frank")
	other := mustCreateUser(t, s, "grace")

	for _, h := range []string{"rt-1", "rt-2"} {
		if err := s.CreateRefreshToken(ctx, id, h, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("CreateRefreshToken() 错误: %v", err)
		}
	}
	if err := s.CreateRefreshToken(ctx, other, "rt-other", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateRefreshToken() 错误: %v", err)
	}

	if err := s.RevokeAllRefreshTokens(ctx, id); err != nil {
		t.Fatalf("RevokeAllRefreshTokens() 错误: %v", err)
	}
	for _, h := range []string{"rt-1", "rt-2"} {
		if _, err := s.GetRefreshToken(ctx, h); !errors.Is(err, ErrRefreshNotFound) {
			t.Errorf("token %s 错误 = %v，期望已吊销", h, err)
		}
	}
	// 其他用户的 token 不受影响
	if _, err := s.GetRefreshToken(ctx, "rt-other"); err != nil {
		t.Errorf("不应影响其他用户: %v", err)
	}
}
