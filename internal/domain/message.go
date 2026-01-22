package domain

// Message описывает входящее сообщение из Telegram

type Message struct {
	ChannelID       int64
	ChatID          int64
	ChatName        string
	Text            string
	PhotoFile       string
	MessageThreadId int64
}
