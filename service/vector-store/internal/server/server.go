// Package server 提供 Vector Store 的 gRPC 服务端封装
package server

import (
	"fmt"
	"log/slog"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	vectorstorev1 "github.com/gangantongxue/GanRAG/api/gen/vector-store/v1"
	"github.com/gangantongxue/GanRAG/service/vector-store/internal/config"
	"github.com/gangantongxue/GanRAG/service/vector-store/internal/store"
)

// Server 封装 gRPC 服务端及其监听器
type Server struct {
	grpc *grpc.Server // gRPC 服务端
	lis  net.Listener // TCP 监听器
	log  *slog.Logger // 日志
}

// New 创建并初始化 gRPC 服务端
//
// 监听指定地址，注册 VectorStore 服务、健康检查服务与反射服务（便于 grpcurl 调试）。
//
// 参数：
//   - cfg: gRPC 服务配置
//   - st: 向量存储实例
//   - log: 日志实例
//
// 返回：
//   - *Server: 服务端实例
//   - error: 监听失败时返回错误
func New(cfg config.ServerConfig, st *store.Store, log *slog.Logger) (*Server, error) {
	lis, err := net.Listen("tcp", cfg.Addr())
	if err != nil {
		return nil, fmt.Errorf("监听 %s 失败: %w", cfg.Addr(), err)
	}

	grpcServer := grpc.NewServer()
	vectorstorev1.RegisterVectorStoreServer(grpcServer, NewHandler(st, log))

	// 健康检查：供负载均衡/编排系统探测服务状态
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	healthv1.RegisterHealthServer(grpcServer, healthServer)

	// 服务反射：支持 grpcurl 等工具在运行时查看服务定义
	reflection.Register(grpcServer)

	return &Server{grpc: grpcServer, lis: lis, log: log}, nil
}

// Serve 启动 gRPC 服务并阻塞，直到服务停止
//
// 返回：
//   - error: 服务异常退出时返回错误；调用 Stop 正常停止时返回 nil
func (s *Server) Serve() error {
	s.log.Info("gRPC 服务启动", "addr", s.lis.Addr().String())
	if err := s.grpc.Serve(s.lis); err != nil {
		return fmt.Errorf("gRPC 服务运行失败: %w", err)
	}
	return nil
}

// Stop 优雅停止 gRPC 服务
//
// 停止接受新请求，等待进行中的请求处理完成。
func (s *Server) Stop() {
	s.log.Info("gRPC 服务停止中")
	s.grpc.GracefulStop()
}
