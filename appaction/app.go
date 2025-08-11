package appaction

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"wangzhiqiang/s5proxy/conf"
	"wangzhiqiang/s5proxy/s5"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

type App struct {
	mu       sync.Mutex
	filename string
	proxy    *s5.Proxy
}

func NewApp(filename string) *App {
	return &App{filename: filename}
}

func (a *App) Run(ctx context.Context) error {
	viper.SetConfigFile(a.filename)

	if err := a.reload(); err != nil {
		return err
	}
	viper.WatchConfig()
	var (
		debounceTimer *time.Timer
		debounceDelay = 500 * time.Millisecond
	)
	viper.OnConfigChange(func(e fsnotify.Event) {
		if debounceTimer != nil {
			debounceTimer.Stop()
		}
		debounceTimer = time.AfterFunc(debounceDelay, func() {
			a.mu.Lock()
			defer a.mu.Unlock()

			slog.Info("Config file changed", "file", e.Name)
			a.shutdown()
			if err := a.reload(); err != nil {
				slog.Error("Config reload failed", "error", err)
			}
		})
	})
	// Block until context is done
	<-ctx.Done()
	slog.Info("Shutting down proxy")
	a.shutdown()
	return nil
}

func (a *App) reload() error {
	cfg := &conf.Config{}
	if err := viper.ReadInConfig(); err != nil {
		slog.Error("Failed to read config file", "error", err)
		return err
	}
	if err := viper.Unmarshal(cfg); err != nil {
		slog.Error("Failed to parse YAML config", "error", err)
		return err
	}
	// Validate required fields
	if cfg.Listen == "" {
		return errors.New("config 'listen' cannot be empty")
	}
	if len(cfg.Users) == 0 {
		return errors.New("config 'users' cannot be empty")
	}
	// Stop old proxy if running
	if a.proxy != nil {
		a.proxy.Stop()
		a.proxy = nil
	}
	// Start new proxy
	a.proxy = s5.New(cfg.Listen, cfg.MaxConns, cfg.Users, cfg.AllowedHosts)
	if err := a.proxy.Start(); err != nil {
		slog.Error("Failed to start proxy", "error", err)
		return err
	}

	return nil
}

func (a *App) shutdown() {
	if a.proxy != nil {
		a.proxy.Stop()
		a.proxy = nil
	}
}
