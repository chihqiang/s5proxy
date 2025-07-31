package main

import (
	"flag"
	"log/slog"
	"wangzhiqiang/s5proxy/appaction"
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
	app := appaction.NewApp([]string{filename})
	app.Run()
}
