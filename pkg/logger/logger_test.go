package logger

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestInit_Console(t *testing.T) {
	l, err := Init(Options{
		Output: Console,
		Level:  slog.LevelDebug,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if l == nil {
		t.Fatal("Init() returned nil logger")
	}
}

func TestInit_File(t *testing.T) {
	dir := t.TempDir()

	l, err := Init(Options{
		Output:    File,
		Level:     slog.LevelInfo,
		Directory: dir,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if l == nil {
		t.Fatal("Init() returned nil logger")
	}

	// 写入日志
	l.Info("test message", "key", "value")

	// 验证日志文件存在
	logFile := filepath.Join(dir, "app.log")
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		t.Errorf("log file not created: %s", logFile)
	}

	// 清理
	Close()
}

func TestInit_Both(t *testing.T) {
	dir := t.TempDir()

	l, err := Init(Options{
		Output:    Both,
		Level:     slog.LevelInfo,
		Directory: dir,
		Format:    Text,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if l == nil {
		t.Fatal("Init() returned nil logger")
	}

	// 写入日志
	l.Info("test message", "key", "value")

	// 验证日志文件存在
	logFile := filepath.Join(dir, "app.log")
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		t.Errorf("log file not created: %s", logFile)
	}

	// 清理
	Close()
}

func TestInit_WithRotateOptions(t *testing.T) {
	dir := t.TempDir()

	l, err := Init(Options{
		Output:    File,
		Level:     slog.LevelDebug,
		Directory: dir,
		Rotate: &RotateOptions{
			MaxSize:    200,
			MaxBackups: 14,
			MaxAge:     60,
			Compress:   true,
		},
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if l == nil {
		t.Fatal("Init() returned nil logger")
	}

	// 清理
	Close()
}

func TestInit_Validation(t *testing.T) {
	// 测试输出到文件但未指定目录
	_, err := Init(Options{
		Output: File,
		Level:  slog.LevelInfo,
	})
	if err == nil {
		t.Error("Init() should fail when Output is File but Directory is empty")
	}

	// 测试输出到 Both 但未指定目录
	_, err = Init(Options{
		Output: Both,
		Level:  slog.LevelInfo,
	})
	if err == nil {
		t.Error("Init() should fail when Output is Both but Directory is empty")
	}
}

func TestMergeRotateOptions(t *testing.T) {
	// 测试 nil 配置
	result := mergeRotateOptions(nil)
	if result.MaxSize != 100 {
		t.Errorf("mergeRotateOptions(nil) MaxSize = %d, want 100", result.MaxSize)
	}
	if result.MaxBackups != 7 {
		t.Errorf("mergeRotateOptions(nil) MaxBackups = %d, want 7", result.MaxBackups)
	}
	if result.MaxAge != 30 {
		t.Errorf("mergeRotateOptions(nil) MaxAge = %d, want 30", result.MaxAge)
	}
	if result.Compress != true {
		t.Error("mergeRotateOptions(nil) Compress should be true")
	}

	// 测试自定义配置
	result = mergeRotateOptions(&RotateOptions{
		MaxSize:    200,
		MaxBackups: 14,
		MaxAge:     60,
		Compress:   true,
	})
	if result.MaxSize != 200 {
		t.Errorf("mergeRotateOptions() MaxSize = %d, want 200", result.MaxSize)
	}
	if result.MaxBackups != 14 {
		t.Errorf("mergeRotateOptions() MaxBackups = %d, want 14", result.MaxBackups)
	}
	if result.MaxAge != 60 {
		t.Errorf("mergeRotateOptions() MaxAge = %d, want 60", result.MaxAge)
	}
	if result.Compress != true {
		t.Error("mergeRotateOptions() Compress should be true")
	}
}

func TestConvenienceFunctions(t *testing.T) {
	// 初始化全局 logger
	_, err := Init(Options{
		Output: Console,
		Level:  slog.LevelDebug,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// 测试便捷函数
	Info("info message", "key", "value")
	Error("error message", "key", "value")
	Debug("debug message", "key", "value")
	Warn("warn message", "key", "value")

	// 测试 With
	l := With("request_id", "123")
	l.Info("with message")

	// 测试 WithGroup
	lg := WithGroup("auth")
	lg.Info("group message")
}
