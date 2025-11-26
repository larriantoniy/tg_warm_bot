package domain

// Message описывает входящее сообщение из Telegram
type ReplyTarget struct {
	DiscussionChatID int64
	DiscussionMsgID  int64
	ThreadID         int64
}
type Message struct {
	ChatID          int64
	ChatName        string
	Text            string
	PhotoFile       string
	MessageThreadId int64
	ReplyTo         *ReplyTarget
}
