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
	"github.com/gangantongxue/GanRAG/pkg/eid"
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
	if err := db.AutoMigrate(&dbmodel.User{}, &dbmodel.Session{}, &dbmodel.UserFollow{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	return New(db), db
}

// mustCreateUser 创建测试用户并返回其 ID（email 用 name 拼装，展示名同名）
func mustCreateUser(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	u, err := s.CreateUser(context.Background(), name+"@example.com", name, "hash-"+name)
	if err != nil {
		t.Fatalf("CreateUser(%s) 错误: %v", name, err)
	}
	return u.ID
}

// TestCreateUser 验证创建用户、xid 生成与邮箱唯一性
func TestCreateUser(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	u, err := s.CreateUser(ctx, "alice@example.com", "alice", "hash-alice")
	if err != nil {
		t.Fatalf("CreateUser() 错误: %v", err)
	}
	if u.ID <= 0 {
		t.Errorf("ID = %d，期望自增主键 > 0", u.ID)
	}
	if !eid.Match(u.Xid, eid.User) {
		t.Errorf("Xid = %q，期望 usr_ 前缀业务主键", u.Xid)
	}
	if u.Status != StatusNormal {
		t.Errorf("Status = %d，期望 %d", u.Status, StatusNormal)
	}
	if u.CreatedAt.IsZero() || u.UpdatedAt.IsZero() {
		t.Error("创建/更新时间不应为零值")
	}

	// 重复邮箱
	if _, err := s.CreateUser(ctx, "alice@example.com", "alice2", "hash-2"); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("重复注册错误 = %v，期望 ErrEmailTaken", err)
	}
}

// TestGetUser 验证按 ID / xid / 邮箱查询与未命中映射
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

	byXID, err := s.GetUserByXID(ctx, byID.Xid)
	if err != nil {
		t.Fatalf("GetUserByXID() 错误: %v", err)
	}
	if byXID.ID != id {
		t.Errorf("ID = %d，期望 %d", byXID.ID, id)
	}

	byEmail, err := s.GetUserByEmail(ctx, "bob@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail() 错误: %v", err)
	}
	if byEmail.ID != id {
		t.Errorf("ID = %d，期望 %d", byEmail.ID, id)
	}

	if _, err := s.GetUserByID(ctx, 99999); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("不存在 ID 错误 = %v，期望 ErrUserNotFound", err)
	}
	if _, err := s.GetUserByXID(ctx, eid.New(eid.User)); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("不存在 xid 错误 = %v，期望 ErrUserNotFound", err)
	}
	if _, err := s.GetUserByEmail(ctx, "ghost@example.com"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("不存在邮箱错误 = %v，期望 ErrUserNotFound", err)
	}
}

// TestUpdatePassword 验证改密事务：更新哈希 + 删除全部会话
func TestUpdatePassword(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	id := mustCreateUser(t, s, "carol")

	// 两个设备的活跃会话
	if err := s.CreateSession(ctx, id, "dev-1", "", "hash-rt-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateSession() 错误: %v", err)
	}
	if err := s.CreateSession(ctx, id, "dev-2", "", "hash-rt-2", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateSession() 错误: %v", err)
	}

	if err := s.UpdatePassword(ctx, id, "new-hash"); err != nil {
		t.Fatalf("UpdatePassword() 错误: %v", err)
	}

	u, err := s.GetUserByEmail(ctx, "carol@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail() 错误: %v", err)
	}
	if u.PasswordHash != "new-hash" {
		t.Errorf("PasswordHash = %q，期望 new-hash", u.PasswordHash)
	}
	for _, h := range []string{"hash-rt-1", "hash-rt-2"} {
		if _, err := s.GetSessionByTokenHash(ctx, h); !errors.Is(err, ErrSessionNotFound) {
			t.Errorf("改密后会话 %s 错误 = %v，期望已删除(ErrSessionNotFound)", h, err)
		}
	}

	// 用户不存在
	if err := s.UpdatePassword(ctx, 99999, "x"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("不存在用户改密错误 = %v，期望 ErrUserNotFound", err)
	}
}

// TestCreateSession_CleansExpired 验证惰性清理：建会话时删除该用户过期行
func TestCreateSession_CleansExpired(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	id := mustCreateUser(t, s, "dave")

	// 直接插入一条已过期会话
	if err := db.Create(&dbmodel.Session{
		Xid: eid.New(eid.Session), UserID: id, DeviceID: "dev-old",
		TokenHash: "expired-hash", ExpiresAt: time.Now().Add(-time.Hour),
	}).Error; err != nil {
		t.Fatalf("插入过期会话失败: %v", err)
	}

	if err := s.CreateSession(ctx, id, "dev-fresh", "", "fresh-hash", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateSession() 错误: %v", err)
	}

	if _, err := s.GetSessionByTokenHash(ctx, "expired-hash"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("过期会话错误 = %v，期望被惰性清理", err)
	}
	if _, err := s.GetSessionByTokenHash(ctx, "fresh-hash"); err != nil {
		t.Errorf("新会话应存在: %v", err)
	}
}

// TestRotateSession 验证轮换原地更新 token_hash、不增行、旧 hash 失效
func TestRotateSession(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	id := mustCreateUser(t, s, "erin")

	if err := s.CreateSession(ctx, id, "dev-1", "", "old-hash", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateSession() 错误: %v", err)
	}
	old, err := s.GetSessionByTokenHash(ctx, "old-hash")
	if err != nil {
		t.Fatalf("GetSessionByTokenHash() 错误: %v", err)
	}

	// 轮换：原地更新 token_hash
	if err := s.RotateSession(ctx, old, "new-hash", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("RotateSession() 错误: %v", err)
	}
	if _, err := s.GetSessionByTokenHash(ctx, "old-hash"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("旧 hash 错误 = %v，期望不再命中（按无效拒绝）", err)
	}
	if _, err := s.GetSessionByTokenHash(ctx, "new-hash"); err != nil {
		t.Errorf("新 hash 应命中: %v", err)
	}
	// 不增行：仍是单设备单行
	var n int64
	if err := db.Model(&dbmodel.Session{}).Where("user_id = ?", id).Count(&n).Error; err != nil {
		t.Fatalf("统计会话行失败: %v", err)
	}
	if n != 1 {
		t.Errorf("会话行数 = %d，期望 1（轮换不增行）", n)
	}

	// 旧会话行（token_hash 已变）再轮换：条件更新不命中，按无效拒绝
	if err := s.RotateSession(ctx, old, "another-hash", time.Now().Add(time.Hour)); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("陈旧行二次轮换错误 = %v，期望 ErrSessionNotFound", err)
	}
}

// TestDeleteUserSessions 验证只删目标用户会话
func TestDeleteUserSessions(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	id := mustCreateUser(t, s, "frank")
	other := mustCreateUser(t, s, "grace")

	for _, h := range []string{"rt-1", "rt-2"} {
		if err := s.CreateSession(ctx, id, eid.New(eid.Device), "", h, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("CreateSession() 错误: %v", err)
		}
	}
	if err := s.CreateSession(ctx, other, eid.New(eid.Device), "", "rt-other", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateSession() 错误: %v", err)
	}

	if err := DeleteUserSessions(ctx, s.q, id); err != nil {
		t.Fatalf("DeleteUserSessions() 错误: %v", err)
	}
	for _, h := range []string{"rt-1", "rt-2"} {
		if _, err := s.GetSessionByTokenHash(ctx, h); !errors.Is(err, ErrSessionNotFound) {
			t.Errorf("会话 %s 错误 = %v，期望已删除", h, err)
		}
	}
	// 其他用户的会话不受影响
	if _, err := s.GetSessionByTokenHash(ctx, "rt-other"); err != nil {
		t.Errorf("不应影响其他用户: %v", err)
	}
}
