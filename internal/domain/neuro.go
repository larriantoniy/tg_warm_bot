package domain

import "encoding/json"

type NeuroModel string
type MessageRole string

const (
	MistralModel string      = "mistralai/mistral-small-24b-instruct"
	RoleUser     MessageRole = "user"
)

type ImageUrl struct {
	Url string `json:"url"`
}

type MessageContent struct {
	Type     string    `json:"type"` // "text" или "image_url"
	Text     string    `json:"text,omitempty"`
	ImageUrl *ImageUrl `json:"image_url,omitempty"`
}

type NeuroMessage struct {
	Role    MessageRole      `json:"role"`
	Content []MessageContent `json:"content"`
}

type DefaultNeuroBody struct {
	Model    string         `json:"model"`
	Messages []NeuroMessage `json:"messages"`
}

// NeuroResponse соответствует корневому JSON-объекту.
type NeuroResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Model   string   `json:"model"`
	Usage   Usage    `json:"usage"`
	Created int64    `json:"created"`
	Choices []Choice `json:"choices"`
}

// Usage — статистика по токенам.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Choice — один из вариантов ответа (обычно только один, index=0).
type Choice struct {
	Index        int             `json:"index"`
	Message      MessageResponse `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

// MessageResponse — собственно тело ответа ассистента.
type MessageResponse struct {
	Role      string            `json:"role"`
	Content   string            `json:"content"`
	ToolCalls []json.RawMessage `json:"tool_calls,omitempty"`
	Prefix    bool              `json:"prefix"`
}
