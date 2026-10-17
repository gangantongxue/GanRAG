// User 服务入口
//
// 启动流程：解析启动参数 -> 读取配置 -> 初始化日志 -> 打开数据库（建库 + 迁移 + 连接池）
// -> 启动 gRPC 服务，收到 SIGINT/SIGTERM 后优雅停止。
//
// 启动示例（在 service/user 目录下，需先 task infra 启动 MySQL）：
//
//	go run ./cmd/server
//	go run ./cmd/server -config configs/config.yaml -env /path/to/.env（缺省自动读仓库根 .env）
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gangantongxue/GanRAG/pkg/config"
	dbpkg "github.com/gangantongxue/GanRAG/pkg/db"
	"github.com/gangantongxue/GanRAG/pkg/logger"
	redispkg "github.com/gangantongxue/GanRAG/pkg/redis"
	"github.com/gangantongxue/GanRAG/service/user/internal/auth"
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

	// 5. 连接 Redis：邮箱验证码等缓存数据（探活失败拒绝启动）
	rdb, err := redispkg.Open(cfg.Redis)
	if err != nil {
		log.Error("初始化 Redis 失败", "err", err)
		return err
	}
	defer func() {
		if err := redispkg.Close(rdb); err != nil {
			log.Error("关闭 Redis 失败", "err", err)
		}
	}()
	log.Info("Redis 初始化完成", "host", cfg.Redis.Host, "port", cfg.Redis.Port)

	// 6. 初始化 gRPC 服务
	st := store.New(gdb)
	srv, err := server.New(cfg.Server, st, cfg.JWT, auth.NewEmailCode(rdb), auth.NewMailer(cfg.Mail, log), log)
	if err != nil {
		log.Error("初始化 gRPC 服务失败", "err", err)
		return err
	}

	// 7. 启动服务并等待退出信号
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
