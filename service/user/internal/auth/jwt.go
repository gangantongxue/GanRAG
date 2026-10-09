package auth

import (
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// accessClaims access token 载荷
//
// 标准声明（sub/iss/iat/exp）+ 业务声明 username；
// Gateway 本地验签后取 sub 作为 user_id 注入下游请求上下文。
type accessClaims struct {
	jwt.RegisteredClaims
	Username string `json:"username"` // 用户名
}

// IssueAccessToken 签发 HS256 access token（短期有效，Gateway 用共享密钥本地校验）
//
// 参数：
//   - secret: HS256 共享密钥（user 签发、Gateway 校验，通过共享配置持有）
//   - issuer: 签发方标识
//   - ttl: 有效期（如 15 分钟）
//   - userID: 用户 ID（写入 sub 声明）
//   - username: 用户名（写入 username 声明）
//
// 返回：
//   - string: JWT 字符串
//   - error: 签名生成失败时返回错误
func IssueAccessToken(secret, issuer string, ttl time.Duration, userID int64, username string) (string, error) {
	now := time.Now()
	claims := accessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(userID, 10), // sub：用户 ID
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
		Username: username,
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("签发 access token 失败: %w", err)
	}
	return token, nil
}
