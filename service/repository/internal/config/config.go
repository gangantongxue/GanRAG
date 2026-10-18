// Package config 定义 Repository 服务的配置结构及类型转换
//
// 配置由 pkg/config 从 YAML 文件与环境变量读取（环境变量前缀 GANRAG_），
// 本包负责合法性校验：端口、数据库（复用 pkg/db.Config）、Garage 对象存储、
// 整段上传上限与日志。
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	dbpkg "github.com/gangantongxue/GanRAG/pkg/db"
	"github.com/gangantongxue/GanRAG/pkg/logger"
)

// Config Repository 服务总配置
type Config struct {
	Server      ServerConfig      `mapstructure:"server"`      // gRPC 服务配置
	Database    dbpkg.Config      `mapstructure:"database"`    // MySQL 配置（pkg/db 统一定义与校验）
	ObjectStore ObjectStoreConfig `mapstructure:"objectstore"` // Garage S3 配置
	Upload      UploadConfig      `mapstructure:"upload"`      // 上传限制
	Log         LogConfig         `mapstructure:"log"`         // 日志配置
}

// ServerConfig gRPC 服务配置
type ServerConfig struct {
	Host string `mapstructure:"host"` // 监听地址
	Port int    `mapstructure:"port"` // 监听端口
}

// Addr 返回 gRPC 监听地址（host:port）
func (c ServerConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// ObjectStoreConfig Garage（S3 协议）配置
//
// access_key_id / secret_access_key 建议由环境变量注入；为空时回退到
// docker-compose 使用的 GARAGE_DEFAULT_ACCESS_KEY / GARAGE_DEFAULT_SECRET_KEY，
// 避免同一份密钥在 .env 中维护两遍。
type ObjectStoreConfig struct {
	Endpoint        string        `mapstructure:"endpoint"`          // S3 endpoint，如 http://127.0.0.1:3900
	Region          string        `mapstructure:"region"`            // Garage 固定 region
	Bucket          string        `mapstructure:"bucket"`            // 共享单桶
	AccessKeyID     string        `mapstructure:"access_key_id"`     // S3 Access Key
	SecretAccessKey string        `mapstructure:"secret_access_key"` // S3 Secret Key
	ForcePathStyle  bool          `mapstructure:"force_path_style"`  // Garage 需 Path Style
	Timeout         time.Duration `mapstructure:"timeout"`           // 单次 S3 调用超时
}

// UploadConfig 上传限制
type UploadConfig struct {
	WholeMaxSize int64 `mapstructure:"whole_max_size"` // 整段上传上限（字节），默认 2MB
}

// LogConfig 日志配置
type LogConfig struct {
	Output    string `mapstructure:"output"`    // 输出目标：console | file | both
	Level     string `mapstructure:"level"`     // 日志级别：debug | info | warn | error
	Format    string `mapstructure:"format"`    // 日志格式：json | text
	Directory string `mapstructure:"directory"` // 文件输出目录（output 为 file/both 时必填）
}

// Validate 校验配置的合法性，并补齐对象存储的安全默认值
func (c *Config) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port 不合法: %d（应在 1-65535 之间）", c.Server.Port)
	}
	if err := c.Database.Validate(); err != nil {
		return err
	}

	// Garage：endpoint/region/bucket 有默认值；AK/SK 回退 compose 使用的 GARAGE_DEFAULT_*
	if c.ObjectStore.Endpoint == "" {
		c.ObjectStore.Endpoint = "http://127.0.0.1:3900"
	}
	if c.ObjectStore.Region == "" {
		c.ObjectStore.Region = "garage"
	}
	if c.ObjectStore.Bucket == "" {
		c.ObjectStore.Bucket = os.Getenv("GARAGE_DEFAULT_BUCKET")
		if c.ObjectStore.Bucket == "" {
			c.ObjectStore.Bucket = "ganrag-objects"
		}
	}
	if c.ObjectStore.AccessKeyID == "" {
		c.ObjectStore.AccessKeyID = os.Getenv("GARAGE_DEFAULT_ACCESS_KEY")
	}
	if c.ObjectStore.SecretAccessKey == "" {
		c.ObjectStore.SecretAccessKey = os.Getenv("GARAGE_DEFAULT_SECRET_KEY")
	}
	if c.ObjectStore.AccessKeyID == "" || c.ObjectStore.SecretAccessKey == "" {
		return fmt.Errorf("objectstore.access_key_id / secret_access_key 不能为空（可用 GANRAG_OBJECTSTORE_* 或 GARAGE_DEFAULT_* 注入）")
	}
	if c.ObjectStore.Timeout <= 0 {
		c.ObjectStore.Timeout = 30 * time.Second
	}

	if c.Upload.WholeMaxSize <= 0 {
		c.Upload.WholeMaxSize = 2 * 1024 * 1024
	}
	// gRPC 默认单条消息 4MB；整段上限保持在其内，避免配置大于传输上限
	if c.Upload.WholeMaxSize > 4*1024*1024 {
		return fmt.Errorf("upload.whole_max_size 不能超过 4MB（gRPC 单条消息默认上限）")
	}

	if _, err := c.LoggerOptions(); err != nil {
		return err
	}
	return nil
}

// LoggerOptions 将日志配置转换为 pkg/logger 的初始化选项
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
