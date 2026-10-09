package auth

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

// TestNewRefreshToken 验证 refresh token 的随机性与格式
func TestNewRefreshToken(t *testing.T) {
	a, err := NewRefreshToken()
	if err != nil {
		t.Fatalf("NewRefreshToken() 错误: %v", err)
	}
	b, err := NewRefreshToken()
	if err != nil {
		t.Fatalf("NewRefreshToken() 错误: %v", err)
	}

	// 32 字节随机数的 base64url（无填充）长度为 43，且可完整解码
	raw, err := base64.RawURLEncoding.DecodeString(a)
	if err != nil {
		t.Fatalf("token 不是合法 base64url: %v", err)
	}
	if len(raw) != 32 {
		t.Errorf("解码后 %d 字节，期望 32 字节", len(raw))
	}
	if a == b {
		t.Error("两次生成的 token 应不同（随机数）")
	}
	// URL 安全字符集（base64url 无 + /）
	if strings.IndexFunc(a, func(r rune) bool {
		return !(r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	}) != -1 {
		t.Errorf("token 含非 URL 安全字符: %s", a)
	}
}

// TestHashRefreshToken 验证 SHA-256 哈希的正确性（库中只存哈希）
func TestHashRefreshToken(t *testing.T) {
	h := HashRefreshToken("abc")
	if len(h) != 64 {
		t.Fatalf("哈希长度 %d，期望 64", len(h))
	}
	// sha256("abc") 的标准向量
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if h != want {
		t.Errorf("哈希 = %s，期望 %s", h, want)
	}
	if _, err := hex.DecodeString(h); err != nil {
		t.Errorf("哈希应为十六进制: %v", err)
	}
	if HashRefreshToken("abd") == h {
		t.Error("不同输入应产生不同哈希")
	}
}
