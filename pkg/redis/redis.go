// Package redis 是 GanRAG 仓库的 Redis 基础设施包，职责：
//
//  1. 连接管理：Open 提供「配置校验 -> 建连 -> PING 探活」一站式接入，各服务直接复用
//  2. 配置统一：Config 与各服务 configs/config.yaml 的 redis 段一一对应（mapstructure 标签），
//     各服务 internal/config.Config 直接嵌入本结构体复用（同 pkg/db.Config）
//
// 约定：Redis key 命名遵循根 AGENTS.md「Redis Key 规范」（<服务短名>:<域>:<标识>）；
// 连接统一由本包提供，各服务不各自手写 go-redis 客户端初始化（当前用于 user 服务邮箱验证码，
// 后续缓存、会话黑名单等用途同样经本包接入）。
package redis

import (
	"context"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Config Redis 连接配置
//
// 字段与各服务 configs/config.yaml 的 redis 段一一对应（mapstructure 标签），
// 各服务 internal/config.Config 直接嵌入本结构体复用。
type Config struct {
	Host     string `mapstructure:"host"`     // Redis 主机（本地 127.0.0.1；容器网络内为容器名 ganrag-redis）
	Port     int    `mapstructure:"port"`     // Redis 端口
	Password string `mapstructure:"password"` // 访问密码（建议 GANRAG_REDIS_PASSWORD 环境变量覆盖，不落配置文件）
	DB       int    `mapstructure:"db"`       // 库编号（默认 0；验证码等按需分库）
}

// Validate 校验配置的合法性
//
// 返回：
//   - error: 存在非法配置项时返回具体错误，全部合法返回 nil
func (c *Config) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("redis.host 不能为空")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("redis.port 不合法: %d（应在 1-65535 之间）", c.Port)
	}
	if c.DB < 0 {
		return fmt.Errorf("redis.db 不能为负: %d", c.DB)
	}
	return nil
}

// Open 打开 Redis 连接（各服务接入入口）
//
// 流程：校验配置 -> 建连 -> PING 探活（失败拒绝启动，与 dbpkg.Open 同口径，
// 避免带着不可用的缓存 / 验证码依赖提供服务）。
//
// 参数：
//   - cfg: Redis 连接配置
//
// 返回：
//   - *goredis.Client: go-redis 客户端（调用 Close 释放；各服务直接使用 go-redis API）
//   - error: 校验、连接或探活失败时返回错误
func Open(cfg Config) (*goredis.Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	client := goredis.NewClient(&goredis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	// 探活超时 5 秒：Redis 不可用时快速失败，拒绝启动
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("连接 Redis 失败: %w", err)
	}
	return client, nil
}

// Close 关闭 Redis 连接
//
// 参数：
//   - client: Open 返回的客户端
//
// 返回：
//   - error: 关闭失败时返回错误
func Close(client *goredis.Client) error {
	return client.Close()
}
