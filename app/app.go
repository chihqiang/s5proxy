package app

import (
	"context"
	"errors"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
	"log/slog"
	"sync"
	"time"
	"wangzhiqiang/s5proxy/config"
	"wangzhiqiang/s5proxy/proxy"
)

type App struct {
	mu       sync.Mutex
	filename string
	proxies  []proxy.IProxy
}

func NewApp(filename string) *App {
	return &App{
		filename: filename,
	}
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
	cfg := &config.Config{}
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
	// 如果没有代理，第一次启动
	if len(a.proxies) == 0 {
		a.shutdown()
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, pxy := range proxy.Get() {
		wg.Add(1)
		go func(pxy proxy.IProxy, cfg *config.Config) {
			defer wg.Done()
			pxy.WithConfig(cfg)
			if err := pxy.Start(); err != nil {
				slog.Error("Failed to start proxy", "error", err)
			}
			mu.Lock()
			a.proxies = append(a.proxies, pxy)
			mu.Unlock()
		}(pxy, cfg)
	}
	wg.Wait()
	return nil
}

func (a *App) shutdown() {
	for _, pxy := range a.proxies {
		pxy.Stop()
	}
	a.proxies = nil
}
