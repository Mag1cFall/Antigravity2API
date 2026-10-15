// Package logging 把 slog 服务日志以 JSON 或文本格式写到标准错误与可选的轮转文件，并把致命错误写入崩溃文件
package logging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"

	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/Mag1cFall/Antigravity2API/internal/config"
)

// FileName 是日志目录中当前日志文件的名称
const FileName = "antigravity2api.log"

// CrashFileName 是日志目录中记录未恢复 panic 与致命错误的文件
const CrashFileName = "crash.log"

// requestIDKey 是请求 ID 在 context 中的键
type requestIDKey struct{}

// WithRequestID 返回携带请求 ID 的 context，经它写出的日志带 request_id 属性
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID 返回 context 中的请求 ID，不存在时返回空串
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// Logger 是可在运行时调整级别、格式与文件输出的服务日志
type Logger struct {
	level slog.LevelVar
	text  atomic.Bool
	dir   string
	mu    sync.Mutex
	file  *lumberjack.Logger
	json  slog.Handler
	plain slog.Handler
}

// New 创建写入标准错误的服务日志并把致命错误同时写入 dir 下的崩溃文件，参数取值已由 config.Validate 校验
func New(dir, level, format string, file bool, maxTotalMB int) (*Logger, error) {
	logger := &Logger{dir: dir}
	options := &slog.HandlerOptions{Level: &logger.level, AddSource: true, ReplaceAttr: caller}
	logger.json = slog.NewJSONHandler(logger, options)
	logger.plain = slog.NewTextHandler(logger, options)
	logger.SetLevel(level)
	logger.SetFormat(format)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建日志目录: %w", err)
	}
	crash, err := os.OpenFile(filepath.Join(dir, CrashFileName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("打开崩溃文件: %w", err)
	}
	defer crash.Close()
	if err := debug.SetCrashOutput(crash, debug.CrashOptions{}); err != nil {
		return nil, fmt.Errorf("设置崩溃输出: %w", err)
	}
	if err := logger.SetFile(file, maxTotalMB); err != nil {
		return nil, err
	}
	return logger, nil
}

// Handler 返回按当前格式输出并附带 context 中请求 ID 的 slog 处理器
func (l *Logger) Handler() slog.Handler {
	return &handler{logger: l, json: l.json, plain: l.plain}
}

// Leveler 返回当前级别，供其他处理器共用同一阈值
func (l *Logger) Leveler() slog.Leveler {
	return &l.level
}

// SetLevel 按 debug、info、warn、error 设置级别
func (l *Logger) SetLevel(name string) {
	var level slog.Level
	level.UnmarshalText([]byte(name))
	l.level.Set(level)
}

// SetFormat 按 json 或 text 设置格式
func (l *Logger) SetFormat(name string) {
	l.text.Store(name == "text")
}

// SetFile 开启或关闭日志文件；单文件按 config.LogFileMB 轮转，目录总量不超过 maxTotalMB，超出时删除最旧的文件
func (l *Logger) SetFile(enabled bool, maxTotalMB int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		if err := l.file.Close(); err != nil {
			return err
		}
		l.file = nil
	}
	if !enabled {
		return nil
	}
	l.file = &lumberjack.Logger{
		Filename:   filepath.Join(l.dir, FileName),
		MaxSize:    config.LogFileMB,
		MaxBackups: maxTotalMB/config.LogFileMB - 1,
		LocalTime:  true,
	}
	return nil
}

// Write 把一条日志写到标准错误与日志文件；标准错误不可写时仍写文件
func (l *Logger) Write(data []byte) (int, error) {
	os.Stderr.Write(data)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		return l.file.Write(data)
	}
	return len(data), nil
}

// Close 关闭日志文件与崩溃文件
func (l *Logger) Close() error {
	return errors.Join(debug.SetCrashOutput(nil, debug.CrashOptions{}), l.SetFile(false, 0))
}

// handler 按 Logger 当前格式选择 JSON 或文本处理器
type handler struct {
	logger *Logger
	json   slog.Handler
	plain  slog.Handler
}

// Enabled 按服务日志级别过滤
func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.json.Enabled(ctx, level)
}

// Handle 补上 context 中的请求 ID 后写出
func (h *handler) Handle(ctx context.Context, record slog.Record) error {
	if id := RequestID(ctx); id != "" {
		record = record.Clone()
		record.AddAttrs(slog.String("request_id", id))
	}
	if h.logger.text.Load() {
		return h.plain.Handle(ctx, record)
	}
	return h.json.Handle(ctx, record)
}

// WithAttrs 返回两种格式都附带固定属性的处理器
func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &handler{logger: h.logger, json: h.json.WithAttrs(attrs), plain: h.plain.WithAttrs(attrs)}
}

// WithGroup 返回两种格式都带组名的处理器
func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{logger: h.logger, json: h.json.WithGroup(name), plain: h.plain.WithGroup(name)}
}

// caller 把 slog 的调用位置改写为 caller 属性，取值为所在目录与文件名加行号，source 属性留给日志来源
func caller(groups []string, attr slog.Attr) slog.Attr {
	if source, ok := attr.Value.Any().(*slog.Source); ok && len(groups) == 0 && attr.Key == slog.SourceKey {
		return slog.String("caller", filepath.Base(filepath.Dir(source.File))+"/"+filepath.Base(source.File)+":"+strconv.Itoa(source.Line))
	}
	return attr
}
