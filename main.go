package main

import (
	"context"
	"flag"
	"golang.org/x/sync/errgroup"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"wangzhiqiang/s5proxy/app"
)

var (
	filename string
)

func init() {
	flag.StringVar(&filename, "config", "config.yaml", "path to the configuration file")
	slog.SetLogLoggerLevel(slog.LevelDebug)
}

func main() {
	flag.Parse()
	// Create context with cancel on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	g, ctx := errgroup.WithContext(ctx)
	a := app.NewApp(filename)
	// Run the app in a goroutine
	g.Go(func() error {
		return a.Run(ctx)
	})
	// Wait for everything to finish
	if err := g.Wait(); err != nil {
		slog.Error("Application exited with error", "error", err)
		os.Exit(1)
	}
	slog.Info("Application exited cleanly")
}
