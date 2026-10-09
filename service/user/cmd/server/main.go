// User 服务入口
//
// 启动流程：解析启动参数 -> 读取配置 -> 初始化日志 -> 打开数据库（建库 + 迁移 + 连接池）
// -> 启动 gRPC 服务，收到 SIGINT/SIGTERM 后优雅停止。
//
// 启动示例（在 service/user 目录下，需先 task infra 启动 MySQL）：
//
//	go run ./cmd/server
//	go run ./cmd/server -config configs/config.yaml -env .env
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"syscall"

	"github.com/gangantongxue/GanRAG/pkg/config"
	dbpkg "github.com/gangantongxue/GanRAG/pkg/db"
	"github.com/gangantongxue/GanRAG/pkg/logger"
	svcconfig "github.com/gangantongxue/GanRAG/service/user/internal/config"
	"github.com/gangantongxue/GanRAG/service/user/internal/server"
	"github.com/gangantongxue/GanRAG/service/user/internal/store"
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
	envPath := flag.String("env", ".env", ".env 文件路径（文件不存在时忽略）")
	flag.Parse()

	// 2. 读取配置（YAML + 环境变量覆盖，环境变量前缀 GANRAG_）
	cfg, err := config.Read[svcconfig.Config](config.Options{
		ConfigPath: *configPath,
		EnvPath:    existingPath(*envPath),
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

	// 4. 打开数据库：确保库存在 -> 按配置执行迁移（失败拒绝启动）-> 连接池
	gdb, err := dbpkg.Open(cfg.Database)
	if err != nil {
		log.Error("初始化数据库失败", "err", err)
		return err
	}
	defer func() {
		if err := dbpkg.Close(gdb); err != nil {
			log.Error("关闭数据库失败", "err", err)
		}
	}()
	log.Info("数据库初始化完成", "dbname", cfg.Database.DBName, "auto_migrate", cfg.Database.AutoMigrate)

	// 5. 初始化 gRPC 服务
	st := store.New(gdb)
	srv, err := server.New(cfg.Server, st, cfg.JWT, log)
	if err != nil {
		log.Error("初始化 gRPC 服务失败", "err", err)
		return err
	}

	// 6. 启动服务并等待退出信号
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve() }()

	select {
	case err := <-serveErr:
		if err != nil {
			log.Error("gRPC 服务异常退出", "err", err)
			return err
		}
	case <-ctx.Done():
		srv.Stop()
		if err := <-serveErr; err != nil {
			log.Error("gRPC 服务停止异常", "err", err)
			return err
		}
	}

	log.Info("服务已退出")
	return nil
}

// existingPath 返回存在的文件路径，不存在时返回空字符串
//
// pkg/config 对空 EnvPath 会跳过 .env 加载，避免默认 .env 不存在时启动报错。
//
// 参数：
//   - path: 待检查的文件路径
//
// 返回：
//   - string: 文件存在时返回原路径，不存在时返回空字符串
func existingPath(path string) string {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	return path
}
