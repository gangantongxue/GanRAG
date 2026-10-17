// Gateway 服务入口
//
// 启动流程：解析启动参数 -> 读取配置 -> 初始化日志 -> 建立下游 gRPC 连接
// -> 启动 HTTP 服务（Hertz 内置信号处理，收到 SIGINT/SIGTERM 后优雅停止）。
//
// 启动示例（在 service/gateway 目录下，需先启动 user 服务）：
//
//	go run ./cmd/server
//	go run ./cmd/server -config configs/config.yaml -env /path/to/.env（缺省自动读仓库根 .env）
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/gangantongxue/GanRAG/pkg/config"
	"github.com/gangantongxue/GanRAG/pkg/logger"
	svcconfig "github.com/gangantongxue/GanRAG/service/gateway/internal/config"
	"github.com/gangantongxue/GanRAG/service/gateway/internal/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "服务启动失败: %v\n", err)
		os.Exit(1)
	}
}

// run 执行服务完整生命周期
//
// 返回：
//   - error: 启动或运行过程中的错误
func run() error {
	// 1. 解析启动参数
	configPath := flag.String("config", "configs/config.yaml", "YAML 配置文件路径")
	envPath := flag.String("env", "", ".env 文件路径（缺省自动加载仓库根 .env，全项目统一）")
	flag.Parse()

	// 2. 读取配置（YAML + 仓库根 .env / 环境变量覆盖，前缀 GANRAG_）
	cfg, err := config.Read[svcconfig.Config](config.Options{
		ConfigPath: *configPath,
		EnvPath:    *envPath,
		EnvPrefix:  "GANRAG",
	})
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	// 3. 初始化日志
	logOpts, err := cfg.LoggerOptions()
	if err != nil {
		return err
	}
	log, err := logger.Init(logOpts)
	if err != nil {
		return err
	}
	defer logger.Close()

	// 4. 建立到 user 服务的 gRPC 连接（懒连接，首次转发时真正建链）
	conn, err := grpc.NewClient(
		cfg.Upstream.User,
		grpc.WithTransportCredentials(insecure.NewCredentials()), // 内部服务间通信，不走 TLS
	)
	if err != nil {
		log.Error("创建下游 gRPC 连接失败", "addr", cfg.Upstream.User, "err", err)
		return err
	}
	defer func() {
		if err := conn.Close(); err != nil {
			log.Error("关闭下游 gRPC 连接失败", "err", err)
		}
	}()

	// 5. 启动 HTTP 服务（Spin 内部处理退出信号与优雅关闭）
	srv := server.New(*cfg, conn, log)
	return srv.Serve()
}
