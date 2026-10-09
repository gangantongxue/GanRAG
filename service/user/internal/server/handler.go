// Package server 提供 User 服务的 gRPC handler 与服务端生命周期封装
package server

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"time"
	"unicode/utf8"

	userv1 "github.com/gangantongxue/GanRAG/api/gen/user/v1"
	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_user"
	"github.com/gangantongxue/GanRAG/service/user/internal/auth"
	"github.com/gangantongxue/GanRAG/service/user/internal/config"
	"github.com/gangantongxue/GanRAG/service/user/internal/store"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// usernameRe 用户名格式：3-32 字符，仅允许字母、数字、_、-
var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,32}$`)

// 密码长度约束（字符数，与架构文档一致）
const (
	minPasswordLen = 8
	maxPasswordLen = 64
)

// Handler 实现 userv1.UserServiceServer
type Handler struct {
	userv1.UnimplementedUserServiceServer
	st  *store.Store     // 数据访问层
	jwt config.JWTConfig // 双 token 签发配置
	log *slog.Logger     // 日志（错误细节只写日志，不回传内部信息）
}

// NewHandler 创建 gRPC handler
//
// 参数：
//   - st: 数据访问层
//   - jwt: JWT 签发配置
//   - log: 日志实例
//
// 返回：
//   - *Handler: handler 实例
func NewHandler(st *store.Store, jwt config.JWTConfig, log *slog.Logger) *Handler {
	return &Handler{st: st, jwt: jwt, log: log}
}

// Register 用户注册
//
// 约束：用户名 3-32 字符（[a-zA-Z0-9_-]）、密码 8-64 字符。
// 错误码：InvalidArgument 参数不合法；AlreadyExists 用户名已存在。
func (h *Handler) Register(ctx context.Context, req *userv1.RegisterRequest) (*userv1.RegisterResponse, error) {
	if !usernameRe.MatchString(req.Username) {
		return nil, status.Error(codes.InvalidArgument, "用户名须为 3-32 位字母、数字、_ 或 -")
	}
	if !validPassword(req.Password) {
		return nil, status.Error(codes.InvalidArgument, "密码须为 8-64 字符")
	}
	// bcrypt 对超过 72 字节的密码报错，这里提前归为参数不合法
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		h.log.Warn("注册密码哈希失败", "err", err)
		return nil, status.Error(codes.InvalidArgument, "密码不合法")
	}

	// 首期昵称默认等于用户名
	u, err := h.st.CreateUser(ctx, req.Username, hash, req.Username)
	if err != nil {
		if errors.Is(err, store.ErrUsernameTaken) {
			return nil, status.Error(codes.AlreadyExists, "用户名已存在")
		}
		h.log.Error("注册失败", "err", err)
		return nil, status.Error(codes.Internal, "内部错误")
	}
	return &userv1.RegisterResponse{User: userToProto(u)}, nil
}

// Login 用户登录
//
// 用户不存在与密码错误统一返回 Unauthenticated，防止用户名枚举。
// 错误码：Unauthenticated 凭证无效；FailedPrecondition 用户被禁用。
func (h *Handler) Login(ctx context.Context, req *userv1.LoginRequest) (*userv1.LoginResponse, error) {
	if req.Username == "" || req.Password == "" {
		return nil, status.Error(codes.InvalidArgument, "用户名和密码不能为空")
	}

	u, err := h.st.GetUserByUsername(ctx, req.Username)
	if err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			// 与密码错误返回同一错误，避免暴露「该用户名不存在」
			h.log.Warn("登录失败：用户不存在", "username", req.Username)
			return nil, status.Error(codes.Unauthenticated, "用户名或密码错误")
		}
		h.log.Error("登录查询用户失败", "err", err)
		return nil, status.Error(codes.Internal, "内部错误")
	}
	if !auth.CheckPassword(u.PasswordHash, req.Password) {
		h.log.Warn("登录失败：密码错误", "username", req.Username)
		return nil, status.Error(codes.Unauthenticated, "用户名或密码错误")
	}
	if u.Status == store.StatusDisabled {
		return nil, status.Error(codes.FailedPrecondition, "账号已被禁用")
	}

	// 生成双 token 并保存 refresh 记录（惰性过期清理在 CreateRefreshToken 事务内）
	pair, err := h.newPair(u)
	if err != nil {
		return nil, err
	}
	if err := h.st.CreateRefreshToken(ctx, u.ID, auth.HashRefreshToken(pair.refresh), time.Now().Add(h.jwt.RefreshTTL)); err != nil {
		h.log.Error("保存 refresh token 失败", "err", err)
		return nil, status.Error(codes.Internal, "内部错误")
	}
	return &userv1.LoginResponse{
		User:             userToProto(u),
		AccessToken:      pair.access,
		RefreshToken:     pair.refresh,
		AccessExpiresIn:  int64(h.jwt.AccessTTL.Seconds()),
		RefreshExpiresIn: int64(h.jwt.RefreshTTL.Seconds()),
	}, nil
}

// Refresh 轮换 token 对：旧 refresh 作废，签发新 access + refresh
//
// 重用已作废 token 视为泄露：吊销该用户全部 refresh token。
// 错误码：Unauthenticated 过期 / 作废 / 重用 / 伪造；FailedPrecondition 用户被禁用。
func (h *Handler) Refresh(ctx context.Context, req *userv1.RefreshRequest) (*userv1.RefreshResponse, error) {
	if req.RefreshToken == "" {
		return nil, status.Error(codes.InvalidArgument, "refresh token 不能为空")
	}

	tok, err := h.st.GetRefreshToken(ctx, auth.HashRefreshToken(req.RefreshToken))
	if err != nil {
		if errors.Is(err, store.ErrRefreshNotFound) {
			return nil, status.Error(codes.Unauthenticated, "refresh token 无效")
		}
		h.log.Error("查询 refresh token 失败", "err", err)
		return nil, status.Error(codes.Internal, "内部错误")
	}

	// 重用检测：已作废（被轮换或吊销）的 token 再次出现，视为泄露
	if tok.RevokedAt != nil {
		h.log.Warn("检测到 refresh token 重用，吊销该用户全部 token", "user_id", tok.UserID)
		if err := h.st.RevokeAllRefreshTokens(ctx, tok.UserID); err != nil {
			h.log.Error("吊销用户 refresh token 失败", "err", err)
		}
		return nil, status.Error(codes.Unauthenticated, "refresh token 无效")
	}
	// 自然过期：正常拒绝（惰性清理交由该用户下次登录/刷新时执行）
	if !tok.ExpiresAt.After(time.Now()) {
		return nil, status.Error(codes.Unauthenticated, "refresh token 已过期")
	}

	u, err := h.st.GetUserByID(ctx, tok.UserID)
	if err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			return nil, status.Error(codes.Unauthenticated, "refresh token 无效")
		}
		h.log.Error("刷新时查询用户失败", "err", err)
		return nil, status.Error(codes.Internal, "内部错误")
	}
	if u.Status == store.StatusDisabled {
		return nil, status.Error(codes.FailedPrecondition, "账号已被禁用")
	}

	// 生成新 token 对（不落库），轮换事务负责「作废旧记录 + 写入新记录 + 惰性清理」
	pair, err := h.newPair(u)
	if err != nil {
		return nil, err
	}
	if err := h.st.RotateRefreshToken(ctx, tok, auth.HashRefreshToken(pair.refresh), time.Now().Add(h.jwt.RefreshTTL)); err != nil {
		if errors.Is(err, store.ErrRefreshRevoked) {
			// 并发重用兜底：同样吊销全部
			h.log.Warn("并发 refresh 重用，吊销该用户全部 token", "user_id", tok.UserID)
			if rerr := h.st.RevokeAllRefreshTokens(ctx, tok.UserID); rerr != nil {
				h.log.Error("吊销用户 refresh token 失败", "err", rerr)
			}
			return nil, status.Error(codes.Unauthenticated, "refresh token 无效")
		}
		h.log.Error("轮换 refresh token 失败", "err", err)
		return nil, status.Error(codes.Internal, "内部错误")
	}

	return &userv1.RefreshResponse{
		AccessToken:      pair.access,
		RefreshToken:     pair.refresh,
		AccessExpiresIn:  int64(h.jwt.AccessTTL.Seconds()),
		RefreshExpiresIn: int64(h.jwt.RefreshTTL.Seconds()),
	}, nil
}

// GetUser 按 ID 查询用户信息（供 Gateway / 其他服务调用）
//
// 错误码：InvalidArgument id 缺失；NotFound 目标不存在。
func (h *Handler) GetUser(ctx context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	if req.Id <= 0 {
		return nil, status.Error(codes.InvalidArgument, "缺少用户 ID")
	}
	u, err := h.st.GetUserByID(ctx, req.Id)
	if err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, "用户不存在")
		}
		h.log.Error("查询用户失败", "err", err)
		return nil, status.Error(codes.Internal, "内部错误")
	}
	return &userv1.GetUserResponse{User: userToProto(u)}, nil
}

// UpdatePassword 修改密码：校验旧密码，成功后吊销该用户全部 refresh token
//
// user_id 由 Gateway 从 access token 解析后注入，不信任请求体。
// 错误码：InvalidArgument user_id 缺失或新密码不合法；
// Unauthenticated 旧密码错误或用户不存在（user_id 来自 token，失效说明凭证陈旧）。
func (h *Handler) UpdatePassword(ctx context.Context, req *userv1.UpdatePasswordRequest) (*userv1.UpdatePasswordResponse, error) {
	if req.UserId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "缺少用户 ID")
	}
	if !validPassword(req.NewPassword) {
		return nil, status.Error(codes.InvalidArgument, "新密码须为 8-64 字符")
	}

	u, err := h.st.GetUserByID(ctx, req.UserId)
	if err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			h.log.Warn("改密失败：用户不存在", "user_id", req.UserId)
			return nil, status.Error(codes.Unauthenticated, "认证失败")
		}
		h.log.Error("改密查询用户失败", "err", err)
		return nil, status.Error(codes.Internal, "内部错误")
	}
	if !auth.CheckPassword(u.PasswordHash, req.OldPassword) {
		h.log.Warn("改密失败：旧密码错误", "user_id", req.UserId)
		return nil, status.Error(codes.Unauthenticated, "旧密码错误")
	}

	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		h.log.Warn("改密密码哈希失败", "err", err)
		return nil, status.Error(codes.InvalidArgument, "新密码不合法")
	}
	if err := h.st.UpdatePassword(ctx, req.UserId, hash); err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			return nil, status.Error(codes.Unauthenticated, "认证失败")
		}
		h.log.Error("改密失败", "err", err)
		return nil, status.Error(codes.Internal, "内部错误")
	}
	return &userv1.UpdatePasswordResponse{}, nil
}

// tokenPair 新签发的双 token
type tokenPair struct {
	access  string // JWT access token
	refresh string // 不透明 refresh token 明文（只返回一次，库存哈希）
}

// newPair 为用户生成 access + refresh token 对（纯生成，不落库）
//
// 落库路径由调用方选择：登录用 CreateRefreshToken，刷新用 RotateRefreshToken（原子轮换）。
// 返回的 error 已映射为 gRPC status 错误，调用方可直接回传。
func (h *Handler) newPair(u *dbmodel.User) (*tokenPair, error) {
	refresh, err := auth.NewRefreshToken()
	if err != nil {
		h.log.Error("生成 refresh token 失败", "err", err)
		return nil, status.Error(codes.Internal, "内部错误")
	}
	access, err := auth.IssueAccessToken(h.jwt.Secret, h.jwt.Issuer, h.jwt.AccessTTL, u.ID, u.Username)
	if err != nil {
		h.log.Error("签发 access token 失败", "err", err)
		return nil, status.Error(codes.Internal, "内部错误")
	}
	return &tokenPair{access: access, refresh: refresh}, nil
}

// validPassword 校验密码长度：8-64 字符（按字符数，与架构文档一致）
func validPassword(s string) bool {
	n := utf8.RuneCountInString(s)
	return n >= minPasswordLen && n <= maxPasswordLen
}

// userToProto 数据模型转 proto 消息
func userToProto(u *dbmodel.User) *userv1.User {
	return &userv1.User{
		Id:            u.ID,
		Username:      u.Username,
		Nickname:      u.Nickname,
		Status:        u.Status,
		CreatedAtUnix: u.CreatedAt.Unix(),
		UpdatedAtUnix: u.UpdatedAt.Unix(),
	}
}
