package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/larriantoniy/tg_user_bot/internal/adapters/tg"
	"github.com/larriantoniy/tg_user_bot/internal/config"
	"github.com/larriantoniy/tg_user_bot/internal/ports"
	"github.com/larriantoniy/tg_user_bot/internal/useCases"
)

const (
	envDev  = "dev"
	envProd = "prod"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	logger := setupLogger(cfg.Env)
	baseDir := cfg.BaseDir

	cfgRepo := config.NewJSONSessionConfigRepo(baseDir)

	// фабрику делаем без tdParams – их теперь создаёт NewClientFromJSON
	factory := func(sc *ports.SessionConfig, l *slog.Logger) (ports.TelegramClient, error) {
		// можно логгер завязывать на сессию:
		sessionLogger := l.With("session", sc.SessionName)
		return tg.NewClientFromJSON(cfg.ApiID, cfg.ApiHash, baseDir, sc.SessionName, sessionLogger)
	}

	runner := useCases.NewRunner(cfgRepo, logger, factory)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		logger.Info("shutdown signal received")
		cancel()
	}()

	if err := runner.StartAll(ctx); err != nil {
		logger.Error("runner.StartAll error", "error", err)
		os.Exit(1)
	}

	logger.Info("exit")
}

func setupLogger(env string) *slog.Logger {
	var logger *slog.Logger

	switch env {
	case envDev:
		logger = slog.New(
			slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}),
		)
	case envProd:
		logger = slog.New(
			slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}),
		)
	}

	return logger
}
