package ports

import "github.com/larriantoniy/tg_user_bot/internal/domain"

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
}
