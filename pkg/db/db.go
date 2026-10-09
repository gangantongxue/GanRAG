// Package db 是 GanRAG 仓库的数据库基础设施包，职责：
//
//  1. SQL 迁移唯一事实来源：migrations/<库名>/ 下 up/down 成对 SQL（golang-migrate 格式）
//  2. 同步执行：Up 将指定库同步到最新迁移版本，Version 查询当前版本（供可修改性检查）
//  3. 连接管理：Open 提供「建库 -> 迁移 -> GORM 连接池」一站式接入，各服务直接复用
//  4. 代码生成数据源：gen 程序连库后用 gorm gen 生成 model / query（见 pkg/db/gen）
//
// 约定：migrations 子目录名 == 数据库名（如 ganrag_user），一个子目录对应一套独立迁移。
// 各服务 import 本包与生成的 query 包使用，不各自手写连接与模型。
package db

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"time"

	gosqlmysql "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Config 数据库连接配置
//
// 字段与各服务 configs/config.yaml 的 database 段一一对应（mapstructure 标签），
// 各服务 internal/config.Config 直接嵌入本结构体复用。
type Config struct {
	Host            string        `mapstructure:"host"`              // 数据库主机（本地127.0.0.1；容器网络内为容器名）
	Port            int           `mapstructure:"port"`              // 数据库端口
	User            string        `mapstructure:"user"`              // 数据库账号
	Password        string        `mapstructure:"password"`          // 数据库密码（建议环境变量覆盖，不落配置文件）
	DBName          string        `mapstructure:"dbname"`            // 库名，必须等于 migrations/ 下的子目录名
	MaxOpenConns    int           `mapstructure:"max_open_conns"`    // 最大打开连接数
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`    // 最大空闲连接数
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"` // 连接最大生命周期
	AutoMigrate     bool          `mapstructure:"auto_migrate"`      // Open 时执行迁移（失败拒绝启动）
}

// dbNameRe 库名合法性（同时作为 CREATE DATABASE 反引号包裹的安全约束）
var dbNameRe = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

// Validate 校验配置的合法性
//
// 返回：
//   - error: 存在非法配置项时返回具体错误，全部合法返回 nil
func (c *Config) Validate() error {
	if c.Host == "" || c.User == "" || c.DBName == "" {
		return fmt.Errorf("database.host / database.user / database.dbname 均不能为空")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("database.port 不合法: %d（应在 1-65535 之间）", c.Port)
	}
	if !dbNameRe.MatchString(c.DBName) {
		return fmt.Errorf("database.dbname 不合法: %q（仅允许字母、数字、下划线）", c.DBName)
	}
	if c.ConnMaxLifetime < 0 {
		return fmt.Errorf("database.conn_max_lifetime 不能为负: %s", c.ConnMaxLifetime)
	}
	return nil
}

// Open 打开指定库的数据库连接（各服务接入入口）
//
// 流程：确保库存在（CREATE DATABASE IF NOT EXISTS）-> AutoMigrate 时执行 migrate.Up
// （失败则拒绝启动，避免带着旧 schema 提供服务）-> 打开 GORM 连接池。
//
// 参数：
//   - cfg: 数据库配置（DBName 必须等于 migrations/ 下的子目录名）
//
// 返回：
//   - *gorm.DB: GORM 连接（调用 Close 释放）
//   - error: 校验、建库、迁移或连接失败时返回错误
func Open(cfg Config) (*gorm.DB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// 1. 确保应用库存在（应用库由迁移首次执行时创建，需账号具备建库权限）
	if err := ensureDatabase(cfg); err != nil {
		return nil, fmt.Errorf("初始化数据库失败: %w", err)
	}

	// 2. 按配置执行迁移
	if cfg.AutoMigrate {
		if err := Up(cfg); err != nil {
			return nil, fmt.Errorf("执行数据库迁移失败: %w", err)
		}
	}

	// 3. 打开 GORM 连接并设置连接池参数
	gdb, err := gorm.Open(gormmysql.New(gormmysql.Config{
		// DSNConfig 由驱动内部 FormatDSN 生成 DSN，密码含特殊字符自动转义
		DSNConfig: &gosqlmysql.Config{
			User:      cfg.User,
			Passwd:    cfg.Password,
			Net:       "tcp",
			Addr:      fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
			DBName:    cfg.DBName,
			Params:    map[string]string{"charset": "utf8mb4"},
			ParseTime: true,
			Loc:       time.Local,
		},
	}), &gorm.Config{
		// 数据库错误（如唯一键冲突）转换为 gorm.ErrDuplicatedKey 等哨兵错误
		TranslateError: true,
		// SQL 日志输出告警及以上；忽略 record not found——未命中（如登录用户不存在）
		// 是正常业务分支，已由 handler 单独记录，避免刷屏
		Logger: gormlogger.New(
			log.New(os.Stdout, "\r\n", log.LstdFlags),
			gormlogger.Config{
				SlowThreshold:             200 * time.Millisecond,
				LogLevel:                  gormlogger.Warn,
				IgnoreRecordNotFoundError: true,
			},
		),
	})
	if err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("获取底层连接失败: %w", err)
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	return gdb, nil
}

// Close 关闭 GORM 连接池
//
// 参数：
//   - gdb: Open 返回的连接
//
// 返回：
//   - error: 关闭失败时返回错误
func Close(gdb *gorm.DB) error {
	sqlDB, err := gdb.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
