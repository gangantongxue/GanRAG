// Package config 提供通用配置读取功能
//
// 支持 YAML 配置文件和 .env 环境变量文件，使用泛型实现类型安全的配置读取。
// 各 service 可定义自己的配置结构体，调用 Read[T]() 即可完成配置加载。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// Options 配置读取选项，由调用方传入
type Options struct {
	ConfigPath string // YAML 配置文件路径，如 "configs/config.yaml"
	EnvPath    string // .env 文件路径；为空时自动加载仓库根 .env（全项目统一，见 loadEnv）
	EnvPrefix  string // 环境变量前缀，如 "GANRAG"，为空则不添加前缀
}

// Read 读取配置到指定结构体 T
//
// 参数：
//   - opts: 配置读取选项，包含配置文件路径、env 文件路径、env 前缀
//
// 返回：
//   - *T: 配置结构体指针
//   - error: 读取或解析过程中的错误
func Read[T any](opts Options) (*T, error) {
	// 1. 加载 .env：显式路径优先，缺省统一读仓库根 .env
	if err := loadEnv(opts.EnvPath); err != nil {
		return nil, fmt.Errorf("加载 .env 文件失败: %w", err)
	}

	// 2. 初始化 viper 读取 YAML 配置
	v := viper.New()
	if opts.ConfigPath != "" {
		v.SetConfigFile(opts.ConfigPath)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("读取配置文件失败: %w", err)
		}
	}

	// 3. 设置环境变量前缀并绑定
	if opts.EnvPrefix != "" {
		v.SetEnvPrefix(opts.EnvPrefix)
		// 将环境变量中的 _ 替换为 .，支持嵌套配置
		v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
		// 自动绑定所有环境变量
		v.AutomaticEnv()
	}

	// 4. 解析到结构体
	var cfg T
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}

	return &cfg, nil
}

// loadEnv 加载 .env 环境变量文件
//
// 全项目统一读仓库根 .env（服务无论从哪个目录启动都生效）：
//   - envPath 非空：按显式路径加载（不存在即报错，明确指定不允许静默跳过）
//   - envPath 为空：向上定位仓库根（go.mod 所在目录）加载根 .env，
//     根 .env 不存在时跳过（如容器内由编排注入环境变量）
//
// 参数：
//   - envPath: 显式 .env 路径，空串表示自动加载仓库根 .env
//
// 返回：
//   - error: 显式路径加载失败时返回错误；自动模式恒返回 nil
func loadEnv(envPath string) error {
	if envPath != "" {
		return godotenv.Load(envPath)
	}
	if root := findRepoRoot(); root != "" {
		// 根 .env 缺失不报错：容器等场景由编排注入环境变量，无需 .env 文件
		_ = godotenv.Load(filepath.Join(root, ".env"))
	}
	return nil
}

// findRepoRoot 从当前目录向上查找 go.mod 所在目录，作为仓库根
//
// 返回：
//   - string: 仓库根目录路径；找不到（如容器内无源码）返回空串
func findRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir { // 已到文件系统根，仍未找到
			return ""
		}
		dir = parent
	}
}
