package ports

import (
	"context"
)

type ProxyConfig struct {
	Enabled  bool
	Server   string
	Port     int32
	Username string
	Password string
}

type SessionConfig struct {
	SessionName        string
	Phone              string
	AppID              int32
	AppHash            string
	DeviceModel        string
	SystemVersion      string
	ApplicationVersion string
	LangCode           string
	Proxy              *ProxyConfig
	Channels           []string
}
type SessionConfigRepo interface {
	// Возвращает список доступных сессий (по именам)
	ListSessions(ctx context.Context) ([]string, error)

	// Загружает конфиг для конкретной сессии
	GetSessionConfig(ctx context.Context, sessionName string) (*SessionConfig, error)
}
