// Package auth 提供 Gateway 的 access token 本地验签中间件
//
// user 服务签发 HS256 access token，Gateway 用共享密钥本地校验（零回源），
// 通过后把 sub（用户 ID）写入请求上下文，供下游转发 handler 读取。
package auth

import (
	"context"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/golang-jwt/jwt/v5"
)

// userIDKey 用户 ID 在请求上下文中的存储键
const userIDKey = "gateway.user_id"

// accessClaims access token 载荷（与 user 服务签发结构一致）
type accessClaims struct {
	jwt.RegisteredClaims
	Username string `json:"username"` // 用户名（验签阶段仅校验存在性，不使用）
}

// Middleware 返回校验 Bearer access token 的 Hertz 中间件
//
// 取 Authorization: Bearer <token> 头，本地 HS256 验签（校验签名、过期时间、签发方），
// 成功后把 sub 解析为用户 ID 写入请求上下文；失败直接返回 401 统一错误体并中断链路。
//
// 参数：
//   - secret: HS256 共享密钥（与 user 签发侧同一份配置）
//   - issuer: 期望的签发方标识（校验 iss 声明，空值不校验）
//
// 返回：
//   - app.HandlerFunc: Hertz 中间件
func Middleware(secret, issuer string) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		token, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			writeUnauthorized(c, "缺少访问令牌")
			return
		}

		userID, ok := verify(token, secret, issuer)
		if !ok {
			writeUnauthorized(c, "访问令牌无效或已过期")
			return
		}

		c.Set(userIDKey, userID)
		c.Next(ctx)
	}
}

// UserID 从请求上下文读取验签通过的用户 ID
//
// 参数：
//   - c: Hertz 请求上下文
//
// 返回：
//   - int64: 用户 ID；未经认证（或 sub 非法）时返回 0
func UserID(c *app.RequestContext) int64 {
	id, _ := c.Get(userIDKey)
	v, _ := id.(int64)
	return v
}

// verify 本地校验 access token 并返回用户 ID
//
// 参数：
//   - token: JWT 字符串
//   - secret: HS256 共享密钥
//   - issuer: 期望签发方（空值跳过 iss 校验）
//
// 返回：
//   - int64: sub 声明解析出的用户 ID
//   - bool: 验签是否通过（签名、过期、iss、sub 格式任一不合法均为 false）
func verify(token, secret, issuer string) (int64, bool) {
	claims := &accessClaims{}
	opts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(), // 必须携带 exp，无过期时间的 token 一律拒绝
	}
	if issuer != "" {
		opts = append(opts, jwt.WithIssuer(issuer))
	}
	if _, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		return []byte(secret), nil // HS256 对称密钥
	}, opts...); err != nil {
		return 0, false
	}
	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || userID <= 0 {
		return 0, false
	}
	return userID, true
}

// bearerToken 从 Authorization 头提取 Bearer令牌
//
// 参数：
//   - header: Authorization 头原始值
//
// 返回：
//   - string: 去掉 "Bearer " 前缀后的 token
//   - bool: 格式是否为合法的 Bearer 认证头
func bearerToken(header []byte) (string, bool) {
	const prefix = "Bearer "
	s := string(header)
	if len(s) <= len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(s[len(prefix):]), true
}

// writeUnauthorized 写 401 统一错误体并中断后续处理
//
// 错误体结构与全服务统一约定一致：{"code": <HTTP 状态码>, "message": <可展示信息>}。
func writeUnauthorized(c *app.RequestContext, msg string) {
	c.AbortWithStatusJSON(401, map[string]any{"code": 401, "message": msg})
}
