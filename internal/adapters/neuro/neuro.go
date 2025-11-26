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

const prompt = "Ты девушка.Это пост из соцсети напиши для него короткий осмысленный , доброжелательный комментарий не более 12 слов:"

type Neuro struct {
	client      *http.Client
	ctx         *context.Context
	logger      *slog.Logger
	baseURL     string                   // https://openrouter.ai/api/v1
	apiKey      string                   // TOKEN neuro
	defaultBody *domain.DefaultNeuroBody // закодированное JSON-тело
	// заготовленный http.Request
}

func NewNeuro(cfg *config.AppConfig, logger *slog.Logger) (*Neuro, error) {
	// 923561770135) Кодируем заранее JSON-тело
	body := domain.DefaultNeuroBody{
		Model: domain.MistralModel, // например "mistral-small-2506"
		Messages: []domain.NeuroMessage{
			{
				Role: domain.RoleUser,
				Content: []domain.MessageContent{
					{
						Type: "text", // "text" для промпта
						Text: prompt,
					},
				},
			},
		},
	}

	// 3) Собираем объект Neuro
	return &Neuro{
		client:      &http.Client{},
		logger:      logger,
		baseURL:     cfg.NeuroAddr,
		apiKey:      cfg.NeuroToken,
		defaultBody: &body,
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
	// Подготовка тела
	body := *n.defaultBody

	body.Messages[0].Content[0].Text = body.Messages[0].Content[0].Text + msg.Text

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal body: %w", err)
	}

	// Создание запроса
	bodyBytesReader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.baseURL, bodyBytesReader)
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+n.apiKey)

	// Ответ нейросети
	var nr domain.NeuroResponse
	n.logger.Info("Neuro request ", req.Body, req.Header, req.URL)

	err = retry(3, time.Second, func() error {
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
