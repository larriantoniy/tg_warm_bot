package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	neuro "github.com/larriantoniy/tg_user_bot/internal/adapters/neuro"
	"github.com/larriantoniy/tg_user_bot/internal/adapters/tg"
	"github.com/larriantoniy/tg_user_bot/internal/config"
	"github.com/larriantoniy/tg_user_bot/internal/domain"
	"github.com/larriantoniy/tg_user_bot/internal/ports"
	"github.com/larriantoniy/tg_user_bot/internal/useCases"
)

const (
	envDev  = "dev"
	envProd = "prod"
	maxInFlightComments = 50
)

func main() {
	rand.Seed(time.Now().UnixNano())

	cfg, err := config.Load()
	if err != nil {
		fmt.Println("config load error", "error", err)
		os.Exit(1)
	}
	logger := setupLogger(cfg.Env)

	baseDir := cfg.BaseDir

	if cfg.Auth {
		// AUTH-режим для одной сессии
		if cfg.Session == "" {
			logger.Error("auth mode requires session (use -session or SESSION_NAME or yaml.session)")
			os.Exit(1)
		}
		if err := runAuthMode(logger, cfg); err != nil {
			logger.Error("auth mode failed", "error", err)
			os.Exit(1)
		}
		logger.Info("auth mode finished successfully")
		return
	}
	cfgRepo := config.NewJSONSessionConfigRepo(baseDir)

	// фабрику делаем без tdParams – их теперь создаёт NewClientFromJSON
	factory := func(sc *ports.SessionConfig, l *slog.Logger) (ports.TelegramClient, error) {
		// можно логгер завязывать на сессию:
		sessionLogger := l.With("session", sc.SessionName)
		return tg.NewClientFromJSON(cfg.ApiID, cfg.ApiHash, baseDir, sc.SessionName, sessionLogger, 0)
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

	clientsCh, err := runner.StartAll(ctx)
	if err != nil {
		logger.Error("runner.StartAll error", "error", err)
		os.Exit(1)
	}

	for cli := range clientsCh {
		neuro, err := neuro.NewNeuro(cfg, logger)
		if err != nil {
			logger.Error("neuro.NewNeuro error", "error", err)
			cli.Close()
			continue
		}

		sender := useCases.NewSender(logger, cli, neuro, cfg.Owner)
		go func(c ports.TelegramClient) {
			defer c.Close()

			msgCh, err := c.Listen()
			if err != nil {
				logger.Error("Listen error", "error", err)
				return
			}

			inFlight := make(chan struct{}, maxInFlightComments)
			for m := range msgCh {
				msg := m
				inFlight <- struct{}{}
				go func(msg domain.Message) {
					defer func() { <-inFlight }()
					c.ImitateReading(ctx, msg.ChatID)
					if err := sender.SendComment(ctx, &msg); err != nil {
						if errors.Is(err, tg.ErrRateLimited) {
							logger.Error("SendComment: rate limited for this client", "error", err)
							// эта горутина просто заканчивается,
							// а сам TDLib-клиент уже закрыт внутри tg.SendMessage
							return
						}
						logger.Error("SendComment error", "error", err)
					}
				}(msg)

			}
		}(cli)
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
	default:
		logger = slog.New(
			slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}),
		)
	}

	return logger
}

func runAuthMode(logger *slog.Logger, cfg *config.AppConfig) error {
	cli, err := tg.NewClientFromJSON(
		cfg.ApiID,
		cfg.ApiHash,
		cfg.BaseDir,
		cfg.Session,
		logger,
		tg.ClientModeRuntime,
	)
	if err != nil {
		return err
	}
	defer cli.Close()

	logger.Info("AUTH success", "session", cfg.Session)
	return nil
}
