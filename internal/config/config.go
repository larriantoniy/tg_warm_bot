package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type AppConfig struct {
	ApiID      int32
	ApiHash    string
	Env        string `yaml:"env"`
	BaseDir    string `yaml:"base_dir"`
	NeuroAddr  string `yaml:"neuro_addr"`
	NeuroToken string `yaml:"neuro_token"`
	Owner      string `yaml:"owner"`

	Session string `yaml:"session"` // имя сессии по умолчанию (может переопределяться флагом/ENV)
	Auth    bool   `yaml:"-"`       // режим авторизации, управляется флагом/ENV, из yaml не читаем
}

// Load читает настройки из переменных окружения
func Load() (*AppConfig, error) {
	parseFlagsOnce()

	path := fetchConfigPath()
	cfgFromFile, err := MustLoadPath(path)
	if err != nil {
		return nil, fmt.Errorf("ошибка загрузки конфига: %w", err)
	}

	apiIDStr := os.Getenv("TELEGRAM_API_ID")
	apiHash := os.Getenv("TELEGRAM_API_HASH")
	neuroAddr := os.Getenv("NEURO_ADDR")
	neuroToken := os.Getenv("NEURO_TOKEN")
	owner := os.Getenv("OWNER")
	sessionFromEnv := os.Getenv("SESSION_NAME")
	authEnv := os.Getenv("AUTH_MODE") // например "true"/"1"

	if apiIDStr == "" || apiHash == "" || cfgFromFile.BaseDir == "" || neuroAddr == "" || neuroToken == "" {
		return nil, fmt.Errorf("TELEGRAM_API_ID, TELEGRAM_API_HASH , BaseDir , NEURO_ADDR , NEURO_TOKEN должны быть заданы")
	}

	apiID, err := strconv.Atoi(apiIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid TELEGRAM_API_ID: %w", err)
	}

	// --- выбираем session: приоритет flag > ENV > yaml ---
	sessionName := sessionFlag
	if sessionName == "" {
		sessionName = sessionFromEnv
	}
	if sessionName == "" {
		sessionName = cfgFromFile.Session // может быть пустым - это не критично
	}

	// --- выбираем auth-режим: приоритет flag > ENV ---
	auth := authFlag
	if !auth && authEnv != "" {
		// грубо, но рабоче: любое непустое значение считаем "true"
		auth = authEnv == "1" || strings.EqualFold(authEnv, "true") || strings.EqualFold(authEnv, "yes")
	}

	return &AppConfig{
		ApiID:      int32(apiID),
		ApiHash:    apiHash,
		Env:        cfgFromFile.Env,
		BaseDir:    cfgFromFile.BaseDir,
		NeuroAddr:  neuroAddr,
		NeuroToken: neuroToken,
		Owner:      owner,
		Session:    sessionName,
		Auth:       auth,
	}, nil
}

func MustLoadPath(path string) (*AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg AppConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

var (
	configPathFlag string
	sessionFlag    string
	authFlag       bool
	flagsParsed    bool
)

// parseFlagsOnce гарантирует, что flag.Parse() вызовется только один раз.
func parseFlagsOnce() {
	if flagsParsed {
		return
	}
	flagsParsed = true

	flag.StringVar(&configPathFlag, "config", "", "path to config file")
	flag.StringVar(&sessionFlag, "session", "", "session name (e.g. phone number without +)")
	flag.BoolVar(&authFlag, "auth", false, "run in auth mode for a single session")

	flag.Parse()
}

// fetchConfigPath fetches config path from command line flag or environment variable.
// Priority: flag > env > default.
// Default value is empty string.
func fetchConfigPath() string {
	parseFlagsOnce()

	if configPathFlag != "" {
		return configPathFlag
	}
	if p := os.Getenv("CONFIG_PATH"); p != "" {
		return p
	}
	return ""
}
