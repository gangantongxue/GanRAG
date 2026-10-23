// Package errcode 提供全项目统一的业务错误定义、业务错误码与状态码转换
//
// 【强制约定】所有服务的业务错误统一使用本包，不要在各服务内自行定义
// 错误码映射逻辑：
//
//  1. store 层哨兵错误用 errcode.New(业务码, gRPC 码, msg) 定义，错误自带
//     gRPC 状态码与业务码，handler 无需再写 errors.Is 手工映射表；
//  2. handler 返回错误统一走 Status() 归一化（未识别的错误自动记日志并降级为
//     Internal，避免泄露内部细节）；
//  3. 仅当同一错误在不同 RPC 有不同契约分歧时（如登录防用户名枚举），才在
//     handler 内声明上下文覆盖错误并保留 errors.Is 分支；
//  4. gRPC 码 → HTTP 码的转换（Gateway 使用）统一用 HTTPStatus()；
//  5. 业务码随 gRPC status details（ErrorInfo）跨服务携带、不改 proto，Gateway
//     提取后写入响应信封 {"code": <业务码>, "message": ..., "data": ...}。
//
// 业务码为 5 位 HHNNN：前三位 = HTTP 状态码（4 开头客户端错误、5 开头服务端
// 错误），后两位为该状态码下的序号——00 是通用兜底、01 起为具体业务错误。
// 分段与全量号表见 README.md（唯一号源，新增业务码先查表再声明）。
//
// 新增错误码直接在本包（通用错误）或服务 store 层哨兵处用 New 声明即可，
// 不要复制 status.Error(codes.X, ...) 字面量。
package errcode

import (
	"errors"
	"log/slog"
	"strconv"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// internalMsg 未识别错误统一对外消息：细节只进日志，不回传调用方
const internalMsg = "服务内部错误"

// bizDomain ErrorInfo 的 Domain 取值，标识业务码归属本项目
const bizDomain = "ganrag"

// Error 业务错误：携带业务码、gRPC 状态码与对外可展示的消息
//
// 实现 error 接口（可被 fmt.Errorf %w 包装、errors.Is/As 识别），
// 同时实现 grpc 的 GRPCStatus() 接口，因此可直接作为 handler 的返回值，
// gRPC 框架会自动转为对应状态的响应。
type Error struct {
	biz  int        // 业务错误码（5 位 HHNNN，见包注释与 README 号表）
	code codes.Code // gRPC 状态码
	msg  string     // 对外可展示的错误消息
}

// New 创建业务错误
//
// 参数：
//   - biz: 业务错误码（5 位 HHNNN，号表见 README；哨兵错误必须显式声明）
//   - code: gRPC 状态码（如 codes.NotFound）
//   - msg: 对外可展示的错误消息（不要包含内部细节，如堆栈、SQL、地址）
//
// 返回：
//   - *Error: 可直接作为哨兵错误使用（配合 errors.Is / %w 包装）
func New(biz int, code codes.Code, msg string) *Error {
	return &Error{biz: biz, code: code, msg: msg}
}

// Error 实现 error 接口，返回对外消息
//
// 返回：
//   - string: 错误消息
func (e *Error) Error() string {
	return e.msg
}

// GRPCStatus 实现 grpc status 识别接口，使 *Error 可被框架直接转为 status 响应
//
// 业务码经 ErrorInfo details 随 status 跨 gRPC 边界携带（不改 proto），
// 对端用 BizCode() 提取；细节附加失败不影响错误本身。
//
// 返回：
//   - *status.Status: 对应状态码、消息与业务码 details 的状态对象
func (e *Error) GRPCStatus() *status.Status {
	st := status.New(e.code, e.msg)
	if e.biz == 0 {
		return st
	}
	withDetails, err := st.WithDetails(&errdetails.ErrorInfo{
		Reason: strconv.Itoa(e.biz),
		Domain: bizDomain,
	})
	if err != nil {
		return st
	}
	return withDetails
}

// statusWithMsg 用完整上下文消息构造 status（业务码随身），Status() 归一化用
//
// 参数：
//   - msg: 保留 %w 包装上下文后的完整消息（如 "集合不存在: docs"）
//
// 返回：
//   - error: 带业务码 details 的 gRPC status 错误
func (e *Error) statusWithMsg(msg string) error {
	return (&Error{biz: e.biz, code: e.code, msg: msg}).GRPCStatus().Err()
}

// Code 返回该错误携带的 gRPC 状态码
//
// 返回：
//   - codes.Code: gRPC 状态码
func (e *Error) Code() codes.Code {
	return e.code
}

// Biz 返回该错误携带的业务错误码
//
// 返回：
//   - int: 业务错误码（5 位 HHNNN）
func (e *Error) Biz() int {
	return e.biz
}

// Internal 构造内部错误：细节只写日志，对外统一 "服务内部错误"（兜底业务码 50000）
//
// 返回：
//   - *Error: codes.Internal 业务错误
func Internal() *Error {
	return New(GenericCode(codes.Internal), codes.Internal, internalMsg)
}

// InvalidArgument 构造参数不合法错误（兜底业务码 40000）
//
// 参数：
//   - msg: 对外可展示的具体原因
//
// 返回：
//   - *Error: codes.InvalidArgument 业务错误
func InvalidArgument(msg string) *Error {
	return New(GenericCode(codes.InvalidArgument), codes.InvalidArgument, msg)
}

// GenericCode 通用兜底业务码：HTTP 状态码 × 100（序号 00）
//
// 未显式声明业务码的错误（一次性参数校验、直接构造的 status 错误等）
// 统一按此规则兜底，Gateway 与中间件也可直接取用。
//
// 参数：
//   - code: gRPC 状态码
//
// 返回：
//   - int: 兜底业务码（如 NotFound → 40400）
func GenericCode(code codes.Code) int {
	return HTTPStatus(code) * 100
}

// BizCode 从 gRPC 错误提取业务码
//
// 识别规则：
//   - details 中携带 ErrorInfo（本包 *Error 经 Status()/GRPCStatus() 附加）→
//     取其 Reason 解析出的业务码
//   - 取不到（直接构造的 status 错误、对端未带）→ 按 gRPC 状态码兜底 GenericCode
//   - 非 gRPC status 错误 → 兜底 50000
//
// 参数：
//   - err: 通常为 gRPC 客户端收到的错误
//
// 返回：
//   - int: 业务错误码（5 位 HHNNN）
func BizCode(err error) int {
	st, ok := status.FromError(err)
	if !ok {
		return GenericCode(codes.Unknown)
	}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			if biz, convErr := strconv.Atoi(info.Reason); convErr == nil {
				return biz
			}
		}
	}
	return GenericCode(st.Code())
}

// Status 将任意 error 归一为 gRPC status 错误（各服务 handler 的统一出口）
//
// 识别规则：
//   - err 为 nil → 返回 nil
//   - 解包后命中 *Error（errors.AsType，支持 %w 包装）→ 使用其状态码与业务码，
//     消息取 err.Error() 全文（保留如 "集合不存在: docs" 的上下文细节）
//   - 已是 gRPC status 错误（如 handler 内部构造后透传）→ 原样返回
//   - 未知错误 → 降级为 Internal + "服务内部错误"，并记录日志
//
// 参数：
//   - log: 日志器，仅未识别的错误会用它记录 "处理请求失败"
//   - op: 操作名（如 "Register"），用于日志定位
//   - err: 待转换的错误（通常来自 store 层）
//
// 返回：
//   - error: gRPC status 错误，调用方可直接 return nil, ...
func Status(log *slog.Logger, op string, err error) error {
	if err == nil {
		return nil
	}

	// 1. 业务错误（含包装）：状态码与业务码来自哨兵，消息保留完整上下文
	if be, ok := errors.AsType[*Error](err); ok {
		return be.statusWithMsg(err.Error())
	}

	// 2. 已是 gRPC status 错误：原样透传
	if _, ok := status.FromError(err); ok {
		return err
	}

	// 3. 未知错误：细节只进日志，对外统一 Internal
	if log != nil {
		log.Error("处理请求失败", "op", op, "err", err)
	}
	return status.Error(codes.Internal, internalMsg)
}

// HTTPStatus 将 gRPC 状态码映射为 HTTP 状态码（Gateway 响应转换用）
//
// 参数：
//   - code: gRPC 状态码
//
// 返回：
//   - int: 对应的 HTTP 状态码，未知码归为 500
func HTTPStatus(code codes.Code) int {
	switch code {
	case codes.InvalidArgument:
		return 400
	case codes.Unauthenticated:
		return 401
	case codes.PermissionDenied:
		return 403
	case codes.NotFound:
		return 404
	case codes.AlreadyExists, codes.Aborted:
		return 409
	case codes.FailedPrecondition:
		return 412
	case codes.ResourceExhausted:
		return 429
	case codes.Unavailable, codes.DeadlineExceeded:
		return 502
	default:
		return 500
	}
}
