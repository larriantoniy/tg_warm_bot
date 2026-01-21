package neuro

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/larriantoniy/tg_user_bot/internal/config"
	"github.com/larriantoniy/tg_user_bot/internal/domain"
)

const systemPrompt = "Экспертный комментарий к посту в Telegram. Русский язык. До 12 слов. Доброжелательно и уверенно. Без вопросов и выдуманных фактов. Ровно одно эмодзи.\nТекст поста:\n"

type Neuro struct {
	client  *http.Client
	ctx     *context.Context
	logger  *slog.Logger 
	baseURL string // https://openrouter.ai/api/v1
	apiKey  string // TOKEN neuro
	// заготовленный http.Request
}

func NewNeuro(cfg *config.AppConfig, logger *slog.Logger) (*Neuro, error) {
	if cfg.NeuroToken == "" {
		logger.Warn("Neuro token is empty; requests will fail with 401")
	}
	// 3) Собираем объект Neuro
	return &Neuro{
		client:  &http.Client{},
		logger:  logger,
		baseURL: cfg.NeuroAddr,
		apiKey:  cfg.NeuroToken,
	}, nil
}

func retry(attempts int, sleep time.Duration, fn func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		time.Sleep(sleep)
	}
	return err
}

func (n *Neuro) GetComment(ctx context.Context, msg *domain.Message) (string, error) {
	// Ответ нейросети
	var nr domain.NeuroResponse

	err := retry(3, time.Second, func() error {
		content := []domain.MessageContent{
			{
				Type: "text", // "text" для промпта
				Text: systemPrompt + msg.Text,
			}, 
		}
		if msg.PhotoFile != "" { 
			content = append(content, domain.MessageContent{
				Type: "image_url",
				ImageUrl: &domain.ImageUrl{
					Url: msg.PhotoFile,
				},
			})
		}

		body := domain.DefaultNeuroBody{
			Model:            domain.MistralModel, // например "mistral-small-2506"
			Temperature:      0.4,
			TopP:             0.9,
			PresencePenalty:  0.2,
			FrequencyPenalty: 0.3,
			MaxTokens:        120,
			Messages: []domain.NeuroMessage{
				{
					Role: domain.RoleUser,
					Content: content,
				},
			},
		}

		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}

		bodyBytesReader := bytes.NewReader(bodyBytes)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.baseURL, bodyBytesReader)
		if err != nil {
			return fmt.Errorf("new request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+n.apiKey)

		n.logger.Info("Neuro request", "url", req.URL.String())

		resp, err := n.client.Do(req)
		if err != nil {
			n.logger.Error("HTTP request to neuro failed", "err", err)
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			data, _ := io.ReadAll(resp.Body)
			n.logger.Error("Neuro API returned error",
				"status", resp.StatusCode,
				"body", string(data),
			)
			return fmt.Errorf("status %d: %s", resp.StatusCode, string(data))
		}

		return json.NewDecoder(resp.Body).Decode(&nr)
	})

	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}

	if len(nr.Choices) == 0 {
		return "", fmt.Errorf("empty choices")
	}

	n.logger.Info("After neuro processing", "result", nr.Choices[0].Message.Content)

	return nr.Choices[0].Message.Content, nil
}
