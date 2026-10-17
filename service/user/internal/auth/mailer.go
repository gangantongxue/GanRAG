// 验证码邮件发送通道：SMTP 下发；mail.host 未配置时降级为日志输出（仅限本地开发联调）
package auth

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"mime"
	"net/smtp"
	"strings"

	"github.com/gangantongxue/GanRAG/service/user/internal/config"
)

// Mailer 验证码邮件发送器
type Mailer struct {
	cfg config.MailConfig // SMTP 配置（host 为空即 dev 降级）
	log *slog.Logger      // 日志（dev 降级输出与失败记录）
}

// NewMailer 创建邮件发送器
//
// 参数：
//   - cfg: SMTP 配置
//   - log: 日志实例
//
// 返回：
//   - *Mailer: 发送器实例
func NewMailer(cfg config.MailConfig, log *slog.Logger) *Mailer {
	return &Mailer{cfg: cfg, log: log}
}

// SendCode 发送验证码邮件（主题固定，正文按场景区分）
//
// 参数：
//   - email: 收件邮箱（调用方已归一）
//   - scene: 场景（register / reset_password）
//   - code: 明文验证码
//
// 返回：
//   - error: 发送失败时返回错误；dev 降级模式恒返回 nil
func (m *Mailer) SendCode(email, scene, code string) error {
	subject := "【GanRAG】邮箱验证码"
	body := fmt.Sprintf(
		"你正在使用「%s」功能，验证码：%s（5 分钟内有效，请勿泄露给他人）。\n若非本人操作，请忽略本邮件。",
		sceneLabel(scene), code,
	)
	return m.send(email, subject, body)
}

// send 发送纯文本邮件（mail.host 未配置时降级日志输出）
//
// 参数：
//   - to: 收件人
//   - subject: 主题
//   - body: 正文（纯文本）
//
// 返回：
//   - error: SMTP 发送失败时返回错误
func (m *Mailer) send(to, subject, body string) error {
	if m.cfg.Host == "" {
		// dev 降级：无 SMTP 环境把内容写日志，本地联调不需要真实邮箱（生产必须配置 mail.host）
		m.log.Warn("SMTP 未配置，验证码邮件降级为日志输出", "to", to, "subject", subject, "body", body)
		return nil
	}

	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)
	msg := buildMessage(m.cfg.From, to, subject, body)
	// PlainAuth 仅在 TLS（或 localhost）下发送凭据，防止明文泄露密码
	if err := smtp.SendMail(addr, smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host), m.cfg.From, []string{to}, msg); err != nil {
		return fmt.Errorf("发送邮件失败: %w", err)
	}
	return nil
}

// buildMessage 组装 RFC 5322 纯文本邮件（UTF-8 主题 Q 编码 + base64 正文）
//
// 参数：
//   - from: 发件人
//   - to: 收件人
//   - subject: 主题（UTF-8 明文，函数内编码）
//   - body: 正文（UTF-8 明文，函数内编码）
//
// 返回：
//   - []byte: 可直接投递的邮件字节
func buildMessage(from, to, subject, body string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\n", from, to)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=\"utf-8\"\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(base64.StdEncoding.EncodeToString([]byte(body)))
	b.WriteString("\r\n")
	return []byte(b.String())
}

// sceneLabel 场景中文名（邮件正文展示）
func sceneLabel(scene string) string {
	if scene == SceneResetPassword {
		return "找回密码"
	}
	return "注册"
}
