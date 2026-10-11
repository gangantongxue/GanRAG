package config

import (
	"strings"
	"testing"
)

// validConfig 返回一份全合法的基线配置，各用例在其上做单点破坏
func validConfig() *Config {
	return &Config{
		Server:   ServerConfig{Host: "0.0.0.0", Port: 8080},
		JWT:      JWTConfig{Secret: "jwt-secret", Issuer: "ganrag"},
		Upstream: UpstreamConfig{User: "127.0.0.1:50051"},
		RateLimit: RateLimitConfig{
			Auth:    RateRule{QPS: 1, Burst: 5},
			Default: RateRule{QPS: 10, Burst: 20},
		},
		Log: LogConfig{Output: "both", Level: "info", Format: "text", Directory: "./logs"},
	}
}

// TestValidate_OK 验证合法配置通过校验
func TestValidate_OK(t *testing.T) {
	cfg := validConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("合法配置不应报错: %v", err)
	}
}

// TestValidate_Errors 表驱动验证各非法配置项的报错
func TestValidate_Errors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantSub string // 错误信息应包含的子串
	}{
		{"server.port 为 0", func(c *Config) { c.Server.Port = 0 }, "server.port"},
		{"server.port 超范围", func(c *Config) { c.Server.Port = 70000 }, "server.port"},
		{"jwt.secret 为空", func(c *Config) { c.JWT.Secret = "" }, "jwt.secret"},
		{"upstream.user 为空", func(c *Config) { c.Upstream.User = "" }, "upstream.user"},
		{"rate_limit.auth.qps 非正", func(c *Config) { c.RateLimit.Auth.QPS = 0 }, "rate_limit.auth.qps"},
		{"rate_limit.auth.burst 小于 1", func(c *Config) { c.RateLimit.Auth.Burst = 0 }, "rate_limit.auth.burst"},
		{"rate_limit.default.qps 非正", func(c *Config) { c.RateLimit.Default.QPS = -1 }, "rate_limit.default.qps"},
		{"rate_limit.default.burst 小于 1", func(c *Config) { c.RateLimit.Default.Burst = 0 }, "rate_limit.default.burst"},
		{"log.level 非法", func(c *Config) { c.Log.Level = "verbose" }, "log.level"},
		{"log.output 非法", func(c *Config) { c.Log.Output = "stdout" }, "log.output"},
		{"log.format 非法", func(c *Config) { c.Log.Format = "xml" }, "log.format"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("期望报错，实际通过")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("错误信息 %q 不包含 %q", err.Error(), tt.wantSub)
			}
		})
	}
}
