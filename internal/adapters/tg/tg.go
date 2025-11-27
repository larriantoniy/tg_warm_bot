package tg

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/larriantoniy/tg_user_bot/internal/domain"
	"github.com/zelenin/go-tdlib/client"
)

// TDLibClient реализует ports.TelegramClient через go-tg

type TelegramClient struct {
	client *client.Client
	logger *slog.Logger
	selfId int64
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

	checkIPv4(log)
	checkIPv6(log)
	checkProxy(log, proxyCfg)

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
		log.Info("TDLib client started in AUTH mode",
			"session", rawCfg.SessionFile,
			"phone", rawCfg.Phone,
		)

		return &TelegramClient{
			client: tdCli,
			logger: log,
			selfId: 0, // узнаешь позже через GetMe, если нужно
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

	// 923345799730) Получаем уже присоединённые
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
				_, err := t.processUpdateNewMessage(out, upd)
				if err != nil {
					t.logger.Error("Error process UpdateNewMessage msg content type", upd.Message.Content.MessageContentType())
				}
			}
		}
	}()

	return out, nil
}

func (t *TelegramClient) IsChannelMember(username string) (bool, error) {

	chat, err := t.client.SearchPublicChat(&client.SearchPublicChatRequest{
		Username: username,
	})
	if err != nil {
		t.logger.Error("SearchPublicChat failed", "username", username, "error", err)
		return false, err
	}

	//  Получаем информацию об участнике

	member, err := t.client.GetChatMember(&client.GetChatMemberRequest{
		ChatId:   chat.Id,
		MemberId: &client.MessageSenderUser{UserId: t.selfId},
	})
	if err != nil {
		t.logger.Debug("GetChatMember failed, assuming not a member", "chat_id", chat.Id, "error", err)
		return false, nil
	}

	//  Определяем статус через type assertion
	switch member.Status.(type) {
	case *client.ChatMemberStatusMember, *client.ChatMemberStatusAdministrator, *client.ChatMemberStatusCreator:
		t.logger.Debug("Bot is channel member", "chat_id", chat.Id)
		return true, nil
	default:
		t.logger.Debug("Bot not member", "chat_id", chat.Id)
		return false, nil
	}
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

func (t *TelegramClient) processUpdateNewMessage(out chan domain.Message, upd *client.UpdateNewMessage) (<-chan domain.Message, error) {
	if !upd.Message.IsChannelPost {
		t.logger.Info("IsChannelPost", "!upd.Message.IsChannelPost", !upd.Message.IsChannelPost)

		return out, nil
	}
	chatName, err := t.getChatTitle(upd.Message.ChatId)
	if err != nil {
		t.logger.Info("Error getting chat title", "err", err)

		chatName = ""
	}

	var replyTo *client.MessageReplyToMessage
	if reply, ok := upd.Message.ReplyTo.(*client.MessageReplyToMessage); ok {
		if reply.ChatId == 0 || reply.MessageId == 0 {
			return out, nil
		}
		replyTo = reply
		t.logger.Info("New channel post with comments",
			"channel_id", upd.Message.ChatId,
			"discussion_chat_id", reply.ChatId,
			"discussion_anchor_msg_id", reply.MessageId,
			"thread_id", upd.Message.MessageThreadId)
	} else {
		// нет корректного ReplyTo -> нет обсуждений, выходим
		return out, nil
	}

	switch content := upd.Message.Content.(type) {
	case *client.MessageText:
		return t.processMessageText(out, content, upd.Message.ChatId, chatName, replyTo, upd.Message.MessageThreadId)
	case *client.MessagePhoto:
		return t.processMessagePhoto(out, content, upd.Message.ChatId, chatName, replyTo, upd.Message.MessageThreadId)
	default:
		t.logger.Debug("cant switch type update", "upd message MessageContentType()", upd.Message.Content.MessageContentType())
		return out, nil
	}
}
func (t *TelegramClient) processMessagePhoto(out chan domain.Message, msg *client.MessagePhoto, msgChatId int64, ChatName string, replyToMsg *client.MessageReplyToMessage, threadId int64) (<-chan domain.Message, error) {
	var text string
	replyTo := &domain.ReplyTarget{DiscussionChatID: replyToMsg.ChatId, DiscussionMsgID: replyToMsg.MessageId, ThreadID: threadId}

	var photoFileId string

	var best *client.PhotoSize
	for i, size := range msg.Photo.Sizes {
		if i == 0 || size.Width*size.Height > best.Width*best.Height {
			best = size
			photoFileId = best.Photo.Remote.Id
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no photo sizes available")
	}
	if msg.Caption != nil {
		text = msg.Caption.Text
	}
	photoFile, err := t.GetPhotoBase64ById(photoFileId)
	if err != nil {
		t.logger.Info("GetPhotoBase64ById", "err", err)
	}
	out <- domain.Message{
		ChatID:          msgChatId,
		Text:            text,
		ChatName:        ChatName,
		PhotoFile:       photoFile,
		MessageThreadId: threadId,
		ReplyTo:         replyTo,
	}
	return out, nil
}
func (t *TelegramClient) processMessageText(out chan domain.Message, msg *client.MessageText, msgChatId int64, ChatName string, replyToMsg *client.MessageReplyToMessage, threadId int64) (<-chan domain.Message, error) {
	replyTo := &domain.ReplyTarget{DiscussionChatID: replyToMsg.ChatId, DiscussionMsgID: replyToMsg.MessageId, ThreadID: threadId}

	out <- domain.Message{
		ChatID:          msgChatId,
		Text:            msg.Text.Text,
		ChatName:        ChatName,
		MessageThreadId: threadId,
		ReplyTo:         replyTo,
	}
	return out, nil
}

func (t *TelegramClient) GetPhotoBase64ById(photoId string) (string, error) {
	//  Регистрируем remote ID и получаем локальный file ID
	remoteFile, err := t.client.GetRemoteFile(&client.GetRemoteFileRequest{
		RemoteFileId: photoId,
	})
	if err != nil {
		return "", fmt.Errorf("GetRemoteFile failed: %w", err)
	}

	_, err = t.client.DownloadFile(&client.DownloadFileRequest{
		FileId:      remoteFile.Id,
		Priority:    32,
		Offset:      0,
		Limit:       0,
		Synchronous: false,
	})
	if err != nil {
		return "", fmt.Errorf("DownloadFile failed: %w", err)
	}
	// 923345799730. Начинаем опрашивать статус загрузки
	var fileInfo *client.File
	for {
		fileInfo, err = t.client.GetFile(&client.GetFileRequest{
			FileId: remoteFile.Id,
		})
		if err != nil {
			return "", fmt.Errorf("GetFile polling failed: %w", err)
		}
		if fileInfo.Local.IsDownloadingCompleted {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// 3. Читаем файл из кеша TDLib
	data, err := os.ReadFile(fileInfo.Local.Path)

	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %w", fileInfo.Local.Path, err)
	}
	return BuildDataURI(bytes.NewReader(data))

}
func (t *TelegramClient) SendMessage(
	chatID int64,
	replyToMsgID int64,
	threadID int64,
	text string,
) error {
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

	if replyToMsgID != 0 {
		req.ReplyTo = &client.InputMessageReplyToMessage{
			MessageId: replyToMsgID,
			Quote:     nil,
		}
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
				"reply_to", replyToMsgID,
				"error", err,
			)
			// Останавливаем конкретный TDLib-клиент
			t.Close()
			return ErrRateLimited
		}

		t.logger.Error("SendMessage failed",
			"chat_id", chatID,
			"thread_id", threadID,
			"reply_to", replyToMsgID,
			"error", err,
		)
		return err
	}

	return nil
}

func (t *TelegramClient) SimulateTyping(chatID, threadID int64, text string) {
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

// BuildDataURI читает первые 512 байт для детектирования MIME,
// затем определяет формат через DecodeConfig и формирует Data URI.
func BuildDataURI(r io.Reader) (string, error) {
	// Читаем все байты (можно оптимизировать потоково)
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read data: %w", err)
	}

	//  Sniff MIME
	mimeType := http.DetectContentType(data[:min(512, len(data))]) // :contentReference[oaicite:9]{index=9}

	//  DecodeConfig для более точного формата

	if _, format, err := image.DecodeConfig(r); err == nil {
		mimeType = "image/" + format // :contentReference[oaicite:10]{index=10}
	}

	// 3) Base64 encode
	b64 := base64.StdEncoding.EncodeToString(data) // :contentReference[oaicite:11]{index=11}

	// 4) Собираем Data URI согласно RFC 2397
	return fmt.Sprintf("data:%s;base64,%s", mimeType, b64), nil // :contentReference[oaicite:12]{index=12}
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
	var tdErr *client.Error
	if errors.As(err, &tdErr) {
		// обычно Code == 429, но подстрахуемся по тексту
		if tdErr.Code == 429 {
			return true
		}
		if strings.Contains(strings.ToLower(tdErr.Message), "too many requests") {
			return true
		}
	}
	return false
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
