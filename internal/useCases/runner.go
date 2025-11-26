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
func (r *Runner) StartAll(ctx context.Context) (<-chan ports.TelegramClient, error) {
	ch := make(chan ports.TelegramClient)

	sessions, err := r.cfgRepo.ListSessions(ctx)
	if err != nil {
		close(ch)
		return ch, err
	}

	go func() {
		var wg sync.WaitGroup

		for _, sName := range sessions {
			wg.Add(1)
			go func(sName string) {
				defer wg.Done()

				cfg, err := r.cfgRepo.GetSessionConfig(ctx, sName)
				if err != nil {
					r.log.Error("GetSessionConfig failed", "session", sName, "error", err)
					return
				}

				cli, err := r.factory(cfg, r.log)
				if err != nil {
					r.log.Error("factory failed", "session", sName, "error", err)
					return
				}
				cli.JoinChannels(cfg.Channels)

				r.log.Info("client started", "session", sName)
				ch <- cli

				<-ctx.Done()
				cli.Close()
				r.log.Info("client stopped", "session", sName)
			}(sName)
		}

		wg.Wait()
		close(ch)
	}()

	return ch, nil
}
