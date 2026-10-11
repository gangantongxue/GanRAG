package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
)

// AccessLog 返回访问日志中间件：每个请求结束时记录方法、路径、状态码、耗时与客户端 IP
//
// 参数：
//   - log: 日志实例
//
// 返回：
//   - app.HandlerFunc: Hertz 中间件
func AccessLog(log *slog.Logger) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		start := time.Now()
		c.Next(ctx) // 放行后续中间件与 handler
		log.Info("http",
			"method", string(c.Method()),
			"path", string(c.Path()),
			"status", c.Response.StatusCode(),
			"cost", time.Since(start).String(),
			"ip", c.ClientIP(),
		)
	}
}
