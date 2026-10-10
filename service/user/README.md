# User 服务

用户管理服务：注册、登录（双 token 签发）、token 轮换、用户信息查询、修改密码。
架构选型与接口设计详见 [docs/architecture.md](docs/architecture.md)（2026-10-09 定稿，同日实现）。

数据库部分（SQL 迁移、同步、gorm gen 生成的 model/query）在仓库全局包 [pkg/db](../../pkg/db/README.md)，
本服务通过 `import pkg/db` 与 `pkg/db/query/ganrag_user` 使用；SQL 修改规范见根 AGENTS.md。

## 运行前提

```bash
# 仓库根目录
cp .env.example .env   # 首次：设置 MySQL 密码
task infra             # 启动 MySQL
task db-up             # 同步迁移（服务配置 auto_migrate=true 时启动也会自动执行）
```

本地运行需在本目录准备 `.env`（已 gitignore）：

```bash
GANRAG_DATABASE_PASSWORD=<与根 .env 的 GANRAG_DB_PASSWORD 一致>
GANRAG_JWT_SECRET=<随机字符串，必填>
```

## Task 命令

本服务的构建与运行由 Task 管理，脚本位于本服务 `scripts/` 目录（各服务独立维护，互不引用）。

```bash
task build                          # 编译到 bin/server-linux-amd64（默认 linux/amd64）
task build OS=windows ARCH=amd64    # 编译指定平台
task build-image                    # 构建镜像 ganrag/user:latest（自动先编译）
task build-image COPY_CONFIG=false  # 构建不含配置的镜像（运行时挂载）
task run                            # 前台启动容器
task run -- --detach                # 后台启动容器
task run -- --detach --mount-config # 用宿主机 configs/config.yaml 覆盖镜像内配置
task stop                           # 停止并删除容器
```

容器内访问 MySQL 需注入网络内主机名：

```bash
task run -- --detach --env GANRAG_DATABASE_HOST=ganrag-mysql --env-file .env
```

在仓库根目录也可以统一操作：

```bash
task build -- user
task build-image -- user
task run -- user --detach
task stop
```

## 接口功能测试

```bash
go test ./... -count=1                 # bufconn gRPC 接口测试（真实协议 + SQLite，无需外部依赖）
./scripts/e2e.sh                       # 真实环境冒烟：真实 MySQL + 真实配置 + TCP，全接口 32 条断言
```

`e2e.sh` 前置：`task infra` 启动 MySQL、仓库根与本目录 `.env` 已配置、已安装 grpcurl；
脚本自行编译并启动服务（50051 端口需空闲），结束自动停止并清理 `e2e_*` 测试用户。

## 本地调试（grpcurl）

```bash
go run ./cmd/server          # 在本目录启动（需 MySQL 已就绪）
grpcurl -plaintext -d '{"username":"demo","password":"password123"}' \
  localhost:50051 user.v1.UserService/Register
grpcurl -plaintext localhost:50051 list   # 查看服务列表（已开反射）
```
