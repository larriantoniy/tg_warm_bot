package tg

import (
	"fmt"

	"github.com/larriantoniy/tg_user_bot/internal/ports"
	"github.com/zelenin/go-tdlib/client"
)

type RawSessionConfig struct {
	SessionFile string `json:"session_file"`
	Phone       string `json:"phone"`
	UserID      int64  `json:"user_id"`

	AppID   int32  `json:"app_id"`
	AppHash string `json:"app_hash"`

	SDK        string `json:"sdk"`         // условно: SystemVersion
	AppVersion string `json:"app_version"` // ApplicationVersion
	Device     string `json:"device"`      // DeviceModel
	LangCode   string `json:"lang_code"`   // SystemLanguageCode
	SystemLang string `json:"system_lang_code"`
	LangPack   string `json:"lang_pack"`

	Proxy    []any    `json:"proxy"` // [type, host, port, useAuth, user, pass]
	Channels []string `json:"channels"`

	// остальное можно добавить по мере необходимости
}

func (c *RawSessionConfig) ToProxyConfig() (*ports.ProxyConfig, error) {
	if len(c.Proxy) == 0 {
		return nil, nil
	}
	if len(c.Proxy) < 6 {
		return nil, fmt.Errorf("invalid proxy length: %d", len(c.Proxy))
	}

	// type := c.Proxy[0] (3 — условно socks5, но нам не важно)
	host, _ := c.Proxy[1].(string)

	// port может прийти как float64 из json.Unmarshal
	var port int32
	switch v := c.Proxy[2].(type) {
	case float64:
		port = int32(v)
	case int:
		port = int32(v)
	default:
		return nil, fmt.Errorf("invalid proxy port type %T", c.Proxy[2])
	}

	useAuth, _ := c.Proxy[3].(bool)
	user, _ := c.Proxy[4].(string)
	pass, _ := c.Proxy[5].(string)

	if host == "" || port == 0 {
		return nil, nil
	}

	p := &ports.ProxyConfig{
		Enabled: true,
		Server:  host,
		Port:    port,
	}
	if useAuth {
		p.Username = user
		p.Password = pass
	}
	return p, nil
}

func (c *RawSessionConfig) GetChannels() ([]string, error) {
	return c.Channels, nil
}

func (c *RawSessionConfig) ToTdParams(apiID int32, apiHash string, dbDir, filesDir string) *client.SetTdlibParametersRequest {
	fmt.Println("NUMBER:", c.Phone)
	fmt.Println("Session_name:", c.SessionFile)
	lang := c.LangCode
	if lang == "" {
		lang = "en"
	}

	systemVersion := c.SDK
	if systemVersion == "" {
		systemVersion = "Windows 10"
	}

	appVersion := c.AppVersion
	if appVersion == "" {
		appVersion = "2.0"
	}

	deviceModel := c.Device
	if deviceModel == "" {
		deviceModel = "Desktop"
	}

	return &client.SetTdlibParametersRequest{
		UseTestDc:           false,
		DatabaseDirectory:   dbDir,
		FilesDirectory:      filesDir,
		UseFileDatabase:     true,
		UseChatInfoDatabase: true,
		UseMessageDatabase:  true,
		UseSecretChats:      false,
		ApiId:               apiID,
		ApiHash:             apiHash,
		SystemLanguageCode:  lang,
		DeviceModel:         deviceModel,
		SystemVersion:       systemVersion,
		ApplicationVersion:  appVersion,
	}
}
