package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	userv1 "github.com/gangantongxue/GanRAG/api/gen/user/v1"
	"github.com/gangantongxue/GanRAG/pkg/eid"
	"github.com/gangantongxue/GanRAG/service/gateway/internal/config"
)

const (
	testSecret = "test-secret"
	testIssuer = "ganrag"
)

// fakeUserServer 假 user 服务：记录收到的请求并按脚本返回，经 bufconn 挂到真实 gRPC 链路上
type fakeUserServer struct {
	userv1.UnimplementedUserServiceServer

	registerErr error                   // Register 返回的错误（nil 表示成功）
	getUserErr  error                   // GetUser 返回的错误
	gotRegister *userv1.RegisterRequest // 收到的 Register 请求
	gotGetUser  *userv1.GetUserRequest  // 收到的 GetUser 请求（断言用户 xid 注入）
	gotUpdate   *userv1.UpdatePasswordRequest
	gotLogin    *userv1.LoginRequest // 收到的 Login 请求（断言设备 ID / device_info 合并）
}

func (f *fakeUserServer) Register(ctx context.Context, req *userv1.RegisterRequest) (*userv1.RegisterResponse, error) {
	f.gotRegister = req
	if f.registerErr != nil {
		return nil, f.registerErr
	}
	return &userv1.RegisterResponse{User: &userv1.User{Xid: "usr_fake", Email: req.Email, Username: req.Username}}, nil
}

func (f *fakeUserServer) Login(ctx context.Context, req *userv1.LoginRequest) (*userv1.LoginResponse, error) {
	f.gotLogin = req
	return &userv1.LoginResponse{
		User:            &userv1.User{Xid: "usr_fake", Email: req.Email},
		AccessToken:     "access",
		RefreshToken:    "refresh",
		AccessExpiresIn: 900,
		// 回填生效设备码（模拟 user 服务权威生成/核对后的返回）
		DeviceId: req.DeviceId,
	}, nil
}

func (f *fakeUserServer) Refresh(ctx context.Context, req *userv1.RefreshRequest) (*userv1.RefreshResponse, error) {
	return &userv1.RefreshResponse{AccessToken: "new-access", RefreshToken: "new-refresh"}, nil
}

func (f *fakeUserServer) GetUser(ctx context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	f.gotGetUser = req
	if f.getUserErr != nil {
		return nil, f.getUserErr
	}
	return &userv1.GetUserResponse{User: &userv1.User{Xid: req.Xid, Email: "alice@example.com"}}, nil
}

func (f *fakeUserServer) UpdatePassword(ctx context.Context, req *userv1.UpdatePasswordRequest) (*userv1.UpdatePasswordResponse, error) {
	f.gotUpdate = req
	return &userv1.UpdatePasswordResponse{}, nil
}

// newTestGateway 通过 bufconn 启动假 user 服务，并用真实 server.New 构造完整网关
//
// 参数：
//   - t: 测试上下文
//   - fake: 假 user 服务实例（用于断言下游收到的请求）
//
// 返回：
//   - *route.Engine: 可直接用于 ut.PerformRequest 的路由引擎
//   - *fakeUserServer: 原样返回 fake，便于链式赋值
func newTestGateway(t *testing.T, fake *fakeUserServer) (*route.Engine, *fakeUserServer) {
	t.Helper()

	// 启动假 user 服务（bufconn 免真实端口）
	lis := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	userv1.RegisterUserServiceServer(grpcServer, fake)
	go func() { _ = grpcServer.Serve(lis) }()
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("创建 gRPC 客户端失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	cfg := config.Config{
		Server:   config.ServerConfig{Host: "127.0.0.1", Port: 8080},
		JWT:      config.JWTConfig{Secret: testSecret, Issuer: testIssuer},
		Upstream: config.UpstreamConfig{User: "bufnet"},
		RateLimit: config.RateLimitConfig{
			Auth:    config.RateRule{QPS: 1000, Burst: 1000}, // 测试中不做限流干扰
			Default: config.RateRule{QPS: 1000, Burst: 1000},
		},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, conn, log).h.Engine, fake
}

// issueToken 按 user 服务的签发约定构造 access token（sub 为用户 xid 全串）
func issueToken(t *testing.T, userXID string) string {
	t.Helper()
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   userXID,
		Issuer:    testIssuer,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("签发测试 token 失败: %v", err)
	}
	return token
}

// jsonBody 构造 JSON 请求体
func jsonBody(s string) *ut.Body {
	return &ut.Body{Body: strings.NewReader(s), Len: len(s)}
}

// TestRegister_Success 验证注册成功时 protojson 直出（200 + 用户 JSON）
func TestRegister_Success(t *testing.T) {
	engine, fake := newTestGateway(t, &fakeUserServer{})
	w := ut.PerformRequest(engine, "POST", "/api/v1/auth/register",
		jsonBody(`{"email":"alice@example.com","code":"123456","password":"password123","username":"alice"}`),
		ut.Header{Key: "Content-Type", Value: "application/json"})

	if w.Code != 200 {
		t.Fatalf("状态码 = %d，期望 200（body: %s）", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"email":"alice@example.com"`) {
		t.Errorf("响应体应包含邮箱: %s", w.Body.String())
	}
	if fake.gotRegister == nil || fake.gotRegister.Email != "alice@example.com" || fake.gotRegister.Code != "123456" {
		t.Errorf("下游未收到正确的注册请求: %+v", fake.gotRegister)
	}
}

// TestRegister_已存在映射409 验证 gRPC AlreadyExists → HTTP 409 统一错误体
func TestRegister_已存在映射409(t *testing.T) {
	engine, _ := newTestGateway(t, &fakeUserServer{
		registerErr: status.Error(codes.AlreadyExists, "邮箱已注册"),
	})
	w := ut.PerformRequest(engine, "POST", "/api/v1/auth/register",
		jsonBody(`{"email":"alice@example.com","code":"123456","password":"password123"}`),
		ut.Header{Key: "Content-Type", Value: "application/json"})

	if w.Code != 409 {
		t.Fatalf("状态码 = %d，期望 409", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"code":40900`) || !strings.Contains(body, "邮箱已注册") {
		t.Errorf("错误体不合规: %s", body)
	}
}

// TestRegister_非法请求体返回400 验证非 JSON 请求体被拒绝
func TestRegister_非法请求体返回400(t *testing.T) {
	engine, _ := newTestGateway(t, &fakeUserServer{})
	w := ut.PerformRequest(engine, "POST", "/api/v1/auth/register",
		jsonBody(`not-json`),
		ut.Header{Key: "Content-Type", Value: "application/json"})

	if w.Code != 400 {
		t.Errorf("状态码 = %d，期望 400", w.Code)
	}
}

// TestRefresh_绕过验签 验证 refresh 路由无需 access token 直达下游（架构硬性要求）
func TestRefresh_绕过验签(t *testing.T) {
	engine, _ := newTestGateway(t, &fakeUserServer{})
	// 不带 Authorization 头
	w := ut.PerformRequest(engine, "POST", "/api/v1/auth/refresh",
		jsonBody(`{"refresh_token":"rt"}`),
		ut.Header{Key: "Content-Type", Value: "application/json"})

	if w.Code != 200 {
		t.Fatalf("refresh 未绕过验签？状态码 = %d，期望 200（body: %s）", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"new-access"`) {
		t.Errorf("响应体应包含新 access token: %s", w.Body.String())
	}
}

// TestLogin_设备ID 验证设备 ID 取值优先级（请求体 > did cookie > 下游生成）与 did cookie 回填
func TestLogin_设备ID(t *testing.T) {
	// 请求体 device_id 优先于 cookie，登录成功 Set-Cookie did= 回填
	engine, fake := newTestGateway(t, &fakeUserServer{})
	w := ut.PerformRequest(engine, "POST", "/api/v1/auth/login",
		jsonBody(`{"email":"alice@example.com","password":"password123","device_id":"web_body"}`),
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "Cookie", Value: "did=web_cookie"})
	if w.Code != 200 {
		t.Fatalf("状态码 = %d，期望 200（body: %s）", w.Code, w.Body.String())
	}
	if fake.gotLogin.GetDeviceId() != "web_body" {
		t.Errorf("下游 device_id = %q，期望请求体优先 web_body", fake.gotLogin.GetDeviceId())
	}
	if sc := string(w.Header().Peek("Set-Cookie")); !strings.Contains(sc, "did=web_body") {
		t.Errorf("Set-Cookie = %q，应回填 did=web_body", sc)
	}

	// 请求体缺失时回落 did cookie
	w = ut.PerformRequest(engine, "POST", "/api/v1/auth/login",
		jsonBody(`{"email":"alice@example.com","password":"password123"}`),
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "Cookie", Value: "did=web_cookie"})
	if fake.gotLogin.GetDeviceId() != "web_cookie" {
		t.Errorf("下游 device_id = %q，期望回落 cookie web_cookie", fake.gotLogin.GetDeviceId())
	}

	// 两者皆缺：留空交 user 服务生成；device_info 缺省取 User-Agent
	w = ut.PerformRequest(engine, "POST", "/api/v1/auth/login",
		jsonBody(`{"email":"alice@example.com","password":"password123"}`),
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "User-Agent", Value: "test-agent/1.0"})
	if fake.gotLogin.GetDeviceId() != "" {
		t.Errorf("下游 device_id = %q，期望留空由 user 服务生成", fake.gotLogin.GetDeviceId())
	}
	if fake.gotLogin.GetDeviceInfo() != "test-agent/1.0" {
		t.Errorf("下游 device_info = %q，期望取 User-Agent", fake.gotLogin.GetDeviceInfo())
	}
	if sc := string(w.Header().Peek("Set-Cookie")); sc != "" {
		t.Errorf("无生效设备码时不应 Set-Cookie，实际 %q", sc)
	}
}

// TestMe_未认证返回401 验证受保护路由无 token 时拒绝
func TestMe_未认证返回401(t *testing.T) {
	engine, _ := newTestGateway(t, &fakeUserServer{})
	w := ut.PerformRequest(engine, "GET", "/api/v1/users/me", nil)

	if w.Code != 401 {
		t.Fatalf("状态码 = %d，期望 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"code":40100`) {
		t.Errorf("错误体不合规: %s", w.Body.String())
	}
}

// TestMe_注入Token中的用户xid 验证用户 xid 取自验签结果而非请求参数
func TestMe_注入Token中的用户xid(t *testing.T) {
	userXID := eid.New(eid.User)
	engine, fake := newTestGateway(t, &fakeUserServer{})
	w := ut.PerformRequest(engine, "GET", "/api/v1/users/me", nil,
		ut.Header{Key: "Authorization", Value: "Bearer " + issueToken(t, userXID)})

	if w.Code != 200 {
		t.Fatalf("状态码 = %d，期望 200（body: %s）", w.Code, w.Body.String())
	}
	if fake.gotGetUser == nil || fake.gotGetUser.Xid != userXID {
		t.Errorf("下游收到的用户 xid = %+v，期望 %s", fake.gotGetUser, userXID)
	}
}

// TestMe_下游404映射 验证 gRPC NotFound → HTTP 404
func TestMe_下游404映射(t *testing.T) {
	engine, _ := newTestGateway(t, &fakeUserServer{
		getUserErr: status.Error(codes.NotFound, "用户不存在"),
	})
	w := ut.PerformRequest(engine, "GET", "/api/v1/users/me", nil,
		ut.Header{Key: "Authorization", Value: "Bearer " + issueToken(t, eid.New(eid.User))})

	if w.Code != 404 {
		t.Errorf("状态码 = %d，期望 404", w.Code)
	}
}

// TestUpdatePassword_注入用户xid 验证改密时用户 xid 由 token 注入、不信任请求体
func TestUpdatePassword_注入用户xid(t *testing.T) {
	userXID := eid.New(eid.User)
	engine, fake := newTestGateway(t, &fakeUserServer{})
	w := ut.PerformRequest(engine, "PUT", "/api/v1/users/me/password",
		jsonBody(`{"old_password":"old-pass-1","new_password":"new-pass-1"}`),
		ut.Header{Key: "Authorization", Value: "Bearer " + issueToken(t, userXID)},
		ut.Header{Key: "Content-Type", Value: "application/json"})

	if w.Code != 200 {
		t.Fatalf("状态码 = %d，期望 200（body: %s）", w.Code, w.Body.String())
	}
	if fake.gotUpdate == nil || fake.gotUpdate.UserXid != userXID {
		t.Errorf("下游收到的用户 xid = %+v，期望 %s", fake.gotUpdate, userXID)
	}
	if fake.gotUpdate.GetOldPassword() != "old-pass-1" {
		t.Errorf("下游收到的旧密码 = %q，期望 old-pass-1", fake.gotUpdate.GetOldPassword())
	}
}
