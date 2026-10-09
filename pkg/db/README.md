# pkg/db — 数据库基础设施包

GanRAG 的数据库唯一入口：**SQL 迁移唯一事实来源 + 同步执行 + gorm gen 代码生成 + 连接管理**。
各服务不各自手写连接与模型，统一 import 本包与生成的 query 包。

## 数据流

```
SQL 迁移（migrations/<库名>/，up/down 成对）
        │  task db-up（golang-migrate up）
        ▼
     MySQL（task infra 启动，库不存在时自动创建）
        │  task db-gen（gorm gen 连库读表结构）
        ▼
model/<库名>（模型）+ query/<库名>（类型化查询）──提交入库──▶ 各服务 import 使用
```

## 目录

```
pkg/db/
├── migrations/<库名>/     # SQL 迁移唯一事实来源（NNNN_name.up.sql / .down.sql 成对）
├── cmd/migrate/           # 迁移 CLI：up（同步）/ status（版本与可修改性检查）
├── gen/                   # gorm gen 生成器（同步后生成 model/query）
├── model/<库名>/          # 生成的模型（*.gen.go，提交入库）
├── query/<库名>/          # 生成的类型化查询（*.gen.go，提交入库）
├── db.go                  # Config 配置定义 + Open（建库→迁移→连接池）/ Close
└── migrate.go             # Up / Version / Exists / Schemas 等迁移操作
```

**约定：migrations 子目录名 == 数据库名（库名），一个子目录对应一套独立迁移**，
与 [docs/data-model.md](../../docs/data-model.md)「每个服务一个 schema」一致。

## Task 命令（仓库根目录）

```bash
task infra        # 启动 MySQL（docker compose，连接参数见 .env.example）
task db-up        # 将全部库同步到最新迁移（幂等）
task db-status    # 查看各库当前版本与「可修改 / 已应用锁定」的 SQL 文件
task db-gen       # 同步迁移后生成 model/query 代码
task infra-down   # 停止 MySQL（数据卷保留）
```

连接参数：仓库根 `.env`（`GANRAG_DB_USER` / `GANRAG_DB_PASSWORD`，可选 `GANRAG_DB_HOST` / `GANRAG_DB_PORT`）。

## SQL 修改规范（重要）

> 修改任何 SQL 迁移脚本前，**必须先执行 `task db-status` 查看当前数据库版本**。
>
> - **已应用**（版本 ≤ 当前版本）的迁移文件**禁止修改**，结构变更一律新增迁移文件
> - **可修改**（版本 > 当前版本，尚未应用）的文件才可以调整内容
> - 改完 SQL 后执行 `task db-up` 同步、`task db-gen` 重新生成并提交生成代码

完整规范见根 [AGENTS.md](../../AGENTS.md)「SQL 迁移修改规范」。

## 各服务接入方式

```go
import (
    dbpkg "github.com/gangantongxue/GanRAG/pkg/db"
    "github.com/gangantongxue/GanRAG/pkg/db/query/ganrag_user"
)

// 配置：服务 configs/config.yaml 的 database 段直接使用 dbpkg.Config（mapstructure 标签齐备）
gdb, err := dbpkg.Open(cfg.Database) // 建库 → migrate.Up（auto_migrate）→ 连接池
defer dbpkg.Close(gdb)

q := ganrag_user.Use(gdb) // 类型化查询
```

- 服务配置结构体中声明 `Database dbpkg.Config` 即可复用全部数据库配置与校验
- `Open` 在 `auto_migrate: true` 时执行迁移，失败拒绝启动（避免带着旧 schema 提供服务）

## 新增 schema 的步骤

1. `migrations/<新库名>/` 建目录，按 `NNNN_name.up.sql` / `.down.sql` 写迁移
2. `gen/main.go` 的 `schemata` 登记库名与表白名单（防止误生成 `schema_migrations`）
3. `task db-up && task db-gen`
4. 对应服务接入 `dbpkg.Open`，`configs/config.yaml` 配置 `dbname: <新库名>`
