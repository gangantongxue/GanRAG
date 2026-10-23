package errcode

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestNewAndError 验证 New 创建的错误实现 error 接口且消息与业务码一致
func TestNewAndError(t *testing.T) {
	e := New(CodeUserNotFound, codes.NotFound, "用户不存在")
	if got := e.Error(); got != "用户不存在" {
		t.Errorf("Error() = %q, 期望 %q", got, "用户不存在")
	}
	if got := e.Code(); got != codes.NotFound {
		t.Errorf("Code() = %v, 期望 %v", got, codes.NotFound)
	}
	if got := e.Biz(); got != CodeUserNotFound {
		t.Errorf("Biz() = %d, 期望 %d", got, CodeUserNotFound)
	}
}

// TestGRPCStatus 验证 *Error 可被 grpc 框架识别为对应状态
func TestGRPCStatus(t *testing.T) {
	e := New(CodeUserEmailTaken, codes.AlreadyExists, "邮箱已注册")
	st, ok := status.FromError(e)
	if !ok {
		t.Fatal("status.FromError 应能识别 *Error")
	}
	if st.Code() != codes.AlreadyExists || st.Message() != "邮箱已注册" {
		t.Errorf("状态 = (%v, %q), 期望 (AlreadyExists, 邮箱已注册)", st.Code(), st.Message())
	}
}

// TestCodeValues 撞号防护：全量业务码清单断言互不重复
//
// 新增错误码时必须在此登记，否则视为未登记号码。
func TestCodeValues(t *testing.T) {
	all := map[int]string{
		// 通用兜底
		CodeInvalidArgument: "CodeInvalidArgument", CodeUnauthenticated: "CodeUnauthenticated",
		CodePermissionDenied: "CodePermissionDenied", CodeNotFound: "CodeNotFound",
		CodeConflict: "CodeConflict", CodeFailedPrecondition: "CodeFailedPrecondition",
		CodeResourceExhausted: "CodeResourceExhausted", CodeInternal: "CodeInternal",
		CodeUnavailable: "CodeUnavailable",
		// user
		CodeUserInvalidCredentials: "CodeUserInvalidCredentials", CodeUserTokenInvalid: "CodeUserTokenInvalid",
		CodeUserTokenExpired: "CodeUserTokenExpired", CodeUserOldPasswordWrong: "CodeUserOldPasswordWrong",
		CodeUserAuthFailed: "CodeUserAuthFailed", CodeUserEmailCodeInvalid: "CodeUserEmailCodeInvalid",
		CodeUserNotFound: "CodeUserNotFound", CodeUserEmailTaken: "CodeUserEmailTaken",
		CodeUserDisabled: "CodeUserDisabled", CodeUserEmailCodeRateLimited: "CodeUserEmailCodeRateLimited",
		// repository
		CodeRepoDirectoryMoveIntoSelf: "CodeRepoDirectoryMoveIntoSelf", CodeRepoKBNotFound: "CodeRepoKBNotFound",
		CodeRepoDirNotFound: "CodeRepoDirNotFound", CodeRepoKBNameTaken: "CodeRepoKBNameTaken",
		CodeRepoDirNameTaken: "CodeRepoDirNameTaken", CodeRepoChecksumMismatch: "CodeRepoChecksumMismatch",
		// vector-store
		CodeVectorCollectionNotFound: "CodeVectorCollectionNotFound", CodeVectorDocumentNotFound: "CodeVectorDocumentNotFound",
		CodeVectorCollectionExists: "CodeVectorCollectionExists",
		// 共享包
		CodeTaskNotFound: "CodeTaskNotFound",
	}
	seen := make(map[int]string, len(all))
	for code, name := range all {
		if prev, dup := seen[code]; dup {
			t.Errorf("业务码 %d 撞号：%s 与 %s", code, prev, name)
		}
		seen[code] = name
	}
}

// TestCodeFallbackConsistency 验证兜底常量与 GenericCode 派生算法一致
func TestCodeFallbackConsistency(t *testing.T) {
	cases := []struct {
		in   codes.Code
		want int
	}{
		{codes.InvalidArgument, CodeInvalidArgument},
		{codes.Unauthenticated, CodeUnauthenticated},
		{codes.PermissionDenied, CodePermissionDenied},
		{codes.NotFound, CodeNotFound},
		{codes.AlreadyExists, CodeConflict},
		{codes.FailedPrecondition, CodeFailedPrecondition},
		{codes.ResourceExhausted, CodeResourceExhausted},
		{codes.Internal, CodeInternal},
		{codes.Unavailable, CodeUnavailable},
	}
	for _, tc := range cases {
		if got := GenericCode(tc.in); got != tc.want {
			t.Errorf("GenericCode(%v) = %d, 期望常量 %d", tc.in, got, tc.want)
		}
	}
}

// TestBizCodeRoundTrip 验证业务码经 status details 跨 gRPC 边界往返不变（含 %w 包装）
func TestBizCodeRoundTrip(t *testing.T) {
	sentinel := New(CodeUserEmailCodeInvalid, codes.Unauthenticated, "验证码无效或已过期")

	cases := []struct {
		name string
		err  error
	}{
		{"裸哨兵", sentinel},
		{"%w 包装", fmt.Errorf("%w: 123456", sentinel)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 模拟 handler 归一化 → 框架序列化 → 对端 status.FromError 提取
			got := Status(nil, "op", tc.err)
			if biz := BizCode(got); biz != CodeUserEmailCodeInvalid {
				t.Errorf("BizCode = %d, 期望 %d", biz, CodeUserEmailCodeInvalid)
			}
		})
	}
}

// TestBizCodeFallback 验证无业务码 details 时按 gRPC 码兜底（HTTP 码×100）
func TestBizCodeFallback(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"直接构造的 status 错误", status.Error(codes.NotFound, "用户不存在"), CodeNotFound},
		{"非 gRPC 错误", errors.New("dial tcp: connection refused"), CodeInternal},
		{"nil 错误", nil, CodeInternal}, // status.FromError(nil) 视为 OK，走兜底 default
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BizCode(tc.err); got != tc.want {
				t.Errorf("BizCode = %d, 期望 %d", got, tc.want)
			}
		})
	}
}

// TestGenericCode 验证兜底业务码 = HTTP 码 × 100
func TestGenericCode(t *testing.T) {
	if got := GenericCode(codes.Unknown); got != 50000 {
		t.Errorf("GenericCode(Unknown) = %d, 期望兜底 50000", got)
	}
}

// TestInternalAndInvalidArgument 验证快捷构造的状态码与消息
func TestInternalAndInvalidArgument(t *testing.T) {
	if got := Internal().Code(); got != codes.Internal {
		t.Errorf("Internal() 码 = %v, 期望 Internal", got)
	}
	if got := Internal().Error(); got != "服务内部错误" {
		t.Errorf("Internal() 消息 = %q, 期望 %q", got, "服务内部错误")
	}
	if got := InvalidArgument("参数不合法").Code(); got != codes.InvalidArgument {
		t.Errorf("InvalidArgument() 码 = %v, 期望 InvalidArgument", got)
	}
}

// TestStatusNil 验证 nil 输入返回 nil
func TestStatusNil(t *testing.T) {
	if got := Status(nil, "op", nil); got != nil {
		t.Errorf("Status(nil) = %v, 期望 nil", got)
	}
}

// TestStatusBusinessError 验证业务错误的转换：码来自哨兵，消息取完整上下文
func TestStatusBusinessError(t *testing.T) {
	sentinel := New(40400, codes.NotFound, "集合不存在")

	cases := []struct {
		name string
		err  error
	}{
		{"裸哨兵", sentinel},
		{"%w 包装", fmt.Errorf("%w: docs", sentinel)},
		{"多层包装", fmt.Errorf("外层: %w", fmt.Errorf("内层: %w", sentinel))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Status(nil, "op", tc.err)
			st, ok := status.FromError(got)
			if !ok {
				t.Fatalf("Status 返回值应为 gRPC status 错误")
			}
			if st.Code() != codes.NotFound {
				t.Errorf("码 = %v, 期望 NotFound", st.Code())
			}
			if st.Message() != tc.err.Error() {
				t.Errorf("消息 = %q, 期望 %q", st.Message(), tc.err.Error())
			}
		})
	}
}

// TestStatusPassthrough 验证已是 gRPC status 的错误原样透传
func TestStatusPassthrough(t *testing.T) {
	orig := status.Error(codes.FailedPrecondition, "账号已被禁用")
	if got := Status(nil, "op", orig); !errors.Is(got, orig) {
		// status.Error 每次比较底层指针，errors.Is 对同一次调用的值应相等
		t.Errorf("Status 应原样透传 status 错误, 实际 = %v", got)
	}
}

// TestStatusUnknown 验证未知错误降级为 Internal，且消息不泄露内部细节、会记日志
func TestStatusUnknown(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	got := Status(log, "Upload", errors.New("dial tcp 10.0.0.1:3306: connection refused"))
	st, ok := status.FromError(got)
	if !ok {
		t.Fatalf("Status 返回值应为 gRPC status 错误")
	}
	if st.Code() != codes.Internal {
		t.Errorf("码 = %v, 期望 Internal", st.Code())
	}
	if st.Message() != "服务内部错误" {
		t.Errorf("消息 = %q, 期望不泄露内部细节", st.Message())
	}
	if !bytes.Contains(buf.Bytes(), []byte("处理请求失败")) || !bytes.Contains(buf.Bytes(), []byte("Upload")) {
		t.Errorf("应记录包含操作名的失败日志, 实际: %s", buf.String())
	}
}

// TestStatusPassthroughNoLog 验证 status 错误透传路径不记日志
func TestStatusPassthroughNoLog(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	Status(log, "op", status.Error(codes.Unauthenticated, "refresh token 无效"))
	if buf.Len() != 0 {
		t.Errorf("status 错误透传不应记日志, 实际: %s", buf.String())
	}
}

// TestHTTPStatus 验证 gRPC 码到 HTTP 码的映射表
func TestHTTPStatus(t *testing.T) {
	cases := []struct {
		in   codes.Code
		want int
	}{
		{codes.InvalidArgument, 400},
		{codes.Unauthenticated, 401},
		{codes.PermissionDenied, 403},
		{codes.NotFound, 404},
		{codes.AlreadyExists, 409},
		{codes.Aborted, 409},
		{codes.FailedPrecondition, 412},
		{codes.ResourceExhausted, 429},
		{codes.Unavailable, 502},
		{codes.Internal, 500},
		{codes.Unknown, 500},
	}
	for _, tc := range cases {
		if got := HTTPStatus(tc.in); got != tc.want {
			t.Errorf("HTTPStatus(%v) = %d, 期望 %d", tc.in, got, tc.want)
		}
	}
}
