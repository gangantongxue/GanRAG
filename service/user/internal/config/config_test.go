package config

import (
	"strings"
	"testing"
	"time"

	dbpkg "github.com/gangantongxue/GanRAG/pkg/db"
)

// validConfig 返回一份全合法的基线配置，各用例在其上做单点破坏
func validConfig() *Config {
	return &Config{
		Server: ServerConfig{Host: "0.0.0.0", Port: 50051},
		Database: dbpkg.Config{
			Host:            "127.0.0.1",
			Port:            3306,
			User:            "ganrag",
			Password:        "secret",
			DBName:          "ganrag_user",
			MaxOpenConns:    20,
			MaxIdleConns:    5,
			ConnMaxLifetime: 30 * time.Minute,
			AutoMigrate:     true,
		},
		JWT: JWTConfig{
			Secret:     "jwt-secret",
			AccessTTL:  15 * time.Minute,
			RefreshTTL: 168 * time.Hour,
		},
		Log: LogConfig{Output: "both", Level: "info", Format: "text", Directory: "./logs"},
	}
}

// TestValidate_OK 验证合法配置通过校验，并补上签发方默认值
func TestValidate_OK(t *testing.T) {
	cfg := validConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("合法配置不应报错: %v", err)
	}
	if cfg.JWT.Issuer != "ganrag" {
		t.Errorf("Issuer 默认值 = %q，期望 ganrag", cfg.JWT.Issuer)
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
		{"database.host 为空", func(c *Config) { c.Database.Host = "" }, "database.host"},
		{"database.user 为空", func(c *Config) { c.Database.User = "" }, "database.user"},
		{"database.dbname 含非法字符", func(c *Config) { c.Database.DBName = "ganrag-user" }, "database.dbname"},
		{"database.port 非法", func(c *Config) { c.Database.Port = -1 }, "database.port"},
		{"jwt.secret 为空", func(c *Config) { c.JWT.Secret = "" }, "jwt.secret"},
		{"jwt.access_ttl 为 0", func(c *Config) { c.JWT.AccessTTL = 0 }, "jwt.access_ttl"},
		{"jwt.refresh_ttl 为 0", func(c *Config) { c.JWT.RefreshTTL = 0 }, "jwt.refresh_ttl"},
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
