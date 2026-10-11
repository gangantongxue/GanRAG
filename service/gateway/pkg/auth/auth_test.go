package auth

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/golang-jwt/jwt/v5"
)

const (
	testSecret = "test-secret"
	testIssuer = "ganrag"
)

// issueToken 按 user 服务的签发约定构造一个 access token
//
// 参数：
//   - t: 测试上下文
//   - sub: 用户 ID（sub 声明）
//   - issuer: 签发方（iss 声明）
//   - ttl: 有效期
//
// 返回：
//   - string: JWT 字符串
func issueToken(t *testing.T, sub, issuer string, ttl time.Duration) string {
	t.Helper()
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   sub,
		Issuer:    issuer,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("签发测试 token 失败: %v", err)
	}
	return token
}

// newTestEngine 构造仅挂载验签中间件与回显 handler 的测试引擎
//
// 回显 handler 把验签得到的用户 ID 写进响应体，便于断言注入结果。
func newTestEngine() *route.Engine {
	h := server.New()
	h.GET("/me", Middleware(testSecret, testIssuer), func(ctx context.Context, c *app.RequestContext) {
		c.JSON(200, map[string]any{"user_id": UserID(c)})
	})
	return h.Engine
}

// perform 以给定 Authorization 头发起请求
//
// 参数：
//   - authHeader: Authorization 头完整值（空串表示不带该头）
//
// 返回：
//   - *ut.ResponseRecorder: 响应记录
func perform(authHeader string) *ut.ResponseRecorder {
	var headers []ut.Header
	if authHeader != "" {
		headers = append(headers, ut.Header{Key: "Authorization", Value: authHeader})
	}
	return ut.PerformRequest(newTestEngine(), "GET", "/me", nil, headers...)
}

// TestMiddleware_ValidToken 验证合法 token 通过验签并把用户 ID 注入上下文
func TestMiddleware_ValidToken(t *testing.T) {
	token := issueToken(t, "42", testIssuer, 15*time.Minute)
	w := perform("Bearer " + token)

	if w.Code != 200 {
		t.Fatalf("状态码 = %d，期望 200（响应体: %s）", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"user_id":42`) {
		t.Errorf("响应体未包含注入的用户 ID: %s", w.Body.String())
	}
}

// TestMiddleware_Rejected 表驱动验证各类非法请求被拒绝为 401 统一错误体
func TestMiddleware_Rejected(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{"缺少 Authorization 头", ""},
		{"非 Bearer 格式", "Basic dXNlcjpwYXNz"},
		{"token 篡改（签名不匹配）", "Bearer " + issueToken(t, "42", testIssuer, 15*time.Minute) + "x"},
		{"已过期 token", "Bearer " + issueToken(t, "42", testIssuer, -time.Minute)},
		{"签发方不符", "Bearer " + issueToken(t, "42", "other", 15*time.Minute)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := perform(tt.header)
			if w.Code != 401 {
				t.Fatalf("状态码 = %d，期望 401", w.Code)
			}
			body := w.Body.String()
			if !strings.Contains(body, `"code":401`) || !strings.Contains(body, `"message"`) {
				t.Errorf("错误体不合规: %s", body)
			}
		})
	}
}

// TestUserID_未认证返回零值 验证未注入 user_id 的上下文返回 0
func TestUserID_未认证返回零值(t *testing.T) {
	// 用空 context 直接构造请求上下文，模拟未经中间件的场景
	h := server.New()
	var got int64
	h.GET("/", func(ctx context.Context, c *app.RequestContext) {
		got = UserID(c)
		c.Status(204)
	})
	ut.PerformRequest(h.Engine, "GET", "/", nil)
	if got != 0 {
		t.Errorf("UserID = %d，期望 0", got)
	}
}

// TestBearerToken 表驱动验证 Authorization 头解析
func TestBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
		wantOK bool
	}{
		{"标准格式", "Bearer abc.def.ghi", "abc.def.ghi", true},
		{"小写 scheme", "bearer abc", "abc", true},
		{"多余空白", "Bearer   abc  ", "abc", true},
		{"空 token", "Bearer ", "", false},
		{"无前缀", "abc", "", false},
		{"仅前缀", "Bearer", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := bearerToken([]byte(tt.header))
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("bearerToken(%q) = (%q, %v)，期望 (%q, %v)", tt.header, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestVerify_用户ID解析 验证 sub 声明解析为用户 ID
func TestVerify_用户ID解析(t *testing.T) {
	token := issueToken(t, strconv.FormatInt(7, 10), testIssuer, 15*time.Minute)
	id, ok := verify(token, testSecret, testIssuer)
	if !ok || id != 7 {
		t.Errorf("verify = (%d, %v)，期望 (7, true)", id, ok)
	}
}
