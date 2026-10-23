// Package middleware 提供 Gateway 的通用 HTTP 中间件：双维度限流与访问日志
package middleware

import (
	"context"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"golang.org/x/time/rate"

	"github.com/gangantongxue/GanRAG/pkg/errcode"
	"github.com/gangantongxue/GanRAG/service/gateway/internal/config"
	"github.com/gangantongxue/GanRAG/service/gateway/pkg/auth"
)

// maxBuckets 闲置桶清理触发的桶数量阈值
//
// 桶数量不设硬上限：仅当超过该阈值时才扫描清理闲置桶，
// 避免高基数来源（IP 变化）导致内存无界增长。
const maxBuckets = 10000

// idleTTL 桶的闲置淘汰时长：超过该时长未命中即回收
const idleTTL = 10 * time.Minute

// RateLimiter 基于令牌桶的双维度限流器
//
// 分桶维度：已登录请求按 user_id 分桶，匿名请求按客户端 IP 分桶——
// 同一用户的多端登录共享额度，未登录攻击源按 IP 隔离。
type RateLimiter struct {
	authRule    rule              // /api/v1/auth/* 路由的严格规则（防爆破）
	defaultRule rule              // 普通业务路由的规则
	mu          sync.Mutex        // 保护 buckets
	buckets     map[string]*entry // key → 令牌桶
}

// rule 单条限流规则（令牌速率 + 桶容量）
type rule struct {
	qps   float64 // 平均每秒允许的请求数
	burst int     // 桶容量
}

// entry 桶及其最近命中时间（闲置淘汰依据）
type entry struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiter 创建限流器
//
// 参数：
//   - cfg: 限流配置（auth 与 default 两套规则）
//
// 返回：
//   - *RateLimiter: 限流器实例
func NewRateLimiter(cfg config.RateLimitConfig) *RateLimiter {
	return &RateLimiter{
		authRule:    rule{qps: cfg.Auth.QPS, burst: cfg.Auth.Burst},
		defaultRule: rule{qps: cfg.Default.QPS, burst: cfg.Default.Burst},
		buckets:     make(map[string]*entry),
	}
}

// AuthMiddleware 返回 auth 路由（注册/登录/刷新）的限流中间件
//
// 这些接口可被匿名访问且是撞库爆破目标，走更严格的 auth 规则。
func (l *RateLimiter) AuthMiddleware() app.HandlerFunc {
	return l.middleware(l.authRule)
}

// DefaultMiddleware 返回普通业务路由的限流中间件
func (l *RateLimiter) DefaultMiddleware() app.HandlerFunc {
	return l.middleware(l.defaultRule)
}

// middleware 按规则对当前请求分桶放行，超限返回 429 统一错误体
//
// 参数：
//   - r: 生效的限流规则
//
// 返回：
//   - app.HandlerFunc: Hertz 中间件
func (l *RateLimiter) middleware(r rule) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		if !l.allow(key(c), r) {
			// 统一信封错误体：兜底码 CodeResourceExhausted
			c.AbortWithStatusJSON(429, map[string]any{
				"code":    errcode.CodeResourceExhausted,
				"message": "请求过于频繁，请稍后重试",
				"data":    nil,
			})
			return
		}
		c.Next(ctx)
	}
}

// allow 对 key 对应的桶尝试取令牌
//
// 参数：
//   - key: 分桶键（u:<userID> 或 ip:<clientIP>）
//   - r: 限流规则
//
// 返回：
//   - bool: true 放行；false 超限
func (l *RateLimiter) allow(key string, r rule) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	e, ok := l.buckets[key]
	if !ok {
		e = &entry{lim: rate.NewLimiter(rate.Limit(r.qps), r.burst)}
		l.buckets[key] = e
		// 桶数量超过阈值时顺带回收闲置桶，防止 IP 变化导致内存无界增长
		if len(l.buckets) > maxBuckets {
			l.evictLocked()
		}
	}
	e.lastSeen = time.Now()
	return e.lim.Allow()
}

// evictLocked 移除闲置超过 idleTTL 的桶（调用方必须已持有锁）
func (l *RateLimiter) evictLocked() {
	cutoff := time.Now().Add(-idleTTL)
	for k, e := range l.buckets {
		if e.lastSeen.Before(cutoff) {
			delete(l.buckets, k)
		}
	}
}

// key 计算当前请求的分桶键：已登录按用户 xid，匿名按客户端 IP
//
// 参数：
//   - c: Hertz 请求上下文
//
// 返回：
//   - string: 分桶键
func key(c *app.RequestContext) string {
	if xid := auth.UserXID(c); xid != "" {
		return userKey(xid)
	}
	return anonKey(c.ClientIP())
}

// userKey 已登录用户的分桶键（与来源 IP 无关，多端共享额度）
func userKey(xid string) string {
	return "u:" + xid
}

// anonKey 匿名请求的分桶键（按客户端 IP 隔离）
func anonKey(ip string) string {
	return "ip:" + ip
}
