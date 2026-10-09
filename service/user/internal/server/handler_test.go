package server

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	userv1 "github.com/gangantongxue/GanRAG/api/gen/user/v1"
	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_user"
	"github.com/gangantongxue/GanRAG/service/user/internal/auth"
	"github.com/gangantongxue/GanRAG/service/user/internal/config"
	"github.com/gangantongxue/GanRAG/service/user/internal/store"
)

// 测试用 JWT 配置常量
const (
	testJWTSecret  = "test-secret"
	testJWTIssuer  = "ganrag-test"
	testAccessTTL  = 15 * time.Minute
	testRefreshTTL = 168 * time.Hour
)

// newTestHandler 构造挂载 SQLite 的 handler，并返回底层连接供用例直接改数据
//
// 表结构由 AutoMigrate 按生成模型创建，测试无需外部 MySQL（go test 保持自治）。
//
// 返回：
//   - *Handler: 待测 handler
//   - *gorm.DB: 底层连接（直接 SQL 改状态 / 断言用）
func newTestHandler(t *testing.T) (*Handler, *gorm.DB) {
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

	jwtCfg := config.JWTConfig{
		Secret:     testJWTSecret,
		Issuer:     testJWTIssuer,
		AccessTTL:  testAccessTTL,
		RefreshTTL: testRefreshTTL,
	}
	// 日志静默，避免测试输出刷屏
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewHandler(store.New(db), jwtCfg, log), db
}

// mustRegister 注册用户，失败则终止测试
func mustRegister(t *testing.T, h *Handler, username, password string) int64 {
	t.Helper()
	resp, err := h.Register(context.Background(), &userv1.RegisterRequest{Username: username, Password: password})
	if err != nil {
		t.Fatalf("Register(%s) 错误: %v", username, err)
	}
	return resp.User.Id
}

// mustLogin 登录并返回响应，失败则终止测试
func mustLogin(t *testing.T, h *Handler, username, password string) *userv1.LoginResponse {
	t.Helper()
	resp, err := h.Login(context.Background(), &userv1.LoginRequest{Username: username, Password: password})
	if err != nil {
		t.Fatalf("Login(%s) 错误: %v", username, err)
	}
	return resp
}

// assertCode 断言 gRPC 错误码
func assertCode(t *testing.T, err error, want codes.Code, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: 期望错误 %v，实际成功", what, want)
	}
	if got := status.Code(err); got != want {
		t.Errorf("%s: 错误码 = %v，期望 %v（err=%v）", what, got, want, err)
	}
}

// TestRegister 校验注册：合法通过、参数校验、用户名冲突
func TestRegister(t *testing.T) {
	h, _ := newTestHandler(t)
	ctx := context.Background()

	// 正常注册：不返回 token，昵称默认等于用户名
	resp, err := h.Register(ctx, &userv1.RegisterRequest{Username: "alice_01", Password: "password123"})
	if err != nil {
		t.Fatalf("Register() 错误: %v", err)
	}
	if resp.User == nil || resp.User.Id <= 0 {
		t.Fatalf("应返回新用户: %+v", resp.User)
	}
	if resp.User.Username != "alice_01" || resp.User.Nickname != "alice_01" {
		t.Errorf("username/nickname = %q/%q", resp.User.Username, resp.User.Nickname)
	}
	if resp.User.Status != 1 {
		t.Errorf("status = %d，期望 1", resp.User.Status)
	}
	// 回归：带 default:CURRENT_TIMESTAMP 的列创建后必须回填时间（曾返回零值）
	if resp.User.CreatedAtUnix <= 0 || resp.User.UpdatedAtUnix <= 0 {
		t.Errorf("createdAtUnix/updatedAtUnix = %d/%d，期望 > 0", resp.User.CreatedAtUnix, resp.User.UpdatedAtUnix)
	}

	// 用户名冲突
	_, err = h.Register(ctx, &userv1.RegisterRequest{Username: "alice_01", Password: "password123"})
	assertCode(t, err, codes.AlreadyExists, "重复用户名")

	// 用户名不合法：过短、含非法字符
	for _, name := range []string{"ab", "bad name!"} {
		_, err := h.Register(ctx, &userv1.RegisterRequest{Username: name, Password: "password123"})
		assertCode(t, err, codes.InvalidArgument, "非法用户名 "+name)
	}
	// 恰好 32 位应合法，33 位应拒绝
	if _, err := h.Register(ctx, &userv1.RegisterRequest{Username: "aaaaaaaaaa_bbbbccccddddeeeeffffg", Password: "password123"}); err != nil {
		t.Fatalf("32 位用户名应合法: %v", err)
	}
	if _, err := h.Register(ctx, &userv1.RegisterRequest{Username: "aaaaaaaaaa_bbbbccccddddeeeeffffgg", Password: "password123"}); err == nil {
		t.Fatal("33 位用户名应被拒绝")
	}

	// 密码不合法：过短、过长（按字符数 8-64）
	for _, pw := range []string{"short1", strings.Repeat("a", 65)} {
		_, err := h.Register(ctx, &userv1.RegisterRequest{Username: "weird_user", Password: pw})
		assertCode(t, err, codes.InvalidArgument, "非法密码")
	}
}

// TestLogin 校验登录：成功签发双 token、凭证错误统一 Unauthenticated、禁用拒绝
func TestLogin(t *testing.T) {
	h, db := newTestHandler(t)
	ctx := context.Background()
	mustRegister(t, h, "bob", "password123")

	// 用户不存在与密码错误统一 Unauthenticated（防用户名枚举）
	_, err := h.Login(ctx, &userv1.LoginRequest{Username: "ghost", Password: "password123"})
	assertCode(t, err, codes.Unauthenticated, "不存在的用户")
	_, err = h.Login(ctx, &userv1.LoginRequest{Username: "bob", Password: "wrong-pass-1"})
	assertCode(t, err, codes.Unauthenticated, "密码错误")

	// 空参数
	_, err = h.Login(ctx, &userv1.LoginRequest{Username: "", Password: ""})
	assertCode(t, err, codes.InvalidArgument, "空参数")

	// 正常登录：返回双 token 与有效期
	resp := mustLogin(t, h, "bob", "password123")
	if resp.AccessToken == "" || resp.RefreshToken == "" {
		t.Fatal("登录应返回 access 与 refresh token")
	}
	if resp.AccessExpiresIn != int64(testAccessTTL.Seconds()) || resp.RefreshExpiresIn != int64(testRefreshTTL.Seconds()) {
		t.Errorf("expires_in = %d/%d，期望 %d/%d",
			resp.AccessExpiresIn, resp.RefreshExpiresIn, int64(testAccessTTL.Seconds()), int64(testRefreshTTL.Seconds()))
	}
	// access token 可用共享密钥验签（Gateway 契约）
	parsed, err := jwt.Parse(resp.AccessToken, func(t *jwt.Token) (any, error) {
		return []byte(testJWTSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !parsed.Valid {
		t.Fatalf("access token 验签失败: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if sub, _ := claims.GetSubject(); sub != strconv.FormatInt(resp.User.Id, 10) {
		t.Errorf("sub = %q，期望 %q", sub, strconv.FormatInt(resp.User.Id, 10))
	}
	if claims["username"] != "bob" {
		t.Errorf("username 声明 = %v，期望 bob", claims["username"])
	}
	// refresh 只存哈希：库中查不到明文
	var count int64
	db.Model(&dbmodel.RefreshToken{}).Where("token_hash = ?", auth.HashRefreshToken(resp.RefreshToken)).Count(&count)
	if count != 1 {
		t.Errorf("库中应存 refresh 哈希 1 条，实际 %d", count)
	}

	// 禁用后登录拒绝
	if err := db.Exec("UPDATE users SET status = 0 WHERE username = ?", "bob").Error; err != nil {
		t.Fatalf("禁用用户失败: %v", err)
	}
	_, err = h.Login(ctx, &userv1.LoginRequest{Username: "bob", Password: "password123"})
	assertCode(t, err, codes.FailedPrecondition, "禁用用户登录")
}

// TestRefresh 校验刷新：轮换、重用检测吊销全部、过期拒绝
func TestRefresh(t *testing.T) {
	h, _ := newTestHandler(t)
	ctx := context.Background()
	userID := mustRegister(t, h, "carol", "password123")
	login := mustLogin(t, h, "carol", "password123")

	// 正常轮换：返回新对，且与旧对不同
	resp1, err := h.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: login.RefreshToken})
	if err != nil {
		t.Fatalf("Refresh() 错误: %v", err)
	}
	if resp1.RefreshToken == login.RefreshToken {
		t.Error("轮换应签发全新的 refresh token")
	}
	// 注：access token 载荷时间精确到秒，同秒内两次签发的 JWT 字符串相同属正常

	// 重用旧 refresh（已作废）：Unauthenticated，且触发吊销该用户全部
	_, err = h.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: login.RefreshToken})
	assertCode(t, err, codes.Unauthenticated, "重用已作废 token")

	// 全部吊销验证：刚轮换出的新 token 也不可用
	_, err = h.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: resp1.RefreshToken})
	assertCode(t, err, codes.Unauthenticated, "吊销后的新 token")

	// 伪造 token
	_, err = h.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: "forged-token"})
	assertCode(t, err, codes.Unauthenticated, "伪造 token")

	// 空参数
	_, err = h.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: ""})
	assertCode(t, err, codes.InvalidArgument, "空 token")

	// 自然过期：写入已过期记录（绕过轮换），刷新拒绝
	if err := h.st.CreateRefreshToken(ctx, userID, auth.HashRefreshToken("expired-rt"), time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("写入过期 token 失败: %v", err)
	}
	_, err = h.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: "expired-rt"})
	assertCode(t, err, codes.Unauthenticated, "过期 token")
}

// TestGetUser 校验查询用户：参数、不存在、正常返回
func TestGetUser(t *testing.T) {
	h, _ := newTestHandler(t)
	ctx := context.Background()
	id := mustRegister(t, h, "dave", "password123")

	_, err := h.GetUser(ctx, &userv1.GetUserRequest{Id: 0})
	assertCode(t, err, codes.InvalidArgument, "缺少 ID")

	_, err = h.GetUser(ctx, &userv1.GetUserRequest{Id: 99999})
	assertCode(t, err, codes.NotFound, "不存在的用户")

	resp, err := h.GetUser(ctx, &userv1.GetUserRequest{Id: id})
	if err != nil {
		t.Fatalf("GetUser() 错误: %v", err)
	}
	if resp.User.Username != "dave" || resp.User.Id != id {
		t.Errorf("返回用户 = %+v", resp.User)
	}
	if resp.User.CreatedAtUnix <= 0 || resp.User.UpdatedAtUnix <= 0 {
		t.Error("时间戳不应为 0")
	}
}

// TestUpdatePassword 校验改密：参数、旧密码错误、成功后旧 token 失效且旧密码作废
func TestUpdatePassword(t *testing.T) {
	h, _ := newTestHandler(t)
	ctx := context.Background()
	id := mustRegister(t, h, "erin", "old-pass-123")
	login := mustLogin(t, h, "erin", "old-pass-123")

	// user_id 缺失 / 新密码不合法
	_, err := h.UpdatePassword(ctx, &userv1.UpdatePasswordRequest{UserId: 0, OldPassword: "old-pass-123", NewPassword: "new-pass-123"})
	assertCode(t, err, codes.InvalidArgument, "缺少 user_id")
	_, err = h.UpdatePassword(ctx, &userv1.UpdatePasswordRequest{UserId: id, OldPassword: "old-pass-123", NewPassword: "short"})
	assertCode(t, err, codes.InvalidArgument, "新密码过短")

	// 用户不存在（user_id 来自 token，失效说明凭证陈旧）
	_, err = h.UpdatePassword(ctx, &userv1.UpdatePasswordRequest{UserId: 99999, OldPassword: "old-pass-123", NewPassword: "new-pass-123"})
	assertCode(t, err, codes.Unauthenticated, "用户不存在")

	// 旧密码错误
	_, err = h.UpdatePassword(ctx, &userv1.UpdatePasswordRequest{UserId: id, OldPassword: "wrong-pass-1", NewPassword: "new-pass-123"})
	assertCode(t, err, codes.Unauthenticated, "旧密码错误")

	// 成功改密
	_, err = h.UpdatePassword(ctx, &userv1.UpdatePasswordRequest{UserId: id, OldPassword: "old-pass-123", NewPassword: "new-pass-123"})
	if err != nil {
		t.Fatalf("UpdatePassword() 错误: %v", err)
	}

	// 改密后：全部 refresh 失效
	_, err = h.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: login.RefreshToken})
	assertCode(t, err, codes.Unauthenticated, "改密后的旧 refresh")

	// 旧密码不可登录，新密码可登录
	_, err = h.Login(ctx, &userv1.LoginRequest{Username: "erin", Password: "old-pass-123"})
	assertCode(t, err, codes.Unauthenticated, "旧密码登录")
	if resp := mustLogin(t, h, "erin", "new-pass-123"); resp.User.Id != id {
		t.Errorf("新密码登录返回用户 = %+v", resp.User)
	}
}
