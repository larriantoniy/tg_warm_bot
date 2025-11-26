package ports

import (
	"context"

	"github.com/larriantoniy/tg_user_bot/internal/domain"
)

// TelegramClient определяет интерфейс для работы с Telegram
// Реализуется конкретными адаптерами (TDLib, Bot API и т.д.).
type TelegramClient interface {
	// JoinChannel подписывается на публичный канал по его username
	JoinChannel(ch string) error
	// JoinChannels подписывается на список каналов
	JoinChannels(chs []string)
	// Listen возвращает канал доменных сообщений
	Listen() (<-chan domain.Message, error)
	// IsChannelMember проверяет есть ли username в чате
	IsChannelMember(username string) (bool, error)
	Close()
	SendMessage(chatID int64,
		replyToMsgID int64, // id сообщения внутри discussion group
		threadID int64, // может быть 0
		text string) error
	SimulateTyping(chatID, threadID int64, text string)
	ImitateReading(ctx context.Context, chatID int64)
	ResolveUsername(username string) (int64, error)
}
