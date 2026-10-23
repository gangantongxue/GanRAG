package middleware

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"golang.org/x/time/rate"

	"github.com/gangantongxue/GanRAG/service/gateway/internal/config"
)

// okHandler 恒返回 200 的占位 handler
func okHandler(ctx context.Context, c *app.RequestContext) {
	c.Status(200)
}

// testLimiterConfig 返回便于测试的限流配置：auth 桶容量 2，默认桶容量 3
func testLimiterConfig() config.RateLimitConfig {
	return config.RateLimitConfig{
		Auth:    config.RateRule{QPS: 0.001, Burst: 2}, // qps 极小，测试窗口内不自动补充令牌
		Default: config.RateRule{QPS: 0.001, Burst: 3},
	}
}

// TestAuthMiddleware_超限返回429 验证 auth 规则在突发容量耗尽后返回 429 统一错误体
func TestAuthMiddleware_超限返回429(t *testing.T) {
	rl := NewRateLimiter(testLimiterConfig())
	h := server.New()
	g := h.Group("/api/v1/auth")
	g.Use(rl.AuthMiddleware())
	g.POST("/login", okHandler)

	// 前 burst 个请求放行，之后超限
	for i := 1; i <= 2; i++ {
		w := ut.PerformRequest(h.Engine, "POST", "/api/v1/auth/login", nil)
		if w.Code != 200 {
			t.Fatalf("第 %d 个请求状态码 = %d，期望 200", i, w.Code)
		}
	}
	w := ut.PerformRequest(h.Engine, "POST", "/api/v1/auth/login", nil)
	if w.Code != 429 {
		t.Fatalf("第 3 个请求状态码 = %d，期望 429", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"code":42900`) || !strings.Contains(body, `"message"`) {
		t.Errorf("429 错误体不合规: %s", body)
	}
}

// TestKey_双维度分桶 验证已登录请求按 user_id 分桶、匿名请求按客户端 IP 分桶
func TestKey_双维度分桶(t *testing.T) {
	// 匿名请求：同 IP 同桶
	if k1, k2 := anonKey("10.0.0.1"), anonKey("10.0.0.1"); k1 != k2 {
		t.Errorf("同 IP 应同桶: %q vs %q", k1, k2)
	}
	if k1, k2 := anonKey("10.0.0.1"), anonKey("10.0.0.2"); k1 == k2 {
		t.Errorf("不同 IP 应不同桶: %q", k1)
	}
	// 登录用户：同用户同桶（与 IP 无关），与匿名桶隔离
	if u1, u2 := userKey("usr_a"), userKey("usr_a"); u1 != u2 {
		t.Errorf("同用户应同桶: %q vs %q", u1, u2)
	}
	if userKey("usr_a") == anonKey("10.0.0.1") {
		t.Error("用户桶与 IP 桶必须隔离")
	}
}

// TestMiddleware_用户维度隔离 验证不同用户的额度互不影响（同一用户共享额度）
func TestMiddleware_用户维度隔离(t *testing.T) {
	rl := NewRateLimiter(testLimiterConfig())
	h := server.New()
	// 模拟验签后的受保护路由：中间件只挂默认限流，用户 xid 由前置验签注入
	g := h.Group("/api/v1")
	g.Use(func(ctx context.Context, c *app.RequestContext) {
		// 按请求头 X-User-Xid 模拟验签注入
		if v := string(c.GetHeader("X-User-Xid")); v != "" {
			c.Set("gateway.user_xid", v)
		}
		c.Next(ctx)
	})
	g.Use(rl.DefaultMiddleware())
	g.GET("/users/me", okHandler)

	// 用户 1 打满默认桶（容量 3）后的第 4 个请求被限流
	for i := 0; i < 3; i++ {
		w := ut.PerformRequest(h.Engine, "GET", "/api/v1/users/me", nil,
			ut.Header{Key: "X-User-Xid", Value: "usr_user1"})
		if w.Code != 200 {
			t.Fatalf("用户 1 第 %d 个请求状态码 = %d，期望 200", i+1, w.Code)
		}
	}
	w := ut.PerformRequest(h.Engine, "GET", "/api/v1/users/me", nil,
		ut.Header{Key: "X-User-Xid", Value: "usr_user1"})
	if w.Code != 429 {
		t.Fatalf("用户 1 超限请求状态码 = %d，期望 429", w.Code)
	}

	// 用户 2 额度独立，不受用户 1 影响
	w = ut.PerformRequest(h.Engine, "GET", "/api/v1/users/me", nil,
		ut.Header{Key: "X-User-Xid", Value: "usr_user2"})
	if w.Code != 200 {
		t.Errorf("用户 2 请求状态码 = %d，期望 200（额度应与用户 1 隔离）", w.Code)
	}
}

// TestEvictLocked 验证闲置桶被回收，防止内存无界增长
func TestEvictLocked(t *testing.T) {
	l := NewRateLimiter(testLimiterConfig())
	l.mu.Lock()
	l.buckets["ip:old"] = &entry{lim: rate.NewLimiter(1, 1), lastSeen: time.Now().Add(-idleTTL - time.Minute)}
	l.buckets["ip:new"] = &entry{lim: rate.NewLimiter(1, 1), lastSeen: time.Now()}
	l.evictLocked()
	_, oldExists := l.buckets["ip:old"]
	_, newExists := l.buckets["ip:new"]
	l.mu.Unlock()

	if oldExists {
		t.Error("闲置超过 idleTTL 的桶应被回收")
	}
	if !newExists {
		t.Error("活跃桶不应被回收")
	}
}
