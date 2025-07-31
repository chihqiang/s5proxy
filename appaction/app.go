package appaction

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"wangzhiqiang/s5proxy/conf"
	"wangzhiqiang/s5proxy/s5"
)

type App struct {
	mu         sync.Mutex
	cfgPaths   []string
	activePath string
	proxy      *s5.Proxy
	stopCtx    context.Context
	cancelFunc context.CancelFunc
}

func NewApp(cfgPaths []string) *App {
	return &App{
		cfgPaths: cfgPaths,
	}
}

func (a *App) Run() {
	a.stopCtx, a.cancelFunc = context.WithCancel(context.Background())
	if err := a.reload(); err != nil {
		slog.Error("Initialization failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	if err := watchConfig(a.activePath, func() {
		slog.Warn("Configuration file changed, reloading...")
		a.shutdown()
		if err := a.reload(); err != nil {
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
	a.shutdown()
	slog.Info("Exited cleanly.")
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
	a.proxy = s5.New(cfg.Listen, cfg.MaxConns, cfg.Users, cfg.AllowedHosts)
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
