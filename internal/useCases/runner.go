package useCases

import (
	"context"
	"log/slog"
	"sync"

	"github.com/larriantoniy/tg_user_bot/internal/ports"
)

type Runner struct {
	cfgRepo ports.SessionConfigRepo
	log     *slog.Logger
	factory func(cfg *ports.SessionConfig, log *slog.Logger) (ports.TelegramClient, error)
}

func NewRunner(
	cfgRepo ports.SessionConfigRepo,
	log *slog.Logger,
	factory func(cfg *ports.SessionConfig, log *slog.Logger) (ports.TelegramClient, error),
) *Runner {
	return &Runner{cfgRepo: cfgRepo, log: log, factory: factory}
}

// StartAll запускает клиентов по всем доступным сессиям
func (r *Runner) StartAll(ctx context.Context) error {
	sessions, err := r.cfgRepo.ListSessions(ctx)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup

	for _, s := range sessions {
		sName := s
		wg.Add(1)

		go func() {
			defer wg.Done()

			cfg, err := r.cfgRepo.GetSessionConfig(ctx, sName)
			if err != nil {
				r.log.Error("GetSessionConfig failed", "session", sName, "error", err)
				return
			}
			r.log.Debug("GetSessionConfig", "cfg", cfg)
			cli, err := r.factory(cfg, r.log)
			if err != nil {
				r.log.Error("factory failed", "session", sName, "error", err)
				return
			}
			defer cli.Close()

			r.log.Info("client started", "session", sName)

			<-ctx.Done()
			r.log.Info("client stopped", "session", sName)
		}()
	}

	wg.Wait()
	return nil
}
