package server

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/golang-jwt/jwt/v5"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	userv1 "github.com/gangantongxue/GanRAG/api/gen/user/v1"
	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_user"
	"github.com/gangantongxue/GanRAG/pkg/eid"
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
//   - *miniredis.Miniredis: 内存 Redis（快进时钟绕过 60s 发送限频）
func newTestHandler(t *testing.T) (*Handler, *gorm.DB, *miniredis.Miniredis) {
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
	// 对齐迁移 0002_sessions 的组合唯一索引（AutoMigrate 从模型标签推不出组合唯一索引）
	if err := db.Exec("CREATE UNIQUE INDEX uk_sessions_user_device ON sessions (user_id, device_id)").Error; err != nil {
		t.Fatalf("建 sessions 唯一索引失败: %v", err)
	}

	jwtCfg := config.JWTConfig{
		Secret:     testJWTSecret,
		Issuer:     testJWTIssuer,
		AccessTTL:  testAccessTTL,
		RefreshTTL: testRefreshTTL,
	}
	// 日志静默，避免测试输出刷屏
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// 验证码组件挂 miniredis、邮件通道走 dev 降级（写日志），测试不依赖外部 Redis / SMTP
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewHandler(store.New(db), jwtCfg, auth.NewEmailCode(rdb), auth.NewMailer(config.MailConfig{}, log), log), db, mr
}

// mustIssueCode 生成并落库邮箱验证码，返回明文码（测试直接取码，跳过邮件通道）
//
// 先快进 miniredis 时钟 61s 过期发送限频键，用例内可对同邮箱反复取码。
func mustIssueCode(t *testing.T, h *Handler, mr *miniredis.Miniredis, scene, email string) string {
	t.Helper()
	mr.FastForward(time.Minute + time.Second)
	code, err := h.codes.Issue(context.Background(), scene, email)
	if err != nil {
		t.Fatalf("Issue(%s, %s) 错误: %v", scene, email, err)
	}
	return code
}

// mustRegister 注册用户，失败则终止测试（自动先取邮箱验证码）
//
// 返回：
//   - *userv1.User: 注册返回的用户
func mustRegister(t *testing.T, h *Handler, mr *miniredis.Miniredis, email, username, password string) *userv1.User {
	t.Helper()
	code := mustIssueCode(t, h, mr, auth.SceneRegister, email)
	resp, err := h.Register(context.Background(), &userv1.RegisterRequest{
		Email: email, Code: code, Password: password, Username: username,
	})
	if err != nil {
		t.Fatalf("Register(%s) 错误: %v", email, err)
	}
	return resp.User
}

// mustLogin 登录并返回响应，失败则终止测试
func mustLogin(t *testing.T, h *Handler, email, password string) *userv1.LoginResponse {
	t.Helper()
	resp, err := h.Login(context.Background(), &userv1.LoginRequest{Email: email, Password: password})
	if err != nil {
		t.Fatalf("Login(%s) 错误: %v", email, err)
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

// TestRegister 校验注册：验证码前置、参数校验、邮箱冲突、xid 生成
func TestRegister(t *testing.T) {
	h, _, mr := newTestHandler(t)
	ctx := context.Background()

	// 无验证码：先验码的防枚举设计（探测者拿不到码无法触达唯一性判断）
	_, err := h.Register(ctx, &userv1.RegisterRequest{Email: "alice@example.com", Password: "password123"})
	assertCode(t, err, codes.Unauthenticated, "缺验证码")

	// 错误验证码
	_, err = h.Register(ctx, &userv1.RegisterRequest{Email: "alice@example.com", Code: "000000", Password: "password123"})
	assertCode(t, err, codes.Unauthenticated, "坏验证码")

	// 正常注册：不返回 token，xid 为对外标识
	code := mustIssueCode(t, h, mr, auth.SceneRegister, "alice@example.com")
	resp, err := h.Register(ctx, &userv1.RegisterRequest{Email: "alice@example.com", Code: code, Password: "password123", Username: "alice"})
	if err != nil {
		t.Fatalf("Register() 错误: %v", err)
	}
	if resp.User == nil {
		t.Fatal("应返回新用户")
	}
	if !eid.Match(resp.User.Xid, eid.User) {
		t.Errorf("xid = %q，期望 usr_ 前缀业务主键", resp.User.Xid)
	}
	if resp.User.Email != "alice@example.com" || resp.User.Username != "alice" {
		t.Errorf("email/username = %q/%q", resp.User.Email, resp.User.Username)
	}
	if resp.User.Status != 1 {
		t.Errorf("status = %d，期望 1", resp.User.Status)
	}
	// 回归：带 default:CURRENT_TIMESTAMP 的列创建后必须回填时间（曾返回零值）
	if resp.User.CreatedAtUnix <= 0 || resp.User.UpdatedAtUnix <= 0 {
		t.Errorf("createdAtUnix/updatedAtUnix = %d/%d，期望 > 0", resp.User.CreatedAtUnix, resp.User.UpdatedAtUnix)
	}

	// 邮箱冲突（持有效码才触达此分支）
	code = mustIssueCode(t, h, mr, auth.SceneRegister, "alice@example.com")
	_, err = h.Register(ctx, &userv1.RegisterRequest{Email: "alice@example.com", Code: code, Password: "password123"})
	assertCode(t, err, codes.AlreadyExists, "重复邮箱")

	// 展示名可空
	code = mustIssueCode(t, h, mr, auth.SceneRegister, "noname@example.com")
	if _, err := h.Register(ctx, &userv1.RegisterRequest{Email: "noname@example.com", Code: code, Password: "password123"}); err != nil {
		t.Fatalf("展示名可空应注册成功: %v", err)
	}

	// 展示名过长：65 字符拒绝、64 字符合法
	long65 := strings.Repeat("名", 65)
	code = mustIssueCode(t, h, mr, auth.SceneRegister, "long65@example.com")
	_, err = h.Register(ctx, &userv1.RegisterRequest{Email: "long65@example.com", Code: code, Password: "password123", Username: long65})
	assertCode(t, err, codes.InvalidArgument, "展示名 65 字符")
	code = mustIssueCode(t, h, mr, auth.SceneRegister, "long64@example.com")
	if _, err := h.Register(ctx, &userv1.RegisterRequest{Email: "long64@example.com", Code: code, Password: "password123", Username: strings.Repeat("名", 64)}); err != nil {
		t.Fatalf("展示名 64 字符应合法: %v", err)
	}

	// 邮箱不合法
	for _, mail := range []string{"", "bad-email", "a@b"} {
		_, err := h.Register(ctx, &userv1.RegisterRequest{Email: mail, Code: "123456", Password: "password123"})
		assertCode(t, err, codes.InvalidArgument, "非法邮箱 "+mail)
	}

	// 密码不合法：过短、过长（按字符数 8-64）
	for _, pw := range []string{"short1", strings.Repeat("a", 65)} {
		_, err := h.Register(ctx, &userv1.RegisterRequest{Email: "weird@example.com", Code: "123456", Password: pw})
		assertCode(t, err, codes.InvalidArgument, "非法密码")
	}
}

// TestLogin 校验登录：成功签发双 token、凭证错误统一 Unauthenticated、禁用拒绝
func TestLogin(t *testing.T) {
	h, db, mr := newTestHandler(t)
	ctx := context.Background()
	mustRegister(t, h, mr, "bob@example.com", "bob", "password123")

	// 邮箱不存在与密码错误统一 Unauthenticated（防账号枚举）
	_, err := h.Login(ctx, &userv1.LoginRequest{Email: "ghost@example.com", Password: "password123"})
	assertCode(t, err, codes.Unauthenticated, "不存在的用户")
	_, err = h.Login(ctx, &userv1.LoginRequest{Email: "bob@example.com", Password: "wrong-pass-1"})
	assertCode(t, err, codes.Unauthenticated, "密码错误")

	// 空参数
	_, err = h.Login(ctx, &userv1.LoginRequest{Email: "", Password: ""})
	assertCode(t, err, codes.InvalidArgument, "空参数")

	// 正常登录：返回双 token 与有效期
	resp := mustLogin(t, h, "bob@example.com", "password123")
	if resp.AccessToken == "" || resp.RefreshToken == "" {
		t.Fatal("登录应返回 access 与 refresh token")
	}
	if resp.AccessExpiresIn != int64(testAccessTTL.Seconds()) || resp.RefreshExpiresIn != int64(testRefreshTTL.Seconds()) {
		t.Errorf("expires_in = %d/%d，期望 %d/%d",
			resp.AccessExpiresIn, resp.RefreshExpiresIn, int64(testAccessTTL.Seconds()), int64(testRefreshTTL.Seconds()))
	}
	// access token 可用共享密钥验签（Gateway 契约），sub 为用户 xid 全串
	parsed, err := jwt.Parse(resp.AccessToken, func(t *jwt.Token) (any, error) {
		return []byte(testJWTSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !parsed.Valid {
		t.Fatalf("access token 验签失败: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if sub, _ := claims.GetSubject(); sub != resp.User.Xid {
		t.Errorf("sub = %q，期望 %q", sub, resp.User.Xid)
	}
	if claims["username"] != "bob" {
		t.Errorf("username 声明 = %v，期望 bob", claims["username"])
	}
	// refresh 只存哈希：库中查不到明文
	var count int64
	db.Model(&dbmodel.Session{}).Where("token_hash = ?", auth.HashRefreshToken(resp.RefreshToken)).Count(&count)
	if count != 1 {
		t.Errorf("库中应存 refresh 哈希 1 条，实际 %d", count)
	}

	// 禁用后登录拒绝
	if err := db.Exec("UPDATE users SET status = 0 WHERE email = ?", "bob@example.com").Error; err != nil {
		t.Fatalf("禁用用户失败: %v", err)
	}
	_, err = h.Login(ctx, &userv1.LoginRequest{Email: "bob@example.com", Password: "password123"})
	assertCode(t, err, codes.FailedPrecondition, "禁用用户登录")
}

// TestLogin_Device 校验设备 ID 方案 A：合法沿用、非法/空服务端生成、回填响应、device_info 截断
func TestLogin_Device(t *testing.T) {
	h, db, mr := newTestHandler(t)
	ctx := context.Background()
	mustRegister(t, h, mr, "grace@example.com", "grace", "password123")

	// 合法设备码沿用并回填响应
	resp, err := h.Login(ctx, &userv1.LoginRequest{
		Email: "grace@example.com", Password: "password123", DeviceId: "web_abc-123",
	})
	if err != nil {
		t.Fatalf("Login() 错误: %v", err)
	}
	if resp.DeviceId != "web_abc-123" {
		t.Errorf("DeviceId = %q，期望沿用 web_abc-123", resp.DeviceId)
	}

	// 非法设备码不报错，换服务端生成（dev_ 前缀）
	resp, err = h.Login(ctx, &userv1.LoginRequest{
		Email: "grace@example.com", Password: "password123", DeviceId: "bad id!",
	})
	if err != nil {
		t.Fatalf("Login() 错误: %v", err)
	}
	if !strings.HasPrefix(resp.DeviceId, "dev_") {
		t.Errorf("非法设备码后 DeviceId = %q，期望服务端生成 dev_ 前缀", resp.DeviceId)
	}

	// 空设备码同样生成
	resp, err = h.Login(ctx, &userv1.LoginRequest{Email: "grace@example.com", Password: "password123"})
	if err != nil {
		t.Fatalf("Login() 错误: %v", err)
	}
	if !strings.HasPrefix(resp.DeviceId, "dev_") {
		t.Errorf("空设备码后 DeviceId = %q，期望服务端生成 dev_ 前缀", resp.DeviceId)
	}

	// device_info 超长截断到 255 字符
	resp, err = h.Login(ctx, &userv1.LoginRequest{
		Email: "grace@example.com", Password: "password123",
		DeviceId: "web_abc-123", DeviceInfo: strings.Repeat("长", 300),
	})
	if err != nil {
		t.Fatalf("Login() 错误: %v", err)
	}
	var info string
	if err := db.Raw("SELECT device_info FROM sessions WHERE device_id = ?",
		resp.DeviceId).Scan(&info).Error; err != nil {
		t.Fatalf("查询 device_info 失败: %v", err)
	}
	if n := utf8.RuneCountInString(info); n != 255 {
		t.Errorf("device_info 长度 = %d，期望 255", n)
	}
}

// TestRefresh 校验刷新：原地轮换、旧 token 复用按无效拒绝、过期拒绝
func TestRefresh(t *testing.T) {
	h, _, mr := newTestHandler(t)
	ctx := context.Background()
	mustRegister(t, h, mr, "carol@example.com", "carol", "password123")
	login := mustLogin(t, h, "carol@example.com", "password123")

	// 正常轮换：返回新对，且与旧对不同
	resp1, err := h.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: login.RefreshToken})
	if err != nil {
		t.Fatalf("Refresh() 错误: %v", err)
	}
	if resp1.RefreshToken == login.RefreshToken {
		t.Error("轮换应签发全新的 refresh token")
	}
	// 注：access token 载荷时间精确到秒，同秒内两次签发的 JWT 字符串相同属正常

	// 旧 refresh 复用（原地轮换后哈希不命中）：按无效拒绝，不做重用检测
	_, err = h.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: login.RefreshToken})
	assertCode(t, err, codes.Unauthenticated, "重用旧 token")

	// 轮换出的新 token 不受影响（区别于旧「吊销全部」语义）
	if _, err := h.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: resp1.RefreshToken}); err != nil {
		t.Errorf("轮换后的新 token 应可用: %v", err)
	}

	// 伪造 token
	_, err = h.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: "forged-token"})
	assertCode(t, err, codes.Unauthenticated, "伪造 token")

	// 空参数
	_, err = h.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: ""})
	assertCode(t, err, codes.InvalidArgument, "空 token")

	// 自然过期：写入已过期会话（绕过轮换），刷新拒绝
	if err := h.st.UpsertSession(ctx, 1, eid.New(eid.Device), "",
		auth.HashRefreshToken("expired-rt"), time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("写入过期会话失败: %v", err)
	}
	_, err = h.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: "expired-rt"})
	assertCode(t, err, codes.Unauthenticated, "过期 token")
}

// TestGetUser 校验查询用户：xid 校验、不存在、正常返回
func TestGetUser(t *testing.T) {
	h, _, mr := newTestHandler(t)
	ctx := context.Background()
	u := mustRegister(t, h, mr, "dave@example.com", "dave", "password123")

	// xid 缺失 / 非法 / 跨实体串用
	for _, x := range []string{"", "not-a-xid", eid.New(eid.Session)} {
		_, err := h.GetUser(ctx, &userv1.GetUserRequest{Xid: x})
		assertCode(t, err, codes.InvalidArgument, "非法 xid "+x)
	}

	// 合法但不存在
	_, err := h.GetUser(ctx, &userv1.GetUserRequest{Xid: eid.New(eid.User)})
	assertCode(t, err, codes.NotFound, "不存在的用户")

	resp, err := h.GetUser(ctx, &userv1.GetUserRequest{Xid: u.Xid})
	if err != nil {
		t.Fatalf("GetUser() 错误: %v", err)
	}
	if resp.User.Email != "dave@example.com" || resp.User.Xid != u.Xid {
		t.Errorf("返回用户 = %+v", resp.User)
	}
	if resp.User.CreatedAtUnix <= 0 || resp.User.UpdatedAtUnix <= 0 {
		t.Error("时间戳不应为 0")
	}
}

// TestUpdatePassword 校验改密：参数、旧密码错误、成功后会话全删且旧密码作废
func TestUpdatePassword(t *testing.T) {
	h, _, mr := newTestHandler(t)
	ctx := context.Background()
	u := mustRegister(t, h, mr, "erin@example.com", "erin", "old-pass-123")
	login := mustLogin(t, h, "erin@example.com", "old-pass-123")

	// xid 缺失 / 新密码不合法
	_, err := h.UpdatePassword(ctx, &userv1.UpdatePasswordRequest{UserXid: "", OldPassword: "old-pass-123", NewPassword: "new-pass-123"})
	assertCode(t, err, codes.InvalidArgument, "缺少 user_xid")
	_, err = h.UpdatePassword(ctx, &userv1.UpdatePasswordRequest{UserXid: u.Xid, OldPassword: "old-pass-123", NewPassword: "short"})
	assertCode(t, err, codes.InvalidArgument, "新密码过短")

	// 用户不存在（user_xid 来自 token，失效说明凭证陈旧）
	_, err = h.UpdatePassword(ctx, &userv1.UpdatePasswordRequest{UserXid: eid.New(eid.User), OldPassword: "old-pass-123", NewPassword: "new-pass-123"})
	assertCode(t, err, codes.Unauthenticated, "用户不存在")

	// 旧密码错误
	_, err = h.UpdatePassword(ctx, &userv1.UpdatePasswordRequest{UserXid: u.Xid, OldPassword: "wrong-pass-1", NewPassword: "new-pass-123"})
	assertCode(t, err, codes.Unauthenticated, "旧密码错误")

	// 成功改密
	_, err = h.UpdatePassword(ctx, &userv1.UpdatePasswordRequest{UserXid: u.Xid, OldPassword: "old-pass-123", NewPassword: "new-pass-123"})
	if err != nil {
		t.Fatalf("UpdatePassword() 错误: %v", err)
	}

	// 改密后：全部会话删除
	_, err = h.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: login.RefreshToken})
	assertCode(t, err, codes.Unauthenticated, "改密后的旧 refresh")

	// 旧密码不可登录，新密码可登录
	_, err = h.Login(ctx, &userv1.LoginRequest{Email: "erin@example.com", Password: "old-pass-123"})
	assertCode(t, err, codes.Unauthenticated, "旧密码登录")
	if resp := mustLogin(t, h, "erin@example.com", "new-pass-123"); resp.User.Xid != u.Xid {
		t.Errorf("新密码登录返回用户 = %+v", resp.User)
	}
}

// TestSendEmailCode 校验发码：参数校验、正常空响应（防枚举）、60s 限频
func TestSendEmailCode(t *testing.T) {
	h, _, _ := newTestHandler(t)
	ctx := t.Context()

	// 邮箱 / 场景不合法
	_, err := h.SendEmailCode(ctx, &userv1.SendEmailCodeRequest{Email: "bad-email", Scene: auth.SceneRegister})
	assertCode(t, err, codes.InvalidArgument, "邮箱格式不合法")
	_, err = h.SendEmailCode(ctx, &userv1.SendEmailCodeRequest{Email: "a@b.com", Scene: "login"})
	assertCode(t, err, codes.InvalidArgument, "场景不合法")

	// 正常发送：空响应（不回传细节）
	resp, err := h.SendEmailCode(ctx, &userv1.SendEmailCodeRequest{Email: "User@Example.com", Scene: auth.SceneRegister})
	if err != nil {
		t.Fatalf("SendEmailCode() 错误: %v", err)
	}
	if resp == nil {
		t.Fatal("SendEmailCode() 返回 nil")
	}

	// 60s 内同邮箱重发触发限频（不分场景）
	_, err = h.SendEmailCode(ctx, &userv1.SendEmailCodeRequest{Email: "user@example.com", Scene: auth.SceneResetPassword})
	assertCode(t, err, codes.ResourceExhausted, "60s 内重发")
}
