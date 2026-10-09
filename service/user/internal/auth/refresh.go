package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// refreshTokenBytes refresh token 的随机字节数（32 字节 = 256 位熵，不可伪造）
const refreshTokenBytes = 32

// NewRefreshToken 生成不透明 refresh token（32 字节随机数，base64url 无填充）
//
// 返回：
//   - string: 随机 token 明文（只在本次响应返回，库中仅存哈希）
//   - error: 系统随机源读取失败时返回错误
func NewRefreshToken() (string, error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成 refresh token 失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashRefreshToken 计算 refresh token 的 SHA-256 十六进制哈希
//
// 数据库只存哈希：即使库泄密，哈希也无法反推为可用 token。
//
// 参数：
//   - token: 明文 refresh token
//
// 返回：
//   - string: 64 位十六进制哈希（对应表字段 token_hash CHAR(64)）
func HashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
