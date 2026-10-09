// gorm gen 代码生成器：先把 SQL 迁移同步到数据库，再从表结构生成 Go 代码
//
// 流程（对 schemata 登记的每个库）：
//  1. db.Open（建库 + migrate.Up 同步到最新，即「SQL 同步到数据库」）
//  2. gorm gen 连库读取白名单表结构
//  3. 生成模型到 pkg/db/model/<库名>、类型化查询到 pkg/db/query/<库名>（提交入库）
//
// 用法：task db-gen（或在仓库根目录 go run ./pkg/db/gen）
// 连接参数：GANRAG_DB_HOST / GANRAG_DB_PORT / GANRAG_DB_USER / GANRAG_DB_PASSWORD，
// 仓库根 .env 兜底（与迁移 CLI 一致）。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/joho/godotenv"
	"gorm.io/gen"
	"gorm.io/gorm"

	"github.com/gangantongxue/GanRAG/pkg/db"
)

// schemaSpec 单个库的生成清单
type schemaSpec struct {
	DBName string   // 库名（== migrations/ 子目录名）
	Tables []string // 待生成的表白名单（与 docs/data-model.md 表结构对应）
}

// schemata 全部待生成的库
//
// 显式白名单避免把 golang-migrate 的 schema_migrations 版本表生成进来；
// migrations/ 下新增库目录时必须在此登记，否则生成报错。
var schemata = []schemaSpec{
	{DBName: "ganrag_user", Tables: []string{"users", "refresh_tokens", "user_follows"}},
	// 未来：ganrag_repository、ganrag_ai 在对应服务落地时登记
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "生成失败:", err)
		os.Exit(1)
	}
}

// run 执行生成主流程：同步迁移 -> 逐库生成 model/query
//
// 返回：
//   - error: 失败时返回错误
func run() error {
	_ = godotenv.Load(".env") // 仓库根 .env（不存在时忽略；不覆盖已导出的环境变量）

	base := db.Config{
		Host: envOr("GANRAG_DB_HOST", "127.0.0.1"),
		Port: envIntOr("GANRAG_DB_PORT", 3306),
		User: envOr("GANRAG_DB_USER", "ganrag"),
		// 密码为空时由 MySQL 侧报错（本地 .env 必须配置 GANRAG_DB_PASSWORD）
		Password:    os.Getenv("GANRAG_DB_PASSWORD"),
		AutoMigrate: true, // 生成前先把 SQL 同步到最新
	}

	// migrations 目录与 schemata 登记一一对应校验（防新增库忘记登记表清单）
	dirs, err := db.Schemas()
	if err != nil {
		return err
	}
	registered := make(map[string]bool, len(schemata))
	for _, s := range schemata {
		registered[s.DBName] = true
	}
	for _, d := range dirs {
		if !registered[d] {
			return fmt.Errorf("迁移目录 %s 未在 pkg/db/gen 的 schemata 中登记表清单", d)
		}
	}

	// 逐库：同步 + 生成
	for _, s := range schemata {
		cfg := base
		cfg.DBName = s.DBName
		gdb, err := db.Open(cfg)
		if err != nil {
			return fmt.Errorf("打开库 %s 失败: %w", s.DBName, err)
		}
		if err := generate(gdb, s); err != nil {
			_ = db.Close(gdb)
			return fmt.Errorf("生成库 %s 失败: %w", s.DBName, err)
		}
		if err := db.Close(gdb); err != nil {
			return err
		}
		fmt.Printf("库 %s: 已生成 %d 张表 -> pkg/db/model/%s, pkg/db/query/%s\n",
			s.DBName, len(s.Tables), s.DBName, s.DBName)
	}
	return nil
}

// generate 对单个库执行 gorm gen
//
// 参数：
//   - gdb: 已同步的数据库连接
//   - s: 库的生成清单
//
// 返回：
//   - error: 生成失败时返回错误
func generate(gdb *gorm.DB, s schemaSpec) error {
	g := gen.NewGenerator(gen.Config{
		OutPath:       filepath.Join("pkg/db", "query", s.DBName), // 类型化查询代码
		ModelPkgPath:  filepath.Join("pkg/db", "model", s.DBName), // 模型代码
		Mode:          gen.WithDefaultQuery,                       // 生成 query.Use 与默认 Q
		FieldNullable: true,                                       // 可空列生成指针类型（如 refresh_tokens.revoked_at）
	})
	g.UseDB(gdb)

	models := make([]any, 0, len(s.Tables))
	for _, t := range s.Tables {
		models = append(models, g.GenerateModel(t))
	}
	g.ApplyBasic(models...)
	g.Execute() // 失败时 gen 内部 panic
	return nil
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
