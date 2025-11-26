package ports

import (
	"context"

	"github.com/larriantoniy/tg_user_bot/internal/domain"
)

type NeuroProccesor interface {
	GetComment(ctx context.Context, msg *domain.Message) (string, error)
}
