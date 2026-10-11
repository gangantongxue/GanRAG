// Package handler 提供下游 gRPC 服务的 HTTP 转发实现
//
// 每个转发 handler 做三件事：绑定并校验请求体 → 调用 gRPC 客户端 →
// 成功用 protojson 直出响应，失败映射为统一错误体。
package handler

import (
	"context"
	"log/slog"

	"github.com/cloudwego/hertz/pkg/app"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	userv1 "github.com/gangantongxue/GanRAG/api/gen/user/v1"
	"github.com/gangantongxue/GanRAG/pkg/errcode"
	"github.com/gangantongxue/GanRAG/service/gateway/pkg/auth"
)

// UserHandler user 服务 gRPC 接口的 HTTP 转发 handler
type UserHandler struct {
	client userv1.UserServiceClient // user 服务客户端
	log    *slog.Logger             // 日志（错误细节只写日志，不回传内部信息）
}

// NewUserHandler 创建 user 转发 handler
//
// 参数：
//   - conn: 到 user 服务的 gRPC 连接
//   - log: 日志实例
//
// 返回：
//   - *UserHandler: handler 实例
func NewUserHandler(conn *grpc.ClientConn, log *slog.Logger) *UserHandler {
	return &UserHandler{client: userv1.NewUserServiceClient(conn), log: log}
}

// registerRequest POST /api/v1/auth/register 请求体
type registerRequest struct {
	Username string `json:"username"` // 3-32 字符，字母数字与 _ -
	Password string `json:"password"` // 8-64 字符
}

// loginRequest POST /api/v1/auth/login 请求体
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// refreshRequest POST /api/v1/auth/refresh 请求体
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"` // 不透明 refresh 令牌
}

// updatePasswordRequest PUT /api/v1/users/me/password 请求体
//
// 不含 user_id：由 Gateway 从 access token 解析后注入，不信任请求体。
type updatePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"` // 8-64 字符
}

// Register 注册（公开路由）
//
// 返回：
//   - HTTP 200: protojson 直出 RegisterResponse
//   - 其余: gRPC 状态码映射后的统一错误体
func (h *UserHandler) Register(ctx context.Context, c *app.RequestContext) {
	var req registerRequest
	if err := c.Bind(&req); err != nil {
		writeError(c, 400, "请求体不合法")
		return
	}
	resp, err := h.client.Register(ctx, &userv1.RegisterRequest{
		Username: req.Username,
		Password: req.Password,
	})
	h.respond(c, resp, err, "register")
}

// Login 登录（公开路由）
func (h *UserHandler) Login(ctx context.Context, c *app.RequestContext) {
	var req loginRequest
	if err := c.Bind(&req); err != nil {
		writeError(c, 400, "请求体不合法")
		return
	}
	resp, err := h.client.Login(ctx, &userv1.LoginRequest{
		Username: req.Username,
		Password: req.Password,
	})
	h.respond(c, resp, err, "login")
}

// Refresh 刷新令牌对（公开路由，按设计必须绕过 access 校验）
func (h *UserHandler) Refresh(ctx context.Context, c *app.RequestContext) {
	var req refreshRequest
	if err := c.Bind(&req); err != nil {
		writeError(c, 400, "请求体不合法")
		return
	}
	resp, err := h.client.Refresh(ctx, &userv1.RefreshRequest{
		RefreshToken: req.RefreshToken,
	})
	h.respond(c, resp, err, "refresh")
}

// Me 获取当前登录用户信息（需验签）
//
// 用户 ID 取自验签通过的 access token，不接受请求体/查询参数指定。
func (h *UserHandler) Me(ctx context.Context, c *app.RequestContext) {
	resp, err := h.client.GetUser(ctx, &userv1.GetUserRequest{
		Id: auth.UserID(c),
	})
	h.respond(c, resp, err, "get_user")
}

// UpdatePassword 修改当前用户密码（需验签）
//
// user_id 由验签结果注入请求，不信任请求体。
func (h *UserHandler) UpdatePassword(ctx context.Context, c *app.RequestContext) {
	var req updatePasswordRequest
	if err := c.Bind(&req); err != nil {
		writeError(c, 400, "请求体不合法")
		return
	}
	resp, err := h.client.UpdatePassword(ctx, &userv1.UpdatePasswordRequest{
		UserId:      auth.UserID(c),
		OldPassword: req.OldPassword,
		NewPassword: req.NewPassword,
	})
	h.respond(c, resp, err, "update_password")
}

// respond 统一处理 gRPC 调用结果：成功 protojson 直出，失败映射统一错误体
//
// 参数：
//   - c: Hertz 请求上下文
//   - resp: gRPC 响应（proto 消息）
//   - err: gRPC 调用错误
//   - op: 操作名，用于日志定位
func (h *UserHandler) respond(c *app.RequestContext, resp proto.Message, err error, op string) {
	if err != nil {
		h.writeRPCError(c, err, op)
		return
	}
	writeProto(c, resp)
}

// writeProto 将 gRPC 响应用 protojson 序列化为 HTTP 200 直出
//
// 字段名遵循 proto 的 JSON 名称（camelCase），前端字段与 proto 定义一一对应，
// 无需在网关层维护重复的响应结构体。
//
// 参数：
//   - c: Hertz 请求上下文
//   - m: gRPC 响应消息
func writeProto(c *app.RequestContext, m proto.Message) {
	b, err := protojson.Marshal(m)
	if err != nil {
		// protojson 序列化失败属程序性错误，不回传细节
		writeError(c, 500, "内部错误")
		return
	}
	c.SetStatusCode(200)
	c.SetContentType("application/json")
	c.SetBodyString(string(b))
}

// writeError 写统一错误体 {"code": <HTTP 状态码>, "message": <可展示信息>} 并中断后续处理
//
// 参数：
//   - c: Hertz 请求上下文
//   - code: HTTP 状态码（同时写入响应体 code 字段）
//   - message: 可直接展示给用户的错误信息
func writeError(c *app.RequestContext, code int, message string) {
	c.AbortWithStatusJSON(code, map[string]any{"code": code, "message": message})
}

// writeRPCError 将 gRPC 状态码映射为 HTTP 状态码与统一错误体，错误细节只写日志
//
// 参数：
//   - c: Hertz 请求上下文
//   - err: gRPC 调用错误
//   - op: 操作名，用于日志定位
func (h *UserHandler) writeRPCError(c *app.RequestContext, err error, op string) {
	st, ok := status.FromError(err)
	if !ok {
		// 非 gRPC 状态错误（如连接失败）：不回传细节，归为 502
		h.log.Error("转发失败", "op", op, "err", err)
		writeError(c, 502, "下游服务不可用")
		return
	}
	switch st.Code() {
	case codes.Internal:
		// 内部错误细节只写日志，不回传
		h.log.Error("下游内部错误", "op", op, "err", st.Message())
		writeError(c, 500, "内部错误")
	case codes.Unavailable, codes.DeadlineExceeded:
		// 连接失败/超时：gRPC 错误串含地址等细节，只回通用信息
		h.log.Error("下游不可用", "op", op, "err", st.Message())
		writeError(c, 502, "下游服务不可用")
	default:
		// 业务错误（参数不合法、未认证、冲突等）：message 由 user 服务设计为可直接展示
		writeError(c, errcode.HTTPStatus(st.Code()), st.Message())
	}
}
