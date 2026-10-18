// Package server 提供 Repository 的 gRPC 服务端封装
package server

import (
	"fmt"
	"log/slog"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	repositoryv1 "github.com/gangantongxue/GanRAG/api/gen/repository/v1"
	"github.com/gangantongxue/GanRAG/pkg/asynctask"
	"github.com/gangantongxue/GanRAG/service/repository/internal/cleanup"
	"github.com/gangantongxue/GanRAG/service/repository/internal/config"
	"github.com/gangantongxue/GanRAG/service/repository/internal/objectstore"
	"github.com/gangantongxue/GanRAG/service/repository/internal/store"
)

// Server 封装 gRPC 服务端及其监听器
type Server struct {
	grpc *grpc.Server
	lis  net.Listener
	log  *slog.Logger
}

// New 创建并初始化 gRPC 服务端
func New(cfg config.ServerConfig, st *store.Store, objects *objectstore.Store, tasks *asynctask.Store, log *slog.Logger, maxWholeSize int64) (*Server, error) {
	lis, err := net.Listen("tcp", cfg.Addr())
	if err != nil {
		return nil, fmt.Errorf("监听 %s 失败: %w", cfg.Addr(), err)
	}

	// 整段上传消息上限与配置对齐，至少保留 gRPC 默认 4MB
	recvSize := max(4*1024*1024, int(maxWholeSize)+1024*1024)
	grpcServer := grpc.NewServer(grpc.MaxRecvMsgSize(recvSize))
	handler := NewHandler(st, objects, cleanup.New(tasks, objects, log), log, maxWholeSize)
	repositoryv1.RegisterRepositoryServiceServer(grpcServer, handler)

	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	healthv1.RegisterHealthServer(grpcServer, healthServer)

	reflection.Register(grpcServer)

	return &Server{grpc: grpcServer, lis: lis, log: log}, nil
}

// Serve 启动 gRPC 服务并阻塞，直到服务停止
func (s *Server) Serve() error {
	s.log.Info("gRPC 服务启动", "addr", s.lis.Addr().String())
	if err := s.grpc.Serve(s.lis); err != nil {
		return fmt.Errorf("gRPC 服务运行失败: %w", err)
	}
	return nil
}

// Stop 优雅停止 gRPC 服务
func (s *Server) Stop() {
	s.log.Info("gRPC 服务停止中")
	s.grpc.GracefulStop()
}
