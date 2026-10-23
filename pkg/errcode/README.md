# pkg/errcode — 统一错误码包

GanRAG 的业务错误、业务错误码与状态码转换的**唯一入口**：**错误自带 gRPC 状态码 + 数字业务码 + 统一归一化转换 + gRPC→HTTP 映射**。
各服务不各自手写 `errors.Is` → `status.Error` 映射表，统一 import 本包。

> **【强制约定】以后的开发一律使用这一套，不要在服务内自行定义错误码映射。**
> 新增错误码直接在本包（通用错误）或服务 store 层（服务私有哨兵错误）用 `errcode.New` 声明即可。

## 响应信封（前后端通信定稿）

```jsonc
// 成功（HTTP 200）
{"code": 200, "message": "ok", "data": {"user": {...}, "accessToken": "..."}}

// 失败（HTTP 状态行照常 4xx/5xx，仅作传输语义）
{"code": 40101, "message": "邮箱或密码错误", "data": null}
```

- **前端判别只看 `code`**（业务码）；HTTP 状态码只作传输语义（监控、网关、`res.ok` 大类兜底）
- 信封三字段恒定齐全；流式路由（文件下载 / SSE）不套信封，内容流直出
- Gateway 是信封组装点：成功 `writeProto` 包 `data`，失败 `writeError`/`writeErrorBiz`（`service/gateway`）

## 业务码号段（HHNNN）

**5 位 `HHNNN` = HTTP 状态码（前三位）× 100 + 2 位序号**：

- **4 开头 = 客户端错误、5 开头 = 服务端错误**，`code/100` 即 HTTP 语义类别
- **序号 `00` = 通用兜底**（`GenericCode`：HTTP 码×100），`01` 起 = 具体业务错误
- 序号按服务分段防撞：**user `01-29` / repository `30-59` / vector-store `60-89` / 共享包 `90-99`**
- **本 README 号表是唯一号源**：新增业务码先查表再声明，不得撞号

### 兜底码（序号 00）

| 兜底码 | 含义 | 来源 gRPC 码 |
|--------|------|--------------|
| `40000` | 参数不合法 | InvalidArgument |
| `40100` | 未认证 | Unauthenticated |
| `40300` | 无权限 | PermissionDenied |
| `40400` | 不存在 | NotFound |
| `40900` | 冲突 | AlreadyExists / Aborted |
| `41200` | 前置条件失败 | FailedPrecondition |
| `42900` | 限频 | ResourceExhausted |
| `50000` | 内部错误 | Internal / 其余 |
| `50200` | 下游不可用 | Unavailable / DeadlineExceeded |

### 具体业务码（哨兵错误）

| 码 | 消息 | 归属 | 声明处 |
|----|------|------|--------|
| `40101` | 邮箱或密码错误 | user | server/handler.go（登录防枚举覆盖） |
| `40102` | refresh token 无效 | user | store/store.go、server/handler.go |
| `40103` | refresh token 已过期 | user | server/handler.go |
| `40104` | 旧密码错误 | user | server/handler.go |
| `40105` | 认证失败 | user | server/handler.go |
| `40106` | 验证码无效或已过期 | user | auth/emailcode.go |
| `40401` | 用户不存在 | user | store/store.go |
| `40901` | 邮箱已注册 | user | store/store.go |
| `41201` | 账号已被禁用 | user | server/handler.go |
| `42901` | 发送过于频繁，请稍后再试 | user | auth/emailcode.go |
| `40030` | 不能移入自身或子孙目录 | repository | store/store.go |
| `40430` | 知识库不存在 | repository | store/store.go |
| `40431` | 目录不存在 | repository | store/store.go |
| `40930` | 知识库名称已存在 | repository | store/store.go |
| `40931` | 同级目录名称已存在 | repository | store/store.go |
| `50030` | sha256 校验失败 | repository | server/handler.go |
| `40460` | 集合不存在 | vector-store | store/store.go |
| `40461` | 文档不存在 | vector-store | store/store.go |
| `40960` | 集合已存在 | vector-store | store/store.go |
| `40490` | 任务不存在 | 共享包 | pkg/asynctask |

**YAGNI 边界**：一次性参数校验（`errcode.InvalidArgument("邮箱格式不正确")` 等）**不逐条分配码**，共享兜底 `40000`，靠 message 区分；前端确实需要分支判断时再升级为具体业务码。

## 数据流

```
store 层哨兵错误 errcode.New(业务码, codes.X, "消息")   handler 上下文覆盖错误（防枚举等契约分歧）
        │                                                      │
        └──────────────► errcode.Status(log, op, err) ◄─────────┘
                                │
                ┌───────────────┼──────────────────┐
                ▼               ▼                  ▼
         命中 *Error      已是 status 错误       未知错误
     码+业务码随 details   原样透传            记日志 + Internal
                │
                ▼
   Gateway：HTTPStatus(code) → HTTP 状态行；BizCode(err) → 信封 code
   （业务码经 google.rpc.ErrorInfo details 跨 gRPC 携带，不改 proto）
```

## 目录

```
pkg/errcode/
├── errcode.go       # Error 类型 + New / Internal / InvalidArgument / Status / HTTPStatus / GenericCode / BizCode
└── errcode_test.go  # 单元测试（业务码往返、兜底派生、状态码映射、包装识别、日志与降级行为）
```

## API 速览

| API | 用途 |
|-----|------|
| `errcode.New(biz, code, msg)` | 创建业务错误（哨兵错误；支持 `%w` 包装、`errors.Is/As` 识别） |
| `(*Error).GRPCStatus()` | 实现 gRPC 识别接口，业务码经 ErrorInfo details 随 status 携带 |
| `(*Error).Biz()` / `(*Error).Code()` | 取业务码 / gRPC 状态码 |
| `errcode.Internal()` | `codes.Internal` + `"服务内部错误"`（兜底码 50000，细节只进日志） |
| `errcode.InvalidArgument(msg)` | `codes.InvalidArgument` 快捷构造（兜底码 40000） |
| `errcode.Status(log, op, err)` | **handler 统一出口**：任意 error → gRPC status 错误（保留业务码与完整消息） |
| `errcode.HTTPStatus(code)` | gRPC 码 → HTTP 码（Gateway 状态行用） |
| `errcode.GenericCode(code)` | gRPC 码 → 兜底业务码（Gateway / 中间件取用） |
| `errcode.BizCode(err)` | 从 gRPC 错误提取业务码（details 取不到按 gRPC 码兜底） |

## 使用规范

### 1. store 层：哨兵错误自带状态码与业务码

```go
import "github.com/gangantongxue/GanRAG/pkg/errcode"
import "google.golang.org/grpc/codes"

var (
    ErrUserNotFound  = errcode.New(40401, codes.NotFound, "用户不存在")      // 号码见 pkg/errcode/README.md 号表
    ErrEmailTaken    = errcode.New(40901, codes.AlreadyExists, "邮箱已注册")
)
```

- 业务码**先查本 README 号表**再声明：同服务分段内递增，不得跨段占用
- 消息必须是**可对外展示**的内容，不得包含内部细节（SQL、堆栈、地址、主机名）
- 包装时用 `%w`：`fmt.Errorf("%w: %s", ErrNotFound, key)`，状态码与业务码自动保留，消息保留上下文

### 2. handler：统一走 `errcode.Status`

```go
func (h *Handler) GetThing(...) (...) {
    thing, err := h.st.GetThing(...)
    if err != nil {
        return nil, errcode.Status(h.log, "GetThing", err)
    }
    ...
}
```

- 未识别的错误自动记日志（`处理请求失败 op=...`）并降级为 `Internal`（50000），**不泄露内部细节**
- 一次性参数校验直接返回 `errcode.InvalidArgument("...")`（40000）

### 3. 上下文覆盖：仅限契约分歧点

同一个错误在不同 RPC 需要不同对外契约时（如 user 服务登录防用户名枚举），
在 **handler 包内**声明覆盖错误并保留 `errors.Is` 分支：

```go
// 登录：用户不存在与密码错误返回同一错误，防用户名枚举
errInvalidCredentials = errcode.New(40101, codes.Unauthenticated, "邮箱或密码错误")

if errors.Is(err, store.ErrUserNotFound) {
    return nil, errInvalidCredentials
}
```

**除此之外不要保留 `errors.Is` + `status.Error` 手工映射。**

### 4. Gateway：信封组装

```go
// 成功：protojson 包进 data
writeProto(c, resp)

// 业务错误：状态行取 HTTPStatus，信封 code 取 BizCode
writeErrorBiz(c, errcode.HTTPStatus(st.Code()), errcode.BizCode(err), st.Message())

// 网关自身错误（bind 失败、限流、未认证）：兜底码 = HTTP 码×100
writeError(c, 400, "请求体不合法")   // → 40000
```

## 新增错误码的流程

1. **通用错误**（任何服务都会用到）→ 在本包 `errcode.go` 增加构造函数或导出变量
2. **服务私有错误** → 查本 README 号表取本服务分段内新号，在该服务 `internal/store` 用 `errcode.New(业务码, codes.X, "消息")` 声明哨兵变量，**并登记进号表**
3. **RPC 契约分歧** → 在该服务 `internal/server` 增加上下文覆盖错误变量（同样登记号表）
4. 补充 `errcode_test.go` 或对应服务的错误码契约测试，跑 `go test -race ./...`
