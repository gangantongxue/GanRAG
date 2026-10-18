// Package server 提供 User 服务的 gRPC handler 与服务端生命周期封装
package server

import (
	"context"
	"errors"
	"log/slog"
	"time"
	"unicode/utf8"

	userv1 "github.com/gangantongxue/GanRAG/api/gen/user/v1"
	dbmodel "github.com/gangantongxue/GanRAG/pkg/db/model/ganrag_user"
	"github.com/gangantongxue/GanRAG/pkg/eid"
	"github.com/gangantongxue/GanRAG/pkg/errcode"
	"github.com/gangantongxue/GanRAG/service/user/internal/auth"
	"github.com/gangantongxue/GanRAG/service/user/internal/config"
	"github.com/gangantongxue/GanRAG/service/user/internal/store"

	"google.golang.org/grpc/codes"
)

// 展示名约束：可空，非空时 ≤64 字符（不限字符集，仅展示用途）
const maxUsernameLen = 64

// 密码长度约束（字符数，与架构文档一致）
const (
	minPasswordLen = 8
	maxPasswordLen = 64
)

// 上下文相关业务错误：同一个 store 哨兵错误在不同 RPC 有不同对外契约时的覆盖值
// （如登录防账号枚举）。错误码统一由 pkg/errcode 定义，不要在服务内另行发明映射。
var (
	// errInvalidCredentials 登录失败（邮箱不存在与密码错误同消息，防账号枚举）
	errInvalidCredentials = errcode.New(codes.Unauthenticated, "邮箱或密码错误")
	// errAuthFailed 改密时 token 中的用户已失效（凭证陈旧），统一按认证失败处理
	errAuthFailed = errcode.New(codes.Unauthenticated, "认证失败")
	// errTokenInvalid refresh token 无效（伪造 / 已失效 / 关联用户不存在，不区分细节防探测）
	errTokenInvalid = errcode.New(codes.Unauthenticated, "refresh token 无效")
	// errTokenExpired refresh token 自然过期
	errTokenExpired = errcode.New(codes.Unauthenticated, "refresh token 已过期")
	// errOldPasswordWrong 改密时旧密码错误
	errOldPasswordWrong = errcode.New(codes.Unauthenticated, "旧密码错误")
	// errUserDisabled 账号被禁用
	errUserDisabled = errcode.New(codes.FailedPrecondition, "账号已被禁用")
)

// Handler 实现 userv1.UserServiceServer
type Handler struct {
	userv1.UnimplementedUserServiceServer
	st     *store.Store     // 数据访问层
	jwt    config.JWTConfig // 双 token 签发配置
	codes  *auth.EmailCode  // 邮箱验证码组件（Redis）
	mailer *auth.Mailer     // 验证码邮件发送通道
	log    *slog.Logger     // 日志（错误细节只写日志，不回传内部信息）
}

// NewHandler 创建 gRPC handler
//
// 参数：
//   - st: 数据访问层
//   - jwt: JWT 签发配置
//   - codes: 邮箱验证码组件
//   - mailer: 验证码邮件发送通道
//   - log: 日志实例
//
// 返回：
//   - *Handler: handler 实例
func NewHandler(st *store.Store, jwt config.JWTConfig, codes *auth.EmailCode, mailer *auth.Mailer, log *slog.Logger) *Handler {
	return &Handler{st: st, jwt: jwt, codes: codes, mailer: mailer, log: log}
}

// SendEmailCode 发送邮箱验证码（注册 / 找回密码）
//
// 防枚举：正常路径一律返回空响应，不回传是否已注册等细节。
// 错误码：InvalidArgument 邮箱 / 场景不合法；ResourceExhausted 触发 60s 发送限频。
func (h *Handler) SendEmailCode(ctx context.Context, req *userv1.SendEmailCodeRequest) (*userv1.SendEmailCodeResponse, error) {
	email := auth.NormalizeEmail(req.Email)
	if !auth.ValidEmail(email) {
		return nil, errcode.InvalidArgument("邮箱格式不正确")
	}
	if !auth.ValidScene(req.Scene) {
		return nil, errcode.InvalidArgument("验证码场景不合法")
	}

	// 生成并落 Redis（限频占用失败即拒绝），明文码只进邮件通道
	code, err := h.codes.Issue(ctx, req.Scene, email)
	if err != nil {
		return nil, errcode.Status(h.log, "SendEmailCode", err)
	}
	if err := h.mailer.SendCode(email, req.Scene, code); err != nil {
		// 邮件通道失败：内部细节只写日志，对外统一 Internal
		return nil, errcode.Status(h.log, "SendEmailCode/发送邮件", err)
	}
	return &userv1.SendEmailCodeResponse{}, nil
}

// Register 用户注册（邮箱+验证码+密码）
//
// 约束：邮箱合法且唯一、验证码有效（scene=register）、密码 8-64 字符、展示名可空 ≤64 字符。
// 防枚举：先验验证码、后判邮箱唯一——探测者无有效码只会得到「验证码无效」，
// 无法借错误差异枚举邮箱是否已注册。
// 错误码：InvalidArgument 参数不合法；Unauthenticated 验证码无效；AlreadyExists 邮箱已注册。
func (h *Handler) Register(ctx context.Context, req *userv1.RegisterRequest) (*userv1.RegisterResponse, error) {
	email := auth.NormalizeEmail(req.Email)
	if !auth.ValidEmail(email) {
		return nil, errcode.InvalidArgument("邮箱格式不正确")
	}
	if !validPassword(req.Password) {
		return nil, errcode.InvalidArgument("密码须为 8-64 字符")
	}
	if utf8.RuneCountInString(req.Username) > maxUsernameLen {
		return nil, errcode.InvalidArgument("展示名不能超过 64 字符")
	}
	// bcrypt 对超过 72 字节的密码报错，这里提前归为参数不合法
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		h.log.Warn("注册密码哈希失败", "err", err)
		return nil, errcode.InvalidArgument("密码不合法")
	}

	// 先验验证码（一次性，成功即删），再判邮箱唯一：防未持码探测者枚举注册状态
	if err := h.codes.Verify(ctx, auth.SceneRegister, email, req.Code); err != nil {
		return nil, errcode.Status(h.log, "Register/校验验证码", err)
	}

	// ErrEmailTaken 由 errcode.Status 自动映射 AlreadyExists
	u, err := h.st.CreateUser(ctx, email, req.Username, hash)
	if err != nil {
		return nil, errcode.Status(h.log, "Register", err)
	}
	return &userv1.RegisterResponse{User: userToProto(u)}, nil
}

// Login 用户登录（邮箱+密码）
//
// 邮箱不存在与密码错误统一返回 Unauthenticated，防止账号枚举。
// 错误码：Unauthenticated 凭证无效；FailedPrecondition 用户被禁用。
func (h *Handler) Login(ctx context.Context, req *userv1.LoginRequest) (*userv1.LoginResponse, error) {
	email := auth.NormalizeEmail(req.Email)
	if email == "" || req.Password == "" {
		return nil, errcode.InvalidArgument("邮箱和密码不能为空")
	}

	u, err := h.st.GetUserByEmail(ctx, email)
	if err != nil {
		// 覆盖 NotFound 契约：与密码错误返回同一错误，避免暴露「该邮箱未注册」
		if errors.Is(err, store.ErrUserNotFound) {
			h.log.Warn("登录失败：用户不存在", "email", email)
			return nil, errInvalidCredentials
		}
		return nil, errcode.Status(h.log, "Login", err)
	}
	if !auth.CheckPassword(u.PasswordHash, req.Password) {
		h.log.Warn("登录失败：密码错误", "email", email)
		return nil, errInvalidCredentials
	}
	if u.Status == store.StatusDisabled {
		return nil, errUserDisabled
	}

	// 生成双 token 并建会话（惰性过期清理在 UpsertSession 事务内）
	pair, err := h.newPair(u)
	if err != nil {
		return nil, err
	}
	// 设备 ID 方案 A：合法请求值（请求体 > Gateway did cookie，合并见 Gateway）则沿用，
	// 否则服务端生成 dev_<xid>；同设备重登 upsert 覆盖旧会话行
	deviceID := req.DeviceId
	if !validDeviceID(deviceID) {
		deviceID = eid.New(eid.Device)
	}
	deviceInfo := truncateDeviceInfo(req.DeviceInfo)
	if err := h.st.UpsertSession(ctx, u.ID, u.Xid, deviceID, deviceInfo,
		auth.HashRefreshToken(pair.refresh), time.Now().Add(h.jwt.RefreshTTL)); err != nil {
		return nil, errcode.Status(h.log, "Login/建立会话", err)
	}
	return &userv1.LoginResponse{
		User:             userToProto(u),
		AccessToken:      pair.access,
		RefreshToken:     pair.refresh,
		AccessExpiresIn:  int64(h.jwt.AccessTTL.Seconds()),
		RefreshExpiresIn: int64(h.jwt.RefreshTTL.Seconds()),
		DeviceId:         deviceID,
	}, nil
}

// Refresh 轮换 token 对：签发新 access + 新 refresh，sessions.token_hash 原地更新
//
// 不做重用检测：轮换后旧 token 哈希不再命中，按无效拒绝（最简语义，已裁决）。
// 错误码：Unauthenticated 过期 / 失效 / 伪造；FailedPrecondition 用户被禁用。
func (h *Handler) Refresh(ctx context.Context, req *userv1.RefreshRequest) (*userv1.RefreshResponse, error) {
	if req.RefreshToken == "" {
		return nil, errcode.InvalidArgument("refresh token 不能为空")
	}

	// ErrSessionNotFound 由 errcode.Status 自动映射 Unauthenticated + "refresh token 无效"
	sess, err := h.st.GetSessionByTokenHash(ctx, auth.HashRefreshToken(req.RefreshToken))
	if err != nil {
		return nil, errcode.Status(h.log, "Refresh/查询会话", err)
	}
	// 自然过期或已吊销（踢设备）：正常拒绝（惰性清理交由该用户下次登录/刷新时执行）
	if !sess.ExpiresAt.After(time.Now()) {
		return nil, errTokenExpired
	}
	if sess.RevokedAt != nil {
		return nil, errTokenInvalid
	}

	u, err := h.st.GetUserByID(ctx, sess.UserID)
	if err != nil {
		// 覆盖 NotFound 契约：关联用户已不存在时，对外仍按 token 无效处理
		if errors.Is(err, store.ErrUserNotFound) {
			return nil, errTokenInvalid
		}
		return nil, errcode.Status(h.log, "Refresh/查询用户", err)
	}
	if u.Status == store.StatusDisabled {
		return nil, errUserDisabled
	}

	// 生成新 token 对（不落库），原地轮换会话行（并发轮换兜底按无效拒绝）
	pair, err := h.newPair(u)
	if err != nil {
		return nil, err
	}
	if err := h.st.RotateSession(ctx, sess, auth.HashRefreshToken(pair.refresh), time.Now().Add(h.jwt.RefreshTTL)); err != nil {
		if errors.Is(err, store.ErrSessionNotFound) {
			return nil, errTokenInvalid
		}
		return nil, errcode.Status(h.log, "Refresh/轮换 token", err)
	}

	return &userv1.RefreshResponse{
		AccessToken:      pair.access,
		RefreshToken:     pair.refresh,
		AccessExpiresIn:  int64(h.jwt.AccessTTL.Seconds()),
		RefreshExpiresIn: int64(h.jwt.RefreshTTL.Seconds()),
	}, nil
}

// GetUser 按 xid 查询用户信息（供 Gateway / 其他服务调用）
//
// 错误码：InvalidArgument xid 缺失或不合法；NotFound 目标不存在。
func (h *Handler) GetUser(ctx context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	if !eid.Match(req.Xid, eid.User) {
		return nil, errcode.InvalidArgument("用户标识不合法")
	}
	// ErrUserNotFound 由 errcode.Status 自动映射 NotFound + "用户不存在"
	u, err := h.st.GetUserByXID(ctx, req.Xid)
	if err != nil {
		return nil, errcode.Status(h.log, "GetUser", err)
	}
	return &userv1.GetUserResponse{User: userToProto(u)}, nil
}

// UpdatePassword 修改密码：校验旧密码，成功后删除该用户全部登录会话
//
// user_xid 由 Gateway 从 access token 解析后注入，不信任请求体。
// 错误码：InvalidArgument user_xid 不合法或新密码不合法；
// Unauthenticated 旧密码错误或用户不存在（user_xid 来自 token，失效说明凭证陈旧）。
func (h *Handler) UpdatePassword(ctx context.Context, req *userv1.UpdatePasswordRequest) (*userv1.UpdatePasswordResponse, error) {
	if !eid.Match(req.UserXid, eid.User) {
		return nil, errcode.InvalidArgument("用户标识不合法")
	}
	if !validPassword(req.NewPassword) {
		return nil, errcode.InvalidArgument("新密码须为 8-64 字符")
	}

	u, err := h.st.GetUserByXID(ctx, req.UserXid)
	if err != nil {
		// 覆盖 NotFound 契约：token 里的用户已失效，按认证失败处理
		if errors.Is(err, store.ErrUserNotFound) {
			h.log.Warn("改密失败：用户不存在", "user_xid", req.UserXid)
			return nil, errAuthFailed
		}
		return nil, errcode.Status(h.log, "UpdatePassword/查询用户", err)
	}
	if !auth.CheckPassword(u.PasswordHash, req.OldPassword) {
		h.log.Warn("改密失败：旧密码错误", "user_xid", req.UserXid)
		return nil, errOldPasswordWrong
	}

	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		h.log.Warn("改密密码哈希失败", "err", err)
		return nil, errcode.InvalidArgument("新密码不合法")
	}
	if err := h.st.UpdatePassword(ctx, u.ID, hash); err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			h.log.Warn("改密失败：用户不存在", "user_xid", req.UserXid)
			return nil, errAuthFailed
		}
		return nil, errcode.Status(h.log, "UpdatePassword", err)
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
// 落库路径由调用方选择：登录用 CreateSession，刷新用 RotateSession（原地轮换）。
// 返回的 error 已映射为 gRPC status 错误，调用方可直接回传。
func (h *Handler) newPair(u *dbmodel.User) (*tokenPair, error) {
	refresh, err := auth.NewRefreshToken()
	if err != nil {
		return nil, errcode.Status(h.log, "生成 refresh token", err)
	}
	access, err := auth.IssueAccessToken(h.jwt.Secret, h.jwt.Issuer, h.jwt.AccessTTL, u.Xid, u.Username)
	if err != nil {
		return nil, errcode.Status(h.log, "签发 access token", err)
	}
	return &tokenPair{access: access, refresh: refresh}, nil
}

// validPassword 校验密码长度：8-64 字符（按字符数，与架构文档一致）
func validPassword(s string) bool {
	n := utf8.RuneCountInString(s)
	return n >= minPasswordLen && n <= maxPasswordLen
}

// maxDeviceIDLen 设备码最大长度（对应 sessions.device_id VARCHAR(64)）
const maxDeviceIDLen = 64

// maxDeviceInfoLen 设备信息最大长度（对应 sessions.device_info VARCHAR(255)）
const maxDeviceInfoLen = 255

// validDeviceID 校验设备码格式：1-64 位 [A-Za-z0-9_-]（data-model.md「设备标识（device_id）口径」）
//
// 不合法值不报错，由调用方换服务端生成，避免设备字段问题阻断登录。
func validDeviceID(s string) bool {
	if len(s) == 0 || len(s) > maxDeviceIDLen {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// truncateDeviceInfo 设备信息截断到 255 字符（按字符数，与 VARCHAR(255) 口径一致）
func truncateDeviceInfo(s string) string {
	r := []rune(s)
	if len(r) > maxDeviceInfoLen {
		return string(r[:maxDeviceInfoLen])
	}
	return s
}

// userToProto 数据模型转 proto 消息（对外标识用 xid 全串，自增 id 不出服务）
func userToProto(u *dbmodel.User) *userv1.User {
	return &userv1.User{
		Xid:           u.Xid,
		Email:         u.Email,
		Username:      u.Username,
		Status:        u.Status,
		CreatedAtUnix: u.CreatedAt.Unix(),
		UpdatedAtUnix: u.UpdatedAt.Unix(),
	}
}
