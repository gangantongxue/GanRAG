// Package config 定义 Vector Store 服务的配置结构及类型转换
//
// 配置由 pkg/config 从 YAML 文件与环境变量读取（环境变量前缀 GANRAG_），
// 本包负责合法性校验，并将字符串配置项转换为 pkg/logger 等组件的类型。
package config

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/gangantongxue/GanRAG/pkg/logger"
)

// 存储模式常量
const (
	StorageModeMemory     = "memory"     // 内存模式：进程退出后数据丢失，适合开发调试
	StorageModePersistent = "persistent" // 持久化模式：写入同步落盘，进程重启后自动加载
)

// Config Vector Store 服务总配置
type Config struct {
	Server  ServerConfig  `mapstructure:"server"`  // gRPC 服务配置
	Storage StorageConfig `mapstructure:"storage"` // 向量存储配置
	Log     LogConfig     `mapstructure:"log"`     // 日志配置
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

// StorageConfig 向量存储配置
type StorageConfig struct {
	Mode     string `mapstructure:"mode"`     // 存储模式：memory | persistent
	Path     string `mapstructure:"path"`     // persistent 模式下的数据目录
	Compress bool   `mapstructure:"compress"` // persistent 模式下是否对落盘文件 gzip 压缩
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

	switch c.Storage.Mode {
	case StorageModeMemory:
	case StorageModePersistent:
		if c.Storage.Path == "" {
			return fmt.Errorf("storage.mode 为 %s 时 storage.path 不能为空", StorageModePersistent)
		}
	default:
		return fmt.Errorf("storage.mode 不支持: %q（可选 %s | %s）", c.Storage.Mode, StorageModeMemory, StorageModePersistent)
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
