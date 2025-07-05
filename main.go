package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"wangzhiqiang/s5proxy/conf"
	"wangzhiqiang/s5proxy/s5"
	"wangzhiqiang/s5proxy/watcher"
)

var (
	configFlag string
)

func init() {
	flag.StringVar(&configFlag, "config", "config.yaml", "path to the configuration file")
	slog.SetLogLoggerLevel(slog.LevelDebug)
}

func main() {
	flag.Parse()
	app := &App{cfgPaths: []string{configFlag}}
	app.stopCtx, app.cancelFunc = context.WithCancel(context.Background())

	if err := app.reload(); err != nil {
		slog.Error("Initialization failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	if err := watcher.WatchConfig(app.activePath, func() {
		slog.Warn("Configuration file changed, reloading...")
		if err := app.reload(); err != nil {
			slog.Error("Reload failed", slog.String("error", err.Error()))
		}
	}); err != nil {
		slog.Error("Failed to watch config file", slog.String("error", err.Error()))
		os.Exit(1)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	<-sigs
	slog.Info("Received exit signal, cleaning up...")
	app.shutdown()
	slog.Info("Exited cleanly.")
}

type App struct {
	mu         sync.Mutex
	cfgPaths   []string
	activePath string
	proxy      *s5.Proxy
	stopCtx    context.Context
	cancelFunc context.CancelFunc
}

// reload 加载配置，停止旧代理，启动新代理
func (a *App) reload() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	cfg, usedPath, err := conf.LoadConfig(a.cfgPaths...)
	if err != nil {
		return err
	}
	a.activePath = usedPath

	// 关闭旧代理
	if a.proxy != nil {
		slog.Info("Stopping existing proxy...")
		a.proxy.Stop()
		a.proxy = nil
	}

	// 创建新代理并启动
	a.proxy = s5.New(cfg.Listen, cfg.MaxConns, cfg.Users, cfg.AllowedIPs)
	slog.Info("Starting new proxy...")
	go func() {
		if err := a.proxy.Start(); err != nil {
			slog.Error("Proxy start error", "error", err)
		}
	}()
	return nil
}

// shutdown 优雅关闭代理和取消上下文
func (a *App) shutdown() {
	a.cancelFunc()

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.proxy != nil {
		a.proxy.Stop()
		a.proxy = nil
	}
}
