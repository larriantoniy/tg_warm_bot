package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

type AppConfig struct {
	ApiID   int32
	ApiHash string
	Env     string `yaml:"env"`
	BaseDir string `yaml:"base_dir"`
}

// Load читает настройки из переменных окружения
func Load() (*AppConfig, error) {
	path := fetchConfigPath()
	cfg, err := MustLoadPath(path)
	apiIDStr := os.Getenv("TELEGRAM_API_ID")
	apiHash := os.Getenv("TELEGRAM_API_HASH")

	if err != nil {
		return nil, fmt.Errorf("ошибка загрузки конфига: %w", err)
	}

	if apiIDStr == "" || apiHash == "" || cfg.BaseDir == "" {

		return nil, fmt.Errorf("TELEGRAM_API_ID, TELEGRAM_API_HASH , BaseDir должны быть заданы")
	}

	apiID, err := strconv.Atoi(apiIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid TELEGRAM_API_ID: %w", err)
	}
	apiID32 := int32(apiID)

	return &AppConfig{
		ApiID:   apiID32,
		ApiHash: apiHash,
		Env:     cfg.Env,
		BaseDir: cfg.BaseDir,
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

// fetchConfigPath fetches config path from command line flag or environment variable.
// Priority: flag > env > default.
// Default value is empty string.
func fetchConfigPath() string {
	var res string

	flag.StringVar(&res, "config", "", "path to config file")
	flag.Parse()

	if res == "" {
		res = os.Getenv("CONFIG_PATH")
	}
	return res
}
