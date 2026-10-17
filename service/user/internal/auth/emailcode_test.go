package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

// newTestEmailCode 构造基于 miniredis 的验证码组件（go test 自治，不依赖外部 Redis）
//
// 返回：
//   - *EmailCode: 待测组件
//   - *miniredis.Miniredis: 内存 Redis（FastForward 推进 TTL 用）
func newTestEmailCode(t *testing.T) (*EmailCode, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewEmailCode(rdb), mr
}

// TestEmailCodeVerifyOnce 校验一次性：验过即删，二次提交失败
func TestEmailCodeVerifyOnce(t *testing.T) {
	e, _ := newTestEmailCode(t)
	ctx := t.Context()

	code, err := e.Issue(ctx, SceneRegister, "User@Example.com")
	if err != nil {
		t.Fatalf("Issue() 错误: %v", err)
	}
	if len(code) != emailCodeLen {
		t.Fatalf("验证码长度 = %d，期望 %d", len(code), emailCodeLen)
	}

	// 归一后的小写邮箱可验（键按归一值）
	if err := e.Verify(ctx, SceneRegister, "user@example.com", code); err != nil {
		t.Fatalf("首次 Verify() 错误: %v", err)
	}
	// 一次性：再次提交同一码失败
	if err := e.Verify(ctx, SceneRegister, "user@example.com", code); !errors.Is(err, ErrEmailCodeInvalid) {
		t.Errorf("二次 Verify() = %v，期望 ErrEmailCodeInvalid", err)
	}
}

// TestEmailCodeMaxTries 校验失败 5 次作废：作废后正确码也不再可用
func TestEmailCodeMaxTries(t *testing.T) {
	e, _ := newTestEmailCode(t)
	ctx := t.Context()

	code, err := e.Issue(ctx, SceneResetPassword, "a@b.com")
	if err != nil {
		t.Fatalf("Issue() 错误: %v", err)
	}

	// 连续 5 次错误：第 5 次触发作废
	for i := range EmailCodeMaxTries {
		if err := e.Verify(ctx, SceneResetPassword, "a@b.com", "000000"); !errors.Is(err, ErrEmailCodeInvalid) {
			t.Fatalf("第 %d 次错误 Verify() = %v，期望 ErrEmailCodeInvalid", i+1, err)
		}
	}
	// 作废后正确码也不可用
	if err := e.Verify(ctx, SceneResetPassword, "a@b.com", code); !errors.Is(err, ErrEmailCodeInvalid) {
		t.Errorf("作废后 Verify(正确码) = %v，期望 ErrEmailCodeInvalid", err)
	}
}

// TestEmailCodeTTL 校验 TTL 5 分钟：过期后验证码自然失效
func TestEmailCodeTTL(t *testing.T) {
	e, mr := newTestEmailCode(t)
	ctx := t.Context()

	code, err := e.Issue(ctx, SceneRegister, "a@b.com")
	if err != nil {
		t.Fatalf("Issue() 错误: %v", err)
	}

	mr.FastForward(EmailCodeTTL + time.Second)
	if err := e.Verify(ctx, SceneRegister, "a@b.com", code); !errors.Is(err, ErrEmailCodeInvalid) {
		t.Errorf("过期后 Verify() = %v，期望 ErrEmailCodeInvalid", err)
	}
}

// TestEmailCodeSendRateLimit 校验发送 60s 限频：限频窗口内拒绝，窗口过后恢复
func TestEmailCodeSendRateLimit(t *testing.T) {
	e, mr := newTestEmailCode(t)
	ctx := t.Context()

	if _, err := e.Issue(ctx, SceneRegister, "a@b.com"); err != nil {
		t.Fatalf("Issue() 错误: %v", err)
	}
	// 限频窗口内重发（不分场景）
	if _, err := e.Issue(ctx, SceneResetPassword, "a@b.com"); !errors.Is(err, ErrEmailCodeRateLimited) {
		t.Fatalf("限频窗口内 Issue() = %v，期望 ErrEmailCodeRateLimited", err)
	}

	// 窗口过后可再次发送
	mr.FastForward(EmailSendInterval + time.Second)
	if _, err := e.Issue(ctx, SceneRegister, "a@b.com"); err != nil {
		t.Errorf("窗口过后 Issue() 错误: %v", err)
	}
}
