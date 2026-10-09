// Package auth 提供 User 服务的认证原语：bcrypt 密码哈希、
// access token（JWT HS256）签发、refresh token（不透明随机串）生成与哈希
package auth

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword 使用 bcrypt 对明文密码生成加盐哈希
//
// 参数：
//   - plain: 明文密码（8-64 字符，调用方已校验；bcrypt 超过 72 字节会返回错误）
//
// 返回：
//   - string: bcrypt 哈希串（自带 cost 与盐，可直接入库）
//   - error: 哈希生成失败（如密码过长）时返回错误
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("生成密码哈希失败: %w", err)
	}
	return string(b), nil
}

// CheckPassword 校验明文密码与 bcrypt 哈希是否匹配
//
// 参数：
//   - hash: 入库的 bcrypt 哈希
//   - plain: 待校验的明文密码
//
// 返回：
//   - bool: 匹配返回 true；不匹配或哈希格式非法返回 false
func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
