package tg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func LoadRawSessionConfig(baseDir, sessionName string) (*RawSessionConfig, error) {
	path := filepath.Join(baseDir, sessionName, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg RawSessionConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", path, err)
	}
	// подстрахуемся: если в json другое имя
	if cfg.SessionFile == "" {
		cfg.SessionFile = sessionName
	}
	return &cfg, nil
}
