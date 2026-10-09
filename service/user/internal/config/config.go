// Package config 定义 User 服务的配置结构及类型转换
//
// 配置由 pkg/config 从 YAML 文件与环境变量读取（环境变量前缀 GANRAG_），
// 本包负责合法性校验：端口范围、数据库（复用 pkg/db.Config）、jwt.secret 必填等。
package config

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	dbpkg "github.com/gangantongxue/GanRAG/pkg/db"
	"github.com/gangantongxue/GanRAG/pkg/logger"
)

// Config User 服务总配置
type Config struct {
	Server   ServerConfig `mapstructure:"server"`   // gRPC 服务配置
	Database dbpkg.Config `mapstructure:"database"` // MySQL 配置（pkg/db 统一定义与校验）
	JWT      JWTConfig    `mapstructure:"jwt"`      // JWT 签发配置
	Log      LogConfig    `mapstructure:"log"`      // 日志配置
}

// ServerConfig gRPC 服务配置
type ServerConfig struct {
	Host string `mapstructure:"host"` // 监听地址，如 0.0.0.0
	Port int    `mapstructure:"port"` // 监听端口
}

// Addr 返回 gRPC 监听地址（host:port）
func (c ServerConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// JWTConfig 双 token 签发配置
type JWTConfig struct {
	Secret     string        `mapstructure:"secret"`      // HS256 签发密钥（必填，空值启动报错）
	Issuer     string        `mapstructure:"issuer"`      // 签发方标识
	AccessTTL  time.Duration `mapstructure:"access_ttl"`  // access token 有效期（如 15m）
	RefreshTTL time.Duration `mapstructure:"refresh_ttl"` // refresh token 有效期（如 168h）
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

	// 数据库配置由 pkg/db 统一校验（必填项、库名合法性等）
	if err := c.Database.Validate(); err != nil {
		return err
	}

	// JWT：secret 为空直接报错，不做硬编码默认值
	if c.JWT.Secret == "" {
		return fmt.Errorf("jwt.secret 不能为空（建议通过 GANRAG_JWT_SECRET 环境变量注入）")
	}
	if c.JWT.Issuer == "" {
		c.JWT.Issuer = "ganrag" // 签发方默认值
	}
	if c.JWT.AccessTTL <= 0 {
		return fmt.Errorf("jwt.access_ttl 必须为正时长: %s", c.JWT.AccessTTL)
	}
	if c.JWT.RefreshTTL <= 0 {
		return fmt.Errorf("jwt.refresh_ttl 必须为正时长: %s", c.JWT.RefreshTTL)
	}

	// 日志配置的转换过程即校验过程
	if _, err := c.LoggerOptions(); err != nil {
		return err
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
