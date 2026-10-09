package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TestIssueAccessToken 验证 access token 签发与载荷内容（Gateway 本地验签的契约）
func TestIssueAccessToken(t *testing.T) {
	const (
		secret = "test-secret"
		issuer = "ganrag-test"
	)
	ttl := 15 * time.Minute

	token, err := IssueAccessToken(secret, issuer, ttl, 42, "alice")
	if err != nil {
		t.Fatalf("IssueAccessToken() 错误: %v", err)
	}

	parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		t.Fatalf("解析 access token 失败: %v", err)
	}
	if !parsed.Valid {
		t.Fatal("access token 应有效")
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("载荷类型断言失败: %T", parsed.Claims)
	}
	if sub, _ := claims.GetSubject(); sub != "42" {
		t.Errorf("sub = %q，期望 %q", sub, "42")
	}
	if got := claims["username"]; got != "alice" {
		t.Errorf("username = %v，期望 alice", got)
	}
	if got := claims["iss"]; got != issuer {
		t.Errorf("iss = %v，期望 %s", got, issuer)
	}

	// 过期时间应约为签发时间 + ttl
	exp, err := claims.GetExpirationTime()
	if err != nil {
		t.Fatalf("读取 exp 失败: %v", err)
	}
	if d := time.Until(exp.Time); d < ttl-time.Minute || d > ttl+time.Minute {
		t.Errorf("有效期剩余 %v，期望约等于 %v", d, ttl)
	}
}

// TestIssueAccessToken_WrongSecret 验证密钥不一致时验签失败
func TestIssueAccessToken_WrongSecret(t *testing.T) {
	token, err := IssueAccessToken("secret-a", "ganrag", time.Minute, 1, "bob")
	if err != nil {
		t.Fatalf("IssueAccessToken() 错误: %v", err)
	}
	if _, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		return []byte("secret-b"), nil
	}); err == nil {
		t.Fatal("错误密钥验签应失败")
	}
}
