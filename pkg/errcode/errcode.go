// Package errcode 提供全项目统一的业务错误定义与状态码转换
//
// 【强制约定】所有服务的业务错误统一使用本包，不要在各服务内自行定义
// 错误码映射逻辑：
//
//  1. store 层哨兵错误用 errcode.New(code, msg) 定义，错误自带 gRPC 状态码，
//     handler 无需再写 errors.Is 手工映射表；
//  2. handler 返回错误统一走 Status() 归一化（未识别的错误自动记日志并降级为
//     Internal，避免泄露内部细节）；
//  3. 仅当同一错误在不同 RPC 有不同契约时（如登录防用户名枚举），才在 handler
//     内声明上下文覆盖错误并保留 errors.Is 分支；
//  4. gRPC 码 → HTTP 码的转换（Gateway 使用）统一用 HTTPStatus()。
//
// 新增错误码直接在本包（或服务 store 层哨兵处）用 New 声明即可，不要复制
// status.Error(codes.X, ...) 字面量。
package errcode

import (
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// internalMsg 未识别错误统一对外消息：细节只进日志，不回传调用方
const internalMsg = "服务内部错误"

// Error 业务错误：携带 gRPC 状态码与对外可展示的消息
//
// 实现 error 接口（可被 fmt.Errorf %w 包装、errors.Is/As 识别），
// 同时实现 grpc 的 GRPCStatus() 接口，因此可直接作为 handler 的返回值，
// gRPC 框架会自动转为对应状态的响应。
type Error struct {
	code codes.Code // gRPC 状态码
	msg  string     // 对外可展示的错误消息
}

// New 创建业务错误
//
// 参数：
//   - code: gRPC 状态码（如 codes.NotFound）
//   - msg: 对外可展示的错误消息（不要包含内部细节，如堆栈、SQL、地址）
//
// 返回：
//   - *Error: 可直接作为哨兵错误使用（配合 errors.Is / %w 包装）
func New(code codes.Code, msg string) *Error {
	return &Error{code: code, msg: msg}
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
// 返回：
//   - *status.Status: 对应状态码与消息的状态对象
func (e *Error) GRPCStatus() *status.Status {
	return status.New(e.code, e.msg)
}

// Code 返回该错误携带的 gRPC 状态码
//
// 返回：
//   - codes.Code: gRPC 状态码
func (e *Error) Code() codes.Code {
	return e.code
}

// Internal 构造内部错误：细节只写日志，对外统一 "服务内部错误"
//
// 返回：
//   - *Error: codes.Internal 业务错误
func Internal() *Error {
	return New(codes.Internal, internalMsg)
}

// InvalidArgument 构造参数不合法错误
//
// 参数：
//   - msg: 对外可展示的具体原因
//
// 返回：
//   - *Error: codes.InvalidArgument 业务错误
func InvalidArgument(msg string) *Error {
	return New(codes.InvalidArgument, msg)
}

// Status 将任意 error 归一为 gRPC status 错误（各服务 handler 的统一出口）
//
// 识别规则：
//   - err 为 nil → 返回 nil
//   - 解包后命中 *Error（errors.As，支持 %w 包装）→ 使用其状态码，
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

	// 1. 业务错误（含包装）：状态码来自哨兵，消息保留完整上下文
	var be *Error
	if errors.As(err, &be) {
		return status.Error(be.code, err.Error())
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
	case codes.Unavailable:
		return 502
	default:
		return 500
	}
}
