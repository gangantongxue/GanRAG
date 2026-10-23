// 邮箱验证码：生成、Redis 落储与校验（注册 / 找回密码共用）
//
// 语义（见根 docs/database.md「邮箱验证码」）：
//   - TTL 5 分钟，value 只存 SHA-256 哈希，不落明文；
//   - 一次性：校验成功即删；
//   - 失败 5 次作废（独立计数键，与码同生命周期）；
//   - 发送 60s 限频（同邮箱，不分场景）。
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/gangantongxue/GanRAG/pkg/errcode"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
)

// 验证码场景（scene）：注册 / 找回密码
const (
	SceneRegister      = "register"
	SceneResetPassword = "reset_password"
)

// 验证码行为参数（与 docs/database.md 口径一致，硬编码不外露配置）
const (
	EmailCodeTTL      = 5 * time.Minute  // 验证码有效期
	EmailCodeMaxTries = 5                // 失败多少次作废
	EmailSendInterval = 60 * time.Second // 同邮箱发送间隔（限频）
	emailCodeLen      = 6                // 验证码位数（十进制数字）
	emailMaxLen       = 254              // 邮箱最大长度（对齐 users.email VARCHAR(254)）
)

// 验证码相关哨兵错误（错误自带 gRPC 状态码，handler 走 errcode.Status 直出）
var (
	// ErrEmailCodeInvalid 验证码无效（错误 / 过期 / 已作废，不细分防探测）
	ErrEmailCodeInvalid = errcode.New(40106, codes.Unauthenticated, "验证码无效或已过期")
	// ErrEmailCodeRateLimited 触发 60s 发送限频
	ErrEmailCodeRateLimited = errcode.New(42901, codes.ResourceExhausted, "发送过于频繁，请稍后再试")
)

// emailRe 邮箱格式简单口径：x@y.z，不允许空白
var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// EmailCode 邮箱验证码组件（Redis 承载存储语义）
type EmailCode struct {
	rdb *goredis.Client
}

// NewEmailCode 创建邮箱验证码组件
//
// 参数：
//   - rdb: Redis 客户端（pkg/redis.Open 获取）
//
// 返回：
//   - *EmailCode: 验证码组件实例
func NewEmailCode(rdb *goredis.Client) *EmailCode {
	return &EmailCode{rdb: rdb}
}

// Issue 生成并保存验证码，返回明文验证码供邮件发送
//
// 流程：同邮箱 60s 限频（SET NX，占用即拒绝）-> 生成 6 位数字码 ->
// 存 SHA-256 哈希（TTL 5 分钟）并清零失败计数（新码重新给满额机会）。
//
// 参数：
//   - ctx: 请求上下文
//   - scene: 场景（register / reset_password，调用方先用 ValidScene 校验）
//   - email: 目标邮箱（调用方先用 ValidEmail 校验）
//
// 返回：
//   - string: 明文验证码
//   - error: ErrEmailCodeRateLimited 触发限频；其余为内部错误（errcode.Status 归一为 Internal）
func (e *EmailCode) Issue(ctx context.Context, scene, email string) (string, error) {
	email = NormalizeEmail(email)

	// 限频键按邮箱（不分场景）：SET NX EX 60，占用即拒绝发送
	ok, err := e.rdb.SetNX(ctx, limitKey(email), "", EmailSendInterval).Result()
	if err != nil {
		return "", fmt.Errorf("写入发送限频键失败: %w", err)
	}
	if !ok {
		return "", ErrEmailCodeRateLimited
	}

	code, err := generateEmailCode()
	if err != nil {
		return "", err
	}
	// 存哈希 + 清零失败计数（新码替换旧码，旧码自然失效）
	pipe := e.rdb.TxPipeline()
	pipe.Set(ctx, codeKey(scene, email), hashEmailCode(code), EmailCodeTTL)
	pipe.Del(ctx, triesKey(scene, email))
	if _, err := pipe.Exec(ctx); err != nil {
		return "", fmt.Errorf("保存验证码失败: %w", err)
	}
	return code, nil
}

// Verify 校验验证码：成功一次性删除；失败计数，满额作废
//
// 参数：
//   - ctx: 请求上下文
//   - scene: 场景（register / reset_password）
//   - email: 目标邮箱
//   - code: 用户提交的验证码
//
// 返回：
//   - error: nil 校验通过；ErrEmailCodeInvalid 校验失败（含失败满额作废后再次提交）；
//     其余为内部错误
func (e *EmailCode) Verify(ctx context.Context, scene, email, code string) error {
	email = NormalizeEmail(email)

	stored, err := e.rdb.Get(ctx, codeKey(scene, email)).Result()
	if errors.Is(err, goredis.Nil) {
		return ErrEmailCodeInvalid
	}
	if err != nil {
		return fmt.Errorf("读取验证码失败: %w", err)
	}

	// 哈希定长比较，防时序侧信道
	if subtle.ConstantTimeCompare([]byte(stored), []byte(hashEmailCode(code))) == 1 {
		// 一次性：成功即删（含失败计数键）
		if err := e.rdb.Del(ctx, codeKey(scene, email), triesKey(scene, email)).Err(); err != nil {
			return fmt.Errorf("删除验证码失败: %w", err)
		}
		return nil
	}

	// 失败计数 +1（计数键与码同生命周期）
	n, err := e.rdb.Incr(ctx, triesKey(scene, email)).Result()
	if err != nil {
		return fmt.Errorf("更新失败计数失败: %w", err)
	}
	// 计数键可能刚创建，补齐 TTL（已存在时 EXPIRE 只续不重置，幂等）
	if err := e.rdb.Expire(ctx, triesKey(scene, email), EmailCodeTTL).Err(); err != nil {
		return fmt.Errorf("更新失败计数 TTL 失败: %w", err)
	}
	if n >= EmailCodeMaxTries {
		// 失败满额：作废验证码（删除码与计数，正确码也不再可用）
		if err := e.rdb.Del(ctx, codeKey(scene, email), triesKey(scene, email)).Err(); err != nil {
			return fmt.Errorf("作废验证码失败: %w", err)
		}
	}
	return ErrEmailCodeInvalid
}

// ValidScene 校验验证码场景是否受支持
//
// 参数：
//   - scene: 场景字符串
//
// 返回：
//   - bool: true 合法（register / reset_password）
func ValidScene(scene string) bool {
	return scene == SceneRegister || scene == SceneResetPassword
}

// NormalizeEmail 邮箱归一：去首尾空白 + 小写（键与唯一性口径统一按归一值）
//
// 参数：
//   - email: 原始邮箱
//
// 返回：
//   - string: 归一后的邮箱
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ValidEmail 校验邮箱格式（简单口径：x@y.z，长度 ≤254，不含空白）
//
// 参数：
//   - email: 待校验邮箱（建议先 NormalizeEmail）
//
// 返回：
//   - bool: true 合法
func ValidEmail(email string) bool {
	return len(email) <= emailMaxLen && emailRe.MatchString(email)
}

// generateEmailCode 生成 6 位数字验证码（crypto/rand，不可预测）
//
// 返回：
//   - string: 定长 6 位验证码（前补零）
//   - error: 随机源失败时返回错误
func generateEmailCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("生成验证码失败: %w", err)
	}
	code := fmt.Sprintf("%0*d", emailCodeLen, n.Int64())
	return code, nil
}

// hashEmailCode 计算验证码的 SHA-256 哈希（hex 小写），库存只存哈希
//
// 参数：
//   - code: 明文验证码
//
// 返回：
//   - string: 64 位 hex 哈希
func hashEmailCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// key 构造遵循根 AGENTS.md「Redis Key 规范」：<服务短名>:<域>:<标识>
//（email 由调用方 NormalizeEmail 归一，scene 经 ValidScene 白名单约束）

// codeKey 验证码哈希键
func codeKey(scene, email string) string {
	return "user:email_code:" + scene + ":" + email
}

// triesKey 校验失败计数键（辅助键，与码同生命周期）
func triesKey(scene, email string) string {
	return "user:email_code_tries:" + scene + ":" + email
}

// limitKey 发送限频键（按邮箱，不分场景）
func limitKey(email string) string {
	return "user:email_code_limit:" + email
}
