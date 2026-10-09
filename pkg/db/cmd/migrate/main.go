// 迁移 CLI：同步数据库（up）与查看版本及 SQL 文件可修改性（status）
//
// 用法（由根 Taskfile 调用，也可在仓库根目录直接 go run）：
//
//	go run ./pkg/db/cmd/migrate up      # 将全部库同步到最新迁移（幂等）
//	go run ./pkg/db/cmd/migrate status  # 查看各库当前版本与「可修改 / 已应用锁定」的迁移文件
//
// 连接参数：优先级为命令行标志 > 环境变量 > 仓库根 .env 文件。
// 环境变量：GANRAG_DB_HOST / GANRAG_DB_PORT / GANRAG_DB_USER / GANRAG_DB_PASSWORD。
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"

	"github.com/gangantongxue/GanRAG/pkg/db"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

// run 执行 CLI 主流程
//
// 参数：
//   - args: 命令行参数（子命令 + 标志）
//
// 返回：
//   - error: 执行失败时返回错误
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: migrate <up|status> [-env .env] [-host] [-port] [-user] [-password]")
	}
	sub, args := args[0], args[1:]

	// 先加载仓库根 .env（不存在时忽略；godotenv 不覆盖已导出的环境变量），
	// 保证下方标志的默认值能读到 .env 中的连接参数
	_ = godotenv.Load(preScanEnvPath(args))

	fs := flag.NewFlagSet(sub, flag.ContinueOnError)
	fs.String("env", ".env", ".env 文件路径（不存在时忽略；加载发生在正式解析前，见 preScanEnvPath）")
	host := fs.String("host", envOr("GANRAG_DB_HOST", "127.0.0.1"), "MySQL 主机")
	port := fs.Int("port", envIntOr("GANRAG_DB_PORT", 3306), "MySQL 端口")
	user := fs.String("user", envOr("GANRAG_DB_USER", "ganrag"), "MySQL 账号")
	password := fs.String("password", os.Getenv("GANRAG_DB_PASSWORD"), "MySQL 密码")
	if err := fs.Parse(args); err != nil {
		return err
	}

	schemas, err := db.Schemas()
	if err != nil {
		return err
	}

	switch sub {
	case "up":
		return cmdUp(schemas, *host, *port, *user, *password)
	case "status":
		return cmdStatus(schemas, *host, *port, *user, *password)
	default:
		return fmt.Errorf("未知子命令 %q（可用 up | status）", sub)
	}
}

// cmdUp 将全部库同步到最新迁移版本
func cmdUp(schemas []string, host string, port int, user, password string) error {
	for _, schema := range schemas {
		cfg := baseConfig(host, port, user, password, schema)
		if err := db.Up(cfg); err != nil {
			return err
		}
		v, dirty, err := db.Version(cfg)
		if err != nil {
			return err
		}
		if dirty {
			return fmt.Errorf("库 %s 迁移处于 dirty 状态，需人工处理（migrate force）", schema)
		}
		fmt.Printf("库 %-18s 已同步到版本 %04d\n", schema, v)
	}
	return nil
}

// cmdStatus 输出各库当前版本与迁移文件的可修改性划分
//
// 规则（项目规范）：已应用（版本 <= 当前版本）的迁移文件禁止修改；
// 尚未应用的文件可以修改。库未创建或未迁移时全部可修改。
func cmdStatus(schemas []string, host string, port int, user, password string) error {
	for _, schema := range schemas {
		cfg := baseConfig(host, port, user, password, schema)
		v, dirty, err := db.Version(cfg)
		if err != nil {
			return fmt.Errorf("查询库 %s 版本失败: %w（MySQL 是否已启动：task infra）", schema, err)
		}
		files, err := db.MigrationFiles(schema)
		if err != nil {
			return err
		}

		fmt.Printf("库 %s（当前版本 %04d）", schema, v)
		if dirty {
			fmt.Printf(" [dirty：上次迁移中断，先人工处理再操作]")
		}
		fmt.Println()
		for _, st := range classify(files, v) {
			if st.Editable {
				fmt.Printf("  [可修改]        %s\n", st.Name)
			} else {
				fmt.Printf("  [已应用·禁止修改] %s\n", st.Name)
			}
		}
	}
	return nil
}

// fileState 单个迁移文件的可修改性状态
type fileState struct {
	Name     string // 文件名，如 0001_users.up.sql
	Version  int64  // 文件名中的版本序号
	Editable bool   // 是否可修改（尚未应用）
}

// classify 按当前数据库版本划分迁移文件的可修改性
//
// 参数：
//   - files: 迁移文件名列表
//   - current: 当前数据库版本（0 = 库未创建或未迁移）
//
// 返回：
//   - []fileState: 与输入同序的状态列表
func classify(files []string, current int64) []fileState {
	states := make([]fileState, 0, len(files))
	for _, name := range files {
		v := db.MigrationVersion(name)
		states = append(states, fileState{
			Name:     name,
			Version:  v,
			Editable: v > current || v < 0, // 版本号无法解析的异常文件也提示可检查
		})
	}
	return states
}

// baseConfig 组装单个库的连接配置
func baseConfig(host string, port int, user, password, schema string) db.Config {
	return db.Config{
		Host:     host,
		Port:     port,
		User:     user,
		Password: password,
		DBName:   schema, // 库名 == migrations/ 子目录名
	}
}

// preScanEnvPath 从参数中预扫描 -env 标志的值（用于在正式解析前加载 .env）
//
// 参数：
//   - args: 命令行参数
//
// 返回：
//   - string: -env 指定的路径，未指定返回 ".env"
func preScanEnvPath(args []string) string {
	for i, a := range args {
		if a == "-env" && i+1 < len(args) {
			return args[i+1]
		}
		if p, ok := strings.CutPrefix(a, "-env="); ok {
			return p
		}
	}
	return ".env"
}

// envOr 读取环境变量，空值返回默认值
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envIntOr 读取整型环境变量，空值或非法返回默认值
func envIntOr(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
