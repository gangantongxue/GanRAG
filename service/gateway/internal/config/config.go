// Package config 定义 Gateway 服务的配置结构及类型转换
//
// 配置由 pkg/config 从 YAML 文件与环境变量读取（环境变量前缀 GANRAG_），
// 本包负责合法性校验：端口范围、JWT 验签密钥、下游 gRPC 地址、限流参数等。
package config

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/gangantongxue/GanRAG/pkg/logger"
)

// Config Gateway 服务总配置
type Config struct {
	Server    ServerConfig    `mapstructure:"server"`     // HTTP 监听配置
	JWT       JWTConfig       `mapstructure:"jwt"`        // access token 验签配置（与 user 服务共享密钥）
	Upstream  UpstreamConfig  `mapstructure:"upstream"`   // 下游 gRPC 服务地址
	RateLimit RateLimitConfig `mapstructure:"rate_limit"` // 限流配置
	Log       LogConfig       `mapstructure:"log"`        // 日志配置
}

// ServerConfig HTTP 服务监听配置
type ServerConfig struct {
	Host string `mapstructure:"host"` // 监听地址，如 0.0.0.0
	Port int    `mapstructure:"port"` // 监听端口
}

// Addr 返回监听地址（host:port）
func (c ServerConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// JWTConfig access token 验签配置
//
// 密钥由 user 服务（签发）与 Gateway（校验）通过共享配置持有，验签本地完成不回源。
type JWTConfig struct {
	Secret string `mapstructure:"secret"` // HS256 共享密钥（必填，空值启动报错）
	Issuer string `mapstructure:"issuer"` // 签发方标识（校验 iss 声明）
}

// UpstreamConfig 下游 gRPC 服务地址
type UpstreamConfig struct {
	User string `mapstructure:"user"` // user 服务 gRPC 地址（host:port）
}

// RateRule 单条限流规则：令牌桶速率与容量
type RateRule struct {
	QPS   float64 `mapstructure:"qps"`   // 平均每秒允许的请求数（令牌补充速率）
	Burst int     `mapstructure:"burst"` // 桶容量，允许的突发请求数
}

// RateLimitConfig 限流配置
//
// Auth 用于 /api/v1/auth/* 登录注册类路由（更严，防撞库爆破），
// Default 用于其余需验签的业务路由。
type RateLimitConfig struct {
	Auth    RateRule `mapstructure:"auth"`    // auth 路由规则
	Default RateRule `mapstructure:"default"` // 普通路由规则
}

// LogConfig 日志配置
type LogConfig struct {
	Output    string `mapstructure:"output"`    // 输出目标：console | file | both
	Level     string `mapstructure:"level"`     // 日志级别：debug | info | warn | error
	Format    string `mapstructure:"format"`    // 日志格式：json | text
	Directory string `mapstructure:"directory"` // 文件输出目录（output 为 file/both 时必填）
}

// Validate 校验配置的合法性
//
// 返回：
//   - error: 存在非法配置项时返回具体错误，全部合法返回 nil
func (c *Config) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port 不合法: %d（应在 1-65535 之间）", c.Server.Port)
	}

	// JWT：验签密钥为空直接报错，不做硬编码默认值（与 user 服务签发侧一致）
	if c.JWT.Secret == "" {
		return fmt.Errorf("jwt.secret 不能为空（建议通过 GANRAG_JWT_SECRET 环境变量注入）")
	}
	if c.JWT.Issuer == "" {
		c.JWT.Issuer = "ganrag" // 签发方默认值（与 user 服务一致）
	}

	if c.Upstream.User == "" {
		return fmt.Errorf("upstream.user 不能为空（user 服务 gRPC 地址，如 127.0.0.1:50051）")
	}

	if err := c.RateLimit.Auth.validate("rate_limit.auth"); err != nil {
		return err
	}
	if err := c.RateLimit.Default.validate("rate_limit.default"); err != nil {
		return err
	}

	// 日志配置的转换过程即校验过程
	if _, err := c.LoggerOptions(); err != nil {
		return err
	}
	return nil
}

// validate 校验单条限流规则
//
// 参数：
//   - prefix: 配置项路径前缀，用于错误信息定位
//
// 返回：
//   - error: 规则非法时返回错误
func (r RateRule) validate(prefix string) error {
	if r.QPS <= 0 {
		return fmt.Errorf("%s.qps 必须为正数: %g", prefix, r.QPS)
	}
	if r.Burst < 1 {
		return fmt.Errorf("%s.burst 必须 >= 1: %d", prefix, r.Burst)
	}
	return nil
}

// LoggerOptions 将日志配置转换为 pkg/logger 的初始化选项
//
// 返回：
//   - logger.Options: 日志组件初始化选项
//   - error: 配置值非法时返回错误
func (c *Config) LoggerOptions() (logger.Options, error) {
	level, err := parseLevel(c.Log.Level)
	if err != nil {
		return logger.Options{}, err
	}
	output, err := parseOutput(c.Log.Output)
	if err != nil {
		return logger.Options{}, err
	}
	format, err := parseFormat(c.Log.Format)
	if err != nil {
		return logger.Options{}, err
	}
	return logger.Options{
		Output:    output,
		Level:     level,
		Directory: c.Log.Directory,
		Format:    format,
	}, nil
}

// parseLevel 解析日志级别，空值默认 info
//
// 使用 slog.Level 自带的文本解析能力，支持 debug/info/warn/error（大小写不敏感）
func parseLevel(s string) (slog.Level, error) {
	if s == "" {
		return slog.LevelInfo, nil
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToUpper(s))); err != nil {
		return 0, fmt.Errorf("log.level 不支持: %q（可选 debug | info | warn | error）", s)
	}
	return level, nil
}

// parseOutput 解析日志输出目标，空值默认 both
func parseOutput(s string) (logger.OutputType, error) {
	switch strings.ToLower(s) {
	case "", "both":
		return logger.Both, nil
	case "console":
		return logger.Console, nil
	case "file":
		return logger.File, nil
	default:
		return 0, fmt.Errorf("log.output 不支持: %q（可选 console | file | both）", s)
	}
}

// parseFormat 解析日志格式，空值默认 text
func parseFormat(s string) (logger.LogFormat, error) {
	switch strings.ToLower(s) {
	case "", "text":
		return logger.Text, nil
	case "json":
		return logger.JSON, nil
	default:
		return 0, fmt.Errorf("log.format 不支持: %q（可选 json | text）", s)
	}
}
