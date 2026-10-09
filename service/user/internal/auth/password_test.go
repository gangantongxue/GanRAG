package auth

import (
	"strings"
	"testing"
)

// TestHashAndCheckPassword 验证 bcrypt 哈希与校验的完整往返
func TestHashAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatalf("HashPassword() 错误: %v", err)
	}
	if hash == "correct horse battery" {
		t.Fatal("哈希不应等于明文")
	}
	if !CheckPassword(hash, "correct horse battery") {
		t.Error("正确密码应校验通过")
	}
	if CheckPassword(hash, "wrong password") {
		t.Error("错误密码应校验失败")
	}
	if CheckPassword("not-a-bcrypt-hash", "correct horse battery") {
		t.Error("非法哈希应校验失败")
	}
}

// TestHashPassword_Uniqueness 验证加盐：相同明文两次哈希结果不同
func TestHashPassword_Uniqueness(t *testing.T) {
	h1, err := HashPassword("same password")
	if err != nil {
		t.Fatalf("HashPassword() 错误: %v", err)
	}
	h2, err := HashPassword("same password")
	if err != nil {
		t.Fatalf("HashPassword() 错误: %v", err)
	}
	if h1 == h2 {
		t.Error("相同明文的两次哈希应因随机盐而不同")
	}
}

// TestHashPassword_TooLong 验证 bcrypt 72 字节上限返回错误
//
// 密码长度按字符数校验为 8-64，但多字节字符可能超过 72 字节，
// 此时 HashPassword 返回错误，handler 映射为 InvalidArgument。
func TestHashPassword_TooLong(t *testing.T) {
	if _, err := HashPassword(strings.Repeat("a", 73)); err == nil {
		t.Fatal("超过 72 字节的密码应返回错误")
	}
}
