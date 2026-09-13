package config

import (
	"os"
	"path/filepath"
	"testing"
)

// 测试配置结构体
type TestConfig struct {
	Server   ServerConfig   `mapstructure:"server"`
	Database DatabaseConfig `mapstructure:"database"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Name     string `mapstructure:"name"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
}

// writeTestConfig 在临时目录创建 YAML 配置文件
func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("创建测试配置文件失败: %v", err)
	}
	return path
}

// writeTestEnvFile 在临时目录创建 .env 文件
func writeTestEnvFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("创建测试 .env 文件失败: %v", err)
	}
	return path
}

// TestReadYAML 测试读取 YAML 配置文件
func TestReadYAML(t *testing.T) {
	cfgPath := writeTestConfig(t, `
server:
  host: "0.0.0.0"
  port: 8080
database:
  host: "localhost"
  port: 3306
  name: "ganrag"
  user: "root"
`)

	cfg, err := Read[TestConfig](Options{
		ConfigPath: cfgPath,
	})
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}

	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("Server.Host = %q, 期望 %q", cfg.Server.Host, "0.0.0.0")
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("Server.Port = %d, 期望 %d", cfg.Server.Port, 8080)
	}
	if cfg.Database.Host != "localhost" {
		t.Errorf("Database.Host = %q, 期望 %q", cfg.Database.Host, "localhost")
	}
	if cfg.Database.Port != 3306 {
		t.Errorf("Database.Port = %d, 期望 %d", cfg.Database.Port, 3306)
	}
	if cfg.Database.Name != "ganrag" {
		t.Errorf("Database.Name = %q, 期望 %q", cfg.Database.Name, "ganrag")
	}
}

// TestReadEnvFile 测试读取 .env 文件并覆盖 YAML 配置
func TestReadEnvFile(t *testing.T) {
	cfgPath := writeTestConfig(t, `
server:
  host: "0.0.0.0"
  port: 8080
database:
  host: "localhost"
  port: 3306
  name: "ganrag"
  user: "root"
  password: ""
`)

	envPath := writeTestEnvFile(t, `
GANRAG_SERVER_HOST=127.0.0.1
GANRAG_DATABASE_PASSWORD=test_password_123
`)

	cfg, err := Read[TestConfig](Options{
		ConfigPath: cfgPath,
		EnvPath:    envPath,
		EnvPrefix:  "GANRAG",
	})
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}

	// 验证 .env 覆盖了 YAML 中的值
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("Server.Host = %q, 期望 %q (从 .env 覆盖)", cfg.Server.Host, "127.0.0.1")
	}
	// 验证 .env 中的字段
	if cfg.Database.Password != "test_password_123" {
		t.Errorf("Database.Password = %q, 期望 %q", cfg.Database.Password, "test_password_123")
	}
}

// TestReadEnvOverride 测试系统环境变量覆盖
func TestReadEnvOverride(t *testing.T) {
	cfgPath := writeTestConfig(t, `
server:
  host: "0.0.0.0"
  port: 8080
`)

	os.Setenv("GANRAG_SERVER_PORT", "9090")
	defer os.Unsetenv("GANRAG_SERVER_PORT")

	cfg, err := Read[TestConfig](Options{
		ConfigPath: cfgPath,
		EnvPrefix:  "GANRAG",
	})
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Errorf("Server.Port = %d, 期望 %d (从环境变量覆盖)", cfg.Server.Port, 9090)
	}
}

// TestReadEmptyOptions 测试空选项返回零值结构体
func TestReadEmptyOptions(t *testing.T) {
	cfg, err := Read[TestConfig](Options{})
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}

	if cfg.Server.Host != "" {
		t.Errorf("Server.Host = %q, 期望空字符串", cfg.Server.Host)
	}
	if cfg.Server.Port != 0 {
		t.Errorf("Server.Port = %d, 期望 0", cfg.Server.Port)
	}
}

// TestReadFileNotFound 测试配置文件不存在时返回错误
func TestReadFileNotFound(t *testing.T) {
	_, err := Read[TestConfig](Options{
		ConfigPath: "/nonexistent/path/config.yaml",
	})
	if err == nil {
		t.Error("期望返回错误，但没有错误")
	}
}

// TestReadEnvFileNotFound 测试 .env 文件不存在时返回错误
func TestReadEnvFileNotFound(t *testing.T) {
	_, err := Read[TestConfig](Options{
		EnvPath: "/nonexistent/path/.env",
	})
	if err == nil {
		t.Error("期望返回错误，但没有错误")
	}
}

// TestReadNestedEnvOverride 测试嵌套环境变量覆盖（database.host）
func TestReadNestedEnvOverride(t *testing.T) {
	cfgPath := writeTestConfig(t, `
database:
  host: "localhost"
`)

	os.Setenv("GANRAG_DATABASE_HOST", "db.example.com")
	defer os.Unsetenv("GANRAG_DATABASE_HOST")

	cfg, err := Read[TestConfig](Options{
		ConfigPath: cfgPath,
		EnvPrefix:  "GANRAG",
	})
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}

	if cfg.Database.Host != "db.example.com" {
		t.Errorf("Database.Host = %q, 期望 %q (从嵌套环境变量覆盖)", cfg.Database.Host, "db.example.com")
	}
}
