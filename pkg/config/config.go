// Package config 提供通用配置读取功能
//
// 支持 YAML 配置文件和 .env 环境变量文件，使用泛型实现类型安全的配置读取。
// 各 service 可定义自己的配置结构体，调用 Read[T]() 即可完成配置加载。
package config

import (
	"fmt"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// Options 配置读取选项，由调用方传入
type Options struct {
	ConfigPath string // YAML 配置文件路径，如 "configs/config.yaml"
	EnvPath    string // .env 文件路径，如 ".env"，为空则跳过加载
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
	// 1. 加载 .env 文件（如果指定了路径）
	if opts.EnvPath != "" {
		if err := godotenv.Load(opts.EnvPath); err != nil {
			return nil, fmt.Errorf("加载 .env 文件失败: %w", err)
		}
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
