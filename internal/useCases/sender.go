package useCases

import (
	"context"
	"errors"
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

type ThreadKey struct {
	ChatID   int64
	ThreadID int64
}
type CommentLimiter struct {
	mu   sync.Mutex
	seen map[ThreadKey]struct{}
}

type Sender struct {
	log   *slog.Logger
	tg    ports.TelegramClient
	neuro ports.NeuroProccesor

	ownerUsername string
	ownerUserID   int64 // кеш, чтобы не делать каждый раз resolve
	limited       bool  // флаг: сессия ушла в rate limit
	limiter       *CommentLimiter
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
		limiter:       &CommentLimiter{seen: make(map[ThreadKey]struct{})},
	}
}
func (s *Sender) SendComment(ctx context.Context, msg *domain.Message) error {
	if !s.Allow(msg.ChatID, msg.MessageThreadId) {
		return fmt.Errorf("SendComment: ChatID %d is not allowed because be send already", msg.ChatID)
	}
	s.mu.Lock()
	limited := s.limited
	s.mu.Unlock()
	if limited {
		s.log.Warn("Skip SendComment: session is already rate-limited",
			"chat_id", msg.ChatID,
			"msg_thread_id", msg.MessageThreadId,
		)
		return tg.ErrRateLimited
	}
	if !s.tg.CanSendToChat(msg.ChatID) {
		s.log.Info("Skip SendComment: cannot send to discussion chat",
			"chat_id", msg.ChatID,
			"msg_thread_id", msg.MessageThreadId,
		)
		return nil
	}
	if !s.tg.IsMember(msg.ChatID) {
		s.log.Info("Skip SendComment: not a member of discussion chat",
			"chat_id", msg.ChatID,
			"msg_thread_id", msg.MessageThreadId,
		)
		return nil
	}
	//  сначала генерим текст от нейросети

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

	s.log.Info("Planned comment delay",
		"chat_id", msg.ChatID,
		"msg_thread_id", msg.MessageThreadId,
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

	if !s.tg.CanSendToChat(msg.ChatID) {
		s.log.Info("Skip SendComment: cannot send to discussion chat (after delay)",
			"chat_id", msg.ChatID,
			"msg_thread_id", msg.MessageThreadId,
		)
		return nil
	}

	if err := s.tg.SendMessage(
		msg.ChatID,
		msg.MessageThreadId,
		replyText,
	); err != nil {
		if errors.Is(err, tg.ErrRateLimited) {
			s.mu.Lock()
			s.limited = true
			s.mu.Unlock()
		}
		s.log.Error("SendComment", "error", err)
		return err
	}
	s.log.Info("Comment sent",
		"chat_id", msg.ChatID,
		"msg_thread_id", msg.MessageThreadId,
	)
	// 5. отправляем уведомление Owner
	err = s.sendOwnerNotify(msg, replyText)
	if err != nil {
		s.log.Warn("SendComment", "error", err)
	}

	return nil
}

func (s *Sender) waitRateLimit(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lastCommentAt.IsZero() {
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
func (s *Sender) sendOwnerNotify(msg *domain.Message, replyText string) error {
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
		msg.Text,
	)
	postLink := s.buildPostLink(msg)
	if postLink != "" {
		toOwner = fmt.Sprintf("%s\n\nСсылка: %s", toOwner, postLink)
	}
	err := s.tg.SendMessage(s.ownerUserID, 0, toOwner)
	if err != nil {
		s.log.Warn("Send Owner Notify", "error", err)
		return err
	}
	return nil
}

func (s *Sender) buildPostLink(msg *domain.Message) string {
	if msg == nil || msg.ChannelID == 0 || msg.MessageThreadId == 0 {
		return ""
	}

	absID := msg.ChannelID
	if absID < 0 {
		absID = -absID
	}

	const channelOffset int64 = 1000000000000
	if absID > channelOffset {
		absID -= channelOffset
	}

	return fmt.Sprintf("https://t.me/c/%d/%d", absID, msg.MessageThreadId)
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

func (s *Sender) Allow(chatID, threadID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := ThreadKey{ChatID: chatID, ThreadID: threadID}
	if _, ok := s.limiter.seen[key]; ok {
		return false
	}
	s.limiter.seen[key] = struct{}{}
	return true
}
