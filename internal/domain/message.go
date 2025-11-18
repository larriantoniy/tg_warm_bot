package domain

// Message описывает входящее сообщение из Telegram
type Message struct {
	ChatID    int64
	ChatName  string
	Text      string
	PhotoFile string
}
