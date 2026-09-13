package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/lumberjack.v2"
)

// 默认轮转配置
var defaultRotate = &RotateOptions{
	MaxSize:    100, // 100MB
	MaxBackups: 7,   // 7 个
	MaxAge:     30,  // 30 天
	Compress:   true,
}

// Init 初始化 logger
//
// 功能：
//  1. 根据配置创建 logger 实例
//  2. 设置为全局默认 logger
//  3. 返回 logger 实例，供 GORM 等场景使用
//
// 参数：
//   - opts: 配置选项，包含输出目标、日志级别、目录、格式和轮转配置
//
// 返回：
//   - *slog.Logger: logger 实例
//   - error: 初始化过程中的错误
func Init(opts Options) (*slog.Logger, error) {
	// 校验参数
	if err := validateOptions(opts); err != nil {
		return nil, fmt.Errorf("logger 初始化参数校验失败: %w", err)
	}

	// 合并默认轮转配置
	rotateOpts := mergeRotateOptions(opts.Rotate)

	// 创建 writer
	writer, cleanup, err := createWriter(opts.Output, opts.Directory, rotateOpts)
	if err != nil {
		return nil, fmt.Errorf("创建日志 writer 失败: %w", err)
	}

	// 创建 handler
	handler := createHandler(writer, opts.Level, opts.Format)

	// 创建 logger
	l := slog.New(handler)

	// 设置为全局默认
	slog.SetDefault(l)

	// 注册清理函数
	registerCleanup(cleanup)

	return l, nil
}

// Info 使用全局 logger 记录 Info 级别日志
func Info(msg string, args ...any) {
	slog.Info(msg, args...)
}

// Error 使用全局 logger 记录 Error 级别日志
func Error(msg string, args ...any) {
	slog.Error(msg, args...)
}

// Debug 使用全局 logger 记录 Debug 级别日志
func Debug(msg string, args ...any) {
	slog.Debug(msg, args...)
}

// Warn 使用全局 logger 记录 Warn 级别日志
func Warn(msg string, args ...any) {
	slog.Warn(msg, args...)
}

// With 使用全局 logger 创建带属性的子 logger
func With(args ...any) *slog.Logger {
	return slog.Default().With(args...)
}

// WithGroup 使用全局 logger 创建带分组的子 logger
func WithGroup(name string) *slog.Logger {
	return slog.Default().WithGroup(name)
}

// validateOptions 校验配置选项
func validateOptions(opts Options) error {
	// 输出到文件时必须指定目录
	if opts.Output == File || opts.Output == Both {
		if opts.Directory == "" {
			return fmt.Errorf("输出到文件时必须指定目录")
		}
	}
	return nil
}

// mergeRotateOptions 合并轮转配置，使用用户传入的值或默认值
func mergeRotateOptions(opts *RotateOptions) RotateOptions {
	// 使用默认配置作为基础
	result := *defaultRotate

	// 如果用户传入了配置，覆盖默认值
	if opts != nil {
		if opts.MaxSize > 0 {
			result.MaxSize = opts.MaxSize
		}
		if opts.MaxBackups > 0 {
			result.MaxBackups = opts.MaxBackups
		}
		if opts.MaxAge > 0 {
			result.MaxAge = opts.MaxAge
		}
		result.Compress = opts.Compress
	}

	return result
}

// createWriter 根据输出类型创建 writer
func createWriter(output OutputType, directory string, rotate RotateOptions) (io.Writer, func(), error) {
	noop := func() {}

	switch output {
	case Console:
		return os.Stdout, noop, nil

	case File:
		writer, err := createFileWriter(directory, rotate)
		if err != nil {
			return nil, noop, err
		}
		cleanup := func() { writer.Close() }
		return writer, cleanup, nil

	case Both:
		consoleWriter := os.Stdout
		fileWriter, err := createFileWriter(directory, rotate)
		if err != nil {
			return nil, noop, err
		}
		multiWriter := io.MultiWriter(consoleWriter, fileWriter)
		cleanup := func() { fileWriter.Close() }
		return multiWriter, cleanup, nil

	default:
		return nil, noop, fmt.Errorf("不支持的输出类型: %d", output)
	}
}

// createFileWriter 创建文件 writer
//
// 使用 lumberjack 实现日志文件轮转，自动创建目录。
func createFileWriter(directory string, rotate RotateOptions) (*lumberjack.Logger, error) {
	// 确保目录存在
	if err := os.MkdirAll(directory, 0755); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", err)
	}

	// 创建日志文件路径
	logFile := filepath.Join(directory, "app.log")

	// 创建 lumberjack logger
	writer := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    rotate.MaxSize, // MB
		MaxBackups: rotate.MaxBackups,
		MaxAge:     rotate.MaxAge, // days
		Compress:   rotate.Compress,
	}

	return writer, nil
}

// createHandler 创建 slog handler
func createHandler(w io.Writer, level slog.Level, format LogFormat) slog.Handler {
	opts := &slog.HandlerOptions{
		Level: level,
	}

	switch format {
	case Text:
		return slog.NewTextHandler(w, opts)
	case JSON:
		fallthrough
	default:
		return slog.NewJSONHandler(w, opts)
	}
}

// cleanupFuncs 存储清理函数
var cleanupFuncs []func()

// registerCleanup 注册清理函数
func registerCleanup(fn func()) {
	cleanupFuncs = append(cleanupFuncs, fn)
}

// Close 关闭所有资源
//
// 在程序退出时调用，确保所有日志文件正确关闭。
func Close() {
	for _, fn := range cleanupFuncs {
		fn()
	}
}
