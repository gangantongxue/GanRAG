# User 服务架构选型

## 概述

账号生命周期管理服务，对内暴露 gRPC 接口，数据持久化到 MySQL。

负责：注册、登录（双 token 签发）、token 轮换、用户信息查询、修改密码。
不负责：access token 校验（由 Gateway 负责）、限流（由 Gateway 负责）、面向前端的 HTTP 接口（由 Gateway 转发）。

## 技术选型

### 数据库

**选型：MySQL + GORM + golang-migrate**（与项目总体架构文档一致）

- MySQL：主数据存储，账号属持久核心数据
- GORM：模型定义、连接池管理、迁移钩子
- golang-migrate：SQL up/down 成对迁移，版本可回滚
- 详细选型理由见 [docs/architecture.md](../../../docs/architecture.md)

### 密码存储

**选型：bcrypt（golang.org/x/crypto/bcrypt）**

- 加盐哈希：天然抗彩虹表
- 内置 cost：计算可控（默认 10）
- 行业标准：无需自管密钥与迭代参数

### Token 方案

**选型：双 token（access JWT + refresh 不透明令牌）**

- access token：JWT（golang-jwt/jwt/v5，HS256），短期有效，Gateway 用共享密钥本地校验，**不回源 user 服务**
- refresh token：随机不透明串，SHA-256 哈希后存 MySQL，支持轮换与吊销

选型原因见下文「认证设计」。

### 通信协议

**选型：gRPC**

- 与其余内部服务一致，Protobuf 强类型接口

## 职责

- 用户注册（用户名唯一性校验、密码哈希存储）
- 用户登录（校验凭证、签发双 token）
- refresh token 轮换（每次刷新签发新 token，旧 token 作废）
- 用户信息查询（供 Gateway / 其他服务按 ID 查询）
- 修改密码（校验旧密码，成功后吊销该用户全部 refresh token）

**明确不做**（YAGNI，需求出现再加）：

- 第三方登录（OAuth）
- 找回 / 重置密码
- 用户列表、禁用 / 删除等管理接口
- 会话黑名单（access token 短期有效，不引入 Redis）
- 用户名查重以外的搜索能力

## 认证设计（双 token）

### 设计决策

| 决策点 | 结论 | 原因 |
|--------|------|------|
| 签发方 | user 服务签发，Gateway 校验 | 账号凭证的签发与校验分离：密钥由 user（签发）与 Gateway（校验）通过共享配置持有，user 不感知请求链路 |
| access token | JWT HS256，有效期 **15 分钟** | Gateway 本地验签零回源；短期限制泄露窗口 |
| refresh token | 32 字节随机数（base64url），有效期 **7 天**，DB 只存 SHA-256 哈希 | 不可伪造、可吊销；哈希存储即使库泄密也无法直接使用 |
| 刷新策略 | **轮换（rotation）**：Refresh 时签发新 access + 新 refresh，旧 refresh 标记 `revoked_at` 作废（行保留至其自然过期） | 行保留才能识别「重用已作废 token」；若直接删除则哈希行消失，无法区分伪造与重用 |
| 重用检测 | 若收到已作废的 refresh token，视为泄露，吊销该用户全部 refresh token | 强制该用户重新登录 |
| 改密行为 | 修改密码成功后吊销该用户全部 refresh token | 已登录会话在 access 过期（≤15 分钟）后需重新登录 |
| 不用 Redis | refresh 落 MySQL，不引入 Redis | 单机账号规模下 DB 完全够用，避免为 token 提前引入基础设施 |

### Token 流转

```
注册/登录 ──user──▶ { access(15m), refresh(7d) }
   │
   ▼ 前端携带 access
Gateway ──本地验签（共享密钥）──▶ 通过则转发，user_id 注入请求上下文
   │
   ▼ access 过期（401）
前端调 /auth/refresh ──Gateway 转发（无需 access）──▶ user.Refresh(refresh)
   │                                              └─ 轮换：旧 refresh 作废，返回新对
   ▼
重用已作废 refresh ──▶ 吊销该用户全部 refresh，返回 Unauthenticated
```

- refresh 请求**必须绕过 access 校验**：Gateway 的 `/auth/refresh` 路由不做认证，直接转发
- access token 中携带 `sub`（用户 ID）与 `username`；Gateway 解析后将 `user_id` 透传给下游服务
- 用户禁用（`status=0`）后，refresh 与新登录均拒绝；access 在其自然过期前仍可通过验签（15 分钟窗口可接受）

## 接口设计

Proto 文件位置：`api/proto/user/v1/user.proto`（生成代码到 `api/gen/user/v1`，修改后执行 `task proto`）。
服务名草案中的 `service User` 与 `message User` 同名（proto3 禁止），已定为 `service UserService`。

### Proto 草案

```protobuf
syntax = "proto3";

package user.v1;

option go_package = "github.com/gangantongxue/GanRAG/api/gen/user/v1;userv1";

service UserService {
  rpc Register(RegisterRequest) returns (RegisterResponse);
  rpc Login(LoginRequest) returns (LoginResponse);
  rpc Refresh(RefreshRequest) returns (RefreshResponse);
  rpc GetUser(GetUserRequest) returns (GetUserResponse);
  rpc UpdatePassword(UpdatePasswordRequest) returns (UpdatePasswordResponse);
}

message User {
  int64  id = 1;         // 用户 ID（自增主键）
  string username = 2;   // 用户名（唯一）
  string nickname = 3;   // 昵称（首期默认等于用户名）
  int32  status = 4;     // 1 正常 0 禁用
  int64  created_at_unix = 5;
  int64  updated_at_unix = 6;
}

message RegisterRequest {
  string username = 1;   // 3-32 字符，字母数字与 _ -
  string password = 2;   // 8-64 字符
}
message RegisterResponse { User user = 1; }   // 不返回 token，注册成功后前端走登录

message LoginRequest {
  string username = 1;
  string password = 2;
}
message LoginResponse {
  User   user = 1;
  string access_token = 2;   // JWT，15 分钟
  string refresh_token = 3;  // 不透明串，7 天
  int64  access_expires_in = 4;   // access 剩余秒数
  int64  refresh_expires_in = 5;  // refresh 剩余秒数
}

message RefreshRequest  { string refresh_token = 1; }
message RefreshResponse {
  string access_token = 1;
  string refresh_token = 2;
  int64  access_expires_in = 3;
  int64  refresh_expires_in = 4;
}

message GetUserRequest { int64 id = 1; }
message GetUserResponse { User user = 1; }

message UpdatePasswordRequest {
  int64  user_id = 1;      // 由 Gateway 从 access token 解析后注入，不信任请求体
  string old_password = 2;
  string new_password = 3; // 8-64 字符
}
message UpdatePasswordResponse {}
```

### 约束与错误码

| RPC | 约束 | 错误码 |
|-----|------|--------|
| Register | 用户名 3-32 字符（`[a-zA-Z0-9_-]`）、密码 8-64 字符 | `InvalidArgument` 参数不合法；`AlreadyExists` 用户名已存在 |
| Login | 用户不存在与密码错误**统一返回** `Unauthenticated` | `Unauthenticated`；`FailedPrecondition` 用户被禁用 |
| Refresh | token 过期 / 作废 / 重用 | `Unauthenticated`（重用场景同时吊销该用户全部 refresh） |
| GetUser | 目标不存在 | `NotFound` |
| UpdatePassword | user_id 缺失、新密码不合法 | `InvalidArgument`；`Unauthenticated` 旧密码错误或用户不存在（user_id 由 Gateway 从 token 注入，用户不存在说明凭证陈旧，与防枚举策略一致，不返回 `NotFound`） |

- Login 不区分「用户不存在」与「密码错误」，防止用户名枚举
- 所有接口错误细节只写日志，不回传内部信息（与 vector-store / file-store 约定一致）

## 数据存储

### 迁移文件位置

`pkg/db/migrations/ganrag_user/`（数据库基础设施集中于全局包 `pkg/db`，遵循「每个服务一个 schema、一套独立迁移」，库名即子目录名）：

```
pkg/db/migrations/ganrag_user/
├── 0001_users.up.sql
├── 0001_users.down.sql
├── 0002_refresh_tokens.up.sql
├── 0002_refresh_tokens.down.sql
├── 0003_user_follows.up.sql
└── 0003_user_follows.down.sql
```

**执行方式**：由 `pkg/db` 执行 `migrate.Up`（服务启动时按配置 `database.auto_migrate`，默认 `true`，迁移失败则拒绝启动；亦可用 `task db-up` 手动同步）。所有服务共用一个 MySQL 实例、各自独立的 migration 子目录。修改 SQL 前必须先 `task db-status` 查看当前版本（见根 AGENTS.md「SQL 迁移修改规范」）。

### 表结构

```sql
-- 0001_users：用户账号
CREATE TABLE users (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  username      VARCHAR(64)  NOT NULL,
  password_hash VARCHAR(100) NOT NULL,           -- bcrypt 结果
  nickname      VARCHAR(64)  NOT NULL DEFAULT '',
  status        TINYINT      NOT NULL DEFAULT 1, -- 1 正常 0 禁用
  created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_users_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 0002_refresh_tokens：refresh token（只存哈希）
CREATE TABLE refresh_tokens (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id    BIGINT UNSIGNED NOT NULL,
  token_hash CHAR(64)        NOT NULL,  -- SHA-256 hex（明文 refresh 的哈希）
  expires_at DATETIME        NOT NULL,
  revoked_at DATETIME        NULL,      -- 轮换/吊销时间；NULL = 活跃，非 NULL = 已作废（重用检测信号）
  created_at DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_refresh_token_hash (token_hash),
  KEY idx_refresh_tokens_user_id (user_id),
  CONSTRAINT fk_refresh_tokens_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

### 读写行为

- **登录 / 刷新**：单条主键或唯一索引查询，无慢路径
- **refresh 轮换**：同事务内标记旧记录作废（`revoked_at` 置为当前时间，条件 `revoked_at IS NULL` 保证原子性）+ 插入新记录；已作废行保留至其自然过期，用于重用检测（直接删除则无法区分伪造与重用）
- **过期清理**：登录 / 刷新时顺带删除该用户已过期记录（惰性清理，无需定时任务）
- **改密**：单事务内更新 `users.password_hash` + 删除该用户全部 refresh 记录

## 配置

`configs/config.yaml`：

```yaml
server:
  host: "0.0.0.0"   # gRPC 监听地址
  port: 50051       # gRPC 监听端口

database:
  host: "127.0.0.1"
  port: 3306
  user: "ganrag"
  password: ""            # 建议用 GANRAG_DATABASE_PASSWORD 环境变量覆盖，不落配置文件
  dbname: "ganrag_user"
  max_open_conns: 20
  max_idle_conns: 5
  conn_max_lifetime: "30m"
  auto_migrate: true      # 启动时执行 migrate.Up

jwt:
  secret: ""              # 必填，建议 GANRAG_JWT_SECRET 环境变量注入（.env 已 gitignore）
  issuer: "ganrag"
  access_ttl: "15m"
  refresh_ttl: "168h"     # 7 天

log:
  output: "both"          # console | file | both
  level: "info"           # debug | info | warn | error
  format: "text"          # json | text
  directory: "./logs"
```

- 配置由公共包 `pkg/config` 读取，所有配置项可用 `GANRAG_` 前缀环境变量覆盖（嵌套用下划线）
- `jwt.secret` 为空时启动报错——不做硬编码默认值

## 基础设施：MySQL 部署

**方案：仓库根目录 `docker-compose.yml` 统一编排**（不放 user 服务内，MySQL 是 user / repository 等多个服务共享的基础设施）：

```yaml
services:
  mysql:
    image: mysql:8.0
    container_name: ganrag-mysql
    # 端口 3306、root/应用密码走环境变量（.env，已 gitignore）
    # 数据卷 mysql-data 持久化，healthcheck: mysqladmin ping
    # 加入 ganrag 网络，应用侧用容器名 ganrag-mysql 访问
```

- 根 `Taskfile.yml` 新增任务：`task infra`（启动）、`task infra-down`（停止）；compose 与各服务脚本相互独立
- **Redis 暂不引入**：其预期用途（限流、会话黑名单、缓存）当前均无落地场景，等需求明确再加入 compose
- 应用库 `ganrag_user`（后续 repository 服务建 `ganrag_repository`）由迁移首次执行时创建

## 端口规划（全局）

| 服务 | 端口 | 协议 | 状态 |
|------|------|------|------|
| gateway | 8080 | HTTP | 未实现 |
| user | 50051 | gRPC | 已实现（2026-10-09） |
| repository | 50052 | gRPC | 未实现 |
| vector-store | 50053 | gRPC | 已实现 |
| file-store | 50054 | gRPC | 已实现 |
| ai | 50055 | gRPC | 未实现 |

## 目录结构

```
service/user/
├── cmd/server/              # 服务入口（配置加载、日志初始化、数据库与迁移、优雅退出）
├── configs/config.yaml      # 默认配置（database 段结构由 pkg/db.Config 定义）
├── docs/architecture.md     # 本文档
├── internal/
│   ├── config/              # 配置结构、校验（jwt.secret 必填校验，database 复用 pkg/db.Config）
│   ├── store/               # 用户与 refresh token 业务查询（基于 pkg/db 生成的 query 包）
│   ├── auth/                # bcrypt 哈希校验、JWT 签发、refresh token 生成与哈希
│   └── server/              # gRPC handler 与错误码映射、服务生命周期
└── scripts/                 # 本服务构建与运行脚本（沿用既有约定）
```

数据库相关（表结构 SQL、迁移执行、gorm gen 生成的 model/query）统一在仓库全局包 `pkg/db/`，本服务通过 `import pkg/db` 与 `pkg/db/query/ganrag_user` 使用。

## 实现状态

> 已实现（2026-10-09）：proto、配置、迁移（0001-0003，位于 `pkg/db/migrations/ganrag_user/`）、
> store/auth/handler、单元测试（SQLite）与端到端冒烟（grpcurl）均完成；`Taskfile.yml` 端口已填 50051。
