package conf

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen       string            `yaml:"listen"`
	MaxConns     int               `yaml:"max_conns"`
	Users        map[string]string `yaml:"users"`
	AllowedHosts []string          `yaml:"allowed_hosts"`
}

// LoadConfig attempts to load the configuration from the specified paths.
func LoadConfig(paths ...string) (*Config, string, error) {
	var tried []string
	for _, p := range paths {
		p = expandPath(p)
		tried = append(tried, p)
		data, err := os.ReadFile(p)
		if err != nil {
			continue // 文件不存在则跳过
		}
		var cfg Config
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, "", fmt.Errorf("failed to parse configuration (%s): %w", p, err)
		}
		if cfg.Listen == "" {
			cfg.Listen = ":1080"
		}
		slog.Info("Successfully loaded the configuration file", slog.String("path", p))
		return &cfg, p, nil
	}
	return nil, "", fmt.Errorf("no valid configuration file was found, try path: %v", tried)
}

// expandPath expands the tilde (~) in the path to the user's home directory.
// If the path does not start with "~/", it returns the path unchanged.
// This is useful for handling user-specific configuration files.
func expandPath(p string) string {
	if len(p) >= 2 && p[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return p
		}
		return filepath.Join(home, p[2:])
	}
	return p
}
