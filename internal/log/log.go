package log

import (
	"context"
	"log/slog"
	"os"

	"github.com/mj52951/evm-tx-sender/internal/config"
)

type ctxKey int

const (
	loggerKey ctxKey = iota
)

// WithLogger returns a context carrying the given logger.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

// FromContext extracts the logger previously attached with WithLogger.
func FromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}

// Setup installs the slog default handler driven by cfg.
func Setup(cfg config.LogConfig) {
	opts := &slog.HandlerOptions{Level: parseLevel(cfg.Level)}
	var h slog.Handler
	if cfg.JSON {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
