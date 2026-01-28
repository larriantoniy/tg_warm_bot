package tg

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/larriantoniy/tg_user_bot/internal/domain"
	"github.com/zelenin/go-tdlib/client"
)

// TDLibClient реализует ports.TelegramClient через go-tg

type TelegramClient struct {
	client *client.Client
	logger *slog.Logger
	selfId int64

	mu          sync.Mutex
	linkedChats map[int64]int64
	joinedChats map[int64]struct{}
	blockedTill map[int64]time.Time
}
type ClientMode int

const (
	ClientModeRuntime ClientMode = iota // боевой режим: GetMe, лог self_id и т.д.
	ClientModeAuth                      // режим авторизации: просто поднять TDLib, пообщаться с TDLib и выйти
)

func NewClientFromJSON(
	apiID int32,
	apiHash string,
	baseDir string, // "/sessions"
	sessionName string, // "923345799730" и т.п.
	log *slog.Logger,
	mode ClientMode,
) (*TelegramClient, error) {
	rawCfg, err := LoadRawSessionConfig(baseDir, sessionName)
	if err != nil {
		log.Error("TDLib LoadRawSessionConfig", "error", err, "sessionName", sessionName, "rawCfg", rawCfg)	
		return nil, err
	}

	sessionDir := filepath.Join(baseDir, rawCfg.SessionFile)
	dbDir := filepath.Join(sessionDir, "database")
	filesDir := filepath.Join(sessionDir, "files")

	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir db dir: %w", err)
	}
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir files dir: %w", err)
	}

	if _, err := client.SetLogVerbosityLevel(&client.SetLogVerbosityLevelRequest{
		NewVerbosityLevel: 1,
	}); err != nil {
		log.Error("TDLib SetLogVerbosityLevel", "error", err)
	}

	tdParams := rawCfg.ToTdParams(apiID, apiHash, dbDir, filesDir)

	proxyCfg, err := rawCfg.ToProxyConfig()
	if err != nil {
		log.Error("parse proxy from json", "error", err)
	}

	var opts []client.Option
	if proxyCfg != nil && proxyCfg.Enabled {
		opts = append(opts, client.WithProxy(&client.AddProxyRequest{
			Server: proxyCfg.Server,
			Port:   proxyCfg.Port,
			Enable: true,
			Type: &client.ProxyTypeSocks5{
				Username: proxyCfg.Username,
				Password: proxyCfg.Password,
			},
		}))
	}

	// ✅ ОДИН authorizer для обоих режимов
	authorizer := client.ClientAuthorizer(tdParams)

	// ✅ В AUTH-режиме запускаем CliInteractor, чтобы были промпты в консоли
	if mode == ClientModeAuth {
		go client.CliInteractor(authorizer)
	}

	tdCli, err := client.NewClient(authorizer, opts...)
	if err != nil {
		log.Error("TDLib NewClient error", "session", rawCfg.SessionFile, "error", err)
		return nil, err
	}

	// === Режим AUTH: просто возвращаем клиента, без GetMe ===
	if mode == ClientModeAuth {
		go logAuthStates(tdCli, log)
		log.Info("TDLib client started in AUTH mode",
			"session", rawCfg.SessionFile,
			"phone", rawCfg.Phone,
		)

		return &TelegramClient{
			client: tdCli,
			logger: log,
			selfId: 0, // узнаешь позже через GetMe, если нужно
			linkedChats: make(map[int64]int64),
			joinedChats: make(map[int64]struct{}),
			blockedTill: make(map[int64]time.Time),
		}, nil
	}

	// === Режим RUNTIME: считаем, что сессия уже авторизована ===
	me, err := tdCli.GetMe()
	if err != nil {
		log.Error("GetMe failed", "session", rawCfg.SessionFile, "error", err)
		return nil, err
	}

	log.Info("TDLib client initialized and authorized",
		"self_id", me.Id,
		"session", rawCfg.SessionFile,
		"phone", rawCfg.Phone,
	)

	return &TelegramClient{
		client: tdCli,
		logger: log,
		selfId: me.Id,
		linkedChats: make(map[int64]int64),
		joinedChats: make(map[int64]struct{}),
		blockedTill: make(map[int64]time.Time),
	}, nil
}

var ErrRateLimited = errors.New("tdlib: too many requests")

// Реализация ports.TelegramClient:

func (t *TelegramClient) GetMe() (int64, error) {
	me, err := t.client.GetMe()
	if err != nil {
		return 0, err
	}
	return me.Id, nil
}

func (t *TelegramClient) Close() {
	t.client.Close()
}

// JoinChannel подписывается на публичный канал по его username, если ещё не подписан
func (t *TelegramClient) JoinChannel(username string) error {
	// Ищем чат по username
	chat, err := t.client.SearchPublicChat(&client.SearchPublicChatRequest{
		Username: username,
	})
	if err != nil {
		t.logger.Error("SearchPublicChat failed", "username", username, "error", err)
		return err
	}

	// Пытаемся подписаться
	_, err = t.client.JoinChat(&client.JoinChatRequest{
		ChatId: chat.Id,
	})
	if err != nil {
		// Telegram вернёт ошибку, если уже в канале — можно логировать как инфо
		t.logger.Error("JoinChat failed", "chat_id", chat.Id, "error", err)
		return err
	}

	t.logger.Info("Joined channel", "channel", username)
	return nil
}
func (t *TelegramClient) JoinChannels(chs []string) {
	//  Логируем входные данные
	t.logger.Info("JoinChannels called", "channels", chs)

	// Получаем уже присоединённые
	joinedChs, err := t.GetJoinedChannelIdentifiers()
	if err != nil {
		t.logger.Error("Failed to fetch joined channels, aborting", "error", err)
		return
	}
	t.logger.Info("Already joined channels", "channels", joinedChs)

	// 3) Если ни одного канала нет — сразу выходим
	if len(chs) == 0 {
		t.logger.Info("No channels to join, exiting")
		return
	}

	// 4) Пробегаем по каждому каналу и логируем, что сейчас обрабатываем
	for _, ch := range chs {
		t.logger.Info("Processing channel", "channel", ch)

		//  Уже в канале?
		if _, isJoined := joinedChs[ch]; isJoined {
			t.logger.Info("Already a member, skipping", "channel", ch)
			continue
		}

		// 4 Username-канал
		if strings.HasPrefix(ch, "@") {
			t.logger.Info("Attempting JoinChannel by username", "channel", ch)
			if err := t.JoinChannel(ch); err != nil {
				t.logger.Error("Failed to join by username", "channel", ch, "error", err)
			} else {
				t.logger.Info("Successfully joined by username", "channel", ch)
			}
			continue
		}

		// 4.3) Invite-link
		t.logger.Info("Attempting JoinChatByInviteLink", "link", ch)
		if _, err := t.client.JoinChatByInviteLink(&client.JoinChatByInviteLinkRequest{
			InviteLink: ch,
		}); err != nil {
			t.logger.Error("Failed to join by invite link", "link", ch, "error", err)
		} else {
			t.logger.Info("Successfully joined by invite link", "link", ch)
		}
	}
}

// Listen возвращает канал доменных сообщений из TDLib и запускает обработку обновлений
func (t *TelegramClient) Listen() (<-chan domain.Message, error) {
	out := make(chan domain.Message)

	// Получаем слушатель обновлений
	listener := t.client.GetListener()
	go func() {
		defer close(out)
		for update := range listener.Updates {

			if upd, ok := update.(*client.UpdateNewMessage); ok {
				t.logger.Debug("UpdateNewMessage received",
					"chat_id", upd.Message.ChatId,
					"is_channel_post", upd.Message.IsChannelPost,
					"message_id", upd.Message.Id,
				)
				_, err := t.processUpdateNewMessage(out, upd)
				if err != nil {
					t.logger.Error("Error process UpdateNewMessage msg content type", upd.Message.Content.MessageContentType())
				}
			}
		}
	}()

	return out, nil
}
func (t *TelegramClient) isMember(chatID int64) bool {
	member, err := t.client.GetChatMember(&client.GetChatMemberRequest{
		ChatId:   chatID,
		MemberId: &client.MessageSenderUser{UserId: t.selfId},
	})
	if err != nil {
		t.logger.Debug("GetChatMember failed, assuming not a member", "chat_id", chatID, "error", err)
		return false
	}

	//  Определяем статус через type assertion
	switch member.Status.(type) {
	case *client.ChatMemberStatusMember, *client.ChatMemberStatusAdministrator, *client.ChatMemberStatusCreator:
		t.logger.Debug("Bot is channel member", "chat_id", chatID)
		return true
	default:
		t.logger.Debug("Bot not member", "chat_id", chatID)
		return false
	}
}

func (t *TelegramClient) IsMember(chatID int64) bool {
	return t.isMember(chatID)
}

func (t *TelegramClient) CanSendToChat(chatID int64) bool {
	member, err := t.client.GetChatMember(&client.GetChatMemberRequest{
		ChatId:   chatID,
		MemberId: &client.MessageSenderUser{UserId: t.selfId},
	})
	if err != nil {
		t.logger.Warn("GetChatMember failed for CanSendToChat", "chat_id", chatID, "error", err)
		return false
	}

	switch status := member.Status.(type) {
	case *client.ChatMemberStatusMember, *client.ChatMemberStatusAdministrator, *client.ChatMemberStatusCreator:
		return true
	case *client.ChatMemberStatusRestricted:
		if status.Permissions != nil {
			return status.Permissions.CanSendBasicMessages
		}
		return status.IsMember
	case *client.ChatMemberStatusBanned:
		return false
	default:
		return false
	}
}

func (t *TelegramClient) IsChannelMember(username string) (bool, error) {

	chat, err := t.client.SearchPublicChat(&client.SearchPublicChatRequest{
		Username: username,
	})
	if err != nil {
		t.logger.Error("SearchPublicChat failed", "username", username, "error", err)
		return false, err
	}
	return t.isMember(chat.Id), nil
}

func (t *TelegramClient) GetJoinedChannelIdentifiers() (map[string]bool, error) {
	const limit = 100
	identifiers := make(map[string]bool, limit)

	//  Получаем первые `limit` чатов из основного списка
	chatsResp, err := t.client.GetChats(&client.GetChatsRequest{
		ChatList: &client.ChatListMain{},
		Limit:    limit,
	})
	if err != nil {
		return nil, fmt.Errorf("GetChats failed: %w", err)
	}

	// 923345799730. Обходим все chatID
	for _, chatID := range chatsResp.ChatIds {
		chat, err := t.client.GetChat(&client.GetChatRequest{ChatId: chatID})
		if err != nil {
			t.logger.Error("GetChat failed", "chat_id", chatID, "error", err)
			continue
		}

		switch ct := chat.Type.(type) {
		// канал или супергруппа
		case *client.ChatTypeSupergroup:
			// получение публичного @username
			sup, err := t.client.GetSupergroup(&client.GetSupergroupRequest{
				SupergroupId: ct.SupergroupId,
			})
			if err == nil && sup != nil && sup.Usernames != nil && sup.Usernames.ActiveUsernames != nil && len(sup.Usernames.ActiveUsernames) > 0 {
				identifiers["@"+sup.Usernames.ActiveUsernames[0]] = true
			}
		case *client.ChatTypePrivate:
			usr, err := t.client.GetUser(&client.GetUserRequest{
				UserId: ct.UserId,
			})
			if err != nil {
				t.logger.Error("GetUser failed", "user_id", ct.UserId, "error", err)
				continue
			}
			if usr != nil && usr.Usernames != nil && usr.Usernames.ActiveUsernames != nil && len(usr.Usernames.ActiveUsernames) > 0 {
				identifiers["@"+usr.Usernames.ActiveUsernames[0]] = true
			}

		default:
			// ничего не делаем
		}
	}

	return identifiers, nil
}

func (t *TelegramClient) getChatTitle(chatID int64) (string, error) {
	chat, err := t.client.GetChat(&client.GetChatRequest{
		ChatId: chatID,
	})
	if err != nil {
		return "", err
	}

	return chat.Title, nil
}

func (t *TelegramClient) getLinkedChatID(chatID int64) (int64, bool) {
	t.mu.Lock()
	if linked, ok := t.linkedChats[chatID]; ok {
		t.mu.Unlock()
		return linked, linked != 0
	}
	t.mu.Unlock()

	chat, err := t.client.GetChat(&client.GetChatRequest{
		ChatId: chatID,
	})
	if err != nil {
		t.logger.Debug("GetChat failed for linked chat lookup", "chat_id", chatID, "error", err)
		return 0, false
	}

	sg, ok := chat.Type.(*client.ChatTypeSupergroup)
	if !ok {
		return 0, false
	}

	info, err := t.client.GetSupergroupFullInfo(&client.GetSupergroupFullInfoRequest{
		SupergroupId: sg.SupergroupId,
	})
	if err != nil {
		t.logger.Debug("GetSupergroupFullInfo failed", "chat_id", chatID, "error", err)
		return 0, false
	}

	t.mu.Lock()
	t.linkedChats[chatID] = info.LinkedChatId
	t.mu.Unlock()

	return info.LinkedChatId, info.LinkedChatId != 0
}

func (t *TelegramClient) ensureJoinedChat(chatID int64) {
	if chatID == 0 {
		return
	}

	if t.isChatBlocked(chatID) {
		return
	}

	t.mu.Lock()
	if _, ok := t.joinedChats[chatID]; ok {
		t.mu.Unlock()
		return
	}
	t.mu.Unlock()

	if t.isMember(chatID) {
		t.mu.Lock()
		t.joinedChats[chatID] = struct{}{}
		t.mu.Unlock()
		return
	}

	if _, err := t.client.JoinChat(&client.JoinChatRequest{ChatId: chatID}); err != nil {
		t.logger.Error("JoinChat failed", "chat_id", chatID, "error", err)
		if isInviteRequestSent(err) || isTooManyRequests(err) {
			t.blockChat(chatID)
		}
		return
	}

	t.mu.Lock()
	t.joinedChats[chatID] = struct{}{}
	t.mu.Unlock()
	t.logger.Info("Joined linked discussion chat", "chat_id", chatID)
}

func (t *TelegramClient) processUpdateNewMessage(out chan domain.Message, upd *client.UpdateNewMessage) (<-chan domain.Message, error) {
	if upd.Message.IsOutgoing {
		return out, nil
	}
	if !upd.Message.IsChannelPost {
		return out, nil
	}

	linkedChatID, ok := t.getLinkedChatID(upd.Message.ChatId)
	if !ok {
		return out, nil
	}
	t.ensureJoinedChat(linkedChatID)

	// Для канального поста threadID соответствует ID самого сообщения.
	threadID := upd.Message.Id
	if threadID == 0 {
		return out, nil
	}
	return t.processChannelPostThread(out, upd.Message.ChatId, linkedChatID, threadID)
}

func (t *TelegramClient) processChannelPostThread(out chan domain.Message, channelChatID int64, discussionChatID int64, threadID int64) (<-chan domain.Message, error) {
	if t.isChatBlocked(discussionChatID) {
		t.logger.Info("Skip post thread: invite required for discussion chat", "chat_id", discussionChatID)
		return out, nil
	}

	t.logger.Info("Processing channel post thread",
		"channel_chat_id", channelChatID,
		"discussion_chat_id", discussionChatID,
		"thread_id", threadID,
	)

	chatName, err := t.getChatTitle(channelChatID)
	if err != nil {
		t.logger.Info("Error getting chat title", "err", err)
		chatName = ""
	}

	threadMsg, err := t.client.GetMessage(&client.GetMessageRequest{
		ChatId:    channelChatID,
		MessageId: threadID,
	})
	if err != nil {
		t.logger.Error("GetMessage for thread root failed",
			"chat_id", channelChatID,
			"thread_id", threadID,
			"error", err,
		)
		return out, err
	}

	text, ok := extractTextFromContent(threadMsg.Content)
	if !ok {
		t.logger.Debug("Post has no text, skipping", "content_type", threadMsg.Content.MessageContentType())
		return out, nil
	}

	text = strings.TrimSpace(text)
	if text == "" {
		t.logger.Debug("Post text is empty, skipping")
		return out, nil
	}

	out <- domain.Message{
		ChannelID:       channelChatID,
		ChatID:          discussionChatID,
		Text:            text,
		ChatName:        chatName,
		MessageThreadId: threadID,
	}
	return out, nil
}

func extractTextFromContent(content client.MessageContent) (string, bool) {
	switch c := content.(type) {
	case *client.MessageText:
		return c.Text.Text, true
	case *client.MessagePhoto:
		if c.Caption != nil {
			return c.Caption.Text, true
		}
	case *client.MessageVideo:
		if c.Caption != nil {
			return c.Caption.Text, true
		}
	case *client.MessageAnimation:
		if c.Caption != nil {
			return c.Caption.Text, true
		}
	case *client.MessageDocument:
		if c.Caption != nil {
			return c.Caption.Text, true
		}
	case *client.MessageAudio:
		if c.Caption != nil {
			return c.Caption.Text, true
		}
	case *client.MessageVoiceNote:
		if c.Caption != nil {
			return c.Caption.Text, true
		}
	}
	return "", false
}
func (t *TelegramClient) SendMessage(
	chatID int64,
	threadID int64,
	text string,
) error {
	if threadID != 0 {
		t.ensureJoinedChat(chatID)
	}

	t.SimulateTyping(chatID, threadID, text)

	input := &client.InputMessageText{
		Text: &client.FormattedText{
			Text: text,
		},
		ClearDraft: true,
	}

	req := &client.SendMessageRequest{
		ChatId:              chatID,
		InputMessageContent: input,
	}

	if threadID != 0 {
		req.MessageThreadId = threadID
	}

	_, err := t.client.SendMessage(req)
	if err != nil {
		// 🔍 проверяем, не словили ли лимит
		if isTooManyRequests(err) {
			t.logger.Error("SendMessage rate-limited: too many requests, stopping client",
				"chat_id", chatID,
				"thread_id", threadID,
				"error", err,
			)
			// Останавливаем конкретный TDLib-клиент
			t.Close()
			return ErrRateLimited
		}

		t.logger.Error("SendMessage failed",
			"chat_id", chatID,
			"thread_id", threadID,
			"error", err,
		)
		return err
	}

	return nil
}

func (t *TelegramClient) SimulateTyping(chatID, threadID int64, text string) {
	if !t.CanSendToChat(chatID) {
		t.logger.Info("Skip typing: cannot send to chat", "chat_id", chatID)
		return
	}
	//  Послали "печатает..."
	_, err := t.client.SendChatAction(&client.SendChatActionRequest{
		ChatId:          chatID,
		MessageThreadId: threadID,                   // можно 0, если не тред
		Action:          &client.ChatActionTyping{}, // тип "печатает"
	})
	if err != nil {
		t.logger.Warn("SendChatAction typing failed", "chat_id", chatID, "error", err)
		// не фейлим общую логику — это косметика
		return
	}

	// 923345799730. Прикидываем время "набора" текста
	runes := []rune(text)
	n := len(runes)

	// базовое и "за символ"
	base := 700 * time.Millisecond   // минимум, даже для коротких
	perChar := 70 * time.Millisecond // ~14 символов/сек
	d := base + time.Duration(n)*perChar

	// ограничим, чтобы не выглядело странно
	if d < 700*time.Millisecond {
		d = 700 * time.Millisecond
	}
	if d > 7*time.Second {
		d = 7 * time.Second
	}

	time.Sleep(d)
}

func (t *TelegramClient) readThread(ctx context.Context, chatID int64, m *client.Message) {
	// получить историю треда
	th, err := t.client.GetMessageThreadHistory(&client.GetMessageThreadHistoryRequest{
		ChatId:        chatID,
		MessageId:     m.Id,
		FromMessageId: 0,
		Limit:         20,
	})
	if err != nil {
		return
	}

	for range th.Messages {
		d := time.Duration(2+rand.Intn(9-2)) * time.Second
		time.Sleep(d)
	}
}

func (t *TelegramClient) sendReactionRandom(chatID, msgID int64) {
	var reactions = []string{"👍", "❤️", "🔥", "😂", "👏"}
	emoji := reactions[rand.Intn(len(reactions))]

	_, _ = t.client.AddMessageReaction(&client.AddMessageReactionRequest{
		ChatId:    chatID,
		MessageId: msgID,
		ReactionType: &client.ReactionTypeEmoji{
			Emoji: emoji,
		},
		IsBig: false,
	})

	t.logger.Info("Reaction added", "chat_id", chatID, "msg_id", msgID, "emoji", emoji)
}
func (t *TelegramClient) ImitateReading(ctx context.Context, chatID int64) {
	// Получаем историю
	if rand.Intn(100) > 10 {
		return
	}
	history, err := t.client.GetChatHistory(&client.GetChatHistoryRequest{
		ChatId: chatID,
		Limit:  30,
	})
	if err != nil {
		t.logger.Error("ImitateReading: GetChatHistory failed", "error", err)
		return
	}

	messages := history.Messages
	// Переворачиваем (человек читает сверху вниз)
	slices.Reverse(messages)

	for _, m := range messages {
		if m == nil {
			continue
		}

		// 923345799730. Имитируем "открыть сообщение"
		_, _ = t.client.OpenMessageContent(&client.OpenMessageContentRequest{
			ChatId:    chatID,
			MessageId: m.Id,
		})

		// 3. Подтверждение просмотра
		_, _ = t.client.ViewMessages(&client.ViewMessagesRequest{
			ChatId:     chatID,
			MessageIds: []int64{m.Id},
			ForceRead:  false,
		})

		// 4. Иногда ставим реакцию
		if rand.Float64() < 0.05 { // 5%
			t.sendReactionRandom(chatID, m.Id)
		}

		// 6. Если у поста есть обсуждение — иногда открываем тред
		if m.MessageThreadId != 0 && rand.Float64() < 0.2 {
			t.readThread(ctx, chatID, m)
		}

		// 7. Реалистичная задержка
		d := time.Duration(5+rand.Intn(10-5)) * time.Second
		time.Sleep(d)
	}
}

func isTooManyRequests(err error) bool {

	// TDLib оборачивается в client.Error
	var respErr client.ResponseError
	if errors.As(err, &respErr) && respErr.Err != nil {
		// обычно Code == 429, но подстрахуемся по тексту
		if respErr.Err.Code == 429 {
			return true
		}
		if strings.Contains(strings.ToLower(respErr.Err.Message), "too many requests") {
			return true
		}
	}
	return false
}

func isInviteRequestSent(err error) bool {
	var respErr client.ResponseError
	if errors.As(err, &respErr) && respErr.Err != nil {
		return strings.Contains(strings.ToUpper(respErr.Err.Message), "INVITE_REQUEST_SENT")
	}
	return strings.Contains(strings.ToUpper(err.Error()), "INVITE_REQUEST_SENT")
}

func (t *TelegramClient) isChatBlocked(chatID int64) bool {
	t.mu.Lock()
	until, ok := t.blockedTill[chatID]
	if ok && time.Now().After(until) {
		delete(t.blockedTill, chatID)
		ok = false
	}
	t.mu.Unlock()
	return ok
}

func (t *TelegramClient) blockChat(chatID int64) {
	const blockTTL = 72 * time.Hour
	t.mu.Lock()
	t.blockedTill[chatID] = time.Now().Add(blockTTL)
	t.mu.Unlock()
}

func (t *TelegramClient) ResolveUsername(username string) (int64, error) {
	if !strings.HasPrefix(username, "@") {
		username = "@" + username
	}

	res, err := t.client.SearchPublicChat(&client.SearchPublicChatRequest{
		Username: username[1:],
	})
	if err != nil {
		return 0, err
	}

	return res.Id, nil
}

func logAuthStates(tdCli *client.Client, log *slog.Logger) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastState string
	for range ticker.C {
		state, err := tdCli.GetAuthorizationState()
		if err != nil || state == nil {
			continue
		}
		stateType := state.AuthorizationStateType()
		if stateType != lastState {
			log.Info("Auth state", "state", stateType)
			lastState = stateType
		}
		if stateType == client.TypeAuthorizationStateReady || stateType == client.TypeAuthorizationStateClosed {
			return
		}
	}
}
