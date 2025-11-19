package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/larriantoniy/tg_user_bot/internal/adapters/tg"
	"github.com/larriantoniy/tg_user_bot/internal/ports"
)

type JSONSessionConfigRepo struct {
	baseDir string // "./tdlib-sessions"
}

func NewJSONSessionConfigRepo(baseDir string) *JSONSessionConfigRepo {
	return &JSONSessionConfigRepo{baseDir: baseDir}
}

func (r *JSONSessionConfigRepo) ListSessions(ctx context.Context) ([]string, error) {
	entries, err := os.ReadDir(r.baseDir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func (r *JSONSessionConfigRepo) GetSessionConfig(ctx context.Context, sessionName string) (*ports.SessionConfig, error) {
	path := filepath.Join(r.baseDir, sessionName, sessionName+".json")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var raw tg.RawSessionConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", path, err)
	}

	sessName := raw.SessionFile
	if sessName == "" {
		sessName = sessionName
	}

	// тут можно переиспользовать ToProxyConfig из прошлого ответа
	proxyCfg, err := raw.ToProxyConfig()
	if err != nil {
		return nil, fmt.Errorf("proxy parse: %w", err)
	}

	return &ports.SessionConfig{
		SessionName:        sessName,
		Phone:              raw.Phone,
		AppID:              raw.AppID,
		AppHash:            raw.AppHash,
		DeviceModel:        raw.Device,
		SystemVersion:      raw.SDK,
		ApplicationVersion: raw.AppVersion,
		LangCode:           raw.LangCode,
		Proxy:              proxyCfg,
	}, nil
}
