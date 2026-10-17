# User 服务架构选型

## 概述

账号生命周期管理服务，对内暴露 gRPC 接口，数据持久化到 MySQL。

负责：注册（邮箱+验证码）、登录（邮箱+密码，双 token 签发）、token 轮换、用户信息查询、修改密码、找回密码（「忘记密码接口」）、登录设备管理（「登录设备管理接口」）、邮箱验证码发送与校验（Redis，「Redis 与邮箱验证码」）。
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
- refresh token：随机不透明串，SHA-256 哈希后存 MySQL `sessions` 表（每设备一行），支持轮换与吊销

选型原因见下文「认证设计」。

### 通信协议

**选型：gRPC**

- 与其余内部服务一致，Protobuf 强类型接口

## 职责

- 用户注册（邮箱验证码校验，email 唯一）
- 用户登录（邮箱+密码校验凭证，签发双 token，按设备建立会话）
- refresh token 轮换（每次刷新原地更新会话 token_hash）
- 用户信息查询（供 Gateway / 其他服务按 ID 查询）
- 修改密码（校验旧密码，成功后吊销该用户全部会话）
- 找回密码（邮箱验证码重置，成功后吊销该用户全部会话）——「忘记密码接口」
- 登录设备管理（设备列表 / 踢设备下线）——「登录设备管理接口」

**明确不做**（YAGNI，需求出现再加）：

- 第三方登录（OAuth）
- 用户列表、禁用 / 删除等管理接口
- 会话黑名单（access token 短期有效，自然过期即可）
- 邮箱查重以外的搜索能力

## 认证设计（双 token）

### 设计决策

| 决策点 | 结论 | 原因 |
|--------|------|------|
| 签发方 | user 服务签发，Gateway 校验 | 账号凭证的签发与校验分离：密钥由 user（签发）与 Gateway（校验）通过共享配置持有，user 不感知请求链路 |
| access token | JWT HS256，有效期 **15 分钟** | Gateway 本地验签零回源；短期限制泄露窗口 |
| refresh token | 32 字节随机数（base64url），有效期 **7 天**，`sessions.token_hash` 只存 SHA-256 哈希 | 不可伪造、可吊销；哈希存储即使库泄密也无法直接使用 |
| 刷新策略 | **轮换（rotation）**：Refresh 时签发新 access + 新 refresh，`sessions.token_hash` **原地更新**为新哈希（`last_active_at` 顺带更新） | 会话按设备一行，轮换不增行；**不做重用检测**，旧 token 复用按无效拒绝（最简语义，已裁决） |
| 改密行为 | 修改密码 / 找回密码成功后删除该用户全部 `sessions` 行 | 已登录会话在 access 过期（≤15 分钟）后需重新登录 |
| 验证码存储 | 注册 / 找回密码的邮箱验证码存 **Redis**（TTL 5 分钟、一次性、失败 5 次作废、发送 60s 限频） | 过期与一次性语义由 Redis 天然承担，不建表；key 规范 `user:email_code:<scene>:<email>`（见根 AGENTS.md「Redis Key 规范」） |

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
旧 refresh 复用（轮换后）──▶ 按无效拒绝，返回 Unauthenticated
```

- refresh 请求**必须绕过 access 校验**：Gateway 的 `/auth/refresh` 路由不做认证，直接转发
- access token 中携带 `sub`（用户 ID）与 `username`（展示名）；Gateway 解析后将 `user_id` 透传给下游服务
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
  rpc SendEmailCode(SendEmailCodeRequest) returns (SendEmailCodeResponse);   // 发送邮箱验证码（register / reset_password）
  rpc Register(RegisterRequest) returns (RegisterResponse);
  rpc Login(LoginRequest) returns (LoginResponse);
  rpc Refresh(RefreshRequest) returns (RefreshResponse);
  rpc GetUser(GetUserRequest) returns (GetUserResponse);
  rpc UpdatePassword(UpdatePasswordRequest) returns (UpdatePasswordResponse);
  rpc ResetPassword(ResetPasswordRequest) returns (ResetPasswordResponse);   // 忘记密码（「忘记密码接口」）
  rpc ListSessions(ListSessionsRequest) returns (ListSessionsResponse);      // 登录设备列表（「登录设备管理接口」）
  rpc RevokeSession(RevokeSessionRequest) returns (RevokeSessionResponse);   // 踢设备下线（「登录设备管理接口」）
}

message User {
  int64  id = 1;         // 用户 ID（自增主键）
  string email = 2;      // 登录标识（唯一）
  string username = 3;   // 展示名，可重复、可为空
  int32  status = 4;     // 1 正常 0 禁用
  int64  created_at_unix = 5;
  int64  updated_at_unix = 6;
}

message SendEmailCodeRequest {
  string email = 1;
  string scene = 2;      // register | reset_password
}
message SendEmailCodeResponse {}   // 防枚举：不回传验证码是否发出等细节

message RegisterRequest {
  string email = 1;      // 邮箱（唯一）
  string code = 2;       // 邮箱验证码
  string password = 3;   // 8-64 字符
  string username = 4;   // 展示名，可选
}
message RegisterResponse { User user = 1; }   // 不返回 token，注册成功后前端走登录

message LoginRequest {
  string email = 1;
  string password = 2;
  string device_id = 3;    // 设备码（可选；缺失由服务端生成，取值口径见 docs/data-model.md「设备标识（device_id）口径」）
  string device_info = 4;  // 设备信息（可选；前端展示名，缺省由 Gateway 取 User-Agent 截断 255）
}
message LoginResponse {
  User   user = 1;
  string access_token = 2;   // JWT，15 分钟
  string refresh_token = 3;  // 不透明串，7 天
  int64  access_expires_in = 4;   // access 剩余秒数
  int64  refresh_expires_in = 5;  // refresh 剩余秒数
  string device_id = 6;      // 回填：本次生效的设备码（前端存 localStorage，Gateway 设 `did` cookie）
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

message ResetPasswordRequest {
  string email = 1;
  string code = 2;         // 邮箱验证码（scene=reset_password）
  string new_password = 3; // 8-64 字符
}
message ResetPasswordResponse {}

message Session {
  int64  id = 1;
  string device_id = 2;
  string device_info = 3;
  int64  last_active_at_unix = 4;
  int64  created_at_unix = 5;
}
message ListSessionsRequest { int64 user_id = 1; }   // Gateway 从 access token 注入
message ListSessionsResponse { repeated Session sessions = 1; }  // 仅返回在线（revoked_at IS NULL）会话
message RevokeSessionRequest {
  int64 user_id = 1;    // Gateway 注入
  int64 session_id = 2;
}
message RevokeSessionResponse {}
```

### 约束与错误码

> 错误码统一定义与映射见 [pkg/errcode](../../../pkg/errcode/README.md)，服务内不自行定义（含防枚举覆盖错误）。

| RPC | 约束 | 错误码 |
|-----|------|--------|
| SendEmailCode | 邮箱格式、scene 合法、发送限频（60s） | `InvalidArgument` 参数不合法；`ResourceExhausted` 触发限频 |
| Register | 邮箱唯一、验证码有效（TTL 5 分钟 / 一次性 / 失败 5 次作废）、密码 8-64 字符 | `InvalidArgument` 参数不合法；`Unauthenticated` 验证码无效；`AlreadyExists` 邮箱已注册 |
| Login | 邮箱不存在与密码错误**统一返回** `Unauthenticated` | `Unauthenticated`；`FailedPrecondition` 用户被禁用 |
| Refresh | token 过期 / 无效（含轮换后旧 token 复用，按无效拒绝） | `Unauthenticated` |
| GetUser | 目标不存在 | `NotFound` |
| UpdatePassword | user_id 缺失、新密码不合法 | `InvalidArgument`；`Unauthenticated` 旧密码错误或用户不存在（user_id 由 Gateway 从 token 注入，用户不存在说明凭证陈旧，与防枚举策略一致，不返回 `NotFound`） |
| ResetPassword | 验证码有效、新密码 8-64 字符 | `InvalidArgument`；`Unauthenticated` 验证码无效（「忘记密码接口」） |
| ListSessions / RevokeSession | user_id 由 Gateway 注入；session_id 需归属该用户 | `InvalidArgument`；`NotFound` 会话不存在（「登录设备管理接口」） |

- Login 不区分「邮箱不存在」与「密码错误」，防止账号枚举；Register / ResetPassword 同样不回传「邮箱是否已注册」等可枚举信息
- 所有接口错误细节只写日志，不回传内部信息（与 vector-store 约定一致）

## 数据存储

### 迁移文件位置

`pkg/db/migrations/ganrag_user/`（数据库基础设施集中于全局包 `pkg/db`，遵循「每个服务一个 schema、一套独立迁移」，库名即子目录名）：

```
pkg/db/migrations/ganrag_user/
├── 0001_users.up.sql
├── 0001_users.down.sql
├── 0002_sessions.up.sql
├── 0002_sessions.down.sql
├── 0003_user_follows.up.sql
└── 0003_user_follows.down.sql
```

**执行方式**：由 `pkg/db` 执行 `migrate.Up`（服务启动时按配置 `database.auto_migrate`，默认 `true`，迁移失败则拒绝启动；亦可用 `task db-up` 手动同步）。所有服务共用一个 MySQL 实例、各自独立的 migration 子目录。修改 SQL 前必须先 `task db-status` 查看当前版本（见根 AGENTS.md「SQL 迁移修改规范」）。

### 表结构（目标定稿）

> **建表迁移已交付**（2026-10-15 迁移基线重写，0001-0003，含 xid；旧 `refresh_tokens` 及其物理外键随重写消除，见 [docs/fix.md](../../../docs/fix.md) `FIX-001`）；代码适配见 [docs/todo.md](../../../docs/todo.md) 「users 相关代码适配」 / 「会话模型代码适配」 / 「对外接口 xid」。完整 DDL 与字段/索引表见 [docs/database.md](../../../docs/database.md)；设计决策见 [docs/data-model.md](../../../docs/data-model.md)「ganrag_user」。

```sql
-- users：用户账号（「users 相关代码适配」 改造：add email + uk_users_email、drop uk_users_username、drop nickname）
CREATE TABLE users (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  email         VARCHAR(254) NOT NULL,             -- 登录标识（登录/找回密码）
  username      VARCHAR(64)  NOT NULL DEFAULT '',  -- 展示名，可重复、可不填
  password_hash VARCHAR(100) NOT NULL,             -- bcrypt 结果
  status        TINYINT      NOT NULL DEFAULT 1,   -- 1 正常 0 禁用
  created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_users_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- sessions：登录会话（「会话模型代码适配」 新建，替代 refresh_tokens；每设备一行）
CREATE TABLE sessions (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id        BIGINT UNSIGNED NOT NULL,          -- 逻辑引用 users.id（无外键）
  device_id      VARCHAR(64)  NOT NULL,             -- 设备码（服务端生成/核对，见 docs/data-model.md「设备标识（device_id）口径」）
  device_info    VARCHAR(255) NOT NULL DEFAULT '',  -- 设备信息（UA/平台摘要，设备列表展示）
  token_hash     CHAR(64)     NOT NULL,             -- 当前 refresh token 的 SHA-256，轮换原地更新
  expires_at     DATETIME     NOT NULL,
  revoked_at     DATETIME     NULL,                 -- NULL=在线；非 NULL=登出/被踢
  last_active_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  created_at     DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_sessions_token_hash (token_hash),
  UNIQUE KEY uk_sessions_user_device (user_id, device_id),
  KEY idx_sessions_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

### 读写行为

- **登录 / 刷新**：单条唯一索引查询，无慢路径
- **登录建会话**：按 `(user_id, device_id)` upsert `sessions` 行（同设备重登覆盖旧会话，`token_hash` 换新、`revoked_at` 清空）
- **设备 ID 下发（方案 A）**：`device_id` 服务端生成/核对（取值优先级：请求体 > Gateway `did` cookie > 服务端生成 xid）；Gateway 登录成功后 `Set-Cookie: did=<device_id>`（HttpOnly，Max-Age 1 年，每次登录续期），登录响应 `device_id` 回填供前端存 localStorage；完整口径见 [docs/data-model.md](../../../docs/data-model.md)「设备标识（device_id）口径」
- **refresh 轮换**：原地更新 `token_hash` 与 `last_active_at`；**不做重用检测**，旧 token 复用按无效拒绝（`ErrRefreshNotFound` → `Unauthenticated`）
- **过期清理**：登录 / 刷新时顺带删除该用户已过期 `sessions` 行（惰性清理，无需定时任务）
- **改密 / 找回密码**：单事务内更新 `users.password_hash` + 删除该用户全部 `sessions` 行
- **踢设备 / 登出**：置 `revoked_at`（行保留供列表展示）；设备列表默认只返回在线行
- **邮箱验证码**：存 Redis（不建表），key `user:email_code:<scene>:<email>`（scene：`register` / `reset_password`），value 为验证码 SHA-256 哈希，TTL 5 分钟；验证成功即 DEL、失败 5 次作废该 key；发送限频 `user:email_code_limit:<email>`（TTL 60s）

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

redis:
  host: "127.0.0.1"
  port: 6379
  password: ""            # 建议 GANRAG_REDIS_PASSWORD 环境变量覆盖
  db: 0

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
  redis:
    image: redis:7
    container_name: ganrag-redis
    # 端口 6379、密码走环境变量；验证码类数据可不做持久化
    # 加入 ganrag 网络，应用侧用容器名 ganrag-redis 访问
```

- 根 `Taskfile.yml` 新增任务：`task infra`（启动）、`task infra-down`（停止）；compose 与各服务脚本相互独立
- **Redis 用途**：邮箱验证码（注册 / 找回密码，「Redis 与邮箱验证码」）；限流、会话黑名单、缓存等其他用途暂不使用（YAGNI）。key 规范见根 AGENTS.md「Redis Key 规范」
- 应用库 `ganrag_user`（后续 repository 服务建 `ganrag_repository`）由迁移首次执行时创建

## 端口规划（全局）

| 服务 | 端口 | 协议 | 状态 |
|------|------|------|------|
| gateway | 8080 | HTTP | 已实现（2026-10-11） |
| user | 50051 | gRPC | 已实现（2026-10-09） |
| repository | 50052 | gRPC | 未实现 |
| vector-store | 50053 | gRPC | 已实现 |
| ai | 50055 | gRPC | 未实现 |

> 原 file-store（50054）已随 2026-10-17 架构迁移移除（对象存储改用 Garage，见 [repository 架构文档](../../repository/docs/architecture.md)）。

## 目录结构

```
service/user/
├── cmd/server/              # 服务入口（配置加载、日志初始化、数据库与迁移、优雅退出）
├── configs/config.yaml      # 默认配置（database 段结构由 pkg/db.Config 定义）
├── docs/architecture.md     # 本文档
├── internal/
│   ├── config/              # 配置结构、校验（jwt.secret 必填校验，database 复用 pkg/db.Config）
│   ├── store/               # 用户与会话 sessions 业务查询（基于 pkg/db 生成的 query 包）
│   ├── auth/                # bcrypt 哈希校验、JWT 签发、refresh token 生成与哈希、验证码生成校验
│   └── server/              # gRPC handler 与错误码映射、服务生命周期
└── scripts/                 # 本服务构建与运行脚本（沿用既有约定）
```

数据库相关（表结构 SQL、迁移执行、gorm gen 生成的 model/query）统一在仓库全局包 `pkg/db/`，本服务通过 `import pkg/db` 与 `pkg/db/query/ganrag_user` 使用。

## 实现状态

> 已实现（2026-10-09）：proto、配置、迁移、store/auth/handler、单元测试（SQLite）与端到端冒烟（grpcurl）均完成；`Taskfile.yml` 端口已填 50051。
>
> **待实施（本文档已按新设计更新）**：邮箱登录标识 + sessions 会话模型 + Redis 邮箱验证码 + 找回密码 + 设备管理。
> 建表迁移已交付（2026-10-15 重写，`pkg/db/migrations/ganrag_user/0001-0003`）；剩余代码适配与功能跟踪见 [docs/fix.md](../../../docs/fix.md) `FIX-001` 与 [docs/todo.md](../../../docs/todo.md) 「users 相关代码适配」至「对外接口 xid」。
