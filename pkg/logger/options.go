// Package logger 提供基于 slog + lumberjack 的日志功能
//
// 支持同时输出到控制台和文件，日志文件自动轮转压缩。
// 使用 slog 作为日志框架，lumberjack 负责日志文件切割。
package logger

import "log/slog"

// OutputType 日志输出目标类型
type OutputType int

const (
	Console OutputType = iota // 仅输出到控制台，便于开发调试
	File                      // 仅输出到文件，便于生产环境日志收集
	Both                      // 同时输出到控制台和文件
)

// LogFormat 日志格式类型
type LogFormat int

const (
	JSON LogFormat = iota // JSON 格式，生产环境推荐，便于日志系统解析
	Text                  // 文本格式，开发环境推荐，便于人工阅读
)

// RotateOptions 日志轮转配置
//
// 用于控制日志文件的切割、备份和压缩策略。
// 所有字段均为可选，未设置则使用默认值。
type RotateOptions struct {
	MaxSize    int  // 单个日志文件最大大小（MB），默认 100MB
	MaxBackups int  // 保留的历史日志文件数量，默认 7 个
	MaxAge     int  // 日志文件保留天数，默认 30 天
	Compress   bool // 是否压缩历史日志，默认 false
}

// Options logger 初始化选项
//
// 用于配置日志的输出目标、级别、格式和轮转策略。
type Options struct {
	Output    OutputType     // 输出目标（必填）
	Level     slog.Level     // 日志级别（必填）
	Directory string         // 文件输出目录（File/Both 时必填）
	Format    LogFormat      // 日志格式（可选，默认 JSON）
	Rotate    *RotateOptions // 轮转配置（可选，nil 使用默认值）
}
