# Gateway 服务架构选型

## HTTP 框架

**选型：Hertz**

Hertz 是字节跳动开源的高性能 HTTP 框架，具有以下特点：
- 高性能：基于 Netpoll 网络库，性能优异
- 易用性：丰富的中间件生态，支持路由分组、参数绑定等
- 云原生：支持服务发现、负载均衡等微服务特性
- 代码生成：支持通过 IDL（如 Thrift、Protobuf）生成代码

## 职责

- API 网关入口
- 请求路由分发
- 认证鉴权
- 限流熔断
- 请求/响应日志

> 当前实现范围（第一期）：路由分发、access token 本地验签、限流、请求/响应日志。
> 熔断与负载均衡等待下游多实例部署后按需添加（YAGNI）。

## 中间件链路

```
请求 ──▶ panic 恢复（server.Default 内置）
      ──▶ 访问日志（方法/路径/状态码/耗时/IP）
      ├─ /api/v1/auth/*  ──▶ auth 限流（匿名按 IP 分桶，1 QPS / 突发 5）
      │                      └──▶ 转发 handler ──▶ user 服务 gRPC
      └─ /api/v1/*       ──▶ access token 本地验签（注入 user_id）
                             ──▶ 默认限流（按 user_id 分桶，10 QPS / 突发 20）
                             ──▶ 转发 handler ──▶ user 服务 gRPC
```

- 验签：golang-jwt/jwt/v5 HS256 本地校验（共享密钥、校验 iss/exp），**零回源** user 服务
- 限流：golang.org/x/time/rate 令牌桶；已登录按 user_id 分桶（多端共享额度），匿名按客户端 IP 分桶；闲置桶超 10 分钟自动回收
- 顺序：验签在限流之前，保证限流能按用户维度分桶

## 接口设计

响应格式（统一信封 `{code, message, data}`，定稿 2026-10-23，见 [pkg/errcode/README.md](../../pkg/errcode/README.md)）：
- 成功：HTTP 200 + `{"code": 200, "message": "ok", "data": <protojson gRPC 响应>}`，data 内字段为 proto 的 JSON 名称，前端字段与 proto 定义一一对应
- 失败：HTTP 状态行照常 4xx/5xx（仅作传输语义），`{"code": <业务错误码>, "message": <可展示信息>, "data": null}`，错误细节只写日志
- 业务错误码 5 位 `HHNNN`（前三位 = HTTP 码，4 开头客户端错误、5 开头服务端错误），前端判别只看 `code`；网关自身错误用兜底码（HTTP 码×100，如 401→`40100`、429→`42900`）
- 流式路由（文件下载 / SSE）不套信封，内容流直出

| 方法 | 路径 | 认证 | 转发 RPC |
|------|------|------|----------|
| POST | `/api/v1/auth/register` | 公开 | UserService.Register |
| POST | `/api/v1/auth/login` | 公开 | UserService.Login |
| POST | `/api/v1/auth/refresh` | **绕过 access 校验**（user 架构文档硬性要求） | UserService.Refresh |
| GET | `/api/v1/users/me` | Bearer access token | UserService.GetUser（id 取自 token） |
| PUT | `/api/v1/users/me/password` | Bearer access token | UserService.UpdatePassword（user_id 取自 token） |
| GET | `/api/v1/articles/{article_xid}/resource?path=...` | Bearer access token（公开库可匿名） | RepositoryService.GetResource（server-streaming），Gateway 流式回写 HTTP body |

文件资源路由（架构迁移 P1，详见 [service/repository/docs/architecture.md](../../repository/docs/architecture.md)「对象存储与文件访问」）：

- 浏览器首次请求稳定地址（前端由 Markdown 相对路径解析拼接），**不直接拼 Garage 地址**
- Gateway 只做 JWT 验签、限流、路由；**知识库权限校验在 Repository**（领域边界），Gateway 不复制规则
- Repository 从 Garage 流式读出对象（gRPC server-streaming），Gateway 流式回写 HTTP body（Garage 不对外暴露，2026-10-18 定；Presigned URL 302 降为可选部署形态）
- S3 凭证只在 Repository，Gateway 与前端均不持有

gRPC 状态码 → HTTP 状态码映射：

| gRPC | HTTP | gRPC | HTTP |
|------|------|------|------|
| InvalidArgument | 400 | FailedPrecondition | 412 |
| Unauthenticated | 401 | AlreadyExists / Aborted | 409 |
| PermissionDenied | 403 | ResourceExhausted | 429 |
| NotFound | 404 | Unavailable / DeadlineExceeded | 502 |
| Internal / 其他 | 500 | 超限（网关自身） | 429 |

- token 中的 `sub`（用户 ID）由验签中间件注入请求上下文，**不信任任何请求体/查询参数**
- 非 gRPC 状态错误（连接失败等）返回 502，不回传下游细节
