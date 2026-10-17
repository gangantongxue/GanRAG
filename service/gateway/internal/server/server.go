// Package server 封装 Gateway 的 Hertz HTTP 服务：路由注册与生命周期
package server

import (
	"log/slog"

	"github.com/cloudwego/hertz/pkg/app/server"
	"google.golang.org/grpc"

	"github.com/gangantongxue/GanRAG/service/gateway/internal/config"
	"github.com/gangantongxue/GanRAG/service/gateway/internal/handler"
	"github.com/gangantongxue/GanRAG/service/gateway/internal/middleware"
	"github.com/gangantongxue/GanRAG/service/gateway/pkg/auth"
)

// Server 封装 Hertz HTTP 服务
type Server struct {
	h    *server.Hertz // Hertz 服务端
	addr string        // 监听地址（日志展示用）
	log  *slog.Logger  // 日志
}

// New 创建并初始化 HTTP 服务，注册全部路由与中间件
//
// 中间件链路：
//   - 全局：panic 恢复（server.Default 内置）→ 访问日志
//   - 公开组 /api/v1/auth/*：auth 限流（匿名按 IP 分桶，防爆破）
//   - 验签组 /api/v1/*：access token 本地验签（注入 user_id）→ 默认限流（按用户分桶）
//
// 参数：
//   - cfg: 服务配置
//   - conn: 到下游 gRPC 服务的连接
//   - log: 日志实例
//
// 返回：
//   - *Server: 服务实例
func New(cfg config.Config, conn *grpc.ClientConn, log *slog.Logger) *Server {
	// server.Default 内置 panic 恢复中间件
	h := server.Default(server.WithHostPorts(cfg.Server.Addr()))

	// 访问日志：恢复中间件之后注册，保证 panic 也留有访问记录
	h.Use(middleware.AccessLog(log))

	rl := middleware.NewRateLimiter(cfg.RateLimit)
	uh := handler.NewUserHandler(conn, log)

	// 公开路由：注册 / 登录 / 刷新。refresh 按设计必须绕过 access 校验（user 架构文档）
	pub := h.Group("/api/v1/auth")
	pub.Use(rl.AuthMiddleware())
	pub.POST("/register", uh.Register)
	pub.POST("/login", uh.Login)
	pub.POST("/refresh", uh.Refresh)
	pub.POST("/email-code", uh.SendEmailCode)

	// 需验签路由：先本地验签注入 user_id，再按用户维度限流
	authed := h.Group("/api/v1")
	authed.Use(auth.Middleware(cfg.JWT.Secret, cfg.JWT.Issuer))
	authed.Use(rl.DefaultMiddleware())
	authed.GET("/users/me", uh.Me)
	authed.PUT("/users/me/password", uh.UpdatePassword)

	return &Server{h: h, addr: cfg.Server.Addr(), log: log}
}

// Serve 启动 HTTP 服务并阻塞，收到 SIGINT/SIGTERM 后优雅停止
//
// 返回：
//   - error: 服务运行过程中的错误（优雅退出时为 nil）
func (s *Server) Serve() error {
	s.log.Info("HTTP 服务启动", "addr", s.addr)
	s.h.Spin() // 内置信号处理与优雅关闭（等待在途请求完成）
	return nil
}
