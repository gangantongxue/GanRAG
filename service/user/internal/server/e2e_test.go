package server

import (
	"context"
	"net"
	"slices"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"gorm.io/gorm"

	userv1 "github.com/gangantongxue/GanRAG/api/gen/user/v1"
	"github.com/gangantongxue/GanRAG/pkg/eid"
	"github.com/gangantongxue/GanRAG/service/user/internal/auth"
)

// newE2EClient 通过 bufconn 启动完整 gRPC 服务端并返回客户端连接与底层数据库连接
//
// 服务端注册内容与 server.New 完全一致：UserService、健康检查服务、反射服务——
// 测试因此覆盖「服务注册 + protobuf 序列化 + gRPC status 跨网络传播」整条链路，
// 而非直接调用 handler 方法（handler_test.go 已覆盖后者）。
//
// 参数：
//   - t: 测试上下文（内部挂载 SQLite 临时库，测试结束自动清理）
//
// 返回值：
//   - userv1.UserServiceClient: 用户服务客户端
//   - healthv1.HealthClient: 健康检查客户端
//   - *grpc.ClientConn: 底层连接（反射测试用）
//   - *gorm.DB: 底层数据库连接（禁用用户等直接改数据用）
//   - *Handler: 服务端 handler（测试取邮箱验证码用，邮件通道不进 SMTP）
func newE2EClient(t *testing.T) (userv1.UserServiceClient, healthv1.HealthClient, *grpc.ClientConn, *gorm.DB, *Handler, *miniredis.Miniredis) {
	t.Helper()
	h, db, mr := newTestHandler(t)

	lis := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	userv1.RegisterUserServiceServer(grpcServer, h)

	// 健康检查与反射：与 server.New 保持一致，保证接口测试覆盖面不缩水
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	healthv1.RegisterHealthServer(grpcServer, healthServer)
	reflection.Register(grpcServer)

	go func() {
		_ = grpcServer.Serve(lis)
	}()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("创建客户端连接失败: %v", err)
	}

	t.Cleanup(func() {
		_ = conn.Close()
		grpcServer.Stop()
		_ = lis.Close()
	})

	return userv1.NewUserServiceClient(conn), healthv1.NewHealthClient(conn), conn, db, h, mr
}

// e2eRegister 走完整 RPC 注册（先经 handler 取邮箱验证码）
func e2eRegister(t *testing.T, client userv1.UserServiceClient, h *Handler, mr *miniredis.Miniredis, email, password string) *userv1.User {
	t.Helper()
	code := mustIssueCode(t, h, mr, auth.SceneRegister, email)
	resp, err := client.Register(t.Context(), &userv1.RegisterRequest{
		Email: email, Code: code, Password: password,
	})
	if err != nil {
		t.Fatalf("Register(%s) 错误: %v", email, err)
	}
	return resp.User
}

// TestUserServiceEndToEnd 全接口端到端测试（走完整 gRPC 协议）
//
// 覆盖 5 个 RPC 的成功链路与契约错误码：
// 注册（邮箱+验证码）-> 参数/邮箱查重 -> 登录（防账号枚举）-> 查询用户 ->
// 刷新轮换（旧 token 复用按无效拒绝）-> 改密（会话全删、旧密码作废）-> 禁用拒绝。
func TestUserServiceEndToEnd(t *testing.T) {
	client, _, _, db, h, mr := newE2EClient(t)
	ctx := t.Context()

	// ---- Register ----
	// 正常注册：xid 对外、状态正常、时间戳非零
	reg := e2eRegister(t, client, h, mr, "lifecycle@example.com", "password123")
	xid := reg.Xid
	if !eid.Match(xid, eid.User) || reg.Email != "lifecycle@example.com" {
		t.Fatalf("注册返回用户不符: %+v", reg)
	}
	if reg.Status != 1 || reg.CreatedAtUnix <= 0 || reg.UpdatedAtUnix <= 0 {
		t.Errorf("status/时间戳不符: %+v", reg)
	}

	// 缺验证码先被拒（防枚举：未持码无法触达邮箱查重）
	_, err := client.Register(ctx, &userv1.RegisterRequest{Email: "lifecycle@example.com", Password: "password123"})
	assertCode(t, err, codes.Unauthenticated, "e2e 缺验证码")

	// 邮箱查重与非法参数（错误码需穿过 protobuf status 序列化保持不变）
	code := mustIssueCode(t, h, mr, auth.SceneRegister, "lifecycle@example.com")
	_, err = client.Register(ctx, &userv1.RegisterRequest{Email: "lifecycle@example.com", Code: code, Password: "password123"})
	assertCode(t, err, codes.AlreadyExists, "e2e 重复邮箱")
	for _, r := range []*userv1.RegisterRequest{
		{Email: "bad-email", Code: "123456", Password: "password123"}, // 非法邮箱
		{Email: "x@example.com", Code: "123456", Password: "short1"},  // 密码过短
	} {
		_, err := client.Register(ctx, r)
		assertCode(t, err, codes.InvalidArgument, "e2e 非法注册参数")
	}

	// ---- Login：防账号枚举 ----
	_, err = client.Login(ctx, &userv1.LoginRequest{Email: "ghost@example.com", Password: "password123"})
	assertCode(t, err, codes.Unauthenticated, "e2e 用户不存在")
	ghostMsg := status.Convert(err).Message()
	_, err = client.Login(ctx, &userv1.LoginRequest{Email: "lifecycle@example.com", Password: "wrong-pass-1"})
	assertCode(t, err, codes.Unauthenticated, "e2e 密码错误")
	// 两种失败必须返回完全相同的消息，否则泄露「邮箱是否已注册」
	if got := status.Convert(err).Message(); got != ghostMsg {
		t.Errorf("防枚举失败：用户不存在消息 %q != 密码错误消息 %q", ghostMsg, got)
	}

	// ---- Login 成功：双 token 契约 ----
	login, err := client.Login(ctx, &userv1.LoginRequest{Email: "lifecycle@example.com", Password: "password123"})
	if err != nil {
		t.Fatalf("Login() 错误: %v", err)
	}
	if login.AccessToken == "" || login.RefreshToken == "" {
		t.Fatal("登录应返回 access 与 refresh token")
	}
	if login.AccessExpiresIn != int64(testAccessTTL.Seconds()) || login.RefreshExpiresIn != int64(testRefreshTTL.Seconds()) {
		t.Errorf("expires_in = %d/%d，期望 %d/%d", login.AccessExpiresIn, login.RefreshExpiresIn,
			int64(testAccessTTL.Seconds()), int64(testRefreshTTL.Seconds()))
	}
	// Gateway 契约：access token 用共享密钥验签且 sub/username 声明正确
	parsed, err := jwt.Parse(login.AccessToken, func(*jwt.Token) (any, error) {
		return []byte(testJWTSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !parsed.Valid {
		t.Fatalf("access token 验签失败: %v", err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if sub, _ := claims.GetSubject(); sub != xid {
		t.Errorf("sub = %q，期望 %q", sub, xid)
	}
	if claims["username"] != "" {
		t.Errorf("username 声明 = %v，期望空展示名", claims["username"])
	}

	// ---- GetUser ----
	got, err := client.GetUser(ctx, &userv1.GetUserRequest{Xid: xid})
	if err != nil {
		t.Fatalf("GetUser() 错误: %v", err)
	}
	if got.User.Xid != xid || got.User.Email != "lifecycle@example.com" {
		t.Errorf("GetUser 返回 = %+v", got.User)
	}
	_, err = client.GetUser(ctx, &userv1.GetUserRequest{Xid: ""})
	assertCode(t, err, codes.InvalidArgument, "e2e 缺少 xid")
	_, err = client.GetUser(ctx, &userv1.GetUserRequest{Xid: eid.New(eid.User)})
	assertCode(t, err, codes.NotFound, "e2e 用户不存在")

	// ---- Refresh：原地轮换，旧 token 复用按无效拒绝 ----
	r1, err := client.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: login.RefreshToken})
	if err != nil {
		t.Fatalf("Refresh() 错误: %v", err)
	}
	if r1.RefreshToken == login.RefreshToken {
		t.Error("轮换应签发全新的 refresh token")
	}
	// 旧 token 复用：哈希不再命中，按无效拒绝（不做重用检测）
	_, err = client.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: login.RefreshToken})
	assertCode(t, err, codes.Unauthenticated, "e2e 重用旧 token")
	// 轮换出的新 token 不受影响
	if _, err := client.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: r1.RefreshToken}); err != nil {
		t.Errorf("轮换后的新 token 应可用: %v", err)
	}
	_, err = client.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: ""})
	assertCode(t, err, codes.InvalidArgument, "e2e 空 refresh token")

	// ---- UpdatePassword ----
	login2, err := client.Login(ctx, &userv1.LoginRequest{Email: "lifecycle@example.com", Password: "password123"})
	if err != nil {
		t.Fatalf("重新登录错误: %v", err)
	}
	_, err = client.UpdatePassword(ctx, &userv1.UpdatePasswordRequest{UserXid: "", OldPassword: "password123", NewPassword: "new-pass-456"})
	assertCode(t, err, codes.InvalidArgument, "e2e 缺少 user_xid")
	_, err = client.UpdatePassword(ctx, &userv1.UpdatePasswordRequest{UserXid: xid, OldPassword: "password123", NewPassword: "short"})
	assertCode(t, err, codes.InvalidArgument, "e2e 新密码过短")
	_, err = client.UpdatePassword(ctx, &userv1.UpdatePasswordRequest{UserXid: xid, OldPassword: "wrong-pass-1", NewPassword: "new-pass-456"})
	assertCode(t, err, codes.Unauthenticated, "e2e 旧密码错误")

	if _, err := client.UpdatePassword(ctx, &userv1.UpdatePasswordRequest{
		UserXid: xid, OldPassword: "password123", NewPassword: "new-pass-456",
	}); err != nil {
		t.Fatalf("UpdatePassword() 错误: %v", err)
	}
	// 改密后该用户全部会话删除
	_, err = client.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: login2.RefreshToken})
	assertCode(t, err, codes.Unauthenticated, "e2e 改密后的旧 refresh")
	// 旧密码作废、新密码可登录
	_, err = client.Login(ctx, &userv1.LoginRequest{Email: "lifecycle@example.com", Password: "password123"})
	assertCode(t, err, codes.Unauthenticated, "e2e 旧密码登录")
	login3, err := client.Login(ctx, &userv1.LoginRequest{Email: "lifecycle@example.com", Password: "new-pass-456"})
	if err != nil {
		t.Fatalf("新密码登录错误: %v", err)
	}

	// ---- 禁用账号：登录与刷新均拒绝 ----
	if err := db.Exec("UPDATE users SET status = 0 WHERE xid = ?", xid).Error; err != nil {
		t.Fatalf("禁用用户失败: %v", err)
	}
	_, err = client.Login(ctx, &userv1.LoginRequest{Email: "lifecycle@example.com", Password: "new-pass-456"})
	assertCode(t, err, codes.FailedPrecondition, "e2e 禁用账号登录")
	_, err = client.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: login3.RefreshToken})
	assertCode(t, err, codes.FailedPrecondition, "e2e 禁用账号刷新")
}

// TestHealthAndReflection 健康检查与服务反射接口测试
//
// 健康检查供编排系统探测；反射供 grpcurl 等工具动态发现服务定义。
func TestHealthAndReflection(t *testing.T) {
	_, healthClient, conn, _, _, _ := newE2EClient(t)
	ctx := t.Context()

	// 健康检查：默认服务（""）应返回 SERVING
	hresp, err := healthClient.Check(ctx, &healthv1.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("健康检查错误: %v", err)
	}
	if hresp.Status != healthv1.HealthCheckResponse_SERVING {
		t.Errorf("健康状态 = %v，期望 SERVING", hresp.Status)
	}

	// 服务反射：列表应包含 UserService 与健康检查服务
	stream, err := grpc_reflection_v1.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	if err != nil {
		t.Fatalf("创建反射流错误: %v", err)
	}
	if err := stream.Send(&grpc_reflection_v1.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1.ServerReflectionRequest_ListServices{ListServices: ""},
	}); err != nil {
		t.Fatalf("发送反射请求错误: %v", err)
	}
	resp, err := stream.Recv()
	if err != nil {
		t.Fatalf("接收反射响应错误: %v", err)
	}
	services := make([]string, 0)
	for _, svc := range resp.GetListServicesResponse().GetService() {
		services = append(services, svc.GetName())
	}
	for _, want := range []string{"user.v1.UserService", "grpc.health.v1.Health"} {
		if !slices.Contains(services, want) {
			t.Errorf("反射服务列表缺少 %s，实际: %v", want, services)
		}
	}
}

// TestConcurrentRefresh 并发刷新竞争测试
//
// 同一 refresh token 被并发使用时，轮换事务的条件更新（token_hash 仍为旧值）
// 保证最多一个成功，失败方按无效拒绝（Unauthenticated）。
func TestConcurrentRefresh(t *testing.T) {
	client, _, _, _, h, mr := newE2EClient(t)
	ctx := t.Context()

	e2eRegister(t, client, h, mr, "race@example.com", "password123")
	login, err := client.Login(ctx, &userv1.LoginRequest{Email: "race@example.com", Password: "password123"})
	if err != nil {
		t.Fatalf("Login() 错误: %v", err)
	}

	const workers = 4
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			_, errs[i] = client.Refresh(ctx, &userv1.RefreshRequest{RefreshToken: login.RefreshToken})
		})
	}
	wg.Wait()

	var success int
	for i, err := range errs {
		if err == nil {
			success++
			continue
		}
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("并发刷新第 %d 个失败方错误码 = %v，期望 Unauthenticated（err=%v）", i, status.Code(err), err)
		}
	}
	if success != 1 {
		t.Errorf("并发刷新成功次数 = %d，期望恰好 1", success)
	}
}
