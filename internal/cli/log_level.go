package cli

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"reasonix/internal/config"
)

// applyLogLevelEnv reads REASONIX_LOG_LEVEL and applies it to the default
// logger before any command writes its first record. The process environment
// wins; Reasonix's global .env supplies the value when the shell does not.
// An empty value keeps the default (info); an unparsable value warns once on
// stderr and keeps the default rather than refusing to start.
func applyLogLevelEnv() {
	raw := strings.TrimSpace(os.Getenv("REASONIX_LOG_LEVEL"))
	if raw == "" {
		if v, ok := config.UserCredentialValue("REASONIX_LOG_LEVEL"); ok {
			raw = strings.TrimSpace(v)
		}
	}
	if raw == "" {
		return
	}
	var level slog.Level
	switch strings.ToLower(raw) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		fmt.Fprintf(os.Stderr, "REASONIX_LOG_LEVEL=%q is not one of debug, info, warn, error; using info\n", raw)
		return
	}
	slog.SetLogLoggerLevel(level)
}
