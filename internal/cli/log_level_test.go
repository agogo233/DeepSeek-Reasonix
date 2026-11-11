package cli

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/config"
)

func TestApplyLogLevelEnv(t *testing.T) {
	cases := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"DEBUG": slog.LevelDebug,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}
	for raw, want := range cases {
		t.Setenv("REASONIX_LOG_LEVEL", raw)
		prev := slog.SetLogLoggerLevel(slog.LevelInfo)
		t.Cleanup(func() { slog.SetLogLoggerLevel(prev) })
		applyLogLevelEnv()
		if got := slog.SetLogLoggerLevel(slog.LevelInfo); got != want {
			t.Fatalf("REASONIX_LOG_LEVEL=%q: level = %v, want %v", raw, got, want)
		}
	}
}

func TestApplyLogLevelEnvKeepsDefault(t *testing.T) {
	for _, raw := range []string{"", "info", "bogus"} {
		t.Setenv("REASONIX_LOG_LEVEL", raw)
		prev := slog.SetLogLoggerLevel(slog.LevelInfo)
		t.Cleanup(func() { slog.SetLogLoggerLevel(prev) })
		applyLogLevelEnv()
		if got := slog.SetLogLoggerLevel(slog.LevelInfo); got != slog.LevelInfo {
			t.Fatalf("REASONIX_LOG_LEVEL=%q: level = %v, want info", raw, got)
		}
	}
}

func writeGlobalEnvFile(t *testing.T, home, content string) {
	t.Helper()
	path := config.UserCredentialsPath()
	if path == "" {
		t.Fatalf("UserCredentialsPath() resolved empty under HOME=%s", home)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestApplyLogLevelEnvReadsGlobalEnvFile(t *testing.T) {
	home := isolateCLIConfigHome(t)
	writeGlobalEnvFile(t, home, "REASONIX_LOG_LEVEL=debug\n")
	t.Setenv("REASONIX_LOG_LEVEL", "")
	prev := slog.SetLogLoggerLevel(slog.LevelInfo)
	t.Cleanup(func() { slog.SetLogLoggerLevel(prev) })
	applyLogLevelEnv()
	if got := slog.SetLogLoggerLevel(slog.LevelInfo); got != slog.LevelDebug {
		t.Fatalf("global .env REASONIX_LOG_LEVEL=debug: level = %v, want debug", got)
	}
}

func TestApplyLogLevelEnvProcessEnvWinsOverFile(t *testing.T) {
	home := isolateCLIConfigHome(t)
	writeGlobalEnvFile(t, home, "REASONIX_LOG_LEVEL=debug\n")
	t.Setenv("REASONIX_LOG_LEVEL", "warn")
	prev := slog.SetLogLoggerLevel(slog.LevelInfo)
	t.Cleanup(func() { slog.SetLogLoggerLevel(prev) })
	applyLogLevelEnv()
	if got := slog.SetLogLoggerLevel(slog.LevelInfo); got != slog.LevelWarn {
		t.Fatalf("shell warn with file debug: level = %v, want warn", got)
	}
}
