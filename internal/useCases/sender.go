package useCases

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/larriantoniy/tg_user_bot/internal/adapters/tg"
	"github.com/larriantoniy/tg_user_bot/internal/domain"
	"github.com/larriantoniy/tg_user_bot/internal/ports"
)

type Sender struct {
	log   *slog.Logger
	tg    ports.TelegramClient
	neuro ports.NeuroProccesor

	ownerUsername string
	ownerUserID   int64 // кеш, чтобы не делать каждый раз resolve
	limited       bool  // флаг: сессия ушла в rate limit

	mu            sync.Mutex
	lastCommentAt time.Time
	minInterval   time.Duration
}

const (
	minDelay = 15 * time.Minute
	maxDelay = 30 * time.Minute
)

func NewSender(
	log *slog.Logger,
	tg ports.TelegramClient,
	neuro ports.NeuroProccesor,

	owner string, // "@user"
) *Sender {
	return &Sender{
		log:           log,
		tg:            tg,
		neuro:         neuro,
		ownerUsername: owner,
		minInterval:   10 * time.Minute,
	}
}
func (s *Sender) SendComment(ctx context.Context, msg *domain.Message) error {
	// нет таргета для реплая — нечего делать
	if msg.ReplyTo == nil {
		return nil
	}
	s.mu.Lock()
	limited := s.limited
	s.mu.Unlock()
	if limited {
		s.log.Warn("Skip SendComment: session is already rate-limited",
			"chat_id", msg.ReplyTo.DiscussionChatID,
			"msg_id", msg.ReplyTo.DiscussionMsgID,
		)
		return tg.ErrRateLimited
	}
	// 923561770135) сначала генерим текст от нейросети

	replyText, err := s.neuro.GetComment(ctx, msg)
	if err != nil {
		s.log.Error("GetComment", "error", err)
		return err
	}
	replyText = strings.TrimSpace(replyText)
	if replyText == "" {
		s.log.Info("Skip SendComment: empty LLM response")
		return nil
	}

	// 923345799730) планируем задержку 15–30 минут
	s.log.Info("Planned comment delay",
		"chat_id", msg.ReplyTo.DiscussionChatID,
		"msg_id", msg.ReplyTo.DiscussionMsgID,
		"min_delay", minDelay,
		"max_delay", maxDelay,
		"comment", replyText,
	)

	if err := randomDelay(ctx, minDelay, maxDelay); err != nil {
		s.log.Warn("Comment canceled during delay (shutdown?)", "error", err)
		return err
	}

	// 3) общий rate-limit на аккаунт
	if err := s.waitRateLimit(ctx); err != nil {
		s.log.Warn("Comment canceled by rate-limit wait (shutdown?)", "error", err)
		return err
	}

	if err := s.tg.SendMessage(
		msg.ReplyTo.DiscussionChatID,
		msg.ReplyTo.DiscussionMsgID,
		msg.MessageThreadId,
		replyText,
	); err != nil {
		s.log.Error("SendComment", "error", err)
		return err
	}
	// 5. отправляем уведомление Owner
	err = s.sendOwnerNotify(msg.Text, replyText)
	if err != nil {
		s.log.Warn("SendComment", "error", err)
	}

	return nil
}

func (s *Sender) waitRateLimit(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lastCommentAt.IsZero() {
		// ещё ни разу не комментировали — можно сразу
		s.lastCommentAt = time.Now()
		return nil
	}

	elapsed := time.Since(s.lastCommentAt)
	if elapsed >= s.minInterval {
		s.lastCommentAt = time.Now()
		return nil
	}

	needWait := s.minInterval - elapsed
	s.log.Info("Rate-limit delay before next comment", "wait", needWait)

	timer := time.NewTimer(needWait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		s.lastCommentAt = time.Now()
		return nil
	}
}
func (s *Sender) sendOwnerNotify(text string, replyText string) error {
	if s.ownerUsername == "" {
		return nil
	}

	// lazy init → resolve username once
	if s.ownerUserID == 0 {
		uid, err := s.tg.ResolveUsername(s.ownerUsername)
		if err != nil {
			s.log.Error("Resolve owner username failed", "owner", s.ownerUsername, "error", err)
			return err
		}
		s.ownerUserID = uid
	}

	toOwner := fmt.Sprintf(
		"💬 Новый комментарий:\n\n%s\n\nНа сообщение: %s",
		replyText,
		text,
	)
	err := s.tg.SendMessage(s.ownerUserID, 0, 0, toOwner)
	if err != nil {
		s.log.Warn("Send Owner Notify", "error", err)
		return err
	}
	return nil
}

func randomDelay(ctx context.Context, min, max time.Duration) error {

	delta := max - min
	if delta <= 0 {
		delta = min
	}

	wait := min + time.Duration(rand.Int63n(int64(delta)))

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
