package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	gosqlmysql "github.com/go-sql-driver/mysql" // init 注册 database/sql 的 mysql 驱动
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/mysql" // init 注册 migrate 的 mysql 驱动（按 URL scheme 查找）
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// migrationsFS 内嵌的全部迁移文件（migrations/<库名>/*.sql）
//
//go:embed migrations
var migrationsFS embed.FS

// Schemas 返回全部待管理的库名（migrations/ 下的子目录名，即库名）
//
// 返回：
//   - []string: 按名称排序的库名列表
//   - error: 读取迁移目录失败时返回错误
func Schemas() ([]string, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("读取迁移目录失败: %w", err)
	}
	schemas := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			schemas = append(schemas, e.Name())
		}
	}
	sort.Strings(schemas)
	return schemas, nil
}

// MigrationFiles 返回指定库的迁移文件名列表（仅 .up.sql，按版本排序）
//
// 参数：
//   - schema: 库名（migrations/ 下的子目录名）
//
// 返回：
//   - []string: 文件名列表，如 0001_users.up.sql
//   - error: 目录不存在时返回错误
func MigrationFiles(schema string) ([]string, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations/"+schema)
	if err != nil {
		return nil, fmt.Errorf("读取库 %s 的迁移文件失败: %w", schema, err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	return files, nil
}

// Up 将指定库同步到最新迁移版本（幂等，无变更时直接返回）
//
// 参数：
//   - cfg: 数据库配置（先确保库存在，再对 migrations/<dbname> 执行 migrate.Up）
//
// 返回：
//   - error: 建库或迁移失败时返回错误
func Up(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := ensureDatabase(cfg); err != nil {
		return err
	}
	m, err := newMigrate(cfg)
	if err != nil {
		return err
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("迁移 %s 失败: %w", cfg.DBName, err)
	}
	return nil
}

// Version 查询指定库的当前迁移版本
//
// 参数：
//   - cfg: 数据库配置
//
// 返回：
//   - int64: 当前版本（取迁移文件序号，如 0002）；库不存在或尚未迁移时为 0
//   - bool: 迁移是否处于 dirty 状态（上次迁移中断，需人工处理）
//   - error: 查询失败时返回错误
func Version(cfg Config) (int64, bool, error) {
	if err := cfg.Validate(); err != nil {
		return 0, false, err
	}
	exists, err := Exists(cfg)
	if err != nil {
		return 0, false, err
	}
	if !exists {
		return 0, false, nil // 库未创建：视为版本 0（全部迁移可修改）
	}

	m, err := newMigrate(cfg)
	if err != nil {
		return 0, false, err
	}
	defer m.Close()

	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil // 库存在但尚未执行任何迁移
	}
	if err != nil {
		return 0, false, err
	}
	return int64(v), dirty, nil
}

// Exists 检查指定库是否已创建
//
// 参数：
//   - cfg: 数据库配置
//
// 返回：
//   - bool: 库存在返回 true
//   - error: 查询失败（如连接失败）时返回错误
func Exists(cfg Config) (bool, error) {
	db, err := sql.Open("mysql", mysqlServerDSN(cfg))
	if err != nil {
		return false, err
	}
	defer db.Close()

	var name string
	err = db.QueryRow(
		"SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?", cfg.DBName,
	).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("查询库 %s 是否存在失败: %w", cfg.DBName, err)
	}
	return true, nil
}

// ensureDatabase 连接 MySQL 服务端并确保应用库存在
//
// 应用库由迁移首次执行时创建，需要账号具备建库权限。
func ensureDatabase(cfg Config) error {
	db, err := sql.Open("mysql", mysqlServerDSN(cfg))
	if err != nil {
		return err
	}
	defer db.Close()

	// 库名已通过 Validate 约束为 [a-zA-Z0-9_]+，反引号包裹防关键字冲突
	query := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", cfg.DBName)
	if _, err := db.Exec(query); err != nil {
		return fmt.Errorf("创建数据库 %s 失败: %w", cfg.DBName, err)
	}
	return nil
}

// mysqlServerDSN 返回不带库名的服务端 DSN（用于建库与存在性检查）
//
// 由 go-sql-driver 的 Config.FormatDSN 生成，密码含特殊字符时自动转义。
func mysqlServerDSN(cfg Config) string {
	return (&gosqlmysql.Config{
		User:      cfg.User,
		Passwd:    cfg.Password,
		Net:       "tcp",
		Addr:      fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		ParseTime: true,
		Loc:       time.Local,
	}).FormatDSN()
}

// newMigrate 创建 golang-migrate 实例（库名对应 migrations/<dbname> 子目录）
//
// 迁移源用 go:embed 嵌入二进制，运行时不依赖外部文件路径；
// DSN 形如 mysql://user:pass@tcp(host:port)/dbname——migrate 去掉前缀后
// 交给 go-sql-driver 的 ParseDSN 解析，故用 FormatDSN 生成（它同时处理特殊字符）。
func newMigrate(cfg Config) (*migrate.Migrate, error) {
	sub, err := fs.Sub(migrationsFS, "migrations/"+cfg.DBName)
	if err != nil {
		return nil, fmt.Errorf("库 %s 没有对应的迁移目录: %w", cfg.DBName, err)
	}
	src, err := iofs.New(sub, ".")
	if err != nil {
		return nil, fmt.Errorf("加载迁移文件失败: %w", err)
	}
	dsn := "mysql://" + (&gosqlmysql.Config{
		User:      cfg.User,
		Passwd:    cfg.Password,
		Net:       "tcp",
		Addr:      fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		DBName:    cfg.DBName,
		ParseTime: true,
	}).FormatDSN()
	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// MigrationVersion 解析迁移文件名中的版本序号（如 0001_users.up.sql -> 1）
//
// 参数：
//   - name: 迁移文件名
//
// 返回：
//   - int64: 版本序号；文件名不合法返回 -1
func MigrationVersion(name string) int64 {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return -1
	}
	v, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil {
		return -1
	}
	return v
}
