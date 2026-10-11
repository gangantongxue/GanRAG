# pkg/errcode — 统一错误码包

GanRAG 的业务错误与错误码**唯一入口**：**错误自带 gRPC 状态码 + 统一归一化转换 + gRPC→HTTP 映射**。
各服务不各自手写 `errors.Is` → `status.Error` 映射表，统一 import 本包。

> **【强制约定】以后的开发一律使用这一套，不要在服务内自行定义错误码映射。**
> 新增错误码直接在本包（通用错误）或服务 store 层（服务私有哨兵错误）用 `errcode.New` 声明即可。

## 数据流

```
store 层哨兵错误 errcode.New(codes.X, "消息")     handler 上下文覆盖错误（防枚举等契约分歧）
        │                                              │
        └──────────────► errcode.Status(log, op, err) ◄─┘
                                │
                ┌───────────────┼──────────────────┐
                ▼               ▼                  ▼
         命中 *Error      已是 status 错误       未知错误
         按自带码返回     原样透传            记日志 + Internal
                                │
              Gateway：errcode.HTTPStatus(code) → HTTP 状态码
```

## 目录

```
pkg/errcode/
├── errcode.go       # Error 类型 + New / Internal / InvalidArgument / Status / HTTPStatus
└── errcode_test.go  # 单元测试（状态码映射、包装识别、日志与降级行为）
```

## API 速览

| API | 用途 |
|-----|------|
| `errcode.New(code, msg)` | 创建业务错误（哨兵错误；支持 `%w` 包装、`errors.Is/As` 识别） |
| `(*Error).GRPCStatus()` | 实现 gRPC 识别接口，`*Error` 可直接 `return nil, err` 由框架转 status |
| `errcode.Internal()` | `codes.Internal` + `"服务内部错误"`（细节只进日志） |
| `errcode.InvalidArgument(msg)` | `codes.InvalidArgument` 快捷构造 |
| `errcode.Status(log, op, err)` | **handler 统一出口**：任意 error → gRPC status 错误 |
| `errcode.HTTPStatus(code)` | gRPC 码 → HTTP 码（Gateway 用） |

## 使用规范

### 1. store 层：哨兵错误自带状态码

```go
import "github.com/gangantongxue/GanRAG/pkg/errcode"
import "google.golang.org/grpc/codes"

var (
    ErrUserNotFound  = errcode.New(codes.NotFound, "用户不存在")
    ErrUsernameTaken = errcode.New(codes.AlreadyExists, "用户名已存在")
)
```

- 消息必须是**可对外展示**的内容，不得包含内部细节（SQL、堆栈、地址、主机名）
- 包装时用 `%w`：`fmt.Errorf("%w: %s", ErrNotFound, key)`，状态码自动保留，消息保留上下文

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

- 未识别的错误自动记日志（`处理请求失败 op=...`）并降级为 `Internal`，**不泄露内部细节**
- 一次性参数校验直接返回 `errcode.InvalidArgument("...")`

### 3. 上下文覆盖：仅限契约分歧点

同一个错误在不同 RPC 需要不同对外契约时（如 user 服务登录防用户名枚举），
在 **handler 包内**声明覆盖错误并保留 `errors.Is` 分支：

```go
// 登录：用户不存在与密码错误返回同一错误，防用户名枚举
errInvalidCredentials = errcode.New(codes.Unauthenticated, "用户名或密码错误")

if errors.Is(err, store.ErrUserNotFound) {
    return nil, errInvalidCredentials
}
```

**除此之外不要保留 `errors.Is` + `status.Error` 手工映射。**

### 4. Gateway：gRPC → HTTP

```go
writeError(c, errcode.HTTPStatus(st.Code()), st.Message())
```

## 新增错误码的流程

1. **通用错误**（任何服务都会用到）→ 在本包 `errcode.go` 增加构造函数或导出变量
2. **服务私有错误** → 在该服务 `internal/store` 用 `errcode.New(codes.X, "消息")` 声明哨兵变量
3. **RPC 契约分歧** → 在该服务 `internal/server` 增加上下文覆盖错误变量
4. 补充 `errcode_test.go` 或对应服务的错误码契约测试，跑 `go test -race ./...`
