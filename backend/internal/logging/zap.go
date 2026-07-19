package logging

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	defaultLogDir     = "logs"
	defaultMaxAgeDays = 14
	envLogDir         = "LOG_DIR"
	envLogLevel       = "LOG_LEVEL"
	envLogMaxAgeDays  = "LOG_MAX_AGE_DAYS"
)

// Config controls where and how process logs are written.
type Config struct {
	Role       string
	Dir        string
	Level      zapcore.Level
	MaxAgeDays int
}

// ConfigFromEnv builds Config for role (for example "api" or "worker") from environment variables.
func ConfigFromEnv(role string) (Config, error) {
	role = strings.TrimSpace(role)
	if role == "" {
		return Config{}, fmt.Errorf("logging role is required")
	}
	level, err := parseLevel(valueOrDefault(envLogLevel, "info"))
	if err != nil {
		return Config{}, err
	}
	maxAgeDays, err := parseMaxAgeDays(valueOrDefault(envLogMaxAgeDays, strconv.Itoa(defaultMaxAgeDays)))
	if err != nil {
		return Config{}, err
	}
	return Config{
		Role:       role,
		Dir:        valueOrDefault(envLogDir, defaultLogDir),
		Level:      level,
		MaxAgeDays: maxAgeDays,
	}, nil
}

type zapLogger struct {
	base *zap.Logger
}

// Open creates a JSON zap logger that writes to stdout and a daily rotating file.
// The returned close function flushes buffers and closes the rotating writer.
func Open(config Config) (Logger, func() error, error) {
	if strings.TrimSpace(config.Role) == "" {
		return nil, nil, fmt.Errorf("logging role is required")
	}
	if strings.TrimSpace(config.Dir) == "" {
		return nil, nil, fmt.Errorf("logging directory is required")
	}
	if config.MaxAgeDays < 1 {
		return nil, nil, fmt.Errorf("LOG_MAX_AGE_DAYS must be at least 1")
	}
	if err := os.MkdirAll(config.Dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create log directory %s: %w", config.Dir, err)
	}

	pattern := filepath.Join(config.Dir, fmt.Sprintf("agent-%s.%%Y%%m%%d.log", config.Role))
	linkName := filepath.Join(config.Dir, fmt.Sprintf("agent-%s.log", config.Role))
	rotator, err := rotatelogs.New(
		pattern,
		rotatelogs.WithLinkName(linkName),
		rotatelogs.WithRotationTime(24*time.Hour),
		rotatelogs.WithMaxAge(time.Duration(config.MaxAgeDays)*24*time.Hour),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("open rotating log writer: %w", err)
	}

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "ts"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.NewMultiWriteSyncer(zapcore.AddSync(os.Stdout), zapcore.AddSync(rotator)),
		config.Level,
	)
	base := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1)).With(zap.String("role", config.Role))
	logger := &zapLogger{base: base}

	closeFn := func() error {
		syncErr := logger.Sync()
		closeErr := rotator.Close()
		if syncErr != nil {
			return fmt.Errorf("sync logger: %w", syncErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close rotating log writer: %w", closeErr)
		}
		return nil
	}
	return logger, closeFn, nil
}

func (logger *zapLogger) Debug(ctx context.Context, msg string, fields ...zap.Field) {
	logger.base.Debug(msg, withTrace(ctx, fields)...)
}

func (logger *zapLogger) Info(ctx context.Context, msg string, fields ...zap.Field) {
	logger.base.Info(msg, withTrace(ctx, fields)...)
}

func (logger *zapLogger) Warn(ctx context.Context, msg string, fields ...zap.Field) {
	logger.base.Warn(msg, withTrace(ctx, fields)...)
}

func (logger *zapLogger) Error(ctx context.Context, msg string, fields ...zap.Field) {
	logger.base.Error(msg, withTrace(ctx, fields)...)
}

func (logger *zapLogger) With(fields ...zap.Field) Logger {
	return &zapLogger{base: logger.base.With(fields...)}
}

func (logger *zapLogger) Sync() error {
	err := logger.base.Sync()
	if err == nil {
		return nil
	}
	// Syncing stdout/stderr often returns EBADF in tests and containers.
	if errors.Is(err, syscall.EBADF) || strings.Contains(err.Error(), "bad file descriptor") {
		return nil
	}
	return err
}

func withTrace(ctx context.Context, fields []zap.Field) []zap.Field {
	traceID := TraceID(ctx)
	if traceID == "" {
		return fields
	}
	return append([]zap.Field{zap.String("trace_id", traceID)}, fields...)
}

func parseLevel(value string) (zapcore.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return zapcore.DebugLevel, nil
	case "info", "":
		return zapcore.InfoLevel, nil
	case "warn", "warning":
		return zapcore.WarnLevel, nil
	case "error":
		return zapcore.ErrorLevel, nil
	default:
		return 0, fmt.Errorf("unsupported LOG_LEVEL %q", value)
	}
}

func parseMaxAgeDays(value string) (int, error) {
	days, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("parse LOG_MAX_AGE_DAYS: %w", err)
	}
	if days < 1 {
		return 0, fmt.Errorf("LOG_MAX_AGE_DAYS must be at least 1")
	}
	return days, nil
}

func valueOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
